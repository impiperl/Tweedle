// Package api is the HTTP interface used by the Dee app. A web server in
// front may forward a path prefix to it with the prefix stripped, so routes
// start at "/".
package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/impiperl/Tweedle/internal/collect"
	"github.com/impiperl/Tweedle/internal/config"
	"github.com/impiperl/Tweedle/internal/i18n"
	"github.com/impiperl/Tweedle/internal/logs"
	"github.com/impiperl/Tweedle/internal/monitor"
	"github.com/impiperl/Tweedle/internal/store"
)

type Server struct {
	cfg     *config.Config
	store   *store.Store
	mon     *monitor.Monitor
	version string
	hub     *hub
	agg     aggregator
	// livePing is how often an open live connection is pinged and its device
	// checked again (a device removed with "tweedle devices rm" is cut off then).
	livePing time.Duration
	pairRL   rateLimiter
	redactor *logs.Redactor
	// fetchLogs reads container logs (collect.ContainerLogs; replaced in tests).
	fetchLogs fetchLogsFunc

	touchMu sync.Mutex
	touched map[int64]time.Time

	// Push, if set, receives every stored event (with its id).
	Push interface{ Enqueue(store.Event) }
}

func New(cfg *config.Config, st *store.Store, version string) *Server {
	return &Server{
		cfg: cfg, store: st, version: version, hub: newHub(), livePing: 30 * time.Second,
		pairRL:    rateLimiter{max: 10, per: time.Minute},
		touched:   map[int64]time.Time{},
		redactor:  logs.NewRedactor(cfg.Logs.RedactKeys),
		fetchLogs: collect.ContainerLogs,
	}
}

// Attach connects the server to the monitor (snapshots for status/live).
func (s *Server) Attach(m *monitor.Monitor) {
	s.mon = m
	m.OnTick = s.onTick
}

// Notify stores an event and sends it to connected apps (monitor.Notifier).
func (s *Server) Notify(e monitor.Event) {
	se := store.Event{Time: e.Time, Key: e.Key, Level: string(e.Level), Code: e.Code, Params: e.Params}
	id, err := s.store.AddEvent(se)
	if err != nil {
		log.Printf("store event: %v", err)
		return
	}
	se.ID = id
	if s.Push != nil {
		s.Push.Enqueue(se)
	}
	s.hub.broadcast(func(c *client) any {
		return map[string]any{"type": "event", "event": s.eventJSON(se, c.lang)}
	})
}

func (s *Server) onTick(snap *monitor.Snapshot) {
	if m, ok := s.agg.add(snap); ok {
		if err := s.store.AddMetric(m); err != nil {
			log.Printf("store metric: %v", err)
		}
	}
	s.hub.broadcast(func(*client) any { return map[string]any{"type": "status", "status": snap} })
}

// Handler returns the HTTP handler with all routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("POST /api/pair", s.handlePair)
	mux.HandleFunc("GET /api/status", s.auth(s.handleStatus))
	mux.HandleFunc("GET /api/history", s.auth(s.handleHistory))
	mux.HandleFunc("GET /api/events", s.auth(s.handleEvents))
	mux.HandleFunc("POST /api/events/seen", s.auth(s.handleSeen))
	mux.HandleFunc("GET /api/device", s.auth(s.handleGetDevice))
	mux.HandleFunc("PUT /api/device", s.auth(s.handlePutDevice))
	mux.HandleFunc("DELETE /api/device", s.auth(s.handleDeleteDevice))
	mux.HandleFunc("GET /api/live", s.auth(s.handleLive))
	mux.HandleFunc("GET /api/containers/{name}/logs", s.auth(s.handleContainerLogs))
	return mux
}

// Run serves until ctx is cancelled and prunes old data every hour.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{Addr: s.cfg.Listen, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sh, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sh)
	}()
	var err error
	if s.cfg.TLSCert != "" {
		r, lerr := newCertReloader(s.cfg.TLSCert, s.cfg.TLSKey)
		if lerr != nil {
			return lerr
		}
		srv.TLSConfig = &tls.Config{GetCertificate: r.get, MinVersion: tls.VersionTLS12}
		go s.pruneLoop(ctx)
		err = srv.ListenAndServeTLS("", "")
	} else {
		go s.pruneLoop(ctx)
		err = srv.ListenAndServe()
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) pruneLoop(ctx context.Context) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		now := time.Now()
		if err := s.store.Prune(now.Add(-s.cfg.Retention.Metrics), now.Add(-s.cfg.Retention.Events)); err != nil {
			log.Printf("prune: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// --- handlers -------------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": s.cfg.Name, "version": s.version})
}

type pairRequest struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	Lang      string `json:"lang"`
	PushToken string `json:"pushToken"`
}

func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	if !s.pairRL.allow() {
		writeError(w, http.StatusTooManyRequests, "too many attempts")
		return
	}
	var req pairRequest
	if !readJSON(w, r, &req) {
		return
	}
	if req.Code == "" {
		writeError(w, http.StatusBadRequest, "code required")
		return
	}
	token := randomToken()
	d := store.Device{Name: limitLen(req.Name, 80), Lang: normLang(req.Lang), PushToken: req.PushToken, MinLevel: "warn"}
	if d.Name == "" {
		d.Name = "Dee"
	}
	id, err := s.store.Pair(HashSecret(NormalizeCode(req.Code)), HashSecret(token), d)
	if errors.Is(err, store.ErrInvalidCode) {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "deviceId": id, "name": s.cfg.Name})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request, d *store.Device) {
	unseen, _ := s.store.Unseen(d.ID, []string{"warn", "error"})
	var snap *monitor.Snapshot
	if s.mon != nil {
		snap = s.mon.Snapshot()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": snap, "unseen": unseen, "version": s.version, "logs": s.cfg.Logs.Enabled,
	})
}

