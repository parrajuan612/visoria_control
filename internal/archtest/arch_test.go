// Package archtest hace cumplir las reglas de la arquitectura hexagonal (SDD §3.4).
package archtest

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// domain, app y ports no importan infraestructura ni adaptadores.
func TestNucleoSinInfraestructura(t *testing.T) {
	prohibidos := []string{"os", "net/http", "github.com/gin-gonic", "github.com/redis", "github.com/hibiken", "database/sql",
		"visoria-control/internal/adapters", "visoria-control/cmd"}
	for _, dir := range []string{"../domain", "../app", "../ports"} {
		files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			af, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range af.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				for _, bad := range prohibidos {
					if path == bad || strings.HasPrefix(path, bad+"/") {
						t.Errorf("%s importa %q: el núcleo no puede depender de infraestructura", f, path)
					}
				}
			}
		}
	}
}

// Fuera de platform/config no se llama a os.Getenv.
func TestSinGetenvFueraDeConfig(t *testing.T) {
	filepath.Walk("..", func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		if strings.Contains(filepath.ToSlash(p), "platform/config/") {
			return nil
		}
		b, _ := os.ReadFile(p)
		if strings.Contains(string(b), "os.Getenv") {
			t.Errorf("%s usa os.Getenv; debe ir por platform/config", p)
		}
		return nil
	})
}
