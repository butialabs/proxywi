package gui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/butialabs/proxywi/internal/server"
)

func (g *GUI) stream(w http.ResponseWriter, r *http.Request, accept map[string]bool) {
	if g.Hub == nil {
		http.Error(w, "realtime hub not configured", http.StatusServiceUnavailable)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // for nginx/caddy
	w.WriteHeader(http.StatusOK)

	sub, cancel := g.Hub.Subscribe()
	defer cancel()

	fmt.Fprintf(w, ": ok\n\n")
	flusher.Flush()

	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()

	enc := json.NewEncoder(w)
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			if _, err := fmt.Fprintf(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case evt, ok := <-sub:
			if !ok {
				return
			}
			if accept != nil && !accept[evt.Type] {
				continue
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: ", evt.Type); err != nil {
				return
			}
			if err := enc.Encode(envelope(evt)); err != nil {
				return
			}
			if _, err := fmt.Fprintf(w, "\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func envelope(e server.Event) map[string]any {
	return map[string]any{
		"type": e.Type,
		"ts":   e.TS.Unix(),
		"data": e.Data,
	}
}

func (g *GUI) eventsDashboard(w http.ResponseWriter, r *http.Request) {
	g.stream(w, r, map[string]bool{
		"client_online":  true,
		"client_offline": true,
		"metrics":        true,
	})
}

func (g *GUI) eventsLogs(w http.ResponseWriter, r *http.Request) {
	g.stream(w, r, map[string]bool{"proxy_event": true})
}
