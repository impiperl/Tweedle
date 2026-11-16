// Package push sends events as push messages through the Expo Push Service.
package push

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/impiperl/Tweedle/internal/config"
	"github.com/impiperl/Tweedle/internal/i18n"
	"github.com/impiperl/Tweedle/internal/store"
)

const (
	sendURL     = "https://exp.host/--/api/v2/push/send"
	receiptsURL = "https://exp.host/--/api/v2/push/getReceipts"
	// More events than this for one device in one burst become one summary.
	maxSingle = 3
	batchSize = 100 // Expo limit per request
)

// Message is one Expo push message.
type Message struct {
	To                string         `json:"to"`
	Title             string         `json:"title"`
	Body              string         `json:"body"`
	Sound             any            `json:"sound"` // "default" or null
	Priority          string         `json:"priority"`
	ChannelID         string         `json:"channelId"`
	InterruptionLevel string         `json:"interruptionLevel,omitempty"`
	Data              map[string]any `json:"data"`
}

type ticket struct {
	Status  string `json:"status"`
	ID      string `json:"id"`
	Message string `json:"message"`
	Details struct {
		Error string `json:"error"`
	} `json:"details"`
}

type pendingTicket struct {
	token string
	sent  time.Time
}

// Expo collects events and sends them to the paired devices.
type Expo struct {
	cfg   *config.Config
	store *store.Store

	SendURL, ReceiptsURL string
	Client               *http.Client
	ReceiptDelay         time.Duration

	queue chan store.Event

	mu      sync.Mutex
	pending map[string]pendingTicket // ticket id → push token
}

func New(cfg *config.Config, st *store.Store) *Expo {
	return &Expo{
		cfg: cfg, store: st,
		SendURL: sendURL, ReceiptsURL: receiptsURL,
		Client:       &http.Client{Timeout: 20 * time.Second},
		ReceiptDelay: 15 * time.Minute,
		queue:        make(chan store.Event, 256),
		pending:      map[string]pendingTicket{},
	}
}

// Enqueue hands over a stored event. It never blocks the monitor.
func (e *Expo) Enqueue(ev store.Event) {
	if !e.cfg.Push.Enabled {
		return
	}
	select {
	case e.queue <- ev:
	default:
		log.Printf("push: queue full, dropping event %d", ev.ID)
	}
}

// Run sends queued events until ctx is cancelled. Events arriving within
// Debounce of each other are sent together.
func (e *Expo) Run(ctx context.Context) {
	receipts := time.NewTicker(5 * time.Minute)
	defer receipts.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-receipts.C:
			e.checkReceipts(ctx)
		case ev := <-e.queue:
			batch := []store.Event{ev}
			timer := time.NewTimer(e.cfg.Push.Debounce)
		collect:
			for {
				select {
				case ev := <-e.queue:
					batch = append(batch, ev)
				case <-timer.C:
					break collect
				case <-ctx.Done():
					timer.Stop()
					return
				}
			}
			e.sendEvents(ctx, batch)
		}
	}
}

// sendEvents builds the messages for every device and sends them.
func (e *Expo) sendEvents(ctx context.Context, events []store.Event) {
	devices, err := e.store.Devices()
	if err != nil {
		log.Printf("push: devices: %v", err)
		return
	}
	var msgs []Message
	for _, d := range devices {
		if d.PushToken == "" {
			continue
		}
		var mine []store.Event
		for _, ev := range events {
			if wants(d.MinLevel, ev.Level) {
				mine = append(mine, ev)
			}
		}
		msgs = append(msgs, e.messagesFor(d, mine)...)
	}
	if len(msgs) > 0 {
		e.send(ctx, msgs)
	}
}

func (e *Expo) messagesFor(d store.Device, evs []store.Event) []Message {
	if len(evs) == 0 {
		return nil
	}
	if len(evs) <= maxSingle {
		out := make([]Message, 0, len(evs))
		for _, ev := range evs {
			out = append(out, e.message(d, ev.Level, i18n.T(d.Lang, ev.Code, ev.Params),
				map[string]any{"eventId": ev.ID, "key": ev.Key}))
		}
		return out
	}
	// Summary: worst level first, then oldest.
	sorted := append([]store.Event(nil), evs...)
	sort.SliceStable(sorted, func(i, j int) bool { return rank(sorted[i].Level) > rank(sorted[j].Level) })
	body := i18n.T(d.Lang, "push_summary", map[string]string{
		"n": strconv.Itoa(len(evs)), "first": i18n.T(d.Lang, sorted[0].Code, sorted[0].Params),
	})
	return []Message{e.message(d, sorted[0].Level, body, map[string]any{"eventId": sorted[0].ID, "count": len(evs)})}
}

