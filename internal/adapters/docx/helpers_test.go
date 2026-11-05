package docx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"testing"
)

// Plantillas SINTÉTICAS con la misma estructura que las reales del cliente (runs partidos, tablas,
// sección), pero sin sus datos (las reales traen la cédula y las cuentas bancarias de la coordinadora
// y no se versionan).

const nsDecl = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" ` +
	`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" ` +
	`xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006" ` +
	`xmlns:w14="http://schemas.microsoft.com/office/word/2010/wordml" mc:Ignorable="w14"`

type tpl struct {
	headerRuns string // runs del encabezado (varía entre plantillas)
	headerPPr  string // <w:pPr> del encabezado
	extraTail  string // contenido extra antes de sectPr
	costRows   string // filas de la tabla de costos (por defecto las 6 becas)
	pagosCols  int
	jugadorRun string // runs del campo Jugador
}

func r(text string) string { return rp("", text) }
func rp(props, text string) string {
	rpr := ""
	if props != "" {
		rpr = "<w:rPr>" + props + "</w:rPr>"
	}
	sp := ""
	if strings.TrimSpace(text) != text {
		sp = ` xml:space="preserve"`
	}
	return fmt.Sprintf("<w:r>%s<w:t%s>%s</w:t></w:r>", rpr, sp, text)
}

const tabRun = `<w:r><w:tab/></w:r>`

func tabs(n int) string { return strings.Repeat(tabRun, n) }

// cadete: encabezado partido en 16 runs como la plantilla real cadete_juvenil.
func headerCadete() string {
	return `<w:bookmarkStart w:id="0" w:name="_GoBack"/><w:bookmarkEnd w:id="0"/>` +
		rp(`<w:b/>`, "TORNEO ") + rp(`<w:b/>`, "X") + `<w:proofErr w:type="spellStart"/>` + rp(`<w:b/>`, "XXXXXXXXXXXXXXXXXX") + `<w:proofErr w:type="spellEnd"/>` +
		tabs(7) + rp(`<w:i/>`, "Fecha:") + rp(``, "XX") + r("/") + r("XX") + r("/") + r("XXXX")
}

// benjamín/alevín: otro partido (la fecha termina en un solo run "/XX/XXXX").
func headerBenjamin() string {
	return rp(`<w:b/>`, "TORNEO X") + rp(`<w:b/>`, "XXXXXXXXXXXXXXXXXX") + tabs(7) + rp(`<w:i/>`, "Fecha:") + r("XX") + r("/XX/XXXX")
}

func defaultCostRows() string {
	row := func(label, val string) string {
		return `<w:tr><w:tc><w:p>` + label + `</w:p></w:tc><w:tc><w:tcPr><w:tcW w:w="1000" w:type="dxa"/><w:vAlign w:val="center"/></w:tcPr><w:p>` + val + `</w:p></w:tc></w:tr>`
	}
	return `<w:tr><w:tc><w:p>` + r("BECA ") + `</w:p></w:tc><w:tc><w:p>` + r("VALOR € EUROS") + `</w:p></w:tc></w:tr>` +
		row(r("Sin Beca"), r("€ 2.800")) +
		row(r("Beca al 30%"), r("€ 1.960")) +
		row(r("Beca al ")+r("50 %"), rp(`<w:b/>`, "€ 1")+r(".550")) +
		row(r("Beca al 70%"), r("€ 990 ")) +
		row(r("Beca al 100%"), r("€ 200 admón.")) +
		row(r("Acompañante")+tabRun, r("€ 1.800"))
}

