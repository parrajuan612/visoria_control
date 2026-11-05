package docx

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"visoria-control/internal/domain"
	"visoria-control/internal/ports"
)

func eur(n int64) domain.Money { return domain.Money(n * 100) }

func cfg() domain.MasterConfig {
	b := func(n string, total int64, p ...int64) domain.Beca {
		k, _ := domain.NormalizeBecaKey(n)
		pagos := make([]domain.Money, len(p))
		for i, v := range p {
			pagos[i] = eur(v)
		}
		return domain.Beca{Clave: k, Nombre: n, Total: eur(total), Pagos: pagos}
	}
	return domain.MasterConfig{Becas: []domain.Beca{
		b("SIN BECA", 2800, 930, 930, 940), b("30%", 1960, 650, 650, 660), b("50%", 1550, 500, 500, 550),
		b("70%", 990, 495, 495), b("100%", 200, 200), b("ACOMPAÑANTE", 1800, 600, 600, 600),
	}}
}

func recip(beca domain.BecaKey) domain.Recipient {
	return domain.Recipient{Jugador: "PRUEBA MAJESTIC", Club: "Club de Prueba", Acudiente: "Acudiente de Prueba", Beca: beca, Anio: 2012,
		Movil: "573209323449", Correos: []string{"acudiente@example.com"}}
}

func input(beca domain.BecaKey, fechas ...string) ports.DocumentInput {
	return ports.DocumentInput{Recipient: recip(beca), Torneo: "Copa Majestic Internacional", FechaEmision: "05/11/2026", FechasPago: fechas, Config: cfg()}
}

var fechas3 = []string{"01/12/2026", "01/01/2027", "01/02/2027"}

func render(t *testing.T, tp tpl, in ports.DocumentInput) []byte {
	t.Helper()
	out, err := NewRenderer().Render(context.Background(), tp.docx(t), in)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return out
}

func TestRenderBeca30ReemplazaCadaCampo(t *testing.T) {
	d := extract(t, render(t, tpl{}, input("30", fechas3...)))
	want := []string{
		"COPA MAJESTIC INTERNACIONAL\tFecha:05/11/2026", // «TORNEO » desaparece y el torneo va en mayúsculas (test_basico)
		"Acudiente: Acudiente de Prueba",
		"Jugador: PRUEBA MAJESTIC",
		"Club:  Club de Prueba", // conserva los dos espacios de la plantilla
		"Año:  2012 ",
		"Móvil (+57) 3209323449", // 10 dígitos: el prefijo (+57) ya viene en la plantilla
		"Correo electrónico: acudiente@example.com",
	}
	for i, w := range want {
		if d.Paras[i] != w {
			t.Errorf("párrafo %d:\n got %q\nwant %q", i, d.Paras[i], w)
		}
	}
	if d.Paras[len(d.Paras)-1] != "Móvil (+57) 3200000000" {
		t.Errorf("la línea de contacto fija no debe tocarse: %q", d.Paras[len(d.Paras)-1])
	}
	// Tabla de costos: valores desde la configuración (incluida la celda partida en 2 runs: "€ 1"+".550")
	wantCostos := [][2]string{{"BECA ", "VALOR € EUROS"}, {"Sin Beca", "€ 2.800"}, {"Beca al 30%", "€ 1.960"}, {"Beca al 50 %", "€ 1.550"},
		{"Beca al 70%", "€ 990"}, {"Beca al 100%", "€ 200 admón."}, {"Acompañante\t", "€ 1.800"}}
	for i, w := range wantCostos {
		if cell(d, 0, i, 0) != w[0] || cell(d, 0, i, 1) != w[1] {
			t.Errorf("costos fila %d: %q|%q, quería %q|%q", i, cell(d, 0, i, 0), cell(d, 0, i, 1), w[0], w[1])
		}
	}
	// Programación de pagos con 3 columnas
	for i, w := range []string{"1er pago € 650", "2do pago € 650", "3er pago € 660"} {
		if got := cell(d, 1, 0, i+1); got != w {
			t.Errorf("pago %d: %q, quería %q", i+1, got, w)
		}
	}
	for i, w := range fechas3 {
		if got := cell(d, 1, 1, i+1); got != w {
			t.Errorf("fecha %d: %q, quería %q", i+1, got, w)
		}
	}
}

func TestRenderOtraPlantillaConElEncabezadoPartidoDistinto(t *testing.T) {
	d := extract(t, render(t, tpl{headerRuns: headerBenjamin()}, input("50", fechas3...)))
	if d.Paras[0] != "COPA MAJESTIC INTERNACIONAL\tFecha:05/11/2026" {
		t.Errorf("encabezado: %q", d.Paras[0])
	}
}

