package httpadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"visoria-control/internal/domain"
	"visoria-control/internal/platform/signedurl"
	"visoria-control/internal/ports"

	"github.com/gin-gonic/gin"
)

type fakeCamp struct {
	createIn  ports.CreateCampaignInput
	createErr error
	detail    ports.CampaignDetail
	getErr    error
	editErr   error
	gotPatch  domain.RecipientPatch
	gotIDs    [2]string
	gotBy     string
	list      []domain.CampaignView
	zipErr    error
}

func (f *fakeCamp) Create(_ context.Context, in ports.CreateCampaignInput) (ports.CampaignDetail, error) {
	f.createIn = in
	return f.detail, f.createErr
}
func (f *fakeCamp) Get(context.Context, string) (ports.CampaignDetail, error) {
	return f.detail, f.getErr
}
func (f *fakeCamp) List(context.Context) ([]domain.CampaignView, error) { return f.list, nil }
func (f *fakeCamp) EditRecipient(_ context.Context, cid, rid string, p domain.RecipientPatch, by string) (ports.EditResult, error) {
	f.gotIDs, f.gotPatch, f.gotBy = [2]string{cid, rid}, p, by
	return ports.EditResult{Recipient: domain.Recipient{ID: rid, Estado: domain.RecipientValid}, Otros: []domain.Recipient{}}, f.editErr
}

func (f *fakeCamp) ExportPDFs(_ context.Context, _ string, w io.Writer) (int, error) {
	if f.zipErr != nil {
		return 0, f.zipErr
	}
	_, _ = w.Write([]byte("PKfake"))
	return 1, nil
}

func (f *fakeCamp) RecipientPDF(_ context.Context, _, rid string) ([]byte, string, error) {
	if rid == "sinpdf" {
		return nil, "", ports.ErrRecipientNotFound
	}
	return []byte("%PDF-fake"), "JUGADOR - BECA 50.pdf", nil
}

func router(f *fakeCamp) *gin.Engine { return routerDocs(f, fakeDocs{}) }

func routerDocs(f *fakeCamp, d fakeDocs) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewCampaignHandler(f, d, fakeDispatch)
	r.POST("/campaigns", h.Create)
	r.GET("/campaigns", h.List)
	r.GET("/campaigns/:id", h.Get)
	r.POST("/campaigns/:id/dispatch", h.Dispatch)
	r.POST("/campaigns/:id/retry", h.Retry)
	r.POST("/campaigns/:id/resume", h.Resume)
	r.GET("/campaigns/:id/deliveries", h.Deliveries)
	r.GET("/campaigns/:id/pdfs.zip", h.PDFsZip)
	r.PATCH("/campaigns/:id/recipients/:rid", h.EditRecipient)
	r.POST("/campaigns/:id/documents", h.Documents)
	return r
}

// routerLinks: el mismo router pero con enlaces firmados activos y reloj controlado.
func routerLinks(f *fakeCamp, secret string, now time.Time) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewCampaignHandler(f, fakeDocs{}, fakeDispatch).WithLinks(signedurl.New(secret))
	h.now = func() time.Time { return now }
	r.GET("/campaigns/:id/pdfs.zip", h.PDFsZip)
	r.POST("/campaigns/:id/pdfs-link", h.ZipLink)
	r.GET("/campaigns/:id/recipients/:rid/pdf", h.PDFFile)
	return r
}

func multipartBody(t *testing.T, filename string, content []byte, fields map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if filename != "" {
		fw, _ := w.CreateFormFile("file", filename)
		fw.Write(content)
	}
	for k, v := range fields {
		w.WriteField(k, v)
	}
	w.Close()
	return &buf, w.FormDataContentType()
}

