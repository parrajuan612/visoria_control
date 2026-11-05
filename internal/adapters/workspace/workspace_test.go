package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAbreEscribeYLimpia(t *testing.T) {
	base := filepath.Join(t.TempDir(), "trabajo")
	w := New(base)
	dir, cleanup, err := w.Open()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(dir) != base {
		t.Errorf("debe crearse dentro de la base: %s", dir)
	}
	p := filepath.Join(dir, "a.docx")
	if err := w.WriteFile(p, []byte("datos")); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(p); info.Mode().Perm() != 0o600 {
		t.Errorf("permisos %v: el contenido son datos de menores", info.Mode().Perm())
	}
	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("la carpeta debe borrarse con todo su contenido")
	}
	// Sin base usa el temporal del sistema.
	d2, c2, err := New("").Open()
	if err != nil {
		t.Fatal(err)
	}
	defer c2()
	if _, err := os.Stat(d2); err != nil {
		t.Error(err)
	}
	if _, _, err := New(filepath.Join(t.TempDir(), "x", string(rune(0)))).Open(); err == nil {
		t.Error("una base inválida debe fallar")
	}
}
