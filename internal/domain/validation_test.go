package domain

import (
	"strings"
	"testing"
)

func TestParseBirthYear(t *testing.T) {
	ok := map[string]int{"2010": 2010, " 2012 ": 2012, "2010.0": 2010, "1999": 1999}
	for in, want := range ok {
		if y, warn, err := ParseBirthYear(in); err != nil || y != want || warn != "" {
			t.Errorf("%q → %d %q %v", in, y, warn, err)
		}
	}
	// Una fecha completa o un número de serie de Excel se toleran con advertencia.
	warns := map[string]int{"15/03/2010": 2010, "2010-03-15": 2010, "15-03-2011": 2011, "40253": 2010}
	for in, want := range warns {
		y, warn, err := ParseBirthYear(in)
		if err != nil || y != want || !strings.Contains(warn, "solo el año") {
			t.Errorf("%q → %d %q %v", in, y, warn, err)
		}
	}
	for _, bad := range []string{"", "  ", "abc", "10", "03/15/10", "1850", "2099", "5", "999999", "2010.5"} {
		if _, _, err := ParseBirthYear(bad); err == nil {
			t.Errorf("%q debía fallar", bad)
		}
	}
}

func TestParseBecaCell(t *testing.T) {
	ok := map[string]BecaKey{"50%": "50", "50": "50", " 30 % ": "30", "0,5": "50", "0.7": "70", "0.7000000000000001": "70",
		"SIN BECA": BecaSinBeca, "sin beca": BecaSinBeca, "100": "100", "ACOMPAÑANTE": BecaAcompanante, "50.0": "50"}
	for in, want := range ok {
		if got, err := ParseBecaCell(in); err != nil || got != want {
			t.Errorf("%q → %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "VIP", "150", "0", "-5", "abc%"} {
		if _, err := ParseBecaCell(bad); err == nil {
			t.Errorf("%q debía fallar", bad)
		}
	}
}

func TestNormalizePhone(t *testing.T) {
	ok := map[string]string{
		"3209323449": "573209323449", "320 932 3449": "573209323449", "320-932-3449": "573209323449",
		"573209323449": "573209323449", "+57 320 932 3449": "573209323449", "(+57) 3209323449": "573209323449",
		"0057 320 9323449": "573209323449", "3.209323449E9": "573209323449", "3209323449.0": "573209323449",
		"5.73209323449E11": "573209323449", "+34 612 345 678": "34612345678", "0034612345678": "34612345678",
	}
	for in, want := range ok {
		if got, err := NormalizePhone(in); err != nil || got != want {
			t.Errorf("NormalizePhone(%q)=%q,%v quería %q", in, got, err, want)
		}
	}
	if got, err := NormalizePhone("   "); got != "" || err != nil {
		t.Errorf("vacío: %q %v", got, err)
	}
	bad := map[string]string{
		"31245": "incompleto", "3209323": "incompleto", "2109323449": "empezar por 3", "5732093234": "empezar por 3",
		"57": "incompleto", "573209": "incompleto", "5712345678901": "incompleto", "571209323449": "empezar por 3",
		"320abc3449": "caracteres no válidos", "+": "no tiene dígitos", "12345678901": "largo válido", "+57 320": "incompleto",
	}
	for in, sub := range bad {
		if _, err := NormalizePhone(in); err == nil || !strings.Contains(err.Error(), sub) {
			t.Errorf("NormalizePhone(%q) debía fallar con %q: %v", in, sub, err)
		}
	}
	// El mensaje reproduce el ejemplo del SDD.
	if _, err := NormalizePhone("31245"); err == nil || err.Error() != "MOVIL «31245» está incompleto (10 dígitos)" {
		t.Errorf("mensaje: %v", err)
	}
	if NationalPhone("573209323449") != "3209323449" || NationalPhone("34612345678") != "34612345678" {
		t.Error("NationalPhone")
	}
}

func TestParseEmails(t *testing.T) {
	v, inv := ParseEmails(" Papa@Gmail.com; mama@hotmail.com , papa@gmail.com\nfoo bar@ x@y @gmail.com a..b@x.com <ok@x.co> ")
	if strings.Join(v, "|") != "papa@gmail.com|mama@hotmail.com|ok@x.co" {
		t.Errorf("válidos: %v", v)
	}
	if len(inv) != 5 { // foo, bar@, x@y, @gmail.com, a..b@x.com
		t.Errorf("inválidos: %v", inv)
	}
	if v, inv := ParseEmails(""); v != nil || inv != nil {
		t.Error("vacío")
	}
	long := strings.Repeat("a", 250) + "@x.com"
	if v, _ := ParseEmails(long); len(v) != 0 {
		t.Error("un correo de más de 254 caracteres no es válido")
	}
}

func cfgReal() MasterConfig { return configValida() }

func row(n int, jug, club, beca, anio, acud, movil, correo string) RawRow {
	return RawRow{Fila: n, Jugador: jug, Club: club, Beca: beca, Anio: anio, Acudiente: acud, Movil: movil, Correo: correo}
}

func issue(r Recipient, lvl IssueLevel, campo, sub string) bool {
	for _, i := range r.Issues {
		if i.Nivel == lvl && i.Campo == campo && strings.Contains(i.Mensaje, sub) {
			return true
		}
	}
	return false
}

func TestValidateRowValidaConAmbosCanales(t *testing.T) {
	r := ValidateRow(row(2, " ATOM  JOSE  COLMENARES ", "Club X", "50%", "2012", "María Pérez", "320 932 3449", "Papa@gmail.com"), cfgReal(), ValidationOptions{})
	if r.Estado != RecipientValid || HasErrors(r.Issues) || len(r.Issues) != 0 {
		t.Fatalf("debía ser válida y sin hallazgos: %+v", r)
	}
	if r.Jugador != "ATOM JOSE COLMENARES" || r.Beca != "50" || r.Anio != 2012 || r.Movil != "573209323449" ||
		r.Categoria != "CADETE" || r.Plantilla != "cadete_juvenil" || len(r.Torneos) != 2 || r.Correos[0] != "papa@gmail.com" {
		t.Errorf("normalización: %+v", r)
	}
	if !r.HasWhatsApp() || !r.HasEmail() {
		t.Error("debe tener ambos canales")
	}
}

func TestValidateRowSinBecaSeOmite(t *testing.T) {
	r := ValidateRow(row(3, "JUAN", "C", "SIN BECA", "2012", "A", "", ""), cfgReal(), ValidationOptions{})
	if r.Estado != RecipientOmitted || HasErrors(r.Issues) || !issue(r, IssueInfo, CampoBeca, "SIN BECA") {
		t.Errorf("omitida: %+v", r)
	}
	// Una beca vacía NO es "sin beca": es un error.
	if r := ValidateRow(row(4, "JUAN", "C", "", "2012", "A", "3209323449", ""), cfgReal(), ValidationOptions{}); r.Estado != RecipientInvalid || !issue(r, IssueError, CampoBeca, "falta la beca") {
		t.Errorf("beca vacía: %+v", r)
	}
}

func TestValidateRowObligatorios(t *testing.T) {
	r := ValidateRow(row(5, "", "", "30%", "2012", "", "3209323449", ""), cfgReal(), ValidationOptions{})
	if r.Estado != RecipientInvalid {
		t.Fatal("debía ser inválida")
	}
	for _, c := range []string{CampoJugador, CampoClub, CampoAcudiente} {
		if !issue(r, IssueError, c, "falta el dato") {
			t.Errorf("falta el error de %s", c)
		}
	}
}

func TestValidateRowBecaYAnio(t *testing.T) {
	cfg := cfgReal()
	cases := []struct {
		name string
		raw  RawRow
		camp string
		sub  string
	}{
		{"beca inválida", row(2, "J", "C", "VIP", "2012", "A", "3209323449", ""), CampoBeca, "no es válida"},
		{"beca fuera de la hoja", row(2, "J", "C", "40%", "2012", "A", "3209323449", ""), CampoBeca, "no existe en la configuración"},
		{"año vacío", row(2, "J", "C", "50%", "", "A", "3209323449", ""), CampoAnio, "falta el año"},
		{"año inválido", row(2, "J", "C", "50%", "20x2", "A", "3209323449", ""), CampoAnio, "no es válido"},
		{"año sin categoría", row(2, "J", "C", "50%", "2020", "A", "3209323449", ""), CampoCategoria, "año 2020"},
		{"año sin torneo", row(2, "J", "C", "50%", "2020", "A", "3209323449", ""), CampoTorneo, "año 2020"},
	}
	for _, c := range cases {
		if r := ValidateRow(c.raw, cfg, ValidationOptions{}); r.Estado != RecipientInvalid || !issue(r, IssueError, c.camp, c.sub) {
			t.Errorf("%s: %+v", c.name, r.Issues)
		}
	}
	// La fecha completa se acepta con advertencia (sigue válida).
	r := ValidateRow(row(2, "J", "C", "50%", "15/03/2012", "A", "3209323449", ""), cfg, ValidationOptions{})
	if r.Estado != RecipientValid || !issue(r, IssueWarning, CampoAnio, "solo el año") || r.Anio != 2012 {
		t.Errorf("fecha completa: %+v", r)
	}
	// Categoría sin plantilla conocida → error (no se podría generar el documento).
	c2 := cfgReal()
	c2.Categorias[1].Plantilla = ""
	if r := ValidateRow(row(2, "J", "C", "50%", "2012", "A", "3209323449", ""), c2, ValidationOptions{}); !issue(r, IssueError, CampoCategoria, "plantilla") {
		t.Errorf("sin plantilla: %+v", r.Issues)
	}
	// SIN BECA con otros datos malos sigue marcando los errores (no se oculta una beca inválida).
	if r := ValidateRow(row(2, "J", "C", "40%", "2012", "A", "x", ""), cfg, ValidationOptions{}); r.Estado == RecipientOmitted {
		t.Error("una beca inexistente no es omitida")
	}
}

func TestValidateRowCanales(t *testing.T) {
	cfg, def := cfgReal(), ValidationOptions{}
	base := func(movil, correo string, opt ValidationOptions) Recipient {
		return ValidateRow(row(2, "J", "C", "50%", "2012", "A", movil, correo), cfg, opt)
	}
	// Solo móvil → válida con advertencia (D3).
	if r := base("3209323449", "", def); r.Estado != RecipientValid || !issue(r, IssueWarning, CampoCorreo, "solo recibirá WhatsApp") || r.HasEmail() {
		t.Errorf("solo móvil: %+v", r)
	}
	// Solo correo → válida con advertencia.
	if r := base("", "a@b.co", def); r.Estado != RecipientValid || !issue(r, IssueWarning, CampoMovil, "solo recibirá correo") || r.HasWhatsApp() {
		t.Errorf("solo correo: %+v", r)
	}
	// Móvil inválido + correo válido → válida, avisa del móvil con el mensaje del SDD.
	if r := base("31245", "a@b.co", def); r.Estado != RecipientValid || !issue(r, IssueWarning, CampoMovil, "MOVIL «31245» está incompleto") {
		t.Errorf("móvil inválido: %+v", r.Issues)
	}
	// Correo inválido + móvil válido → válida.
	if r := base("3209323449", "sin-arroba", def); r.Estado != RecipientValid || !issue(r, IssueWarning, CampoCorreo, "no es válido") {
		t.Errorf("correo inválido: %+v", r.Issues)
	}
	// Ninguno → error.
	if r := base("", "", def); r.Estado != RecipientInvalid || !issue(r, IssueError, CampoCanales, "ningún canal") {
		t.Errorf("sin canales: %+v", r.Issues)
	}
	// Ambos presentes pero inválidos → errores en cada columna + canales.
	r := base("31245", "malo", def)
	if r.Estado != RecipientInvalid || !issue(r, IssueError, CampoMovil, "incompleto") || !issue(r, IssueError, CampoCorreo, "no es válido") || !issue(r, IssueError, CampoCanales, "ningún canal") {
		t.Errorf("ambos inválidos: %+v", r.Issues)
	}
	// Varios correos: se conservan los válidos y se avisa de los inválidos.
	r = base("", "a@b.co; roto, c@d.org", def)
	if len(r.Correos) != 2 || !issue(r, IssueWarning, CampoCorreo, "se ignora el correo no válido «roto»") {
		t.Errorf("varios correos: %+v", r)
	}
	// Error de digitación frecuente en el dominio.
	if r := base("3209323449", "papa@gmial.com", def); !issue(r, IssueWarning, CampoCorreo, "@gmail.com") || r.Estado != RecipientValid {
		t.Errorf("typo: %+v", r.Issues)
	}
	// "Requerir los dos canales": cualquier canal faltante es error.
	req := ValidationOptions{RequireBothChannels: true}
	if r := base("3209323449", "", req); r.Estado != RecipientInvalid || !issue(r, IssueError, CampoCorreo, "exige móvil y correo") {
		t.Errorf("exige ambos (falta correo): %+v", r.Issues)
	}
	if r := base("", "a@b.co", req); r.Estado != RecipientInvalid || !issue(r, IssueError, CampoMovil, "exige móvil y correo") {
		t.Errorf("exige ambos (falta móvil): %+v", r.Issues)
	}
	if r := base("3209323449", "a@b.co", req); r.Estado != RecipientValid {
		t.Errorf("ambos presentes: %+v", r.Issues)
	}
}

func TestValidateBatchDuplicadosYResumen(t *testing.T) {
	cfg := cfgReal()
	rows := []RawRow{
		row(2, "Ana Gómez", "C", "50%", "2012", "A", "3209323449", ""),
		row(3, "ANA  GOMEZ", "C", "30%", "2012", "A", "3001112233", ""), // mismo nombre y año → duplicado
		row(4, "Ana Gómez", "C", "50%", "2013", "A", "3209323449", ""),  // otro año → distinto
		row(5, "Luis", "C", "SIN BECA", "2012", "A", "", ""),
		row(6, "Luis", "C", "SIN BECA", "2012", "A", "", ""), // omitidas no cuentan como duplicado
		row(7, "Pedro", "C", "50%", "2020", "A", "3209323449", ""),
	}
	rs := ValidateBatch(rows, cfg, ValidationOptions{})
	if rs[0].Estado != RecipientValid || rs[1].Estado != RecipientInvalid || !issue(rs[1], IssueError, CampoDuplicado, "ya aparece en la fila 2") || rs[2].Estado != RecipientValid {
		t.Errorf("duplicados: %v %v %v", rs[0].Estado, rs[1].Estado, rs[2].Estado)
	}
	if rs[3].Estado != RecipientOmitted || rs[4].Estado != RecipientOmitted || rs[5].Estado != RecipientInvalid {
		t.Errorf("estados: %v %v %v", rs[3].Estado, rs[4].Estado, rs[5].Estado)
	}
	s := Summarize(rs)
	if s.Total != 6 || s.Validas != 2 || s.ConErrores != 2 || s.Omitidas != 2 {
		t.Errorf("resumen: %+v", s)
	}
	// Las filas válidas con advertencias se cuentan aparte.
	rs = ValidateBatch([]RawRow{row(2, "Z", "C", "50%", "2012", "A", "3209323449", "")}, cfg, ValidationOptions{})
	if s := Summarize(rs); s.Validas != 1 || s.Advertencias != 1 {
		t.Errorf("advertencias: %+v", s)
	}
}

func TestNormalizeHeader(t *testing.T) {
	for in, want := range map[string]string{"PAGO # 1": "PAGO 1", "AÑOS": "ANOS", "E-mail": "E MAIL", "  Móvil ": "MOVIL", "\xef\xbb\xbfNombre": "NOMBRE", "V/TOTAL": "V/TOTAL", "Año_nacimiento": "ANO NACIMIENTO"} {
		if got := NormalizeHeader(in); got != want {
			t.Errorf("NormalizeHeader(%q)=%q quería %q", in, got, want)
		}
	}
}