func call(r *gin.Engine, method, path string, body *bytes.Buffer, ctype string) (int, map[string]any) {
	var rd *bytes.Buffer
	if body != nil {
		rd = body
	} else {
		rd = &bytes.Buffer{}
	}
	req := httptest.NewRequest(method, path, rd)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestCreateCampana(t *testing.T) {
	f := &fakeCamp{detail: ports.CampaignDetail{Campaign: domain.CampaignView{Campaign: domain.Campaign{ID: "abc"}}, Recipients: []domain.Recipient{}}}
	body, ct := multipartBody(t, "../../jugadores.XLSX", []byte("datos"), map[string]string{
		"torneo": "Copa X", "fecha_pago_1": "2026-12-01", "fecha_pago_2": "01/01/2027", "requerir_ambos": "on"})
	code, out := call(router(f), "POST", "/campaigns", body, ct)
	if code != 201 || out["campaign"] == nil {
		t.Fatalf("creación: %d %v", code, out)
	}
	in := f.createIn
	if string(in.Data) != "datos" || in.Torneo != "Copa X" || in.FechasPago[0] != "2026-12-01" || in.FechasPago[1] != "01/01/2027" || in.FechasPago[2] != "" ||
		!in.RequireBoth || in.By != "operador" {
		t.Errorf("entrada: %+v", in)
	}
	// El nombre del archivo se reduce a su base (sin rutas) y la extensión no distingue mayúsculas.
	if in.Filename != "jugadores.XLSX" {
		t.Errorf("filename=%q", in.Filename)
	}
}

func TestCreateErrores(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"entrada inválida", &ports.InputError{Message: "Fecha inválida en el pago 1."}, 400},
		{"sin configuración", ports.ErrNoConfig, 409},
		{"excel inválido", errors.Join(ports.ErrInvalidSpreadsheet, errors.New("faltan columnas")), 422},
		{"error interno", errors.New("redis: password=secreto"), 500},
	}
	for _, c := range cases {
		body, ct := multipartBody(t, "x.xlsx", []byte("d"), map[string]string{"torneo": "T"})
		code, out := call(router(&fakeCamp{createErr: c.err}), "POST", "/campaigns", body, ct)
		if code != c.want {
			t.Errorf("%s: HTTP %d, quería %d (%v)", c.name, code, c.want, out)
		}
		if c.want == 500 && strings.Contains(out["error"].(string), "secreto") {
			t.Errorf("el error interno no debe filtrarse: %v", out)
		}
		if c.want == 400 && out["error"] != "Fecha inválida en el pago 1." {
			t.Errorf("el mensaje de entrada debe llegar tal cual: %v", out)
		}
	}
	// Sin archivo / extensión incorrecta
	body, ct := multipartBody(t, "", nil, map[string]string{"torneo": "T"})
	if code, _ := call(router(&fakeCamp{}), "POST", "/campaigns", body, ct); code != 400 {
		t.Errorf("sin archivo: %d", code)
	}
	body, ct = multipartBody(t, "x.csv", []byte("d"), nil)
	if code, _ := call(router(&fakeCamp{}), "POST", "/campaigns", body, ct); code != 400 {
		t.Errorf("extensión: %d", code)
	}
	// Archivo de más de 10 MB → 413 sin llegar al caso de uso
	big := bytes.Repeat([]byte("x"), maxExcelBytes+10)
	f := &fakeCamp{}
	body, ct = multipartBody(t, "x.xlsx", big, nil)
	if code, _ := call(router(f), "POST", "/campaigns", body, ct); code != 413 || f.createIn.Data != nil {
		t.Errorf("archivo grande: %d", code)
	}
}

func TestGetList(t *testing.T) {
	d := ports.CampaignDetail{Campaign: domain.CampaignView{Campaign: domain.Campaign{ID: "abc"}}, Recipients: []domain.Recipient{{ID: "r1", Jugador: "Ana"}}}
	r := router(&fakeCamp{detail: d})
	if code, out := call(r, "GET", "/campaigns/abc", nil, ""); code != 200 || out["recipients"] == nil {
		t.Errorf("get: %d %v", code, out)
	}
	if code, out := call(r, "GET", "/campaigns", nil, ""); code != 200 || out["campaigns"] == nil {
		t.Errorf("list vacío debe ser [] y no null: %d %v", code, out)
	}
	if code, _ := call(router(&fakeCamp{getErr: ports.ErrCampaignNotFound}), "GET", "/campaigns/abc", nil, ""); code != 404 {
		t.Errorf("no encontrada: %d", code)
	}
	// IDs con caracteres raros no llegan a Redis.
	for _, bad := range []string{"a:b", "a%20b", "..", "x*"} {
		if code, _ := call(r, "GET", "/campaigns/"+bad, nil, ""); code != 404 {
			t.Errorf("id %q: %d", bad, code)
		}
	}
}

