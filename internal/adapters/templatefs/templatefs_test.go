package templatefs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"visoria-control/internal/ports"
)

func TestInstalarVersionarYListar(t *testing.T) {
	dir := t.TempDir()
	s, err := New(filepath.Join(dir, "plantillas"))
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return t0 }
	ctx := context.Background()

	if _, err := s.Get(ctx, "infantil"); !errors.Is(err, ports.ErrTemplateNotFound) {
		t.Errorf("antes de instalar: %v", err)
	}
	if b, err := s.Put(ctx, "infantil", []byte("v1")); err != nil || b != "" {
		t.Fatalf("primera instalación: %q %v", b, err)
	}
	// El reemplazo guarda la anterior en anteriores/{clave}_{fecha}.docx.
	b, err := s.Put(ctx, "infantil", []byte("v2"))
	if err != nil || b != "infantil_20261105_090000.docx" {
		t.Fatalf("reemplazo: %q %v", b, err)
	}
	if old, _ := os.ReadFile(filepath.Join(dir, "plantillas", "anteriores", b)); string(old) != "v1" {
		t.Errorf("la versión anterior debe quedar intacta: %q", old)
	}
	if cur, _ := s.Get(ctx, "infantil"); string(cur) != "v2" {
		t.Errorf("vigente: %q", cur)
	}
	// Dos reemplazos en el mismo segundo no se pisan.
	b3, err := s.Put(ctx, "infantil", []byte("v3"))
	if err != nil || b3 != "infantil_20261105_090000_2.docx" {
		t.Fatalf("mismo segundo: %q %v", b3, err)
	}
	if old, _ := os.ReadFile(filepath.Join(dir, "plantillas", "anteriores", b3)); string(old) != "v2" {
		t.Errorf("respaldo 2: %q", old)
	}
	if first, _ := os.ReadFile(filepath.Join(dir, "plantillas", "anteriores", b)); string(first) != "v1" {
		t.Error("el primer respaldo no debe sobrescribirse")
	}

	list, err := s.List(ctx)
	if err != nil || len(list) != 3 {
		t.Fatalf("listado: %v %v", list, err)
	}
	byKey := map[string]ports.TemplateInfo{}
	for _, i := range list {
		byKey[i.Key] = i
	}
	if i := byKey["infantil"]; !i.Installed || i.Versions != 2 || i.Size != 2 || i.Name != "Infantil" {
		t.Errorf("infantil: %+v", i)
	}
	if i := byKey["cadete_juvenil"]; i.Installed || i.Versions != 0 {
		t.Errorf("cadete: %+v", i)
	}
	// Sin archivos temporales ni permisos abiertos.
	entries, _ := os.ReadDir(filepath.Join(dir, "plantillas"))
	for _, e := range entries {
		if info, _ := e.Info(); !e.IsDir() && info.Mode().Perm()&0o007 != 0 {
			t.Errorf("%s es legible por cualquiera", e.Name())
		}
		if len(e.Name()) > 5 && e.Name()[:5] == ".tmp-" {
			t.Errorf("temporal sin limpiar: %s", e.Name())
		}
	}
}

func TestClavesYBase(t *testing.T) {
	s, _ := New(t.TempDir())
	ctx := context.Background()
	// Una clave desconocida (o con ruta) no escribe ni lee nada fuera de la carpeta.
	for _, k := range []string{"", "otra", "../x", "infantil/../../x", "cadete_juvenil.docx"} {
		if _, err := s.Put(ctx, k, []byte("x")); err == nil {
			t.Errorf("Put(%q) debía fallar", k)
		}
		if _, err := s.Get(ctx, k); !errors.Is(err, ports.ErrTemplateNotFound) {
			t.Errorf("Get(%q): %v", k, err)
		}
	}
	if _, err := New(" "); err == nil {
		t.Error("carpeta vacía debe fallar")
	}
	f := filepath.Join(t.TempDir(), "archivo")
	os.WriteFile(f, []byte("x"), 0o600)
	if _, err := New(f); err == nil {
		t.Error("una carpeta que es un archivo debe fallar")
	}
}
