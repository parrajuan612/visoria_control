package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"visoria-control/internal/domain"
	"visoria-control/internal/ports"
)

type fakeSource struct {
	cfg    domain.MasterConfig
	issues []domain.Issue
	err    error
	calls  []string
}

func (f *fakeSource) Load(_ context.Context, url string) (domain.MasterConfig, []domain.Issue, error) {
	f.calls = append(f.calls, url)
	return f.cfg, f.issues, f.err
}

type fakeStore struct {
	snap       *domain.ConfigSnapshot
	url        string
	saveErr    error
	currentErr error
	urlErr     error
}

func (f *fakeStore) Current(context.Context) (*domain.ConfigSnapshot, error) {
	return f.snap, f.currentErr
}
func (f *fakeStore) Save(_ context.Context, s domain.ConfigSnapshot) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.snap = &s
	return nil
}
func (f *fakeStore) SourceURL(context.Context) (string, error) { return f.url, f.urlErr }
func (f *fakeStore) SetSourceURL(_ context.Context, u string) error {
	f.url = u
	return nil
}

type fakeApplier struct{ applied []domain.MasterConfig }

func (f *fakeApplier) Apply(c domain.MasterConfig) { f.applied = append(f.applied, c) }

func validCfg() domain.MasterConfig {
	eur := func(n int64) domain.Money { return domain.Money(n * 100) } // importes en euros enteros
	beca := func(n string, total domain.Money, p ...domain.Money) domain.Beca {
		k, _ := domain.NormalizeBecaKey(n)
		return domain.Beca{Clave: k, Nombre: n, Total: total, Pagos: p}
	}
	return domain.MasterConfig{
		Becas: []domain.Beca{
			beca("SIN BECA", eur(300), eur(100), eur(200)), beca("30%", eur(200), eur(200)), beca("50%", eur(100), eur(100)),
			beca("70%", eur(50), eur(50)), beca("100%", eur(10), eur(10)), beca("ACOMPAÑANTE", eur(180), eur(180)),
		},
		Torneos:    []domain.TorneoRango{{Anios: domain.YearRange{Min: 2010, Max: 2012}, Nombre: "T"}},
		Categorias: []domain.CategoriaRango{{Anios: domain.YearRange{Min: 2010, Max: 2012}, Nombre: "CADETE", Plantilla: "cadete_juvenil"}},
	}
}

func newSvc(src *fakeSource, st *fakeStore, ap *fakeApplier, def string) *ConfigService {
	s := NewConfigService(src, st, ap, def, func(u string) error {
		if !strings.HasPrefix(u, "https://docs.google.com/") {
			return errors.New("solo Google")
		}
		return nil
	})
	s.now = func() time.Time { return time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC) }
	return s
}

const gURL = "https://docs.google.com/spreadsheets/d/e/X/pub?output=csv"

func TestSyncValidaReemplazaYGuardaURL(t *testing.T) {
	src, st, ap := &fakeSource{cfg: validCfg()}, &fakeStore{}, &fakeApplier{}
	svc := newSvc(src, st, ap, "")
	res, err := svc.Sync(context.Background(), gURL)
	if err != nil || !res.Applied || !res.Changed || res.Hash == "" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if len(ap.applied) != 1 || st.snap == nil || st.url != gURL || src.calls[0] != gURL {
		t.Errorf("applied=%d snap=%v url=%q calls=%v", len(ap.applied), st.snap != nil, st.url, src.calls)
	}
	if res.Summary.Becas != 6 || !res.SyncedAt.Equal(time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("resumen: %+v", res)
	}
	// Segunda sincronización sin cambios: aplica pero Changed=false; sin URL nueva usa la guardada.
	res, err = svc.Sync(context.Background(), "")
	if err != nil || !res.Applied || res.Changed || src.calls[1] != gURL {
		t.Errorf("2ª: res=%+v err=%v calls=%v", res, err, src.calls)
	}
	// Si la hoja cambia, Changed=true.
	src.cfg.Becas[0].Total, src.cfg.Becas[0].Pagos = 40000, []domain.Money{40000}
	if res, _ = svc.Sync(context.Background(), ""); !res.Changed {
		t.Error("debía detectar el cambio")
	}
}

func TestSyncInvalidaConservaLaVigente(t *testing.T) {
	prev := &domain.ConfigSnapshot{Config: validCfg(), Hash: "viejo"}
	bad := validCfg()
	bad.Becas[1].Total = 99900 // los pagos ya no suman
	src, st, ap := &fakeSource{cfg: bad}, &fakeStore{snap: prev, url: "https://docs.google.com/spreadsheets/guardada"}, &fakeApplier{}
	res, err := newSvc(src, st, ap, "").Sync(context.Background(), gURL)
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied || res.Summary.Errores == 0 || !domain.HasErrors(res.Issues) {
		t.Fatalf("debía rechazarse: %+v", res)
	}
	if len(ap.applied) != 0 || st.snap != prev || st.url != "https://docs.google.com/spreadsheets/guardada" {
		t.Error("una configuración inválida no debe reemplazar la vigente ni guardar la URL nueva")
	}
}

func TestSyncConErroresDeLecturaNoAgregaRuido(t *testing.T) {
	src := &fakeSource{issues: []domain.Issue{{Nivel: domain.IssueError, Campo: "Encabezados", Mensaje: "falta V/TOTAL"}}}
	res, _ := newSvc(src, &fakeStore{}, &fakeApplier{}, gURL).Sync(context.Background(), "")
	if res.Applied || len(res.Issues) != 1 {
		t.Errorf("solo debe informar el error de lectura: %+v", res.Issues)
	}
}