func TestPatchRecipient(t *testing.T) {
	patch := func(f *fakeCamp, path, body string) (int, map[string]any) {
		return call(router(f), "PATCH", path, bytes.NewBufferString(body), "application/json")
	}
	f := &fakeCamp{}
	code, out := patch(f, "/campaigns/c1/recipients/r1", `{"correo":"papa@gmail.com","club":""}`)
	if code != 200 || out["recipient"] == nil || f.gotIDs != [2]string{"c1", "r1"} || f.gotBy != "operador" {
		t.Fatalf("patch: %d %v %+v", code, out, f.gotIDs)
	}
	if f.gotPatch.Correo == nil || *f.gotPatch.Correo != "papa@gmail.com" || f.gotPatch.Club == nil || *f.gotPatch.Club != "" || f.gotPatch.Movil != nil {
		t.Errorf("el parche distingue 'no enviado' de 'vacío': %+v", f.gotPatch)
	}
	for name, body := range map[string]string{"campo desconocido": `{"telefono":"1"}`, "tipo incorrecto": `{"anio":2012}`, "json roto": `{`, "vacío": ``} {
		if code, _ := patch(&fakeCamp{}, "/campaigns/c1/recipients/r1", body); code != 400 {
			t.Errorf("%s: %d", name, code)
		}
	}
	errs := map[int]error{404: ports.ErrRecipientNotFound, 409: ports.ErrCampaignLocked, 400: &ports.InputError{Message: "m"}, 500: errors.New("x")}
	for want, e := range errs {
		if code, _ := patch(&fakeCamp{editErr: e}, "/campaigns/c1/recipients/r1", `{"club":"x"}`); code != want {
			t.Errorf("error %v → %d, quería %d", e, code, want)
		}
	}
	if code, _ := patch(&fakeCamp{}, "/campaigns/c1/recipients/a:b", `{"club":"x"}`); code != 404 {
		t.Errorf("rid inválido: %d", code)
	}
	if code, _ := patch(&fakeCamp{}, "/campaigns/a:b/recipients/r1", `{"club":"x"}`); code != 404 {
		t.Errorf("id inválido: %d", code)
	}
	// Cuerpo enorme → 413
	huge := `{"club":"` + strings.Repeat("x", maxPatchBytes) + `"}`
	if code, _ := patch(&fakeCamp{}, "/campaigns/c1/recipients/r1", huge); code != 413 {
		t.Errorf("cuerpo grande: %d", code)
	}
}

func TestTruthy(t *testing.T) {
	for in, want := range map[string]bool{"on": true, "TRUE": true, "1": true, " sí ": true, "": false, "no": false, "0": false} {
		if truthy(in) != want {
			t.Errorf("truthy(%q)", in)
		}
	}
}

type fakeDocs struct {
	res ports.GenerateResult
	err error
}

func (f fakeDocs) Generate(context.Context, string) (ports.GenerateResult, error) {
	return f.res, f.err
}

func TestDocumentsEndpoint(t *testing.T) {
	post := func(d fakeDocs, id string) (int, map[string]any) {
		return call(routerDocs(&fakeCamp{}, d), "POST", "/campaigns/"+id+"/documents", nil, "")
	}
	if code, out := post(fakeDocs{res: ports.GenerateResult{Queued: 7, Batches: 1, Pending: 7}}, "c1"); code != 202 || out["resultado"].(map[string]any)["encolados"] != float64(7) {
		t.Errorf("encolada: %d %v", code, out)
	}
	if code, out := post(fakeDocs{res: ports.GenerateResult{}}, "c1"); code != 200 || out["message"] != "Todas las propuestas ya están generadas." {
		t.Errorf("sin pendientes: %d %v", code, out)
	}
	cases := map[int]error{
		400: &ports.InputError{Message: "Falta la plantilla «cadete_juvenil.docx» en la carpeta plantillas."},
		404: ports.ErrCampaignNotFound, 409: ports.ErrCampaignLocked, 500: errors.New("redis: password=secreto"),
	}
	if code, out := post(fakeDocs{err: ports.ErrGenerationRunning}, "c1"); code != 409 || out["error"] != "Ya se están generando las propuestas de esta campaña." {
		t.Errorf("ya en curso: %d %v", code, out)
	}
	for want, e := range cases {
		code, out := post(fakeDocs{err: e}, "c1")
		if code != want {
			t.Errorf("%v → %d, quería %d", e, code, want)
		}
		if want == 500 && strings.Contains(out["error"].(string), "secreto") {
			t.Error("el error interno no debe filtrarse")
		}
		if want == 400 && !strings.HasPrefix(out["error"].(string), "Falta la plantilla") {
			t.Errorf("mensaje: %v", out)
		}
	}
	if code, _ := post(fakeDocs{}, "a:b"); code != 404 {
		t.Errorf("id inválido: %d", code)
	}
}

