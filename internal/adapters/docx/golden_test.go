package docx

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"visoria-control/internal/domain"
)

// Golden files AUTÉNTICOS: los genera tools/golden/generate_golden.py con el código real del cliente
// (python-docx). Guardan solo lo que cambia respecto a la plantilla (así no incluyen su texto fijo, que
// trae datos bancarios). Aquí se genera el mismo caso con Go y se exige el mismo resultado.

type golden struct {
	Generador string   `json:"generador"`
	Plantilla string   `json:"plantilla"`
	Beca      string   `json:"beca"`
	Torneo    string   `json:"torneo"`
	Emision   string   `json:"emision"`
	Fechas    []string `json:"fechas"`
	Cambios   struct {
		Parrafos map[string]string `json:"parrafos"`
		Celdas   map[string]string `json:"celdas"`
	} `json:"cambios"`
	Columnas []int `json:"columnas_tabla_pagos"`
	NumPars  int   `json:"num_parrafos"`
}

// diffDocs calcula lo mismo que diferencias() del script de Python.
func diffDocs(before, after doc) (pars map[string]string, cells map[string]string) {
	pars, cells = map[string]string{}, map[string]string{}
	for i := range before.Paras {
		if i < len(after.Paras) && before.Paras[i] != after.Paras[i] {
			pars[strconv.Itoa(i)] = after.Paras[i]
		}
	}
	for ti := range before.Tables {
		for ri := range before.Tables[ti] {
			for ci := range before.Tables[ti][ri] {
				if ti < len(after.Tables) && ri < len(after.Tables[ti]) && ci < len(after.Tables[ti][ri]) &&
					before.Tables[ti][ri][ci] != after.Tables[ti][ri][ci] {
					cells[fmt.Sprintf("%d,%d,%d", ti, ri, ci)] = after.Tables[ti][ri][ci]
				}
			}
		}
	}
	return
}

// compareGolden genera el caso con Go y lo compara con el golden.
func compareGolden(t *testing.T, g golden, template []byte) {
	t.Helper()
	b, _ := cfg().Beca(domain.BecaKey(g.Beca))
	if len(b.Pagos) != len(g.Fechas) {
		t.Fatalf("el golden %s/%s no coincide con la configuración", g.Plantilla, g.Beca)
	}
	in := input(domain.BecaKey(g.Beca), g.Fechas...)
	in.Torneo, in.FechaEmision = g.Torneo, g.Emision
	out, err := NewRenderer().Render(context.Background(), template, in)
	if err != nil {
		t.Fatalf("%s/%s: %v", g.Plantilla, g.Beca, err)
	}
	pars, cells := diffDocs(extract(t, template), extract(t, out))
	if !reflect.DeepEqual(pars, g.Cambios.Parrafos) {
		t.Errorf("%s/%s párrafos distintos:\n Go=%v\n Py=%v", g.Plantilla, g.Beca, pars, g.Cambios.Parrafos)
	}
	if !reflect.DeepEqual(cells, g.Cambios.Celdas) {
		t.Errorf("%s/%s celdas distintas:\n Go=%v\n Py=%v", g.Plantilla, g.Beca, cells, g.Cambios.Celdas)
	}
	d := extract(t, out)
	var cols []int
	for _, row := range d.Tables[1] {
		cols = append(cols, len(row))
	}
	if !reflect.DeepEqual(cols, g.Columnas) || len(d.Paras) != g.NumPars {
		t.Errorf("%s/%s estructura: columnas Go=%v Py=%v, párrafos Go=%d Py=%d", g.Plantilla, g.Beca, cols, g.Columnas, len(d.Paras), g.NumPars)
	}
}

func TestGoldenDelCliente(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("testdata", "golden", "*.json"))
	if len(files) == 0 {
		t.Skip("no hay golden files: genérelos con tools/golden/generate_golden.py (ver su cabecera)")
	}
	templates := realTemplates(t)
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var g golden
		if err := json.Unmarshal(raw, &g); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if g.Generador != "majestic-python" {
			t.Errorf("%s: el golden debe venir del código del cliente (generador=%q)", f, g.Generador)
		}
		tpl, ok := templates[g.Plantilla]
		if !ok {
			t.Fatalf("%s: plantilla desconocida %q", f, g.Plantilla)
		}
		compareGolden(t, g, tpl)
	}
}

// La maquinaria de comparación se prueba con un golden construido a partir de la plantilla sintética:
// si la comparación estuviera rota, el test del cliente pasaría sin comprobar nada.
func TestMaquinariaDeGolden(t *testing.T) {
	template := tpl{}.docx(t)
	in := input("70", fechas3[:2]...)
	in.Torneo, in.FechaEmision = "Copa Prueba", "05/11/2026"
	out, err := NewRenderer().Render(context.Background(), template, in)
	if err != nil {
		t.Fatal(err)
	}
	var g golden
	g.Generador, g.Plantilla, g.Beca, g.Torneo, g.Emision, g.Fechas = "sintetico", "x", "70", "Copa Prueba", "05/11/2026", fechas3[:2]
	g.Cambios.Parrafos, g.Cambios.Celdas = diffDocs(extract(t, template), extract(t, out))
	d := extract(t, out)
	for _, row := range d.Tables[1] {
		g.Columnas = append(g.Columnas, len(row))
	}
	g.NumPars = len(d.Paras)
	compareGolden(t, g, template) // debe coincidir consigo mismo

	if len(g.Cambios.Parrafos) < 6 || g.Cambios.Parrafos["0"] != "COPA PRUEBA\tFecha:05/11/2026" || g.Cambios.Celdas["1,0,2"] != "2do pago € 495" {
		t.Errorf("el diff no captura lo esperado: %v %v", g.Cambios.Parrafos, g.Cambios.Celdas)
	}
	// Y detecta una diferencia real.
	g.Cambios.Parrafos["2"] = "Jugador: OTRO"
	if reflect.DeepEqual(g.Cambios.Parrafos, mustDiff(t, template, out)) {
		t.Error("la comparación no distingue una diferencia")
	}
}

func mustDiff(t *testing.T, template, out []byte) map[string]string {
	p, _ := diffDocs(extract(t, template), extract(t, out))
	return p
}
