package whatsapp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"visoria-control/internal/ports"
)

type rec struct {
	method, path, auth, ctype string
	body                      []byte
}

func server(t *testing.T, status int, resp string, seen *[]rec) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*seen = append(*seen, rec{r.Method, r.URL.RequestURI(), r.Header.Get("Authorization"), r.Header.Get("Content-Type"), b})
		w.WriteHeader(status)
		w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	return NewClient(Options{Token: "SECRETO-TOKEN", PhoneID: "12345", APIVersion: "v19.0", BaseURL: srv.URL})
}

func TestSendTemplateArmaElMensajeConElPDFPorMediaID(t *testing.T) {
	var seen []rec
	c := server(t, 200, `{"messaging_product":"whatsapp","messages":[{"id":"wamid.HBgM123"}]}`, &seen)
	wamid, err := c.SendTemplate(context.Background(), "573209323449", ports.TemplateMessage{
		Name: "purchase_receipt_3", Language: "es_CO", MediaID: "MEDIA9", DocFilename: "JUAN - BECA 50.pdf",
		BodyParams: []string{"María  Pérez\n", "50", "   "}})
	if err != nil || wamid != "wamid.HBgM123" {
		t.Fatalf("%q %v", wamid, err)
	}
	r := seen[0]
	if r.method != "POST" || r.path != "/v19.0/12345/messages" || r.auth != "Bearer SECRETO-TOKEN" || !strings.HasPrefix(r.ctype, "application/json") {
		t.Errorf("petición: %+v", r)
	}
	var p struct {
		Messaging string `json:"messaging_product"`
		To        string `json:"to"`
		Type      string `json:"type"`
		Template  struct {
			Name     string            `json:"name"`
			Language map[string]string `json:"language"`
			Comps    []struct {
				Type   string `json:"type"`
				Params []struct {
					Type     string            `json:"type"`
					Text     string            `json:"text"`
					Document map[string]string `json:"document"`
				} `json:"parameters"`
			} `json:"components"`
		} `json:"template"`
	}
	if err := json.Unmarshal(r.body, &p); err != nil {
		t.Fatal(err)
	}
	if p.Messaging != "whatsapp" || p.To != "573209323449" || p.Type != "template" || p.Template.Name != "purchase_receipt_3" || p.Template.Language["code"] != "es_CO" {
		t.Errorf("cabecera: %+v", p)
	}
	h, b := p.Template.Comps[0], p.Template.Comps[1]
	if h.Type != "header" || h.Params[0].Type != "document" || h.Params[0].Document["id"] != "MEDIA9" || h.Params[0].Document["filename"] != "JUAN - BECA 50.pdf" {
		t.Errorf("encabezado: %+v", h)
	}
	// Meta rechaza saltos de línea, espacios repetidos y parámetros vacíos: se limpian.
	if b.Type != "body" || b.Params[0].Text != "María Pérez" || b.Params[1].Text != "50" || b.Params[2].Text != "-" {
		t.Errorf("cuerpo: %+v", b.Params)
	}
	if strings.Contains(string(r.body), "link") {
		t.Error("ya no se envía un enlace público al PDF")
	}
}

func TestUploadMedia(t *testing.T) {
	var seen []rec
	c := server(t, 200, `{"id":"MEDIA777"}`, &seen)
	id, err := c.UploadMedia(context.Background(), `Ana "X"\n - BECA 30.pdf`, []byte("%PDF-1.4 contenido"))
	if err != nil || id != "MEDIA777" {
		t.Fatalf("%q %v", id, err)
	}
	r := seen[0]
	if r.path != "/v19.0/12345/media" || r.auth != "Bearer SECRETO-TOKEN" {
		t.Errorf("petición: %+v", r)
	}
	mt, params, _ := mime.ParseMediaType(r.ctype)
	if mt != "multipart/form-data" {
		t.Fatalf("content-type %q", r.ctype)
	}
	mr := multipart.NewReader(strings.NewReader(string(r.body)), params["boundary"])
	fields := map[string]string{}
	var fileCT, fileName string
	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}
		b, _ := io.ReadAll(part)
		if part.FormName() == "file" {
			fields["file"], fileCT, fileName = string(b), part.Header.Get("Content-Type"), part.FileName()
		} else {
			fields[part.FormName()] = string(b)
		}
	}
	if fields["messaging_product"] != "whatsapp" || fields["type"] != "application/pdf" || fields["file"] != "%PDF-1.4 contenido" || fileCT != "application/pdf" {
		t.Errorf("campos: %v ct=%s", fields, fileCT)
	}
	if strings.ContainsAny(fileName, "\"\r\n") {
		t.Errorf("el nombre del archivo no debe poder inyectar cabeceras: %q", fileName)
	}
	// sin id → incierto, se reintenta
	c2 := server(t, 200, `{}`, new([]rec))
	if _, err := c2.UploadMedia(context.Background(), "a.pdf", []byte("x")); err == nil {
		t.Error("sin id debe fallar")
	}
}

func TestCheckToken(t *testing.T) {
	var seen []rec
	c := server(t, 200, `{"id":"12345","display_phone_number":"+57 320"}`, &seen)
	if err := c.CheckToken(context.Background()); err != nil || seen[0].method != "GET" || !strings.HasPrefix(seen[0].path, "/v19.0/12345?fields=") {
		t.Errorf("%v %+v", err, seen)
	}
	c = server(t, 401, `{"error":{"message":"Invalid OAuth access token.","type":"OAuthException","code":190}}`, new([]rec))
	var pe *ports.ProviderError
	if err := c.CheckToken(context.Background()); !errors.As(err, &pe) || pe.Action != ports.ActionPause {
		t.Errorf("token vencido: %v", err)
	}
}

