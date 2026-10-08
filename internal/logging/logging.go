package logging

import (
	"context"
	"io"
	"log/slog"
	"regexp"
	"strings"
)

var telegramToken = regexp.MustCompile(`[0-9]{5,}:[A-Za-z0-9_-]{20,}`)
var urlCredentials = regexp.MustCompile(`(://)[^/@\s]+@`)

// Redact also applies to formatted errors/stack traces and arbitrary slog attributes.
func Redact(s string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "[REDACTED]")
		}
	}
	s = telegramToken.ReplaceAllString(s, "[REDACTED]")
	return urlCredentials.ReplaceAllString(s, "${1}[REDACTED]@")
}

type handler struct {
	next    slog.Handler
	secrets []string
}

func New(w io.Writer, level slog.Level, secrets ...string) *slog.Logger {
	return slog.New(&handler{next: slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}), secrets: secrets})
}
func (h *handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}
func (h *handler) clean(a slog.Attr) slog.Attr {
	a.Key = Redact(a.Key, h.secrets...)
	a.Value = a.Value.Resolve()
	if a.Value.Kind() == slog.KindGroup {
		original := a.Value.Group()
		attrs := make([]slog.Attr, len(original))
		for i := range original {
			attrs[i] = h.clean(original[i])
		}
		a.Value = slog.GroupValue(attrs...)
	} else if a.Value.Kind() == slog.KindString {
		a.Value = slog.StringValue(Redact(a.Value.String(), h.secrets...))
	} else if a.Value.Kind() == slog.KindAny {
		a.Value = slog.StringValue(Redact(a.Value.String(), h.secrets...))
	}
	return a
}
func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	clean := slog.NewRecord(r.Time, r.Level, Redact(r.Message, h.secrets...), r.PC)
	r.Attrs(func(a slog.Attr) bool { clean.AddAttrs(h.clean(a)); return true })
	return h.next.Handle(ctx, clean)
}
func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		clean[i] = h.clean(a)
	}
	return &handler{next: h.next.WithAttrs(clean), secrets: h.secrets}
}
func (h *handler) WithGroup(name string) slog.Handler {
	return &handler{next: h.next.WithGroup(Redact(name, h.secrets...)), secrets: h.secrets}
}