func pagosTable(cols int) string {
	grid := `<w:gridCol w:w="2000"/>`
	head, fecha := `<w:tc><w:p>`+r("PAGOS")+`</w:p></w:tc>`, `<w:tc><w:p>`+r("FECHAS")+`</w:p></w:tc>`
	ord := []string{"1er", "2do", "3er", "4to"}
	for i := 0; i < cols-1; i++ {
		grid += `<w:gridCol w:w="2000"/>`
		head += `<w:tc><w:tcPr><w:shd w:val="clear" w:color="auto" w:fill="FFFF00"/></w:tcPr><w:p>` + r(ord[i]+" pago € ") + r("XXX") + `</w:p></w:tc>`
		fecha += `<w:tc><w:p>` + r("XX") + r("/") + r("XX") + r("/") + r("XXXX") + `</w:p></w:tc>`
	}
	return `<w:tbl><w:tblPr><w:tblW w:w="0" w:type="auto"/></w:tblPr><w:tblGrid>` + grid + `</w:tblGrid><w:tr>` + head + `</w:tr><w:tr>` + fecha + `</w:tr></w:tbl>`
}

func (t tpl) document() string {
	if t.headerRuns == "" {
		t.headerRuns = headerCadete()
	}
	if t.headerPPr == "" {
		t.headerPPr = `<w:pPr><w:pStyle w:val="Normal"/><w:spacing w:after="0"/></w:pPr>`
	}
	if t.costRows == "" {
		t.costRows = defaultCostRows()
	}
	if t.pagosCols == 0 {
		t.pagosCols = 4
	}
	if t.jugadorRun == "" {
		t.jugadorRun = rp(`<w:b/>`, "J") + r("ug") + r("ad") + r("or") + r(":") + r(" ") + rp(`<w:b/>`, "XXXXXXXXXXX")
	}
	p := func(runs string) string { return `<w:p>` + runs + `</w:p>` }
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<w:document ` + nsDecl + `><w:body>` +
		`<w:p w14:paraId="0A1B2C3D">` + t.headerPPr + t.headerRuns + `</w:p>` +
		p(r("Acudiente")+r(": ")+rp(`<w:b/>`, "XXXXXXXXXX")) +
		p(t.jugadorRun) +
		p(r("Club:")+r(" ")+r(" ")+rp(`<w:b/>`, "XXXXXXXXXX")) +
		p(r("Año")+r(":  ")+rp(`<w:b/>`, "XXXXXXXXX")+r(" ")) +
		p(r("Móvil")+r(" ")+r("(+57) ")+rp(`<w:b/>`, "XXXXXXXXX")) +
		p(r("Correo electrónico:")+r(" ")+rp(`<w:b/>`, "XXXXXXXXXXX")) +
		p(r("La Ciudad")) +
		p(r("Ref. Propuesta Jugadores seleccionados ")) +
		p(r("Consiste en la participación del ALUMNO – DEPORTISTA en el torneo de futbol en ESPAÑA &amp; más. ")) +
		p(r("COSTOS:")) +
		`<w:tbl><w:tblPr><w:tblW w:w="0" w:type="auto"/></w:tblPr><w:tblGrid><w:gridCol w:w="4000"/><w:gridCol w:w="4000"/></w:tblGrid>` + t.costRows + `</w:tbl>` +
		p(r("Programación Pagos:")) +
		pagosTable(t.pagosCols) +
		p(r("Móvil (+57) 3200000000")) + // línea fija de contacto: sin campos X
		t.extraTail +
		`<w:sectPr><w:headerReference w:type="default" r:id="rId10"/><w:pgSz w:w="12240" w:h="15840" w:code="1"/>` +
		`<w:pgMar w:top="1417" w:right="1701" w:bottom="1417" w:left="1701" w:header="708" w:footer="708" w:gutter="0"/></w:sectPr>` +
		`</w:body></w:document>`
}

// docx arma el .docx con partes extra (para comprobar que no se tocan).
func (t tpl) docx(tb testing.TB) []byte {
	return zipOf(tb, t.document())
}

var fixedParts = []struct {
	name string
	data string
}{
	{"[Content_Types].xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`},
	{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`},
	{"word/_rels/document.xml.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"/>`},
	{"word/header1.xml", `<?xml version="1.0"?><w:hdr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:p><w:r><w:t>Logo XXX</w:t></w:r></w:p></w:hdr>`}, // un XXX en un encabezado: no es del cuerpo
	{"word/media/image1.png", "\x89PNG\r\n\x1a\n-pretend-image-bytes-"},
}

