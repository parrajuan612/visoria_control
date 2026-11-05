package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// shutdownGrace es el tiempo máximo para terminar peticiones en curso (SDD §3.7).
const shutdownGrace = 30 * time.Second

// serve atiende peticiones en ln hasta que ctx se cancele (SIGTERM/SIGINT) o el
// servidor falle. Al cancelarse deja de aceptar conexiones nuevas y espera hasta
// `grace` a que terminen las peticiones en curso; pasado ese plazo las corta.
func serve(ctx context.Context, srv *http.Server, ln net.Listener, grace time.Duration) error {
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case err := <-errCh: // falló antes de recibir la señal
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	slog.Info("cerrando API: no se aceptan conexiones nuevas", "grace", grace.String())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("la API no terminó a tiempo; se cortan las peticiones restantes", "error", err)
		_ = srv.Close()
		return err
	}
	slog.Info("API cerrada")
	return nil
}

// newHTTPServer crea el servidor con timeouts contra clientes lentos. No se fija
// WriteTimeout porque el stream de progreso (SSE) es de larga duración.
func newHTTPServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second, // subida del Excel
		IdleTimeout:       120 * time.Second,
	}
}
