package docx

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// Prueba DIFERENCIAL contra el código del cliente: testdata/python_cases.json lo genera
// testdata/gen_cases.py ejecutando, sin modificarlas, _reemplazar, es_encabezado, formato_euros y
// las expresiones regulares de MajesticBecas/becas/generador.py. Aquí se exige el mismo resultado.

type pyCases struct {
	Regex []struct {
		Texto       string  `json:"texto"`
		Encabezado  bool    `json:"encabezado"`
		X           *[2]int `json:"x"`
		Fecha       *[2]int `json:"fecha"`
		Torneo      *[2]int `json:"torneo"`
		Placeholder *[2]int `json:"placeholder"`
		XXX         *[2]int `json:"xxx"`
	} `json:"regex"`
	Reemplazo []struct {
		Runs      []string `json:"runs"`
		Patron    string   `json:"patron"`
		Nuevo     string   `json:"nuevo"`
		OK        bool     `json:"ok"`
		Resultado []string `json:"resultado"`
	} `json:"reemplazo"`
	Euros []struct {
		N     int    `json:"n"`
		Texto string `json:"texto"`
	} `json:"euros"`
}

func loadCases(t *testing.T) pyCases {
	t.Helper()
	b, err := os.ReadFile("testdata/python_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var c pyCases
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func spanOf(p *[2]int) *span {
	if p == nil {
		return nil
	}
	return &span{p[0], p[1]}
}

func got(m matcher, text string) *span {
	s, ok := m([]rune(text))
	if !ok {
		return nil
	}
	return &s
}

func TestRegexIgualAPython(t *testing.T) {
	c := loadCases(t)
	if len(c.Regex) < 1000 {
		t.Fatalf("pocos casos: %d", len(c.Regex))
	}
	matchers := []struct {
		name string
		m    matcher
		want func(i int) *span
	}{
		{"RE_X", matchX, func(i int) *span { return spanOf(c.Regex[i].X) }},
		{"RE_FECHA_X", matchFechaX, func(i int) *span { return spanOf(c.Regex[i].Fecha) }},
		{"RE_TORNEO", matchTorneo, func(i int) *span { return spanOf(c.Regex[i].Torneo) }},
		{"RE_PLACEHOLDER", matchPlaceholder, func(i int) *span { return spanOf(c.Regex[i].Placeholder) }},
		{"X{3}", matchXXX, func(i int) *span { return spanOf(c.Regex[i].XXX) }},
	}
	fails := map[string]int{}
	for i, k := range c.Regex {
		for _, mm := range matchers {
			g, w := got(mm.m, k.Texto), mm.want(i)
			if !reflect.DeepEqual(g, w) {
				if fails[mm.name]++; fails[mm.name] <= 3 {
					t.Errorf("%s sobre %q: Go=%v Python=%v", mm.name, k.Texto, g, w)
				}
			}
		}
		if esEncabezado(k.Texto) != k.Encabezado {
			if fails["es_encabezado"]++; fails["es_encabezado"] <= 3 {
				t.Errorf("es_encabezado(%q): Go=%v Python=%v", k.Texto, esEncabezado(k.Texto), k.Encabezado)
			}
		}
	}
	for name, n := range fails {
		t.Errorf("%s: %d discrepancias de %d casos", name, n, len(c.Regex))
	}
	// El corpus debe ejercitar de verdad los casos delicados (si no, la prueba no vale).
	var torneo, retroceso int
	for _, k := range c.Regex {
		if k.Torneo != nil {
			torneo++
			if r := []rune(k.Texto); k.Torneo[1] < len(r) && r[k.Torneo[1]] == 'X' {
				retroceso++ // coincidencia que dejó una X sin consumir: retroceso por (?!/)
			}
		}
	}
	if torneo < 500 || retroceso < 5 {
		t.Errorf("el corpus es pobre: torneo=%d retroceso=%d", torneo, retroceso)
	}
}

func TestReemplazoEnRunsIgualAPython(t *testing.T) {
	c := loadCases(t)
	byName := map[string]matcher{"x": matchX, "fecha": matchFechaX, "torneo": matchTorneo, "placeholder": matchPlaceholder, "xxx": matchXXX}
	fails := 0
	for _, k := range c.Reemplazo {
		out, ok := replaceInRuns(k.Runs, byName[k.Patron], k.Nuevo)
		if ok != k.OK || (ok && !reflect.DeepEqual(out, k.Resultado)) {
			if fails++; fails <= 5 {
				t.Errorf("patrón %s nuevo=%q runs=%q:\n Go=%q (%v)\n Py=%q (%v)", k.Patron, k.Nuevo, k.Runs, out, ok, k.Resultado, k.OK)
			}
		}
	}
	if fails > 0 {
		t.Errorf("%d discrepancias de %d casos", fails, len(c.Reemplazo))
	}
}

func TestFormatEurosIgualAPython(t *testing.T) {
	for _, k := range loadCases(t).Euros {
		if g := FormatEuros(k.N); g != k.Texto {
			t.Errorf("FormatEuros(%d)=%q, Python=%q", k.N, g, k.Texto)
		}
	}
}
