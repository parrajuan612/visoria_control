package domain

import (
	"strings"
	"testing"
)

func TestParseMoneyEUR(t *testing.T) {
	ok := map[string]Money{
		" €  2.800,00 ": 280000, "€ 930,00": 93000, "930,5": 93050, "200": 20000, "2800": 280000,
		"1.960,00": 196000, "2.800": 280000, "930.50": 93050, "0,99": 99, " € 495,00": 49500, "1.000.000,00": 100000000,
	}
	for in, want := range ok {
		got, present, err := ParseMoneyEUR(in)
		if err != nil || !present || got != want {
			t.Errorf("ParseMoneyEUR(%q)=%d,%v,%v quería %d", in, got, present, err, want)
		}
	}
	for _, blank := range []string{"", "   ", "€", " € "} {
		if _, present, err := ParseMoneyEUR(blank); present || err != nil {
			t.Errorf("%q debe ser vacío sin error", blank)
		}
	}
	for _, bad := range []string{"abc", "-5,00", "1,2,3", "12,345", "1.2.3", "1.96,00", "€ 12a", "1.234.56", "9999999999999,00"} {
		if _, present, err := ParseMoneyEUR(bad); err == nil || !present {
			t.Errorf("%q debía fallar", bad)
		}
	}
}

func TestFormatEUR(t *testing.T) {
	for in, want := range map[Money]string{280000: "€ 2.800,00", 93050: "€ 930,50", 5: "€ 0,05", 100000000: "€ 1.000.000,00", -150: "-€ 1,50", 0: "€ 0,00"} {
		if got := FormatEUR(in); got != want {
			t.Errorf("FormatEUR(%d)=%q quería %q", in, got, want)
		}
	}
	// ida y vuelta
	for _, m := range []Money{1, 99, 100, 123456, 99999999} {
		back, _, err := ParseMoneyEUR(FormatEUR(m))
		if err != nil || back != m {
			t.Errorf("ida y vuelta de %d dio %d (%v)", m, back, err)
		}
	}
}