// Portado de test_excel_de_ejemplo_y_docx: la tabla de pagos queda con 1 + N columnas.
func TestRenderQuitaLasColumnasDeLosPagosQueSobran(t *testing.T) {
	for beca, plan := range map[domain.BecaKey]int{"30": 3, "50": 3, "70": 2, "100": 1} {
		out := render(t, tpl{}, input(beca, fechas3[:plan]...))
		d := extract(t, out)
		for r, row := range d.Tables[1] {
			if len(row) != 1+plan {
				t.Errorf("beca %s fila %d: %d celdas, quería %d", beca, r, len(row), 1+plan)
			}
		}
		root, _ := parseXML(partOf(t, out, "word/document.xml"))
		if cols := tableColumns(bodyTables(body(root))[1]); cols != 1+plan {
			t.Errorf("beca %s: tblGrid tiene %d columnas, quería %d", beca, cols, 1+plan)
		}
	}
}

// Portado: solo la fila de su beca va en amarillo (las dos casillas) y sin subrayado.
func TestRenderResaltaSoloLaFilaDeLaBeca(t *testing.T) {
	for beca, label := range map[domain.BecaKey]string{"30": "Beca al 30%", "50": "Beca al 50 %", "70": "Beca al 70%", "100": "Beca al 100%", "sin_beca": "Sin Beca", "acompanante": "Acompañante\t"} {
		plan := len(func() []domain.Money { b, _ := cfg().Beca(beca); return b.Pagos }())
		out := render(t, tpl{}, input(beca, fechas3[:plan]...))
		root, _ := parseXML(partOf(t, out, "word/document.xml"))
		costos := bodyTables(body(root))[0]
		var amarillas []string
		for _, tr := range rows(costos) {
			all := true
			for _, tc := range cells(tr) {
				pr := tc.first("w:tcPr")
				shd := (*node)(nil)
				if pr != nil {
					shd = pr.first("w:shd")
				}
				if shd == nil {
					all = false
					continue
				}
				if f, _ := shd.attr("w:fill"); f != "FFFF00" {
					all = false
				}
			}
			if all {
				amarillas = append(amarillas, cellText(cells(tr)[0]))
			}
		}
		if len(amarillas) != 1 || amarillas[0] != label {
			t.Errorf("beca %s: filas amarillas %q, quería solo %q", beca, amarillas, label)
		}
		if bytes.Contains(bodyTables(body(root))[0].bytes(), []byte("<w:u")) {
			t.Errorf("beca %s: la tabla de costos no debe tener subrayado", beca)
		}
		// El orden del esquema: shd antes de vAlign dentro de tcPr.
		x := string(costos.bytes())
		if i, j := strings.Index(x, `<w:shd`), strings.Index(x, `<w:vAlign`); i > j && strings.Contains(x[:j+1], `FFFF00`) {
			t.Errorf("w:shd debe ir antes de w:vAlign en tcPr")
		}
	}
}

func TestRenderConservaElFormatoDelPrimerRunAfectado(t *testing.T) {
	out := render(t, tpl{}, input("30", fechas3...))
	x := string(partOf(t, out, "word/document.xml"))
	for _, want := range []string{
		`<w:r><w:rPr><w:b/></w:rPr><w:t>COPA MAJESTIC INTERNACIONAL</w:t></w:r>`,            // negrita del run «TORNEO »
		`<w:r><w:rPr><w:b/></w:rPr><w:t>PRUEBA MAJESTIC</w:t></w:r>`,                        // negrita del run del valor
		`<w:r><w:rPr><w:i/></w:rPr><w:t>Fecha:</w:t></w:r><w:r><w:t>05/11/2026</w:t></w:r>`, // la fecha, sin formato extra
		`<w:r><w:rPr><w:b/></w:rPr><w:t>€ 1.550</w:t></w:r>`,                                // celda partida: el valor toma el formato del 1.er run
	} {
		if !strings.Contains(x, want) {
			t.Errorf("falta %s", want)
		}
	}
	// Los runs que quedaron sin texto no dejan rastro de la «X» original.
	if strings.Contains(x, ">XX<") || strings.Contains(x, ">XXXX<") {
		t.Error("quedaron restos de campos")
	}
}

