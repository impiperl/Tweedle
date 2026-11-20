package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/impiperl/Tweedle/internal/collect"
	"github.com/impiperl/Tweedle/internal/logs"
	"github.com/impiperl/Tweedle/internal/store"
)

// logSince are the time ranges the app can ask for ("" = no limit).
var logSince = map[string]time.Duration{
	"15m": 15 * time.Minute, "1h": time.Hour, "6h": 6 * time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour,
}

const (
	defaultLogEntries = 300
	maxLogEntries     = 2000
	// logScan is how many lines are read from podman when filtering, so that
	// enough matching entries remain.
	logScan = 20000
)

// handleContainerLogs answers GET /api/containers/{name}/logs?since=1h&level=error&q=text&limit=300.
// Only containers listed in the config can be read, and only if logs.enabled.
func (s *Server) handleContainerLogs(w http.ResponseWriter, r *http.Request, d *store.Device) {
	if !s.cfg.Logs.Enabled {
		writeError(w, http.StatusForbidden, "logs disabled")
		return
	}
	name := r.PathValue("name")
	watched := false
	for _, c := range s.cfg.Containers {
		if c.Name == name {
			watched = true
			break
		}
	}
	if !watched {
		writeError(w, http.StatusNotFound, "container not watched")
		return
	}
	q := r.URL.Query()
	sinceKey := q.Get("since")
	if sinceKey == "" {
		sinceKey = "1h"
	}
	var since time.Time
	if sinceKey != "all" {
		dur, ok := logSince[sinceKey]
		if !ok {
			writeError(w, http.StatusBadRequest, "since must be 15m, 1h, 6h, 24h, 7d or all")
			return
		}
		since = time.Now().Add(-dur)
	}
	level := q.Get("level")
	switch level {
	case "", "all", "warn", "error":
	default:
		writeError(w, http.StatusBadRequest, "level must be all, warn or error")
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = defaultLogEntries
	}
	limit = min(limit, maxLogEntries)
	search := limitLen(q.Get("q"), 200)

	scan := min(limit*4, logScan) // room for stack trace lines
	if (level != "" && level != "all") || search != "" {
		scan = logScan
	}
	lines, err := s.fetchLogs(s.cfg.PodmanCmd, name, since, scan)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	entries := logs.Group(lines, s.redactor)
	entries, truncated := logs.Filter(entries, level, search, limit)
	if entries == nil {
		entries = []logs.Entry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"container": name, "entries": entries, "truncated": truncated || len(lines) >= scan,
	})
}

// fetchLogsFunc is collect.ContainerLogs (replaced in tests).
type fetchLogsFunc func(cmd []string, name string, since time.Time, tail int) ([]collect.LogLine, error)
