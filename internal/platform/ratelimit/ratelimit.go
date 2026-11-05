// Package ratelimit limita la velocidad de envío (token bucket) para no saturar a Meta ni a Gmail.
package ratelimit

import (
	"context"
	"time"

	"golang.org/x/time/rate"
)

// Limiter espera hasta que se pueda enviar el siguiente mensaje. Es seguro entre goroutines.
type Limiter struct{ l *rate.Limiter }

// PerSecond permite n eventos por segundo (n puede ser fraccionario).
func PerSecond(n float64) *Limiter {
	if n <= 0 {
		return &Limiter{l: rate.NewLimiter(rate.Inf, 1)}
	}
	return &Limiter{l: rate.NewLimiter(rate.Limit(n), 1)}
}

// Every permite un evento cada d (p. ej. 1,5 s entre correos, como el cliente).
func Every(d time.Duration) *Limiter {
	if d <= 0 {
		return &Limiter{l: rate.NewLimiter(rate.Inf, 1)}
	}
	return &Limiter{l: rate.NewLimiter(rate.Every(d), 1)}
}

// Wait bloquea hasta poder enviar o hasta que ctx termine.
func (l *Limiter) Wait(ctx context.Context) error { return l.l.Wait(ctx) }
