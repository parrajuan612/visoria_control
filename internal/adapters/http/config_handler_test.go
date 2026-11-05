package httpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"visoria-control/internal/domain"
	"visoria-control/internal/ports"

	"github.com/gin-gonic/gin"
)

type fakeUC struct {
	res    domain.SyncResult
	err    error
	status domain.ConfigStatus
	stErr  error
	gotURL string
	called bool
}

func (f *fakeUC) Sync(_ context.Context, url string) (domain.SyncResult, error) {
	f.called, f.gotURL = true, url
	return f.res, f.err
}
func (f *fakeUC) Status(context.Context) (domain.ConfigStatus, error) { return f.status, f.stErr }

func do(t *testing.T, uc *fakeUC, method, path, body string) (int, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewConfigHandler(uc)
	r.POST("/config/sync", h.Sync)
	r.GET("/config", h.Get)
	var rd *strings.Reader
	if body != "" {
		rd = strings.NewReader(body)
	} else {
		rd = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestSyncCodigos(t *testing.T) {
	ok := domain.SyncResult{Applied: true, Hash: "h"}
	cases := []struct {
		name string
		uc   *fakeUC
		body string
		want int
	}{
		{"aplicada", &fakeUC{res: ok}, "", 200},
		{"con url", &fakeUC{res: ok}, `{"url":"https://docs.google.com/spreadsheets/x"}`, 200},
		{"hoja con errores", &fakeUC{res: domain.SyncResult{Applied: false, Issues: []domain.Issue{{Nivel: domain.IssueError}}}}, "", 422},
		{"url inválida", &fakeUC{err: ports.ErrInvalidSourceURL}, `{"url":"x"}`, 400},
		{"sin url", &fakeUC{err: ports.ErrNoSourceURL}, "", 400},
		{"hoja no disponible", &fakeUC{err: ports.ErrSourceUnavailable}, "", 502},
		{"error interno", &fakeUC{err: errors.New("redis: password=secreto")}, "", 500},
		{"json roto", &fakeUC{}, "{no", 400},
	}
	for _, c := range cases {
		code, out := do(t, c.uc, "POST", "/config/sync", c.body)
		if code != c.want {
			t.Errorf("%s: HTTP %d, quería %d (%v)", c.name, code, c.want, out)
		}
		if c.want == 500 && strings.Contains(out["error"].(string), "secreto") {
			t.Errorf("el error interno no debe filtrarse al cliente: %v", out)
		}
	}
	uc := &fakeUC{res: ok}
	do(t, uc, "POST", "/config/sync", `{"url":"https://docs.google.com/spreadsheets/x"}`)
	if uc.gotURL != "https://docs.google.com/spreadsheets/x" {
		t.Errorf("la URL enviada debe llegar al caso de uso: %q", uc.gotURL)
	}
	// El 422 incluye los hallazgos para que la pantalla los muestre.
	_, out := do(t, &fakeUC{res: domain.SyncResult{Issues: []domain.Issue{{Nivel: domain.IssueError, Mensaje: "m"}}}}, "POST", "/config/sync", "")
	if res, _ := out["resultado"].(map[string]any); res == nil || len(res["hallazgos"].([]any)) != 1 {
		t.Errorf("faltan los hallazgos: %v", out)
	}
}

func TestGetConfig(t *testing.T) {
	// sin configuración: arreglos vacíos (no null) para la pantalla
	code, out := do(t, &fakeUC{}, "GET", "/config", "")
	if code != 200 || out["sincronizada"] != false || out["becas"] == nil || out["torneos"] == nil || out["hallazgos"] == nil {
		t.Errorf("vacío: %d %v", code, out)
	}
	snap := &domain.ConfigSnapshot{
		Hash: "abc", SyncedAt: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC),
		Config: domain.MasterConfig{
			Becas:      []domain.Beca{{Clave: "30", Nombre: "30%", Total: 196000, Pagos: []domain.Money{65000, 65000, 66000}}},
			Torneos:    []domain.TorneoRango{{Anios: domain.YearRange{Min: 2008, Max: 2014}, Nombre: "T"}},
			Categorias: []domain.CategoriaRango{{Anios: domain.YearRange{Min: 2008, Max: 2010}, Nombre: "JUVENIL"}},
		},
		Issues: []domain.Issue{{Nivel: domain.IssueInfo, Mensaje: "i"}},
	}
	code, out = do(t, &fakeUC{status: domain.ConfigStatus{Sincronizada: true, URL: "https://docs.google.com/spreadsheets/x", Snapshot: snap}}, "GET", "/config", "")
	becas := out["becas"].([]any)
	if code != 200 || out["version"] != "abc" || out["enlace_hoja"] != "https://docs.google.com/spreadsheets/x" || len(becas) != 1 {
		t.Fatalf("con datos: %d %v", code, out)
	}
	if b := becas[0].(map[string]any); b["total"] != "€ 1.960,00" || b["pagos"].([]any)[2] != "€ 660,00" {
		t.Errorf("importes formateados: %v", b)
	}
	if code, _ = do(t, &fakeUC{stErr: errors.New("x")}, "GET", "/config", ""); code != 500 {
		t.Errorf("error del almacén → %d", code)
	}
}
