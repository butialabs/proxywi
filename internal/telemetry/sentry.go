package telemetry

import (
	"log/slog"
	"time"

	"github.com/getsentry/sentry-go"
)

func Init(dsn, environment, release string) (func(), error) {
	if dsn == "" {
		return func() {}, nil
	}
	err := sentry.Init(sentry.ClientOptions{
		Dsn:              dsn,
		Environment:      environment,
		Release:          release,
		AttachStacktrace: true,
	})
	if err != nil {
		return func() {}, err
	}
	return func() { sentry.Flush(2 * time.Second) }, nil
}

func Recover(recovered any) {
	sentry.CurrentHub().Recover(recovered)
	sentry.Flush(2 * time.Second)
}

func RecoverAndLog(log *slog.Logger, msg string) {
	if r := recover(); r != nil {
		Recover(r)
		log.Error(msg, "panic", r)
	}
}
