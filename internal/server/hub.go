package server

import (
	"net"
	"sync"
	"time"
)

type Event struct {
	Type string    `json:"type"`
	TS   time.Time `json:"ts"`
	Data any       `json:"data,omitempty"`
}

type ClientEvent struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	RemoteIP string `json:"remote_ip,omitempty"`
}

type MetricsEvent struct {
	ClientID    int64 `json:"client_id"`
	BytesIn     int64 `json:"bytes_in"`
	BytesOut    int64 `json:"bytes_out"`
	ActiveConns int   `json:"active_conns"`
}

type ProxyLogEvent struct {
	ID         int64  `json:"id"`
	TS         int64  `json:"ts"`
	SourceIP   string `json:"source_ip"` // full, unmasked
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

func IsUntrustedPeerIP(ip string) bool {
	if ip == "" {
		return true
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return true
	}
	if parsed.IsLoopback() || parsed.IsUnspecified() || parsed.IsLinkLocalUnicast() || parsed.IsPrivate() {
		return true
	}
	if v4 := parsed.To4(); v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
		return true
	}
	return false
}

func MaskIP(ip string) string {
	if ip == "" {
		return ""
	}
	if lastIndexByte(ip, ':') < 0 {
		dots := 0
		for i := len(ip) - 1; i >= 0; i-- {
			if ip[i] == '.' {
				dots++
				if dots == 2 {
					return ip[:i] + ".0.0"
				}
			}
		}
		return ip
	}
	if lastIndexByte(ip, ':') >= 0 {
		count := 0
		for i := 0; i < len(ip); i++ {
			if ip[i] == ':' {
				count++
				if count == 2 {
					return ip[:i] + "::"
				}
			}
		}
	}
	return ip
}

func lastIndexByte(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c {
			return i
		}
	}
	return -1
}