func TestRenderSoloCambiaDocumentXML(t *testing.T) {
	tp := tpl{}.docx(t)
	out := render(t, tpl{}, input("30", fechas3...))
	if a, b := names(tp), names(out); strings.Join(a, "|") != strings.Join(b, "|") {
		t.Fatalf("las partes cambiaron de orden o de nombre:\n%v\n%v", a, b)
	}
	for _, n := range names(tp) {
		same := bytes.Equal(partOf(t, tp, n), partOf(t, out, n))
		if n == "word/document.xml" && same {
			t.Error("document.xml debía cambiar")
		}
		if n != "word/document.xml" && !same {
			t.Errorf("la parte %s no debe cambiar", n)
		}
	}
}

// El resto del documento debe quedar idéntico: se compara el XML de los párrafos sin campos.
func TestRenderNoAlteraLoQueNoTocaYLaSerializacionEsFiel(t *testing.T) {
	orig := tpl{}.document()
	root, err := parseXML([]byte(orig))
	if err != nil {
		t.Fatal(err)
	}
	if string(root.bytes()) != orig {
		t.Fatalf("el árbol no reserializa idéntico:\n got %.300s\nwant %.300s", root.bytes(), orig)
	}
	out := string(partOf(t, render(t, tpl{}, input("30", fechas3...)), "word/document.xml"))
	for _, keep := range []string{
		`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`,
		nsDecl, // todas las declaraciones de espacios de nombres, con su prefijo
		`<w:p w14:paraId="0A1B2C3D">`,
		`<w:bookmarkStart w:id="0" w:name="_GoBack"/>`,
		`<w:p><w:r><w:t xml:space="preserve">Consiste en la participación del ALUMNO – DEPORTISTA en el torneo de futbol en ESPAÑA &amp; más. </w:t></w:r></w:p>`,
		`<w:sectPr><w:headerReference w:type="default" r:id="rId10"/>`,
	} {
		if !strings.Contains(out, keep) {
			t.Errorf("se perdió o alteró: %s", keep)
		}
	}
}

func TestEncabezadoUnaSolaTabulacionAlineadaALaDerecha(t *testing.T) {
	out := string(partOf(t, render(t, tpl{}, input("30", fechas3...)), "word/document.xml"))
	root, _ := parseXML([]byte(out))
	enc := bodyParagraphs(body(root))[0]
	n := 0
	for _, rn := range runs(enc) {
		if runText(rn) == "\t" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("debe quedar 1 tabulación, hay %d", n)
	}
	// ancho útil = 12240 − 1701 − 1701 = 8838 twips; <w:tabs> va después de pStyle y antes de spacing
	want := `<w:pPr><w:pStyle w:val="Normal"/><w:tabs><w:tab w:val="right" w:pos="8838"/></w:tabs><w:spacing w:after="0"/></w:pPr>`
	if !strings.Contains(out, want) {
		t.Errorf("pPr del encabezado:\n%s", enc.first("w:pPr").bytes())
	}
	// Si el encabezado ya define tabulaciones, no se tocan.
	out2 := string(partOf(t, render(t, tpl{headerPPr: `<w:pPr><w:tabs><w:tab w:val="left" w:pos="100"/></w:tabs></w:pPr>`}, input("30", fechas3...)), "word/document.xml"))
	if strings.Contains(out2, `w:pos="8838"`) || !strings.Contains(out2, `<w:tab w:val="left" w:pos="100"/>`) {
		t.Error("las tabulaciones propias de la plantilla deben respetarse")
	}
	// Sin pPr se crea uno.
	out3 := string(partOf(t, render(t, tpl{headerPPr: " "}, input("30", fechas3...)), "word/document.xml"))
	if !strings.Contains(out3, `<w:pPr><w:tabs><w:tab w:val="right" w:pos="8838"/></w:tabs></w:pPr>`) {
		t.Error("debía crear el pPr del encabezado")
	}
}

func TestValoresEspecialesSeEscapanYConservanEspacios(t *testing.T) {
	in := input("30", fechas3...)
	in.Recipient.Jugador = `  O'Brien & "Hijos" <Ñandú> 😀 `
	in.Torneo = `Copa <A&B> "especial"`
	out := render(t, tpl{}, in)
	d := extract(t, out)                                         // lo parsea un lector XML independiente: si no estuviera bien escapado, fallaría
	if d.Paras[2] != `Jugador:   O'Brien & "Hijos" <Ñandú> 😀 ` { // el espacio de la plantilla + los 2 del valor
		t.Errorf("jugador: %q", d.Paras[2])
	}
	if d.Paras[0] != "COPA <A&B> \"ESPECIAL\"\tFecha:05/11/2026" {
		t.Errorf("torneo: %q", d.Paras[0])
	}
	if !strings.Contains(string(partOf(t, out, "word/document.xml")), `<w:t xml:space="preserve">  O'Brien &amp; "Hijos" &lt;Ñandú&gt; 😀 </w:t>`) {
		t.Error("el valor con espacios en los extremos debe llevar xml:space=preserve y estar escapado")
	}
}