// ---- despacho ----

type fakeDisp struct {
	res    ports.DispatchResult
	retry  ports.RetryResult
	snap   ports.DeliverySnapshot
	err    error
	gotIn  ports.DispatchInput
	called string
}

func (f *fakeDisp) Dispatch(_ context.Context, id string, in ports.DispatchInput) (ports.DispatchResult, error) {
	f.gotIn, f.called = in, "dispatch:"+id
	return f.res, f.err
}
func (f *fakeDisp) Retry(_ context.Context, id string) (ports.RetryResult, error) {
	f.called = "retry:" + id
	return f.retry, f.err
}
func (f *fakeDisp) Resume(_ context.Context, id string) error {
	f.called = "resume:" + id
	return f.err
}
func (f *fakeDisp) Snapshot(_ context.Context, id string) (ports.DeliverySnapshot, error) {
	f.called = "snapshot:" + id
	return f.snap, f.err
}

var fakeDispatch ports.DispatchUseCase = &fakeDisp{}

func dispRouter(d *fakeDisp) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewCampaignHandler(&fakeCamp{}, fakeDocs{}, d)
	r.POST("/campaigns/:id/dispatch", h.Dispatch)
	r.POST("/campaigns/:id/retry", h.Retry)
	r.POST("/campaigns/:id/resume", h.Resume)
	r.GET("/campaigns/:id/deliveries", h.Deliveries)
	return r
}

func TestDispatchEndpoints(t *testing.T) {
	d := &fakeDisp{res: ports.DispatchResult{Created: 10, Queued: 8, Skipped: 2}, retry: ports.RetryResult{Requeued: 3, Permanent: 1},
		snap: ports.DeliverySnapshot{Status: "DISPATCHING", Progress: map[string]ports.ChannelProgress{"whatsapp": {Total: 5}}, Deliveries: []ports.DeliveryView{{Key: "k"}}}}
	r := dispRouter(d)

	code, out := call(r, "POST", "/campaigns/c1/dispatch", bytes.NewBufferString(`{"channels":["whatsapp"]}`), "application/json")
	if code != 202 || d.called != "dispatch:c1" || len(d.gotIn.Channels) != 1 || d.gotIn.Channels[0] != "whatsapp" || d.gotIn.By != "operador" ||
		out["resultado"].(map[string]any)["en_cola"] != float64(8) {
		t.Errorf("dispatch: %d %v %+v", code, out, d.gotIn)
	}
	// Sin cuerpo: todos los canales configurados.
	d.gotIn = ports.DispatchInput{}
	if code, _ := call(r, "POST", "/campaigns/c1/dispatch", nil, ""); code != 202 || len(d.gotIn.Channels) != 0 {
		t.Errorf("sin cuerpo: %d %+v", code, d.gotIn)
	}
	if code, _ := call(r, "POST", "/campaigns/c1/dispatch", bytes.NewBufferString(`{roto`), "application/json"); code != 400 {
		t.Errorf("json roto: %d", code)
	}
	if code, out := call(r, "POST", "/campaigns/c1/retry", nil, ""); code != 200 || out["resultado"].(map[string]any)["reencoladas"] != float64(3) || d.called != "retry:c1" {
		t.Errorf("retry: %d %v", code, out)
	}
	if code, _ := call(r, "POST", "/campaigns/c1/resume", nil, ""); code != 200 || d.called != "resume:c1" {
		t.Errorf("resume: %d", code)
	}
	code, out = call(r, "GET", "/campaigns/c1/deliveries", nil, "")
	if code != 200 || out["status"] != "DISPATCHING" || len(out["entregas"].([]any)) != 1 {
		t.Errorf("deliveries: %d %v", code, out)
	}
	for _, path := range []string{"dispatch", "retry", "resume"} {
		if code, _ := call(r, "POST", "/campaigns/a:b/"+path, nil, ""); code != 404 {
			t.Errorf("%s con id inválido: %d", path, code)
		}
	}
	if code, _ := call(r, "GET", "/campaigns/a:b/deliveries", nil, ""); code != 404 {
		t.Error("deliveries con id inválido")
	}
	// Mapeo de errores: mensajes de entrada → 400; ya despachada / pausa / bloqueo → 409; inexistente → 404; interno → 500 sin filtrar.
	cases := map[int]error{
		400: &ports.InputError{Message: "No se puede enviar por WhatsApp: el token venció."},
		409: ports.ErrDispatchRunning, 404: ports.ErrCampaignNotFound, 500: errors.New("redis: password=secreto"),
	}
	for want, e := range cases {
		d.err = e
		for _, route := range [][2]string{{"POST", "dispatch"}, {"POST", "retry"}, {"POST", "resume"}, {"GET", "deliveries"}} {
			code, out := call(r, route[0], "/campaigns/c1/"+route[1], nil, "")
			if code != want {
				t.Errorf("%s %v → %d, quería %d", route[1], e, code, want)
			}
			if want == 500 && strings.Contains(out["error"].(string), "secreto") {
				t.Error("el error interno no debe filtrarse")
			}
		}
	}
	d.err = ports.ErrCampaignPaused
	if code, out := call(r, "POST", "/campaigns/c1/retry", nil, ""); code != 409 || !strings.Contains(out["error"].(string), "pausa") {
		t.Errorf("pausada: %d %v", code, out)
	}
}

