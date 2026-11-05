package chatwoot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

type fake struct {
	mu         sync.Mutex
	searches   int
	contactsMk int
	convsMk    int
	notes      []map[string]any
	rawQuery   string
	convs      string // JSON de payload de conversaciones
	searchResp string
	token      string
}

func (f *fake) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/accounts/1/contacts/search", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.searches++
		f.rawQuery = r.URL.RawQuery
		f.token = r.Header.Get("api_access_token")
		w.Write([]byte(f.searchResp))
	})
	mux.HandleFunc("/api/v1/accounts/1/contacts", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.contactsMk++
		f.mu.Unlock()
		w.Write([]byte(`{"payload":{"contact":{"id":77}}}`))
	})
	mux.HandleFunc("/api/v1/accounts/1/contacts/77/conversations", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(f.convs))
	})
	mux.HandleFunc("/api/v1/accounts/1/conversations", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.convsMk++
		f.mu.Unlock()
		w.Write([]byte(`{"id":900}`))
	})
	mux.HandleFunc("/api/v1/accounts/1/conversations/", func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		json.NewDecoder(r.Body).Decode(&b)
		b["_path"] = r.URL.Path
		f.mu.Lock()
		f.notes = append(f.notes, b)
		f.mu.Unlock()
		w.Write([]byte(`{}`))
	})
	return mux
}

func setup(t *testing.T, f *fake) *Client {
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	return New(srv.URL, "1", 2, "tok", srv.Client())
}

func TestCreaContactoYConversacionYNotaPrivada(t *testing.T) {
	f := &fake{searchResp: `{"payload":[]}`, convs: `{"payload":[]}`}
	c := setup(t, f)
	if err := c.RegisterNote(context.Background(), "+57 300-111 2233", "Jugador", "hola"); err != nil {
		t.Fatal(err)
	}
	if f.contactsMk != 1 || f.convsMk != 1 || len(f.notes) != 1 {
		t.Fatalf("contactos=%d convs=%d notas=%d", f.contactsMk, f.convsMk, len(f.notes))
	}
	n := f.notes[0]
	if n["private"] != true || n["message_type"] != "outgoing" || n["_path"] != "/api/v1/accounts/1/conversations/900/messages" {
		t.Errorf("nota inesperada: %v", n)
	}
	if f.token != "tok" {
		t.Error("falta api_access_token")
	}
	if f.rawQuery != "q=573001112233" {
		t.Errorf("q mal codificado: %s", f.rawQuery)
	}
}

func TestUsaContactoExactoYConversacionAbiertaDelInbox(t *testing.T) {
	f := &fake{
		// 1º es coincidencia parcial de otro número; 2º es el exacto.
		searchResp: `{"payload":[{"id":5,"phone_number":"+573001112299"},{"id":77,"phone_number":"+573001112233"}]}`,
		convs:      `{"payload":[{"id":1,"inbox_id":9,"status":"open"},{"id":2,"inbox_id":2,"status":"resolved"},{"id":3,"inbox_id":2,"status":"open"}]}`,
	}
	c := setup(t, f)
	if err := c.RegisterNote(context.Background(), "573001112233", "x", "n"); err != nil {
		t.Fatal(err)
	}
	if f.contactsMk != 0 || f.convsMk != 0 {
		t.Errorf("no debía crear nada: %d/%d", f.contactsMk, f.convsMk)
	}
	if got := f.notes[0]["_path"]; got != "/api/v1/accounts/1/conversations/3/messages" {
		t.Errorf("conversación equivocada: %v", got)
	}
}

func TestCacheDeContactos(t *testing.T) {
	f := &fake{searchResp: `{"payload":[]}`, convs: `{"payload":[]}`}
	c := setup(t, f)
	for i := 0; i < 3; i++ {
		if err := c.RegisterNote(context.Background(), "573001112233", "x", "n"); err != nil {
			t.Fatal(err)
		}
	}
	if f.searches != 1 || f.contactsMk != 1 {
		t.Errorf("búsquedas=%d creaciones=%d, esperaba 1/1", f.searches, f.contactsMk)
	}
}

func TestErrorHTTPNoFiltraCuerpo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"secreto":"dato personal"}`))
	}))
	defer srv.Close()
	err := New(srv.URL, "1", 2, "mal", srv.Client()).RegisterNote(context.Background(), "573001112233", "x", "n")
	if err == nil {
		t.Fatal("se esperaba error")
	}
	if got := err.Error(); got != "chatwoot buscar contacto: HTTP 401" {
		t.Errorf("mensaje = %q", got)
	}
}