func TestSinMovilOSinCorreoSeMarcaConUnaRaya(t *testing.T) {
	in := input("30", fechas3...)
	in.Recipient.Movil, in.Recipient.Correos = "", nil
	d := extract(t, render(t, tpl{}, in))
	if d.Paras[5] != "Móvil (+57) —" || d.Paras[6] != "Correo electrónico: —" {
		t.Errorf("%q | %q", d.Paras[5], d.Paras[6])
	}
	// varios correos y móvil internacional
	in = input("30", fechas3...)
	in.Recipient.Correos = []string{"a@b.co", "c@d.org"}
	in.Recipient.Movil = "34612345678"
	d = extract(t, render(t, tpl{}, in))
	if d.Paras[6] != "Correo electrónico: a@b.co, c@d.org" || d.Paras[5] != "Móvil (+57) 34612345678" {
		t.Errorf("%q | %q", d.Paras[5], d.Paras[6])
	}
}

// Errores con los textos del cliente (generador.py).
func TestErroresConLosMensajesDelCliente(t *testing.T) {
	sinPlan := cfg()
	sinPlan.Becas[1].Pagos = nil
	ine := input("30", fechas3...)
	ine.Config = sinPlan
	cents := cfg()
	cents.Becas[1].Pagos[0] = 65050
	inc := input("30", fechas3...)
	inc.Config = cents
	cases := []struct {
		name string
		tp   tpl
		in   ports.DocumentInput
		sub  string
	}{
		{"sin plan", tpl{}, ine, "No hay plan de pagos configurado para esta beca."},
		{"beca inexistente", tpl{}, input("99", fechas3...), "No hay plan de pagos configurado para esta beca."},
		{"fechas de menos", tpl{}, input("30", "01/12/2026"), "El plan tiene 3 pago(s) pero se indicaron 1 fecha(s)."},
		{"fechas de más", tpl{}, input("100", fechas3...), "El plan tiene 1 pago(s) pero se indicaron 3 fecha(s)."},
		{"importe con céntimos", tpl{}, inc, "no es un importe en euros enteros"},
		{"sin encabezado", tpl{headerRuns: r("Sin campos")}, input("30", fechas3...), "La plantilla no tiene el encabezado «XXXX … Fecha:XX/XX/XXXX»."},
		{"sin campo Jugador", tpl{jugadorRun: r("Jugador: ya escrito")}, input("30", fechas3...), "La plantilla no tiene el campo «Jugador» con XXXX."},
		{"sin fila de la beca", tpl{costRows: strings.ReplaceAll(defaultCostRows(), "Beca al 30%", "Otra cosa")}, input("30", fechas3...), "No encontré la fila de la beca en la tabla de costos."},
		{"celda de costo vacía", tpl{costRows: `<w:tr><w:tc><w:p></w:p></w:tc><w:tc><w:p></w:p></w:tc></w:tr><w:tr><w:tc><w:p>` + r("Sin Beca") + `</w:p></w:tc><w:tc><w:p></w:p></w:tc></w:tr>`}, input("30", fechas3...), "Una celda de la plantilla está vacía."},
		{"campo XXX en el cuerpo sin reemplazar", tpl{extraTail: `<w:p>` + r("Otro campo XXXX sin usar") + `</w:p>`}, input("30", fechas3...), "Quedaron campos «XXX» sin reemplazar en el documento."},
		{"un nombre con XXX activa el control final", tpl{}, func() ports.DocumentInput { i := input("30", fechas3...); i.Recipient.Club = "MAXXXIMO"; return i }(), "Quedaron campos «XXX» sin reemplazar en el documento."},
		{"menos columnas de pago", tpl{pagosCols: 3}, input("30", fechas3...), "La tabla de pagos no tiene el campo XXX del valor."},
	}
	for _, c := range cases {
		_, err := NewRenderer().Render(context.Background(), c.tp.docx(t), c.in)
		var de *ports.DocError
		if !errors.As(err, &de) || !strings.Contains(de.Message, c.sub) {
			t.Errorf("%s: esperaba %q, fue %v", c.name, c.sub, err)
		}
	}
	// El «XXX» del encabezado de página (otra parte del zip) NO cuenta: el control es solo del cuerpo.
	if _, err := NewRenderer().Render(context.Background(), tpl{}.docx(t), input("30", fechas3...)); err != nil {
		t.Errorf("un XXX en header1.xml no debe bloquear: %v", err)
	}
}