func TestPDFsZipSoloLocal(t *testing.T) {
	get := func(f *fakeCamp, remote string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/campaigns/abc/pdfs.zip", nil)
		req.RemoteAddr = remote
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		router(f).ServeHTTP(w, req)
		return w
	}
	if w := get(&fakeCamp{}, "127.0.0.1:5555", nil); w.Code != 200 || w.Header().Get("Content-Type") != "application/zip" ||
		!strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("loopback debe descargar: %d %v", w.Code, w.Header())
	}
	if w := get(&fakeCamp{}, "[::1]:5555", nil); w.Code != 200 {
		t.Fatalf("::1 debe descargar: %d", w.Code)
	}
	for name, c := range map[string]struct {
		remote string
		hdr    map[string]string
	}{
		"IP de la red":        {"192.168.1.20:4000", nil},
		"IP pública":          {"8.8.8.8:4000", nil},
		"proxy (nginx local)": {"127.0.0.1:5555", map[string]string{"X-Forwarded-For": "8.8.8.8"}},
		"X-Real-IP":           {"127.0.0.1:5555", map[string]string{"X-Real-IP": "8.8.8.8"}},
	} {
		if w := get(&fakeCamp{}, c.remote, c.hdr); w.Code != http.StatusForbidden {
			t.Errorf("%s: esperaba 403, fue %d", name, w.Code)
		}
	}
	if w := get(&fakeCamp{zipErr: ports.ErrNoPDFs}, "127.0.0.1:1", nil); w.Code != http.StatusConflict {
		t.Errorf("sin PDFs: esperaba 409, fue %d", w.Code)
	}
	if w := get(&fakeCamp{zipErr: ports.ErrCampaignNotFound}, "127.0.0.1:1", nil); w.Code != http.StatusNotFound {
		t.Errorf("campaña inexistente: esperaba 404, fue %d", w.Code)
	}
}