func TestParseYearRange(t *testing.T) {
	ok := map[string]YearRange{"2008-2010": {2008, 2010}, " 2011 – 2012 ": {2011, 2012}, "2015—2016": {2015, 2016}, "2011": {2011, 2011}}
	for in, want := range ok {
		if got, err := ParseYearRange(in); err != nil || got != want {
			t.Errorf("%q → %v %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "abc", "20-21", "2010-2008", "2008-2010-2012", "2008-", "20a8-2010"} {
		if _, err := ParseYearRange(bad); err == nil {
			t.Errorf("%q debía fallar", bad)
		}
	}
	r := YearRange{2008, 2010}
	if !r.Contains(2008) || !r.Contains(2010) || r.Contains(2011) || r.String() != "2008-2010" || (YearRange{2011, 2011}).String() != "2011" {
		t.Error("YearRange.Contains/String")
	}
}

func TestNormalizeBecaKeyYPlantilla(t *testing.T) {
	keys := map[string]BecaKey{"SIN BECA": BecaSinBeca, " sin  beca ": BecaSinBeca, "30%": "30", "50 %": "50", "100": "100", "ACOMPAÑANTE": BecaAcompanante, "Acompanante": BecaAcompanante}
	for in, want := range keys {
		if got, ok := NormalizeBecaKey(in); !ok || got != want {
			t.Errorf("NormalizeBecaKey(%q)=%q,%v", in, got, ok)
		}
	}
	for _, bad := range []string{"", "VIP", "0%", "150%", "abc%"} {
		if _, ok := NormalizeBecaKey(bad); ok {
			t.Errorf("%q no debe reconocerse", bad)
		}
	}
	pl := map[string]string{"JUVENIL": "cadete_juvenil", "cadete": "cadete_juvenil", "INFANTIL": "infantil", "Alevín": "benjamin_alevin", "BENJAMIN": "benjamin_alevin", "SENIOR": ""}
	for in, want := range pl {
		if got := PlantillaParaCategoria(in); got != want {
			t.Errorf("PlantillaParaCategoria(%q)=%q", in, got)
		}
	}
}

// configValida replica los datos reales de la hoja del cliente.
func configValida() MasterConfig {
	beca := func(n string, fila int, total Money, pagos ...Money) Beca {
		k, _ := NormalizeBecaKey(n)
		return Beca{Clave: k, Nombre: n, Total: total, Pagos: pagos, Fila: fila}
	}
	cat := func(y YearRange, n string, fila int) CategoriaRango {
		return CategoriaRango{Anios: y, Nombre: n, Plantilla: PlantillaParaCategoria(n), Fila: fila}
	}
	return MasterConfig{
		Becas: []Beca{
			beca("SIN BECA", 2, 280000, 93000, 93000, 94000),
			beca("30%", 3, 196000, 65000, 65000, 66000),
			beca("50%", 4, 155000, 50000, 50000, 55000),
			beca("70%", 5, 99000, 49500, 49500),
			beca("100%", 6, 20000, 20000),
			beca("ACOMPAÑANTE", 7, 180000, 60000, 60000, 60000),
		},
		Torneos: []TorneoRango{
			{YearRange{2008, 2014}, "COPA MIC", 2}, {YearRange{2011, 2014}, "EASTER CUP", 3}, {YearRange{2015, 2018}, "VILLARREAL", 4},
		},
		Categorias: []CategoriaRango{
			cat(YearRange{2008, 2010}, "JUVENIL", 2), cat(YearRange{2011, 2012}, "CADETE", 3), cat(YearRange{2013, 2014}, "INFANTIL", 4),
			cat(YearRange{2015, 2016}, "ALEVIN", 5), cat(YearRange{2017, 2018}, "BENJAMIN", 6),
		},
	}
}

func find(issues []Issue, lvl IssueLevel, sub string) bool {
	for _, i := range issues {
		if i.Nivel == lvl && strings.Contains(i.Mensaje+" "+i.Campo, sub) {
			return true
		}
	}
	return false
}

func TestValidateConfigRealEsValida(t *testing.T) {
	issues := configValida().Validate()
	if HasErrors(issues) {
		t.Fatalf("la configuración real no debe tener errores: %+v", issues)
	}
	if !find(issues, IssueInfo, "2011-2014") {
		t.Errorf("debía informar el solape intencional de torneos 2011-2014: %+v", issues)
	}
	if c := CountByLevel(issues); c[IssueInfo] != 1 || c[IssueError] != 0 {
		t.Errorf("conteo: %v", c)
	}
}

func TestValidateErroresDeBecas(t *testing.T) {
	mut := func(f func(*MasterConfig)) []Issue {
		c := configValida()
		f(&c)
		return c.Validate()
	}
	cases := []struct {
		name string
		f    func(*MasterConfig)
		sub  string
	}{
		{"suma distinta", func(c *MasterConfig) { c.Becas[1].Pagos[2] = 70000 }, "suman € 2.000,00 pero el precio total es € 1.960,00 (diferencia € 40,00)"},
		{"duplicada", func(c *MasterConfig) { c.Becas = append(c.Becas, c.Becas[1]) }, "está repetida"},
		{"falta requerida", func(c *MasterConfig) { c.Becas = c.Becas[:1] }, `falta la beca "30%"`},
		{"total cero", func(c *MasterConfig) { c.Becas[0].Total = 0 }, "mayor que cero"},
		{"sin pagos", func(c *MasterConfig) { c.Becas[0].Pagos = nil }, "ningún pago"},
		{"demasiados pagos", func(c *MasterConfig) { c.Becas[0].Pagos = []Money{1, 1, 1, 1} }, "máximo es 3"},
		{"pago cero", func(c *MasterConfig) { c.Becas[0].Pagos = []Money{280000, 0} }, "pago #2"},
		{"sin becas", func(c *MasterConfig) { c.Becas = nil }, "ninguna beca"},
	}
	for _, c := range cases {
		if got := mut(c.f); !find(got, IssueError, c.sub) {
			t.Errorf("%s: faltó el error %q en %+v", c.name, c.sub, got)
		}
	}
	// El mensaje de la suma incluye la fila para que el cliente la encuentre.
	got := mut(func(c *MasterConfig) { c.Becas[1].Pagos[2] = 70000 })
	if !find(got, IssueError, "Becas · fila 3 (30%)") {
		t.Errorf("falta la ubicación: %+v", got)
	}
}

func TestValidateErroresDeAnios(t *testing.T) {
	mut := func(f func(*MasterConfig)) []Issue {
		c := configValida()
		f(&c)
		return c.Validate()
	}
	cases := []struct {
		name string
		lvl  IssueLevel
		f    func(*MasterConfig)
		sub  string
	}{
		{"categorías solapadas", IssueError, func(c *MasterConfig) { c.Categorias[1].Anios = YearRange{2010, 2012} }, "2010 están en dos categorías"},
		{"año fuera de rango (torneo)", IssueError, func(c *MasterConfig) { c.Torneos[0].Anios = YearRange{1850, 2014} }, "fuera del rango plausible"},
		{"año fuera de rango (categoría)", IssueError, func(c *MasterConfig) { c.Categorias[0].Anios = YearRange{2008, 2090} }, "fuera del rango plausible"},
		{"torneo sin nombre", IssueError, func(c *MasterConfig) { c.Torneos[0].Nombre = "" }, "no tiene nombre"},
		{"categoría sin nombre", IssueError, func(c *MasterConfig) { c.Categorias[0].Nombre = "" }, "no tiene nombre"},
		{"sin torneos", IssueError, func(c *MasterConfig) { c.Torneos = nil }, "ningún torneo"},
		{"sin categorías", IssueError, func(c *MasterConfig) { c.Categorias = nil }, "ninguna categoría"},
		{"categoría sin plantilla", IssueWarning, func(c *MasterConfig) { c.Categorias[0].Plantilla = "" }, "no tiene una plantilla"},
		{"categoría sin torneo", IssueWarning, func(c *MasterConfig) { c.Torneos[2].Anios = YearRange{2015, 2016} }, "2017-2018 tienen categoría pero ningún torneo"},
		{"torneo sin categoría", IssueWarning, func(c *MasterConfig) { c.Categorias = c.Categorias[:4] }, "2017-2018 tienen torneo pero ninguna categoría"},
	}
	for _, c := range cases {
		if got := mut(c.f); !find(got, c.lvl, c.sub) {
			t.Errorf("%s: faltó %s %q en %+v", c.name, c.lvl, c.sub, got)
		}
	}
	// Solo advertencias → no bloquea.
	if HasErrors(mut(func(c *MasterConfig) { c.Categorias[0].Plantilla = "" })) {
		t.Error("una advertencia no debe contar como error")
	}
}

func TestConsultasYHash(t *testing.T) {
	c := configValida()
	if b, ok := c.Beca("50"); !ok || b.Total != 155000 {
		t.Errorf("Beca(50)=%+v %v", b, ok)
	}
	if _, ok := c.Beca("99"); ok {
		t.Error("beca inexistente")
	}
	if got := c.TorneosParaAnio(2012); len(got) != 2 || got[0] != "COPA MIC" || got[1] != "EASTER CUP" {
		t.Errorf("TorneosParaAnio(2012)=%v", got)
	}
	if got := c.TorneosParaAnio(2000); got != nil {
		t.Errorf("2000 no tiene torneos: %v", got)
	}
	if got := c.CategoriasParaAnio(2013); len(got) != 1 || got[0].Nombre != "INFANTIL" {
		t.Errorf("CategoriasParaAnio(2013)=%v", got)
	}
	// El hash ignora las filas pero detecta cambios de contenido.
	h1 := c.Hash()
	c.Becas[0].Fila = 99
	if c.Hash() != h1 {
		t.Error("el hash no debe depender de la fila")
	}
	c.Becas[0].Total++
	if c.Hash() == h1 {
		t.Error("el hash debe cambiar si cambia un precio")
	}
	if len(h1) != 16 {
		t.Errorf("hash=%q", h1)
	}
}

func TestHelpersDeAnios(t *testing.T) {
	if got := compactYears([]int{2008, 2009, 2010, 2013, 2015, 2016}); got != "2008-2010, 2013, 2015-2016" {
		t.Errorf("compactYears=%q", got)
	}
	if compactYears(nil) != "" {
		t.Error("vacío")
	}
	if got := abs(-5); got != 5 {
		t.Error("abs")
	}
	if got := NormalizeText("  ñandú   Ál "); got != "NANDU AL" {
		t.Errorf("NormalizeText=%q", got)
	}
	if BecaLabel("50") != "50%" || BecaLabel(BecaAcompanante) != "ACOMPAÑANTE" {
		t.Error("becaLabel")
	}
	// rangos absurdos no cuelgan el cálculo de cobertura
	c := configValida()
	c.Torneos = append(c.Torneos, TorneoRango{Anios: YearRange{0, 99999999}, Nombre: "X"})
	_ = c.Validate()
}