func TestSyncErrores(t *testing.T) {
	ctx := context.Background()
	// URL no permitida
	if _, err := newSvc(&fakeSource{}, &fakeStore{}, &fakeApplier{}, "").Sync(ctx, "https://evil.com/x"); !errors.Is(err, ports.ErrInvalidSourceURL) {
		t.Errorf("URL inválida: %v", err)
	}
	// sin URL en ningún lado
	if _, err := newSvc(&fakeSource{}, &fakeStore{}, &fakeApplier{}, "").Sync(ctx, ""); !errors.Is(err, ports.ErrNoSourceURL) {
		t.Errorf("sin URL: %v", err)
	}
	// el origen falla
	if _, err := newSvc(&fakeSource{err: errors.New("404")}, &fakeStore{}, &fakeApplier{}, gURL).Sync(ctx, ""); !errors.Is(err, ports.ErrSourceUnavailable) {
		t.Errorf("origen caído: %v", err)
	}
	// fallos del almacén
	boom := errors.New("redis caído")
	for name, st := range map[string]*fakeStore{
		"url":     {urlErr: boom},
		"current": {currentErr: boom},
		"save":    {saveErr: boom},
	} {
		if _, err := newSvc(&fakeSource{cfg: validCfg()}, st, &fakeApplier{}, gURL).Sync(ctx, ""); !errors.Is(err, boom) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestPrioridadDeURL(t *testing.T) {
	src := &fakeSource{cfg: validCfg()}
	svc := newSvc(src, &fakeStore{url: "https://docs.google.com/spreadsheets/guardada"}, &fakeApplier{}, "https://docs.google.com/spreadsheets/entorno")
	svc.Sync(context.Background(), "")
	svc.Sync(context.Background(), gURL)
	svc2 := newSvc(src, &fakeStore{}, &fakeApplier{}, "https://docs.google.com/spreadsheets/entorno")
	svc2.Sync(context.Background(), "")
	want := []string{"https://docs.google.com/spreadsheets/guardada", gURL, "https://docs.google.com/spreadsheets/entorno"}
	for i, w := range want {
		if src.calls[i] != w {
			t.Errorf("llamada %d: %q, quería %q", i, src.calls[i], w)
		}
	}
}

func TestRestore(t *testing.T) {
	ctx := context.Background()
	// Hay configuración guardada → se aplica sin tocar la red.
	ap, src := &fakeApplier{}, &fakeSource{}
	st := &fakeStore{snap: &domain.ConfigSnapshot{Config: validCfg(), Hash: "h"}}
	if err := newSvc(src, st, ap, gURL).Restore(ctx); err != nil || len(ap.applied) != 1 || len(src.calls) != 0 {
		t.Errorf("restaurar guardada: err=%v applied=%d calls=%v", err, len(ap.applied), src.calls)
	}
	// No hay nada guardado pero hay URL por defecto → sincroniza.
	ap, src = &fakeApplier{}, &fakeSource{cfg: validCfg()}
	if err := newSvc(src, &fakeStore{}, ap, gURL).Restore(ctx); err != nil || len(ap.applied) != 1 {
		t.Errorf("sincroniza al arrancar: err=%v applied=%d", err, len(ap.applied))
	}
	// Sin nada ni URL → no es un error.
	if err := newSvc(&fakeSource{}, &fakeStore{}, &fakeApplier{}, "").Restore(ctx); err != nil {
		t.Errorf("sin URL no debe fallar: %v", err)
	}
	// La hoja existe pero es inválida → se informa.
	bad := validCfg()
	bad.Becas[1].Total = 100
	if err := newSvc(&fakeSource{cfg: bad}, &fakeStore{}, &fakeApplier{}, gURL).Restore(ctx); err == nil {
		t.Error("una hoja inválida debe informarse")
	}
	// Fallo del origen y del almacén.
	if err := newSvc(&fakeSource{err: errors.New("x")}, &fakeStore{}, &fakeApplier{}, gURL).Restore(ctx); err == nil {
		t.Error("origen caído debe informarse")
	}
	if err := newSvc(&fakeSource{}, &fakeStore{currentErr: errors.New("x")}, &fakeApplier{}, gURL).Restore(ctx); err == nil {
		t.Error("almacén caído debe informarse")
	}
}

func TestStatus(t *testing.T) {
	ctx := context.Background()
	svc := newSvc(&fakeSource{}, &fakeStore{}, &fakeApplier{}, "")
	if st, err := svc.Status(ctx); err != nil || st.Sincronizada || st.URL != "" {
		t.Errorf("vacío: %+v %v", st, err)
	}
	svc = newSvc(&fakeSource{}, &fakeStore{snap: &domain.ConfigSnapshot{Hash: "h"}, url: gURL}, &fakeApplier{}, "")
	if st, err := svc.Status(ctx); err != nil || !st.Sincronizada || st.URL != gURL || st.Snapshot.Hash != "h" {
		t.Errorf("con datos: %+v %v", st, err)
	}
	for _, st := range []*fakeStore{{currentErr: errors.New("x")}, {urlErr: errors.New("x")}} {
		if _, err := newSvc(&fakeSource{}, st, &fakeApplier{}, "").Status(ctx); err == nil {
			t.Error("debía propagar el error del almacén")
		}
	}
}

// Dos sincronizaciones simultáneas no se pisan.
func TestSyncSerializada(t *testing.T) {
	src, st, ap := &fakeSource{cfg: validCfg()}, &fakeStore{}, &fakeApplier{}
	svc := newSvc(src, st, ap, gURL)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); svc.Sync(context.Background(), "") }()
	}
	wg.Wait()
	if len(ap.applied) != 20 {
		t.Errorf("applied=%d", len(ap.applied))
	}
}