// Clasificación de errores de Meta (SDD §3.5.2).
func TestClasificaLosErroresDeMeta(t *testing.T) {
	cases := []struct {
		name   string
		status int
		code   int
		want   ports.ErrorAction
		sub    string
	}{
		{"número sin WhatsApp", 400, 131026, ports.ActionFail, "no tiene WhatsApp"},
		{"ventana 24 h", 400, 131047, ports.ActionFail, "no pudo entregar"},
		{"tipo no soportado", 400, 131051, ports.ActionFail, "no pudo entregar"},
		{"calidad (no entregado)", 400, 131049, ports.ActionFail, "calidad"},
		{"plantilla inexistente", 400, 132001, ports.ActionPause, "plantilla"},
		{"parámetros", 400, 132000, ports.ActionPause, "plantilla"},
		{"formato de parámetro", 400, 132012, ports.ActionPause, "plantilla"},
		{"plantilla pausada", 400, 132015, ports.ActionPause, "plantilla"},
		{"token inválido (190)", 400, 190, ports.ActionPause, "token"},
		{"HTTP 401", 401, 0, ports.ActionPause, "token"},
		{"sesión", 400, 102, ports.ActionPause, "token"},
		{"permisos", 403, 200, ports.ActionPause, "permisos"},
		{"límite de calidad", 400, 131048, ports.ActionPause, "calidad"},
		{"media", 400, 131053, ports.ActionRetry, "PDF"},
		{"ritmo (130429)", 400, 130429, ports.ActionRetry, "más despacio"},
		{"ritmo par (131056)", 400, 131056, ports.ActionRetry, "más despacio"},
		{"ritmo (80007)", 400, 80007, ports.ActionRetry, "más despacio"},
		{"HTTP 429", 429, 0, ports.ActionRetry, "más despacio"},
		{"servicio", 400, 131016, ports.ActionRetry, "temporal"},
		{"HTTP 503", 503, 0, ports.ActionRetry, "temporal"},
		{"HTTP 500 sin cuerpo", 500, 0, ports.ActionRetry, "temporal"},
		{"parámetro inválido", 400, 100, ports.ActionFail, "rechazó los datos"},
		{"desconocido 4xx", 400, 999999, ports.ActionFail, "rechazó"},
	}
	for _, c := range cases {
		body := ""
		if c.code != 0 {
			body = `{"error":{"message":"msg de Meta","type":"X","code":` + itoa(c.code) + `,"error_data":{"details":"detalle técnico"},"fbtrace_id":"abc"}}`
		}
		cl := server(t, c.status, body, new([]rec))
		_, err := cl.SendTemplate(context.Background(), "573209323449", ports.TemplateMessage{Name: "t", Language: "es_CO", MediaID: "m"})
		var pe *ports.ProviderError
		if !errors.As(err, &pe) || pe.Action != c.want || !strings.Contains(pe.Message, c.sub) {
			t.Errorf("%s: %+v", c.name, err)
			continue
		}
		if strings.Contains(pe.Message+pe.Raw, "SECRETO-TOKEN") {
			t.Errorf("%s: el token no debe aparecer en el error", c.name)
		}
		if c.want == ports.ActionRetry && c.code == 130429 && pe.RetryAfter != time.Minute {
			t.Errorf("%s: debe pedir esperar", c.name)
		}
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestRespuestasInciertasNoSeReintentan(t *testing.T) {
	for name, body := range map[string]string{"sin wamid": `{"messages":[]}`, "ilegible": `no es json`} {
		cl := server(t, 200, body, new([]rec))
		_, err := cl.SendTemplate(context.Background(), "57", ports.TemplateMessage{Name: "t", Language: "es_CO", MediaID: "m"})
		var pe *ports.ProviderError
		if !errors.As(err, &pe) || pe.Action != ports.ActionReview {
			t.Errorf("%s: un 200 dudoso debe quedar en revisión, no reintentarse: %+v", name, err)
		}
	}
}

func TestErroresDeRed(t *testing.T) {
	// Servidor caído: error transitorio, sin la URL ni el token.
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	c := NewClient(Options{Token: "SECRETO-TOKEN", PhoneID: "1", BaseURL: url})
	_, err := c.SendTemplate(context.Background(), "57", ports.TemplateMessage{Name: "t", Language: "es_CO", MediaID: "m"})
	var pe *ports.ProviderError
	if !errors.As(err, &pe) || pe.Action != ports.ActionRetry || strings.Contains(err.Error()+pe.Raw, url) || strings.Contains(err.Error()+pe.Raw, "SECRETO") {
		t.Errorf("red caída: %+v", err)
	}
	// Plazo vencido
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(300 * time.Millisecond) }))
	defer slow.Close()
	c = NewClient(Options{Token: "t", PhoneID: "1", BaseURL: slow.URL, HTTP: &http.Client{Timeout: 50 * time.Millisecond}})
	if _, err := c.UploadMedia(context.Background(), "a.pdf", []byte("x")); !errors.As(err, &pe) || pe.Action != ports.ActionRetry || !strings.Contains(pe.Message, "tardó") {
		t.Errorf("timeout: %+v", err)
	}
	// Cancelación: se propaga tal cual (no es un error del proveedor).
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := NewClient(Options{Token: "t", PhoneID: "1", BaseURL: slow.URL}).CheckToken(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelado: %v", err)
	}
}

func TestParam(t *testing.T) {
	long := strings.Repeat("ñ", 1200)
	for in, want := range map[string]string{"  a \t b\n\n c ": "a b c", "": "-", " \n ": "-", "x": "x", long: strings.Repeat("ñ", 1000)} {
		if got := param(in); got != want {
			t.Errorf("param(%q)", in[:min(len(in), 10)])
		}
	}
}
