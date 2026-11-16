package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/impiperl/Tweedle/internal/store"
)

type client struct {
	lang string
	send chan []byte
}

type hub struct {
	mu      sync.Mutex
	clients map[*client]bool
}

func newHub() *hub { return &hub{clients: map[*client]bool{}} }

// broadcast sends a message built per client (language may differ). Slow
// clients that cannot keep up are skipped for this message.
func (h *hub) broadcast(build func(*client) any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		data, err := json.Marshal(build(c))
		if err != nil {
			continue
		}
		select {
		case c.send <- data:
		default:
		}
	}
}

func (h *hub) add(c *client)    { h.mu.Lock(); h.clients[c] = true; h.mu.Unlock() }
func (h *hub) remove(c *client) { h.mu.Lock(); delete(h.clients, c); h.mu.Unlock() }

// handleLive is a WebSocket that pushes {"type":"status"} on every tick and
// {"type":"event"} for every new event. The app only listens.
func (s *Server) handleLive(w http.ResponseWriter, r *http.Request, d *store.Device) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	c := &client{lang: d.Lang, send: make(chan []byte, 16)}
	s.hub.add(c)
	defer s.hub.remove(c)

	ctx := conn.CloseRead(r.Context()) // we do not expect messages; detects close
	if s.mon != nil {
		if snap := s.mon.Snapshot(); snap != nil {
			data, _ := json.Marshal(map[string]any{"type": "status", "status": snap})
			c.send <- data
		}
	}
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case data := <-c.send:
			wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Write(wctx, websocket.MessageText, data)
			cancel()
			if err != nil {
				return
			}
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Ping(pctx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}