var ranges = map[string]time.Duration{"1h": time.Hour, "6h": 6 * time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour}

const maxPoints = 360

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request, d *store.Device) {
	rng := r.URL.Query().Get("range")
	if rng == "" {
		rng = "1h"
	}
	dur, ok := ranges[rng]
	if !ok {
		writeError(w, http.StatusBadRequest, "range must be 1h, 6h, 24h or 7d")
		return
	}
	pts, err := s.store.Metrics(time.Now().Add(-dur))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"range": rng, "points": downsample(pts, maxPoints)})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request, d *store.Device) {
	q := r.URL.Query()
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	levels := levelsFrom(q.Get("minLevel"))
	evs, err := s.store.Events(d.ID, before, limit, levels)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(evs))
	for _, e := range evs {
		out = append(out, s.eventJSON(e, d.Lang))
	}
	unseen, _ := s.store.Unseen(d.ID, []string{"warn", "error"})
	writeJSON(w, http.StatusOK, map[string]any{"events": out, "unseen": unseen})
}

func (s *Server) handleSeen(w http.ResponseWriter, r *http.Request, d *store.Device) {
	// ids: the events the app has shown; upTo (older apps): everything up to it.
	var req struct {
		IDs  []int64 `json:"ids"`
		UpTo int64   `json:"upTo"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.IDs) > 500 {
		writeError(w, http.StatusBadRequest, "at most 500 ids")
		return
	}
	err := s.store.MarkSeen(d.ID, req.IDs)
	if err == nil && req.UpTo > 0 {
		err = s.store.MarkSeenUpTo(d.ID, req.UpTo)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	unseen, _ := s.store.Unseen(d.ID, []string{"warn", "error"})
	writeJSON(w, http.StatusOK, map[string]any{"unseen": unseen})
}

func (s *Server) handleGetDevice(w http.ResponseWriter, r *http.Request, d *store.Device) {
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handlePutDevice(w http.ResponseWriter, r *http.Request, d *store.Device) {
	var req struct {
		Name      *string `json:"name"`
		Lang      *string `json:"lang"`
		PushToken *string `json:"pushToken"`
		MinLevel  *string `json:"minLevel"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Name != nil && *req.Name != "" {
		d.Name = limitLen(*req.Name, 80)
	}
	if req.Lang != nil {
		d.Lang = normLang(*req.Lang)
	}
	if req.PushToken != nil {
		d.PushToken = *req.PushToken
	}
	if req.MinLevel != nil {
		switch *req.MinLevel {
		case "info", "warn", "error", "none":
			d.MinLevel = *req.MinLevel
		default:
			writeError(w, http.StatusBadRequest, "minLevel must be info, warn, error or none")
			return
		}
	}
	if err := s.store.UpdateDevice(*d); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request, d *store.Device) {
	if _, err := s.store.DeleteDevice(d.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.hub.dropDevice(d.ID)
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers --------------------------------------------------------------

func (s *Server) eventJSON(e store.Event, lang string) map[string]any {
	return map[string]any{
		"id": e.ID, "time": e.Time, "key": e.Key, "level": e.Level, "code": e.Code,
		"params": e.Params, "seen": e.Seen, "message": i18n.T(lang, e.Code, e.Params),
	}
}

type authedHandler func(http.ResponseWriter, *http.Request, *store.Device)

func (s *Server) auth(h authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		hdr := r.Header.Get("Authorization")
		if len(hdr) <= len(prefix) || hdr[:len(prefix)] != prefix {
			writeError(w, http.StatusUnauthorized, "missing token")
			return
		}
		d, err := s.store.DeviceByToken(HashSecret(hdr[len(prefix):]))
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if d == nil {
			writeError(w, http.StatusUnauthorized, "unknown token")
			return
		}
		s.touch(d.ID)
		h(w, r, d)
	}
}

// touch updates last_seen at most once per minute per device.
func (s *Server) touch(id int64) {
	s.touchMu.Lock()
	last := s.touched[id]
	now := time.Now()
	if now.Sub(last) < time.Minute {
		s.touchMu.Unlock()
		return
	}
	s.touched[id] = now
	s.touchMu.Unlock()
	_ = s.store.TouchDevice(id)
}

// levelsFrom returns the levels at or above min (empty = all).
func levelsFrom(min string) []string {
	switch min {
	case "warn":
		return []string{"warn", "error"}
	case "error":
		return []string{"error"}
	}
	return nil
}

func normLang(l string) string {
	if len(l) >= 2 && i18n.Supported(l[:2]) {
		return l[:2]
	}
	return "en"
}

func limitLen(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// HashSecret is how tokens and pairing codes are stored (never in clear).
func HashSecret(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

type rateLimiter struct {
	mu    sync.Mutex
	max   int
	per   time.Duration
	start time.Time
	n     int
}

func (l *rateLimiter) allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if now.Sub(l.start) > l.per {
		l.start, l.n = now, 0
	}
	l.n++
	return l.n <= l.max
}
