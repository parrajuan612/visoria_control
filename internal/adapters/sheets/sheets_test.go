package sheets

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"visoria-control/internal/domain"
)

func fixture(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/informacion_torneos.csv")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Se lee la hoja REAL del cliente y la configuración resultante es válida.
func TestParseHojaReal(t *testing.T) {
	cfg, issues := Parse(strings.NewReader(fixture(t)))
	if domain.HasErrors(issues) {
		t.Fatalf("la lectura no debe dar errores: %+v", issues)
	}
	if vi := cfg.Validate(); domain.HasErrors(vi) {
		t.Fatalf("la hoja real debe ser válida: %+v", vi)
	}
	if len(cfg.Becas) != 6 || len(cfg.Torneos) != 3 || len(cfg.Categorias) != 5 {
		t.Fatalf("becas=%d torneos=%d categorias=%d", len(cfg.Becas), len(cfg.Torneos), len(cfg.Categorias))
	}
	b30, _ := cfg.Beca("30")
	if b30.Total != 196000 || len(b30.Pagos) != 3 || b30.Pagos[2] != 66000 || b30.Fila != 3 {
		t.Errorf("beca 30%%: %+v", b30)
	}
	b70, _ := cfg.Beca("70")
	if len(b70.Pagos) != 2 { // la hoja deja PAGO # 3 vacío
		t.Errorf("70%% debe tener 2 pagos: %+v", b70)
	}
	b100, _ := cfg.Beca("100")
	if len(b100.Pagos) != 1 || b100.Total != 20000 {
		t.Errorf("100%%: %+v", b100)
	}
	if _, ok := cfg.Beca(domain.BecaAcompanante); !ok {
		t.Error("falta ACOMPAÑANTE")
	}
	// espacios sobrantes del nombre ("... VILLARREAL  ") se limpian
	if got := cfg.Torneos[2].Nombre; got != "TORNEO VILLARREAL YELLOW CUP EASTER CIUDAD VILLARREAL" {
		t.Errorf("nombre de torneo: %q", got)
	}
	// Las columnas extra (AEROPUERTO, SALIDA…) se ignoran sin problema.
	if got := cfg.CategoriasParaAnio(2012); len(got) != 1 || got[0].Nombre != "CADETE" || got[0].Plantilla != "cadete_juvenil" {
		t.Errorf("2012: %+v", got)
	}
	if len(cfg.TorneosParaAnio(2012)) != 2 {
		t.Errorf("2012 debe estar en 2 torneos: %v", cfg.TorneosParaAnio(2012))
	}
}

func TestParseReordenadoYConBOM(t *testing.T) {
	// Columnas en otro orden, encabezados con otras grafías, BOM y filas de título antes.
	csv := "\xef\xbb\xbfLISTA DE PRECIOS 2027,,,,,,,,\n" +
		"TORNEO,AÑOS,PAGO 2,Pago #1,TOTAL,BECAS,Años,Categorías,PAGO 3\n" +
		"COPA X,2008-2014,\"€ 500,00\",\"€ 500,00\",\"€ 1.000,00\",SIN BECA,2008-2014,JUVENIL,\n"
	cfg, issues := Parse(strings.NewReader(csv))
	if domain.HasErrors(issues) {
		t.Fatalf("issues: %+v", issues)
	}
	if len(cfg.Becas) != 1 || cfg.Becas[0].Total != 100000 || len(cfg.Becas[0].Pagos) != 2 {
		t.Errorf("becas: %+v", cfg.Becas)
	}
	if len(cfg.Torneos) != 1 || cfg.Torneos[0].Nombre != "COPA X" || len(cfg.Categorias) != 1 {
		t.Errorf("torneos=%+v categorias=%+v", cfg.Torneos, cfg.Categorias)
	}
}

func TestParseErroresDeLectura(t *testing.T) {
	head := "BECA,V/TOTAL,PAGO # 1,PAGO # 2,PAGO # 3,AÑOS,TORNEO,AÑOS,CATEGORIA\n"
	cases := []struct {
		name, csv, lvl, sub string
	}{
		{"importe inválido", head + "30%,abc,\"€ 1,00\",,,,,,\n", "error", "V/TOTAL"},
		{"total vacío", head + "30%,,\"€ 1,00\",,,,,,\n", "error", "no tiene precio total"},
		{"pago con hueco", head + "30%,\"€ 3,00\",\"€ 1,00\",,\"€ 2,00\",,,,\n", "error", "PAGO # 3 tiene valor pero el PAGO # 2 está vacío"},
		{"pago inválido", head + "30%,\"€ 3,00\",x,,,,,,\n", "error", "PAGO # 1"},
		{"beca desconocida", head + "VIP,\"€ 3,00\",\"€ 3,00\",,,,,,\n", "advertencia", "no reconocida"},
		{"importes sin nombre", head + ",\"€ 3,00\",\"€ 3,00\",,,,,,\n", "advertencia", "sin el nombre de la beca"},
		{"años de torneo inválidos", head + ",,,,,20x8,COPA,,\n", "error", "Torneos · fila 2"},
		{"torneo sin años", head + ",,,,,,COPA,,\n", "error", "Torneos · fila 2"},
		{"años invertidos", head + ",,,,,,,2010-2008,JUVENIL\n", "error", "invertido"},
	}
	for _, c := range cases {
		_, issues := Parse(strings.NewReader(c.csv))
		found := false
		for _, i := range issues {
			if string(i.Nivel) == c.lvl && strings.Contains(i.Mensaje+" "+i.Campo, c.sub) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: faltó %s %q en %+v", c.name, c.lvl, c.sub, issues)
		}
	}
}

func TestParseEncabezadosFaltantes(t *testing.T) {
	cases := map[string]string{
		"sin fila de encabezados": "A,B\n1,2\n",
		"sin V/TOTAL":             "BECA,PAGO 1,PAGO 2,PAGO 3,AÑOS,TORNEO,AÑOS,CATEGORIA\n",
		"sin pagos":               "BECA,V/TOTAL,AÑOS,TORNEO,AÑOS,CATEGORIA\n",
		"sin torneos":             "BECA,V/TOTAL,PAGO 1,PAGO 2,PAGO 3,AÑOS,CATEGORIA\n",
		"sin categorías":          "BECA,V/TOTAL,PAGO 1,PAGO 2,PAGO 3,AÑOS,TORNEO\n",
	}
	for name, csv := range cases {
		_, issues := Parse(strings.NewReader(csv))
		if !domain.HasErrors(issues) {
			t.Errorf("%s: debía dar error: %+v", name, issues)
		}
	}
	// un CSV con comillas rotas no debe provocar pánico
	if _, issues := Parse(strings.NewReader("BECA,V/TOTAL\n\"sin cerrar,1\n")); issues == nil {
		t.Log("csv con comillas sin cerrar tolerado (LazyQuotes)")
	}
}

func TestValidateGoogleSheetsURL(t *testing.T) {
	ok := "https://docs.google.com/spreadsheets/d/e/2PACX-xxx/pub?gid=1&single=true&output=csv"
	if err := ValidateGoogleSheetsURL(ok); err != nil {
		t.Errorf("la URL real debe aceptarse: %v", err)
	}
	for _, bad := range []string{
		"", "no es url", "http://docs.google.com/spreadsheets/d/e/x/pub", "https://evil.com/spreadsheets/x",
		"https://docs.google.com.evil.com/spreadsheets/x", "https://docs.google.com@evil.com/spreadsheets/x",
		"https://user:pw@docs.google.com/spreadsheets/x", "https://docs.google.com:8443/spreadsheets/x",
		"https://docs.google.com/document/d/x", "http://127.0.0.1:6380/", "https://169.254.169.254/latest/meta-data",
		"file:///etc/passwd",
	} {
		if err := ValidateGoogleSheetsURL(bad); !errors.Is(err, ErrInvalidURL) {
			t.Errorf("%q debía rechazarse, dio %v", bad, err)
		}
	}
}

func TestGoogleHost(t *testing.T) {
	for h, want := range map[string]bool{
		"docs.google.com": true, "DOC-10-6O-SHEETS.GOOGLEUSERCONTENT.COM": true, "googleusercontent.com": true,
		"evilgoogleusercontent.com": false, "googleusercontent.com.evil.org": false, "google.com": false, "": false,
	} {
		if googleHost(h) != want {
			t.Errorf("googleHost(%q)", h)
		}
	}
}

func testSource() *Source {
	// En tests se permite http://127.0.0.1 (httptest); en producción solo Google por https.
	return newSource(func(u *url.URL) bool { return u.Hostname() == "127.0.0.1" })
}

func TestSourceLoad(t *testing.T) {
	var gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		switch r.URL.Path {
		case "/ok":
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			w.Write([]byte(fixture(t)))
		case "/redir": // redirección permitida
			http.Redirect(w, r, "/ok", http.StatusFound)
		case "/redir-fuera": // redirección a un host no permitido
			http.Redirect(w, r, "http://localhost:1/x", http.StatusFound)
		case "/html":
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<html>login</html>"))
		case "/grande":
			w.Header().Set("Content-Type", "text/csv")
			w.Write([]byte(strings.Repeat("x", maxCSVBytes+10)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	s := testSource()
	ctx := context.Background()

	cfg, issues, err := s.Load(ctx, srv.URL+"/ok")
	if err != nil || domain.HasErrors(issues) || len(cfg.Becas) != 6 {
		t.Fatalf("ok: err=%v issues=%v becas=%d", err, issues, len(cfg.Becas))
	}
	if gotAccept != "text/csv" {
		t.Errorf("Accept=%q", gotAccept)
	}
	if _, _, err := s.Load(ctx, srv.URL+"/redir"); err != nil {
		t.Errorf("redirección permitida: %v", err)
	}
	for path, sub := range map[string]string{
		"/redir-fuera": "no permitido", "/html": "página web", "/grande": "tamaño máximo", "/nada": "HTTP 404",
	} {
		_, _, err := s.Load(ctx, srv.URL+path)
		if err == nil || !strings.Contains(err.Error(), sub) {
			t.Errorf("%s: esperaba error con %q, fue %v", path, sub, err)
		}
		if err != nil && strings.Contains(err.Error(), srv.URL) {
			t.Errorf("%s: el error no debe incluir la URL: %v", path, err)
		}
	}
	// producción: un host que no es Google se rechaza antes de conectar
	if _, _, err := NewSource().Load(ctx, srv.URL+"/ok"); !errors.Is(err, ErrInvalidURL) {
		t.Errorf("NewSource debe rechazar hosts no Google: %v", err)
	}
	if _, _, err := NewSource().Load(ctx, "https://docs.google.com/%zz"); !errors.Is(err, ErrInvalidURL) {
		t.Errorf("URL mal formada: %v", err)
	}
}

func TestRepositorio(t *testing.T) {
	r := NewRepository()
	ctx := context.Background()
	// Sin configuración todavía → "No definido" y no falla.
	if info, err := r.GetTournamentForPlayer(ctx, 2012, "50%"); err != nil || info.Pricing.Total != "No definido" {
		t.Fatalf("sin config: %+v %v", info, err)
	}
	cfg, _ := Parse(strings.NewReader(fixture(t)))
	r.Apply(cfg)

	info, _ := r.GetTournamentForPlayer(ctx, 2012, "50%")
	if info.Pricing.Total != "€ 1.550,00" || info.Pricing.Pago1 != "€ 500,00" || info.Pricing.Pago3 != "€ 550,00" {
		t.Errorf("precios 50%%: %+v", info.Pricing)
	}
	if info.Category != "CADETE" {
		t.Errorf("categoría: %q", info.Category)
	}
	// 2012 está en dos torneos: se listan todos (comportamiento histórico).
	if want := "TORNEO COPA MIC FOOTBALL COSTA BRAVA-BARCELONA\n- TORNEO IDA EASTER CUP COMUNIDAD VALENCIANA"; info.Name != want {
		t.Errorf("torneos: %q", info.Name)
	}
	// pagos ausentes → cadena vacía (el PDF cuenta los no vacíos)
	if i70, _ := r.GetTournamentForPlayer(ctx, 2016, "70%"); i70.Pricing.Pago3 != "" || i70.Pricing.Pago2 != "€ 495,00" || i70.Category != "ALEVIN" {
		t.Errorf("70%%: %+v", i70)
	}
	if i, _ := r.GetTournamentForPlayer(ctx, 2012, "99%"); i.Pricing.Total != "No definido" {
		t.Errorf("beca inexistente: %+v", i.Pricing)
	}
	if i, _ := r.GetTournamentForPlayer(ctx, 2012, "sin beca"); i.Pricing.Total != "€ 2.800,00" {
		t.Errorf("sin beca: %+v", i.Pricing)
	}
	if r.Current() == nil {
		t.Error("Current debe devolver la configuración")
	}
}

// Consultar mientras se reemplaza la configuración no debe producir carreras (go test -race).
func TestRepositorioSinCarreras(t *testing.T) {
	r := NewRepository()
	cfg, _ := Parse(strings.NewReader(fixture(t)))
	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			r.Apply(cfg)
		}
		close(done)
	}()
	for i := 0; i < 200; i++ {
		r.GetTournamentForPlayer(context.Background(), 2012, "30%")
	}
	<-done
}
