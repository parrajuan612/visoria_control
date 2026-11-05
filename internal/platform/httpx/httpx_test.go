package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTimeoutYTransportCompartido(t *testing.T) {
	a, b := New(ShortTimeout), New(LongTimeout)
	if a.Timeout != ShortTimeout || b.Timeout != LongTimeout {
		t.Fatal("timeouts incorrectos")
	}
	if a.Transport != b.Transport {
		t.Fatal("deben compartir Transport")
	}
}

func TestExpiraPorTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
	}))
	defer srv.Close()
	if _, err := New(50 * time.Millisecond).Get(srv.URL); err == nil {
		t.Fatal("se esperaba timeout")
	}
}
