package campaignfs

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"visoria-control/internal/domain"
)

func camp() domain.Campaign {
	c := domain.Campaign{ID: "abc", Torneo: "Copa X", CreatedAt: time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC),
		Config: domain.MasterConfig{Becas: []domain.Beca{{Clave: "30", Nombre: "30%", Total: 196000, Pagos: []domain.Money{196000}}}}}
	c.Dir = c.DirName()
	return c
}

func TestGuardaOriginalYSnapshot(t *testing.T) {
	base := t.TempDir()
	s, err := New(base)
	if err != nil {
		t.Fatal(err)
	}
	c := camp()
	ctx := context.Background()
	if err := s.SaveOriginal(ctx, c, []byte("excel-bytes")); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSnapshot(ctx, c); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "campaigns", c.Dir)
	if b, _ := os.ReadFile(filepath.Join(dir, "EXCEL_ORIGINAL.xlsx")); string(b) != "excel-bytes" {
		t.Errorf("excel original: %q", b)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "campaign.json"))
	var snap struct {
		ID     string              `json:"id"`
		Torneo string              `json:"torneo"`
		Config domain.MasterConfig `json:"config"`
	}
	if err := json.Unmarshal(raw, &snap); err != nil || snap.ID != "abc" || snap.Torneo != "Copa X" || len(snap.Config.Becas) != 1 {
		t.Errorf("snapshot: %v %s", err, raw)
	}
	// Sin temporales sobrantes y con permisos restringidos (el Excel trae datos de menores).
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("temporal sin limpiar: %s", e.Name())
		}
		if info, _ := e.Info(); info.Mode().Perm()&0o007 != 0 {
			t.Errorf("%s es legible por cualquiera: %v", e.Name(), info.Mode())
		}
	}
	// Reescribir el original no deja el archivo a medias (reemplazo atómico).
	if err := s.SaveOriginal(ctx, c, []byte("v2")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "EXCEL_ORIGINAL.xlsx")); string(b) != "v2" {
		t.Errorf("reemplazo: %q", b)
	}
}

func TestAuditSoloAgregaYEsConcurrenteSeguro(t *testing.T) {
	s, _ := New(t.TempDir())
	c := camp()
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e := domain.AuditEvent{Time: time.Now().UTC(), Type: "recipient.edited", CampaignID: "abc", RecipientID: "r", Field: "club",
				Old: "a", New: strings.Repeat("ñ", i+1), By: "operador"}
			if err := s.Audit(ctx, c, e); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	d, _ := s.dir(c)
	f, _ := os.Open(filepath.Join(d, "events.jsonl"))
	defer f.Close()
	sc := bufio.NewScanner(f)
	n := 0
	for sc.Scan() {
		var e domain.AuditEvent
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil || e.Type != "recipient.edited" || e.CampaignID != "abc" {
			t.Fatalf("línea %d corrupta (¿escrituras mezcladas?): %v %q", n, err, sc.Text())
		}
		n++
	}
	if n != 50 {
		t.Errorf("líneas=%d, quería 50", n)
	}
	// Un evento posterior se agrega sin tocar los anteriores.
	s.Audit(ctx, c, domain.AuditEvent{Type: "x", CampaignID: "abc", By: "op"})
	b, _ := os.ReadFile(filepath.Join(d, "events.jsonl"))
	if got := strings.Count(string(b), "\n"); got != 51 {
		t.Errorf("líneas tras agregar: %d", got)
	}
}

func TestRechazaRutasPeligrosasYBaseVacia(t *testing.T) {
	s, _ := New(t.TempDir())
	for _, dir := range []string{"", "..", ".", "../fuera", "a/b", `a\b`, "/etc"} {
		c := camp()
		c.Dir = dir
		if err := s.SaveOriginal(context.Background(), c, []byte("x")); err == nil {
			t.Errorf("Dir %q debía rechazarse", dir)
		}
	}
	if _, err := New("  "); err == nil {
		t.Error("base vacía debe fallar")
	}
	// Una base que es un archivo no se puede usar.
	f := filepath.Join(t.TempDir(), "archivo")
	os.WriteFile(f, []byte("x"), 0o600)
	if _, err := New(f); err == nil {
		t.Error("base inválida debe fallar")
	}
}

func TestSavePDF(t *testing.T) {
	s, _ := New(t.TempDir())
	c := camp()
	ctx := context.Background()
	src := filepath.Join(t.TempDir(), "x.pdf")
	os.WriteFile(src, []byte("%PDF-1.4\ncontenido"), 0o600)

	rel, sum, err := s.SavePDF(ctx, c, "abc123", src)
	if err != nil || rel != "pdf/abc123.pdf" {
		t.Fatalf("SavePDF: %q %v", rel, err)
	}
	// SHA-256 de "%PDF-1.4\ncontenido"
	if len(sum) != 64 {
		t.Errorf("sha=%q", sum)
	}
	d, _ := s.dir(c)
	got, _ := os.ReadFile(filepath.Join(d, rel))
	if string(got) != "%PDF-1.4\ncontenido" {
		t.Errorf("contenido: %q", got)
	}
	if info, _ := os.Stat(filepath.Join(d, rel)); info.Mode().Perm()&0o007 != 0 {
		t.Errorf("permisos abiertos: %v", info.Mode())
	}
	// Reemplazar (regenerar) es atómico y no deja temporales.
	os.WriteFile(src, []byte("%PDF-1.7 nuevo"), 0o600)
	if _, sum2, err := s.SavePDF(ctx, c, "abc123", src); err != nil || sum2 == sum {
		t.Errorf("regenerar: %v (la huella debe cambiar)", err)
	}
	entries, _ := os.ReadDir(filepath.Join(d, "pdf"))
	if len(entries) != 1 {
		t.Errorf("solo debe haber un PDF: %v", entries)
	}
	// Lo que no es PDF, o no existe, se rechaza; y los IDs peligrosos también.
	bad := filepath.Join(t.TempDir(), "bad.pdf")
	os.WriteFile(bad, []byte("no soy un pdf"), 0o600)
	if _, _, err := s.SavePDF(ctx, c, "abc123", bad); err == nil {
		t.Error("un archivo que no es PDF debe rechazarse")
	}
	empty := filepath.Join(t.TempDir(), "e.pdf")
	os.WriteFile(empty, nil, 0o600)
	if _, _, err := s.SavePDF(ctx, c, "abc123", empty); err == nil {
		t.Error("un PDF vacío debe rechazarse")
	}
	if _, _, err := s.SavePDF(ctx, c, "abc123", "/no/existe.pdf"); err == nil {
		t.Error("origen inexistente")
	}
	for _, id := range []string{"", "../x", "a/b", "a b", "x.pdf"} {
		if _, _, err := s.SavePDF(ctx, c, id, src); err == nil {
			t.Errorf("ID %q debía rechazarse", id)
		}
	}
}
