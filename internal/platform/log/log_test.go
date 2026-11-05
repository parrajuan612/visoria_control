package log

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestContextoHeredaCampos(t *testing.T) {
	var buf bytes.Buffer
	ctx := WithContext(context.Background(), New(&buf, slog.LevelInfo))
	ctx = With(ctx, "request_id", "abc")
	FromContext(ctx).Info("hola", "delivery_key", "k1")

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("no es JSON: %v (%s)", err, buf.String())
	}
	if got["request_id"] != "abc" || got["delivery_key"] != "k1" || got["msg"] != "hola" {
		t.Errorf("campos inesperados: %v", got)
	}
}

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{"debug": slog.LevelDebug, "WARN": slog.LevelWarn, "error": slog.LevelError, "": slog.LevelInfo, "x": slog.LevelInfo}
	for in, want := range cases {
		if got := ParseLevel(in); got != want {
			t.Errorf("ParseLevel(%q)=%v", in, got)
		}
	}
}

func TestFromContextSinLogger(t *testing.T) {
	if FromContext(context.Background()) == nil {
		t.Fatal("debe devolver el logger por defecto")
	}
}
