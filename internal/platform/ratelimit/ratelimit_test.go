package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestEsperaEntreEventos(t *testing.T) {
	l := Every(80 * time.Millisecond)
	ctx := context.Background()
	start := time.Now()
	for i := 0; i < 4; i++ {
		if err := l.Wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d < 220*time.Millisecond { // 1.º inmediato + 3 esperas de ~80 ms
		t.Errorf("tardó %v: no limita", d)
	}
}

func TestSinLimiteYCancelacion(t *testing.T) {
	start := time.Now()
	for _, l := range []*Limiter{PerSecond(0), Every(0)} {
		for i := 0; i < 100; i++ {
			l.Wait(context.Background())
		}
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Error("sin límite no debe esperar")
	}
	l := PerSecond(0.5) // 1 cada 2 s
	l.Wait(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := l.Wait(ctx); err == nil {
		t.Error("debe respetar la cancelación")
	}
}