func (e *Expo) message(d store.Device, level, body string, data map[string]any) Message {
	title := i18n.T(d.Lang, "push_title", map[string]string{
		"server": e.cfg.Name, "level": i18n.T(d.Lang, "level_"+level, nil),
	})
	m := Message{To: d.PushToken, Title: title, Body: body, ChannelID: level, Data: data}
	data["server"] = e.cfg.Name
	data["url"] = e.cfg.PublicURL
	data["level"] = level
	switch level {
	case "error":
		m.Sound, m.Priority, m.InterruptionLevel = "default", "high", "time-sensitive"
	case "warn":
		m.Sound, m.Priority, m.InterruptionLevel = "default", "high", "active"
	default:
		m.Sound, m.Priority, m.InterruptionLevel = nil, "normal", "passive"
	}
	return m
}

// SendTest sends a test message to every device with a push token.
func (e *Expo) SendTest(ctx context.Context) (sent, failed, skipped int, err error) {
	devices, err := e.store.Devices()
	if err != nil {
		return 0, 0, 0, err
	}
	var msgs []Message
	for _, d := range devices {
		if d.PushToken == "" {
			skipped++
			continue
		}
		msgs = append(msgs, e.message(d, "info", i18n.T(d.Lang, "push_test", nil), map[string]any{"test": true}))
	}
	if len(msgs) == 0 {
		return 0, 0, skipped, nil
	}
	ok, bad := e.send(ctx, msgs)
	return ok, bad, skipped, nil
}

// send posts messages in batches with a few retries and handles the tickets.
func (e *Expo) send(ctx context.Context, msgs []Message) (ok, failed int) {
	for start := 0; start < len(msgs); start += batchSize {
		end := min(start+batchSize, len(msgs))
		batch := msgs[start:end]
		var resp struct {
			Data []ticket `json:"data"`
		}
		var err error
		for attempt := 0; attempt < 3; attempt++ {
			if attempt > 0 {
				select {
				case <-ctx.Done():
					return ok, failed + len(batch)
				case <-time.After(time.Duration(attempt*attempt) * 2 * time.Second):
				}
			}
			if err = e.post(ctx, e.SendURL, batch, &resp); err == nil {
				break
			}
		}
		if err != nil {
			log.Printf("push: send failed: %v", err)
			failed += len(batch)
			continue
		}
		for i, t := range resp.Data {
			if i >= len(batch) {
				break
			}
			if t.Status == "ok" {
				ok++
				e.mu.Lock()
				e.pending[t.ID] = pendingTicket{token: batch[i].To, sent: time.Now()}
				e.mu.Unlock()
				continue
			}
			failed++
			e.handleError(batch[i].To, t.Details.Error, t.Message)
		}
	}
	return ok, failed
}

func (e *Expo) handleError(token, code, msg string) {
	if code == "DeviceNotRegistered" {
		log.Printf("push: device not registered any more, removing push token")
		if err := e.store.ClearPushToken(token); err != nil {
			log.Printf("push: clear token: %v", err)
		}
		return
	}
	log.Printf("push: %s %s", code, msg)
}

// checkReceipts asks Expo about tickets older than ReceiptDelay; this is
// where errors such as DeviceNotRegistered show up after delivery.
func (e *Expo) checkReceipts(ctx context.Context) {
	e.mu.Lock()
	ids := []string{}
	tokens := map[string]string{}
	for id, p := range e.pending {
		if time.Since(p.sent) >= e.ReceiptDelay {
			ids = append(ids, id)
			tokens[id] = p.token
			delete(e.pending, id)
		}
	}
	e.mu.Unlock()
	for start := 0; start < len(ids); start += 1000 {
		end := min(start+1000, len(ids))
		var resp struct {
			Data map[string]ticket `json:"data"`
		}
		if err := e.post(ctx, e.ReceiptsURL, map[string]any{"ids": ids[start:end]}, &resp); err != nil {
			log.Printf("push: receipts: %v", err)
			return
		}
		for id, r := range resp.Data {
			if r.Status == "error" {
				e.handleError(tokens[id], r.Details.Error, r.Message)
			}
		}
	}
}

func (e *Expo) post(ctx context.Context, url string, body, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if e.cfg.Push.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+e.cfg.Push.AccessToken)
	}
	resp, err := e.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("expo: HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// wants reports whether a device with minLevel gets events of level.
func wants(minLevel, level string) bool {
	switch minLevel {
	case "none":
		return false
	case "info":
		return true
	case "error":
		return level == "error"
	default: // warn
		return level == "warn" || level == "error"
	}
}

func rank(level string) int {
	switch level {
	case "error":
		return 3
	case "warn":
		return 2
	}
	return 1
}
