// Package logx configures structured JSON logging (log/slog) and carries
// request-scoped attributes through context.Context.
package logx

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

type ctxKey struct{}

// New builds a JSON logger for service, installs it as the slog default and
// returns it.
func New(service, level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	l := slog.New(h).With("service", service)
	slog.SetDefault(l)
	return l
}

// With returns a copy of ctx whose logger carries the extra attributes.
func With(ctx context.Context, args ...any) context.Context {
	return context.WithValue(ctx, ctxKey{}, From(ctx).With(args...))
}

// From returns the logger stored in ctx, or the default logger.
func From(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// RedactEmail keeps the first character and the domain: "j***@example.com".
func RedactEmail(email string) string {
	at := strings.LastIndexByte(email, '@')
	if at <= 0 {
		return "***"
	}
	return email[:1] + "***" + email[at:]
}
