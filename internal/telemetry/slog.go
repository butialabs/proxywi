package telemetry

import (
	"context"
	"log/slog"

	"github.com/getsentry/sentry-go"
)

type SlogHandler struct {
	next slog.Handler
}

func WrapSlog(next slog.Handler) *SlogHandler {
	return &SlogHandler{next: next}
}

func (h *SlogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *SlogHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelError && sentry.CurrentHub().Client() != nil {
		reportRecord(r)
	}
	return h.next.Handle(ctx, r)
}

func (h *SlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &SlogHandler{next: h.next.WithAttrs(attrs)}
}

func (h *SlogHandler) WithGroup(name string) slog.Handler {
	return &SlogHandler{next: h.next.WithGroup(name)}
}

func reportRecord(r slog.Record) {
	attrs := make(map[string]any, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()
		return true
	})

	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetLevel(sentry.LevelError)
		if err, ok := attrs["err"].(error); ok {
			delete(attrs, "err")
			if len(attrs) > 0 {
				scope.SetContext("log_attrs", attrs)
			}
			scope.SetFingerprint([]string{r.Message})
			sentry.CaptureException(err)
			return
		}
		if len(attrs) > 0 {
			scope.SetContext("log_attrs", attrs)
		}
		sentry.CaptureMessage(r.Message)
	})
}
