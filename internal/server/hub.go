package server

import (
	"sync"
	"time"
)

type Event struct {
	Type string    `json:"type"`
	TS   time.Time `json:"ts"`
	Data any       `json:"data,omitempty"`
}

type ClientEvent struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type MetricsEvent struct {
	ClientID int64 `json:"client_id"`
	BytesIn  int64 `json:"bytes_in"`
	BytesOut int64 `json:"bytes_out"`
}

type ProxyLogEvent struct {
	ID         int64  `json:"id"`
	TS         int64  `json:"ts"`
	Origin     string `json:"origin"` // short opaque origin key, never a raw IP
	User       string `json:"user"`
	ClientID   int64  `json:"client_id"`
	ClientName string `json:"client_name"`
	Target     string `json:"target"`
	Protocol   string `json:"protocol"`
	BytesIn    int64  `json:"bytes_in"`
	BytesOut   int64  `json:"bytes_out"`
	DurationMS int64  `json:"duration_ms"`
	Outcome    string `json:"outcome"` // ok | denied | failed
}

type Hub struct {
	mu   sync.RWMutex
	subs map[chan Event]struct{}
}

func NewHub() *Hub {
	return &Hub{subs: map[chan Event]struct{}{}}
}

func (h *Hub) Publish(e Event) {
	if e.TS.IsZero() {
		e.TS = time.Now()
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

func (h *Hub) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
		close(ch)
	}
}