func TestEnlacesFirmados(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	do := func(r *gin.Engine, method, url, remote string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, url, nil)
		req.RemoteAddr = remote
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	proxy := map[string]string{"X-Forwarded-For": "8.8.8.8"} // detrás de nginx
	rt := routerLinks(&fakeCamp{}, secret, now)

	// ZIP: el panel pide un enlace y con él descarga aunque pase por nginx.
	w := do(rt, http.MethodPost, "/campaigns/abc/pdfs-link", "127.0.0.1:1", proxy)
	var out struct{ URL string }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || !strings.HasPrefix(out.URL, "/api/v1/campaigns/abc/pdfs.zip?t=") {
		t.Fatalf("enlace del ZIP: %d %s", w.Code, w.Body)
	}
	tok := out.URL[strings.Index(out.URL, "t=")+2:]
	if w := do(rt, http.MethodGet, "/campaigns/abc/pdfs.zip?t="+tok, "127.0.0.1:1", proxy); w.Code != 200 {
		t.Errorf("con enlace firmado y proxy debe descargar: %d", w.Code)
	}
	if w := do(rt, http.MethodGet, "/campaigns/abc/pdfs.zip", "127.0.0.1:1", proxy); w.Code != http.StatusForbidden {
		t.Errorf("sin enlace y con proxy debe negarse: %d", w.Code)
	}
	if w := do(rt, http.MethodGet, "/campaigns/otra/pdfs.zip?t="+tok, "127.0.0.1:1", proxy); w.Code != http.StatusForbidden {
		t.Errorf("el enlace de una campaña no sirve para otra: %d", w.Code)
	}
	if w := do(routerLinks(&fakeCamp{}, secret, now.Add(time.Hour)), http.MethodGet, "/campaigns/abc/pdfs.zip?t="+tok, "8.8.8.8:1", nil); w.Code != http.StatusForbidden {
		t.Errorf("enlace vencido: %d", w.Code)
	}
	if w := do(routerLinks(&fakeCamp{getErr: ports.ErrCampaignNotFound}, secret, now), http.MethodPost, "/campaigns/abc/pdfs-link", "127.0.0.1:1", nil); w.Code != http.StatusNotFound {
		t.Errorf("campaña inexistente: %d", w.Code)
	}
	if w := do(routerLinks(&fakeCamp{}, "", now), http.MethodPost, "/campaigns/abc/pdfs-link", "127.0.0.1:1", nil); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "LINK_SECRET") {
		t.Errorf("sin LINK_SECRET: %d %s", w.Code, w.Body)
	}

	// PDF de un destinatario (el enlace va en la nota de Chatwoot).
	pdfTok := signedurl.New(secret).Token(PDFResource("abc", "r1"), now.Add(30*24*time.Hour))
	w = do(rt, http.MethodGet, "/campaigns/abc/recipients/r1/pdf?t="+pdfTok, "8.8.8.8:1", nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/pdf" || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "inline") ||
		w.Header().Get("Cache-Control") != "private, no-store" || w.Body.String() != "%PDF-fake" {
		t.Errorf("PDF firmado: %d %v", w.Code, w.Header())
	}
	for name, url := range map[string]string{
		"sin token":         "/campaigns/abc/recipients/r1/pdf",
		"otro destinatario": "/campaigns/abc/recipients/r2/pdf?t=" + pdfTok,
		"otra campaña":      "/campaigns/zzz/recipients/r1/pdf?t=" + pdfTok,
		"token del zip":     "/campaigns/abc/recipients/r1/pdf?t=" + tok,
		"basura":            "/campaigns/abc/recipients/r1/pdf?t=1.2",
	} {
		if w := do(rt, http.MethodGet, url, "8.8.8.8:1", nil); w.Code != http.StatusForbidden {
			t.Errorf("%s: esperaba 403, fue %d", name, w.Code)
		}
	}
	other := signedurl.New(secret).Token(PDFResource("abc", "sinpdf"), now.Add(time.Hour))
	if w := do(rt, http.MethodGet, "/campaigns/abc/recipients/sinpdf/pdf?t="+other, "8.8.8.8:1", nil); w.Code != http.StatusNotFound {
		t.Errorf("sin PDF: %d", w.Code)
	}
	// Sin LINK_SECRET nada firmado vale.
	if w := do(routerLinks(&fakeCamp{}, "", now), http.MethodGet, "/campaigns/abc/recipients/r1/pdf?t="+pdfTok, "8.8.8.8:1", nil); w.Code != http.StatusForbidden {
		t.Errorf("sin secreto: %d", w.Code)
	}
}