func TestPlantillaInvalidaComoArchivo(t *testing.T) {
	for name, data := range map[string][]byte{"vacío": nil, "texto": []byte("no soy un docx"), "zip sin documento": zipWithout(t)} {
		_, err := NewRenderer().Render(context.Background(), data, input("30", fechas3...))
		var de *ports.DocError
		if !errors.As(err, &de) || !strings.Contains(de.Message, "¿es un Word válido?") {
			t.Errorf("%s: %v", name, err)
		}
	}
	// XML dañado dentro del zip
	if _, err := NewRenderer().Render(context.Background(), zipOf(t, "<w:document><w:body></w:document>"), input("30", fechas3...)); err == nil {
		t.Error("XML dañado debe fallar")
	}
}

func zipWithout(t *testing.T) []byte { return zipOfParts(t, map[string]string{"otra.txt": "x"}) }

func TestRunConImagenNoSeDestruye(t *testing.T) {
	// El campo comparte run con un dibujo: python-docx lo borraría en silencio; aquí es un error claro.
	tp := tpl{jugadorRun: r("Jugador: ") + `<w:r><w:drawing/><w:t>XXXXXXXXXXX</w:t></w:r>`}
	_, err := NewRenderer().Render(context.Background(), tp.docx(t), input("30", fechas3...))
	if err == nil || !strings.Contains(err.Error(), "objeto") {
		t.Errorf("run con imagen: %v", err)
	}
}

func TestRenderEsSeguroEnParalelo(t *testing.T) {
	tmpl := tpl{}.docx(t)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			in := input("30", fechas3...)
			in.Recipient.Jugador = strings.Repeat("A", i+1)
			out, err := NewRenderer().Render(context.Background(), tmpl, in)
			if err != nil || extract(t, out).Paras[2] != "Jugador: "+in.Recipient.Jugador {
				t.Errorf("render %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
}

func TestValidateTemplate(t *testing.T) {
	v := NewRenderer()
	if msg := v.ValidateTemplate(tpl{}.docx(t)); msg != "" {
		t.Errorf("la plantilla sintética debe ser válida: %q", msg)
	}
	cases := []struct {
		name string
		tp   tpl
		want string
	}{
		{"sin encabezado", tpl{headerRuns: r("Hola")}, "No encontré el encabezado con el torneo «XXXX» y la fecha «XX/XX/XXXX» en la misma línea."},
		{"sin campo Móvil", tpl{costRows: defaultCostRows(), headerRuns: headerCadete(), extraTail: ""}, ""}, // control de sanidad
		{"2 columnas de pago", tpl{pagosCols: 3}, "La tabla de «Programación Pagos» debe tener 2 filas y 4 columnas (PAGOS + 3 pagos)."},
		{"5 columnas de pago", tpl{pagosCols: 5}, "La tabla de «Programación Pagos» debe tener 2 filas y 4 columnas (PAGOS + 3 pagos)."},
		{"costos sin acompañante", tpl{costRows: strings.ReplaceAll(defaultCostRows(), "Acompañante", "Otro")}, "La tabla de costos debe tener las filas: Sin Beca, 30%, 50%, 70%, 100% y Acompañante."},
		{"sin campo Jugador", tpl{jugadorRun: r("Jugador sin campo")}, "No encontré el campo «Jugador: XXXX»."},
	}
	for _, c := range cases {
		if got := v.ValidateTemplate(c.tp.docx(t)); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
	for _, bad := range [][]byte{nil, []byte("x"), zipWithout(t), zipOf(t, "<w:document></w:document>"), zipOf(t, "no es xml")} {
		if got := v.ValidateTemplate(bad); got != "No pude abrir el archivo; ¿es un Word válido?" {
			t.Errorf("archivo inválido: %q", got)
		}
	}
	// Una sola tabla.
	one := strings.Replace(tpl{}.document(), pagosTable(4), "", 1)
	if got := v.ValidateTemplate(zipOf(t, one)); got != "Debe tener la tabla de costos y la tabla de pagos." {
		t.Errorf("una tabla: %q", got)
	}
	// Sin fecha XX/XX/XXXX pero con torneo (no cumple es_encabezado, así que el primer mensaje manda).
	noFecha := tpl{headerRuns: r("TORNEO XXXXXXXX")}
	if got := v.ValidateTemplate(noFecha.docx(t)); !strings.HasPrefix(got, "No encontré el encabezado") {
		t.Errorf("sin fecha: %q", got)
	}
}
