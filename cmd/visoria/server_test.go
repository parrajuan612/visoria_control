package main

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

// Una petición en curso termina bien aunque llegue la señal; las nuevas se rechazan.
func TestServeCierreOrdenado(t *testing.T) {
	started := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/lenta", func(w http.ResponseWriter, r *http.Request) {
		close(started)
		time.Sleep(400 * time.Millisecond)
		w.Write([]byte("completa"))
	})
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, newHTTPServer("", mux), ln, 5*time.Second) }()

	type res struct {
		body string
		err  error
	}
	resCh := make(chan res, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/lenta")
		if err != nil {
			resCh <- res{err: err}
			return
		}
		defer resp.Body.Close()
		b := make([]byte, 16)
		n, _ := resp.Body.Read(b)
		resCh <- res{body: string(b[:n])}
	}()

	<-started
	cancel() // SIGTERM
	time.Sleep(100 * time.Millisecond)
	if _, err := net.DialTimeout("tcp", ln.Addr().String(), 300*time.Millisecond); err == nil {
		t.Error("tras la señal no debe aceptar conexiones nuevas")
	}
	r := <-resCh
	if r.err != nil || r.body != "completa" {
		t.Errorf("la petición en curso debía terminar bien: %+v", r)
	}
	if err := <-done; err != nil {
		t.Errorf("serve devolvió %v", err)
	}
}

// Si las peticiones no terminan en el plazo, se cortan y se informa.
func TestServeCortaTrasElPlazo(t *testing.T) {
	started := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/eterna", func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	})
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, newHTTPServer("", mux), ln, 200*time.Millisecond) }()
	go http.Get("http://" + ln.Addr().String() + "/eterna")
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Error("debía informar que venció el plazo")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serve no terminó")
	}
}

func TestServeFalloAlEscuchar(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	ln.Close() // listener cerrado: Serve falla de inmediato
	err := serve(context.Background(), newHTTPServer("", http.NewServeMux()), ln, time.Second)
	if err == nil {
		t.Error("debía devolver el error de Serve")
	}
}
