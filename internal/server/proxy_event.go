package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/butialabs/proxywi/internal/storage"
)

type proxyEventSink struct {
	Store *storage.Store
	Log   *slog.Logger
	Hub   *Hub
}

func (s proxyEventSink) record(ctx context.Context, ev storage.ProxyEvent) {
	if ev.TS.IsZero() {
		ev.TS = time.Now()
	}
	id, err := s.Store.InsertProxyEvent(ctx, ev)
	if err != nil {
		s.Log.Warn("proxy event insert", "err", err)
		return
	}
	if s.Hub == nil {
		return
	}
	s.Hub.Publish(Event{
		Type: "proxy_event",
		Data: ProxyLogEvent{
			ID:         id,
			TS:         ev.TS.Unix(),
			User:       ev.Username,
			ClientID:   ev.ClientID,
			ClientName: ev.ClientName,
			Target:     ev.TargetHost,
			Protocol:   ev.Protocol,
			BytesIn:    ev.BytesIn,
			BytesOut:   ev.BytesOut,
			DurationMS: ev.DurationMS,
			ConnectMS:  ev.ConnectMS,
			Outcome:    ev.Outcome,
		},
	})
}
