// Package log centraliza el logger estructurado (slog JSON). Los componentes
// obtienen el logger del contexto para heredar campos como request_id.
package log

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
)

type ctxKey struct{}

// ParseLevel traduce "debug|info|warn|error" (por defecto info).
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// New crea un logger JSON hacia w.
func New(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
}

// Setup crea el logger JSON de la app (stdout) y lo deja como logger por defecto.
func Setup(level slog.Level) *slog.Logger {
	l := New(os.Stdout, level)
	slog.SetDefault(l)
	return l
}

// WithContext guarda el logger en el contexto.
func WithContext(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext devuelve el logger del contexto o el logger por defecto.
func FromContext(ctx context.Context) *slog.Logger {
	if ctx != nil {
		if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
			return l
		}
	}
	return slog.Default()
}

// With agrega atributos (p. ej. campaign_id, delivery_key) al logger del contexto.
func With(ctx context.Context, args ...any) context.Context {
	return WithContext(ctx, FromContext(ctx).With(args...))
}