func zipOf(tb testing.TB, document string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name string, data []byte, method uint16) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			tb.Fatal(err)
		}
		w.Write(data)
	}
	for i, p := range fixedParts {
		write(p.name, []byte(p.data), zip.Deflate)
		if i == 2 { // word/document.xml va después de las relaciones, como en los .docx reales
			write("word/document.xml", []byte(document), zip.Deflate)
		}
	}
	if err := zw.Close(); err != nil {
		tb.Fatal(err)
	}
	return buf.Bytes()
}

// ---------------------------------------------------------------------------------------------
// Extractor de texto INDEPENDIENTE del parser del adaptador (encoding/xml genérico).

type doc struct {
	Paras  []string     // párrafos del cuerpo
	Tables [][][]string // tabla → fila → celda (texto de la celda, párrafos unidos por \n)
}

func extract(tb testing.TB, docx []byte) doc {
	tb.Helper()
	zr, err := zip.NewReader(bytes.NewReader(docx), int64(len(docx)))
	if err != nil {
		tb.Fatal(err)
	}
	var xmlData []byte
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			rc, _ := f.Open()
			xmlData, _ = io.ReadAll(rc)
			rc.Close()
		}
	}
	if xmlData == nil {
		tb.Fatal("sin word/document.xml")
	}
	dec := xml.NewDecoder(bytes.NewReader(xmlData))
	var stack []string
	var out doc
	var curP strings.Builder
	var cellParas []string
	var row []string
	var tbl [][]string
	in := func(name string) bool {
		for _, s := range stack {
			if s == name {
				return true
			}
		}
		return false
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			tb.Fatalf("el documento generado no es XML válido: %v", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			stack = append(stack, t.Name.Local)
			switch t.Name.Local {
			case "p":
				curP.Reset()
			case "tbl":
				tbl = nil
			case "tr":
				row = nil
			case "tc":
				cellParas = nil
			case "tab":
				if in("r") {
					curP.WriteByte('\t')
				}
			case "br":
				curP.WriteByte('\n')
			}
		case xml.CharData:
			if len(stack) > 0 && stack[len(stack)-1] == "t" {
				curP.Write(t)
			}
		case xml.EndElement:
			stack = stack[:len(stack)-1]
			switch t.Name.Local {
			case "p":
				if in("tc") {
					cellParas = append(cellParas, curP.String())
				} else if !in("tbl") {
					out.Paras = append(out.Paras, curP.String())
				}
			case "tc":
				row = append(row, strings.Join(cellParas, "\n"))
			case "tr":
				tbl = append(tbl, row)
			case "tbl":
				out.Tables = append(out.Tables, tbl)
			}
		}
	}
	return out
}

func cell(d doc, table, row, col int) string { return d.Tables[table][row][col] }

func partOf(tb testing.TB, docx []byte, name string) []byte {
	zr, _ := zip.NewReader(bytes.NewReader(docx), int64(len(docx)))
	for _, f := range zr.File {
		if f.Name == name {
			rc, _ := f.Open()
			defer rc.Close()
			b, _ := io.ReadAll(rc)
			return b
		}
	}
	tb.Fatalf("no existe la parte %s", name)
	return nil
}

func names(docx []byte) []string {
	zr, _ := zip.NewReader(bytes.NewReader(docx), int64(len(docx)))
	var out []string
	for _, f := range zr.File {
		out = append(out, f.Name)
	}
	return out
}

func zipOfParts(tb testing.TB, parts map[string]string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, d := range parts {
		w, _ := zw.Create(n)
		w.Write([]byte(d))
	}
	zw.Close()
	return buf.Bytes()
}
