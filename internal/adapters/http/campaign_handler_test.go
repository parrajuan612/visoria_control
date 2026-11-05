package httpadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"

	"visoria-control/internal/domain"
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

type fakeLegacy struct{ ports.VisoriaService }

func (fakeLegacy) ToPlayers(_ context.Context, recs []domain.Recipient) []domain.Player {
	out := make([]domain.Player, len(recs))
	for i, r := range recs {
		out[i] = domain.Player{Name: r.Jugador, Status: "PENDING"}
	}
	return out
}

func router(f *fakeCamp) *gin.Engine { return routerDocs(f, fakeDocs{}) }

func routerDocs(f *fakeCamp, d fakeDocs) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewCampaignHandler(f, fakeLegacy{}, d)
	r.POST("/campaigns", h.Create)
	r.GET("/campaigns", h.List)
	r.GET("/campaigns/:id", h.Get)
	r.GET("/campaigns/:id/players", h.Players)
	r.PATCH("/campaigns/:id/recipients/:rid", h.EditRecipient)
	r.POST("/campaigns/:id/documents", h.Documents)
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

func TestGetListYPlayers(t *testing.T) {
	d := ports.CampaignDetail{Campaign: domain.CampaignView{Campaign: domain.Campaign{ID: "abc"}}, Recipients: []domain.Recipient{{ID: "r1", Jugador: "Ana"}}}
	r := router(&fakeCamp{detail: d})
	if code, out := call(r, "GET", "/campaigns/abc", nil, ""); code != 200 || out["recipients"] == nil {
		t.Errorf("get: %d %v", code, out)
	}
	if code, out := call(r, "GET", "/campaigns", nil, ""); code != 200 || out["campaigns"] == nil {
		t.Errorf("list vacío debe ser [] y no null: %d %v", code, out)
	}
	code, out := call(r, "GET", "/campaigns/abc/players", nil, "")
	if players, _ := out["data"].([]any); code != 200 || len(players) != 1 || players[0].(map[string]any)["Name"] != "Ana" {
		t.Errorf("players: %d %v", code, out)
	}
	if code, _ := call(router(&fakeCamp{getErr: ports.ErrCampaignNotFound}), "GET", "/campaigns/abc", nil, ""); code != 404 {
		t.Errorf("no encontrada: %d", code)
	}
	if code, _ := call(router(&fakeCamp{getErr: ports.ErrCampaignNotFound}), "GET", "/campaigns/abc/players", nil, ""); code != 404 {
		t.Errorf("players de campaña inexistente: %d", code)
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
