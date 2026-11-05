// Package docx rellena las plantillas Word del cliente. Port de generador.py: se abre la plantilla
// original y solo se reemplazan los campos «X», así el logo, la tipografía y el diseño quedan
// idénticos. Nunca se rehace el documento.
package docx

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"visoria-control/internal/domain"
	"visoria-control/internal/ports"
)

const (
	maxTemplateBytes = 25 << 20
	maxDocumentXML   = 40 << 20
	amarillo         = "FFFF00" // el mismo sombreado de la tabla «Programación Pagos»
)

// Mensajes del cliente (generador.py).
func genErr(format string, a ...any) error {
	return &ports.DocError{Message: fmt.Sprintf(format, a...)}
}

// Renderer implementa ports.DocRenderer.
type Renderer struct{}

var _ ports.DocRenderer = Renderer{}

func NewRenderer() Renderer { return Renderer{} }

// elementos que en <w:tcPr> van DESPUÉS de <w:shd> (orden del esquema de Word).
var trasShd = map[string]bool{
	"w:noWrap": true, "w:tcMar": true, "w:textDirection": true, "w:tcFitText": true, "w:vAlign": true,
	"w:hideMark": true, "w:headers": true, "w:cellIns": true, "w:cellDel": true, "w:cellMerge": true, "w:tcPrChange": true,
}

// elementos que en <w:pPr> van ANTES de <w:tabs>.
var antesTabs = map[string]bool{
	"w:pStyle": true, "w:keepNext": true, "w:keepLines": true, "w:pageBreakBefore": true, "w:framePr": true,
	"w:widowControl": true, "w:numPr": true, "w:suppressLineNumbers": true, "w:pBdr": true, "w:shd": true,
}

var reBecaPct = regexp.MustCompile(`(\d+)\s*%`)

// FormatEuros: 1960 → "1.960" (punto de miles, como en las plantillas).
func FormatEuros(n int) string {
	s := strconv.Itoa(n)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func euros(m domain.Money) (int, error) {
	if m%100 != 0 {
		return 0, genErr("El valor %s no es un importe en euros enteros.", domain.FormatEUR(m))
	}
	return int(m / 100), nil
}

// readDocument abre el .docx y devuelve el zip y el XML del cuerpo.
func readDocument(data []byte) (*zip.Reader, []byte, error) {
	if len(data) == 0 || len(data) > maxTemplateBytes {
		return nil, nil, genErr("No pude abrir el archivo; ¿es un Word válido?")
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, nil, genErr("No pude abrir el archivo; ¿es un Word válido?")
	}
	for _, f := range zr.File {
		if f.Name != "word/document.xml" {
			continue
		}
		if f.UncompressedSize64 > maxDocumentXML {
			return nil, nil, genErr("El documento es demasiado grande.")
		}
		rc, err := f.Open()
		if err != nil {
			return nil, nil, genErr("No pude abrir el archivo; ¿es un Word válido?")
		}
		defer rc.Close()
		xmlData, err := io.ReadAll(io.LimitReader(rc, maxDocumentXML+1))
		if err != nil || len(xmlData) > maxDocumentXML {
			return nil, nil, genErr("No pude abrir el archivo; ¿es un Word válido?")
		}
		return zr, xmlData, nil
	}
	return nil, nil, genErr("No pude abrir el archivo; ¿es un Word válido?")
}

// writeDocument reescribe el .docx cambiando solo word/document.xml. Las demás partes se copian
// sin recomprimir (idénticas byte a byte).
func writeDocument(zr *zip.Reader, newDoc []byte) ([]byte, error) {
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range zr.File {
		if f.Name != "word/document.xml" {
			if err := zw.Copy(f); err != nil {
				return nil, err
			}
			continue
		}
		h := f.FileHeader
		h.Method = zip.Deflate
		h.CompressedSize64, h.UncompressedSize64, h.CRC32 = 0, 0, 0
		w, err := zw.CreateHeader(&h)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(newDoc); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// buscar: primer párrafo del cuerpo que empieza con la etiqueta y todavía tiene campos «X».
func buscar(b *node, etiqueta string) (*node, error) {
	for _, p := range bodyParagraphs(b) {
		t := paragraphText(p)
		if startsWithTrim(t, etiqueta) {
			if _, ok := matchX([]rune(t)); ok {
				return p, nil
			}
		}
	}
	return nil, genErr("La plantilla no tiene el campo «%s» con XXXX.", etiqueta)
}

func replaceOrFail(p *node, m matcher, nuevo, msg string) error {
	ok, err := replaceInParagraph(p, m, nuevo)
	if err != nil {
		return err
	}
	if !ok {
		return genErr("%s", msg)
	}
	return nil
}

// ponerTextoCelda: el valor va en el primer run de la celda; los demás se vacían.
func ponerTextoCelda(tc *node, texto string) error {
	ps := tc.els("w:p")
	var rs []*node
	if len(ps) > 0 {
		rs = runs(ps[0])
	}
	if len(rs) == 0 {
		return genErr("Una celda de la plantilla está vacía.")
	}
	if err := setRunText(rs[0], texto); err != nil {
		return err
	}
	for _, r := range rs[1:] {
		if err := setRunText(r, ""); err != nil {
			return err
		}
	}
	return nil
}

// resaltarFila pinta de amarillo las casillas completas de la fila (sombreado de celda).
func resaltarFila(tr *node) {
	for _, tc := range cells(tr) {
		pr := tc.first("w:tcPr")
		if pr == nil {
			pr = newEl("w:tcPr")
			i := 0
			for k, c := range tc.Children {
				if c.Kind == kElement && (c.Name == "w:p" || c.Name == "w:tbl" || c.Name == "w:sdt") {
					i = k
					break
				}
				i = k + 1
			}
			tc.insertAt(i, pr)
		}
		for _, v := range pr.els("w:shd") {
			pr.remove(v)
		}
		shd := newEl("w:shd", attr{"w:val", "clear"}, attr{"w:color", "auto"}, attr{"w:fill", amarillo})
		pos := len(pr.Children)
		for k, c := range pr.Children {
			if c.Kind == kElement && trasShd[c.Name] {
				pos = k
				break
			}
		}
		pr.insertAt(pos, shd)
	}
}

// sectionTextWidth: ancho útil de la 1ª sección en twips (página − márgenes), como python-docx.
func sectionTextWidth(b *node) (int, error) {
	var sect *node
	for _, c := range b.Children {
		if c.Kind != kElement {
			continue
		}
		if c.Name == "w:p" {
			if pr := c.first("w:pPr"); pr != nil {
				if s := pr.first("w:sectPr"); s != nil {
					sect = s
					break
				}
			}
		}
		if c.Name == "w:sectPr" {
			sect = c
			break
		}
	}
	if sect == nil {
		return 0, genErr("La plantilla no tiene definido el tamaño de página.")
	}
	num := func(el, a string) (int, bool) {
		e := sect.first(el)
		if e == nil {
			return 0, false
		}
		v, ok := e.attr(a)
		if !ok {
			return 0, false
		}
		n, err := strconv.Atoi(v)
		return n, err == nil
	}
	w, ok1 := num("w:pgSz", "w:w")
	l, ok2 := num("w:pgMar", "w:left")
	r, ok3 := num("w:pgMar", "w:right")
	if !ok1 || !ok2 || !ok3 {
		return 0, genErr("La plantilla no tiene definido el tamaño de página.")
	}
	return w - l - r, nil
}

// tabulacionDerechaEncabezado: el encabezado separa el torneo de «Fecha:» con 7 tabulaciones; si el
// nombre del torneo es largo, la fecha se desalinea. Se reemplazan por una sola tabulación alineada
// al margen derecho.
func tabulacionDerechaEncabezado(enc, b *node) error {
	var tabs []*node
	for _, r := range runs(enc) {
		if runText(r) == "\t" {
			tabs = append(tabs, r)
		}
	}
	if len(tabs) == 0 {
		return nil
	}
	for _, r := range tabs[1:] {
		enc.remove(r)
	}
	ancho, err := sectionTextWidth(b)
	if err != nil {
		return err
	}
	pr := enc.first("w:pPr")
	if pr == nil {
		pr = newEl("w:pPr")
		enc.insertAt(0, pr)
	}
	if pr.first("w:tabs") != nil {
		return nil
	}
	tabsEl := newEl("w:tabs")
	tab := newEl("w:tab", attr{"w:val", "right"}, attr{"w:pos", strconv.Itoa(ancho)})
	tab.parent = tabsEl
	tabsEl.Children = []*node{tab}
	pos := 0
	for k, c := range pr.Children { // <w:tabs> va después de pStyle, numPr… y antes de spacing
		if c.Kind == kElement && antesTabs[c.Name] {
			pos = k + 1
		}
	}
	pr.insertAt(pos, tabsEl)
	return nil
}

func quitarColumna(tbl *node, idx int) {
	for _, tr := range rows(tbl) {
		cs := cells(tr)
		if idx < len(cs) {
			tr.remove(cs[idx])
		}
	}
	if g := tbl.first("w:tblGrid"); g != nil {
		if cols := g.els("w:gridCol"); idx < len(cols) {
			g.remove(cols[idx])
		}
	}
}

func todoElTexto(b *node) string {
	var parts []string
	for _, p := range bodyParagraphs(b) {
		parts = append(parts, paragraphText(p))
	}
	for _, t := range bodyTables(b) {
		for _, tr := range rows(t) {
			for _, tc := range cells(tr) {
				for _, p := range tc.els("w:p") {
					parts = append(parts, paragraphText(p))
				}
			}
		}
	}
	return strings.Join(parts, "\n")
}

// Render genera el .docx de un destinatario (port de crear_docx).
func (Renderer) Render(_ context.Context, template []byte, in ports.DocumentInput) ([]byte, error) {
	r, cfg := in.Recipient, in.Config
	beca, ok := cfg.Beca(r.Beca)
	if !ok || len(beca.Pagos) == 0 {
		return nil, genErr("No hay plan de pagos configurado para esta beca.")
	}
	plan := make([]int, len(beca.Pagos))
	for i, p := range beca.Pagos {
		v, err := euros(p)
		if err != nil {
			return nil, err
		}
		plan[i] = v
	}
	if len(in.FechasPago) != len(plan) {
		return nil, genErr("El plan tiene %d pago(s) pero se indicaron %d fecha(s).", len(plan), len(in.FechasPago))
	}

	zr, xmlData, err := readDocument(template)
	if err != nil {
		return nil, err
	}
	root, err := parseXML(xmlData)
	if err != nil {
		return nil, genErr("No pude abrir el archivo; ¿es un Word válido?")
	}
	b := body(root)
	if b == nil {
		return nil, genErr("No pude abrir el archivo; ¿es un Word válido?")
	}

	// --- Encabezado: torneo de origen y fecha de emisión
	var enc *node
	for _, p := range bodyParagraphs(b) {
		if esEncabezado(paragraphText(p)) {
			enc = p
			break
		}
	}
	if enc == nil {
		return nil, genErr("La plantilla no tiene el encabezado «XXXX … Fecha:XX/XX/XXXX».")
	}
	if _, err := replaceInParagraph(enc, matchTorneo, strings.ToUpper(in.Torneo)); err != nil {
		return nil, err
	}
	if err := replaceOrFail(enc, matchFechaX, in.FechaEmision, "La plantilla no tiene el campo de fecha XX/XX/XXXX."); err != nil {
		return nil, err
	}
	if err := tabulacionDerechaEncabezado(enc, b); err != nil {
		return nil, err
	}

	// --- Datos del jugador
	movil, correo := "—", "—" // sin ese dato (basta con un canal): se deja una raya en lugar de un campo vacío
	if m := domain.NationalPhone(r.Movil); m != "" {
		movil = m
	}
	if len(r.Correos) > 0 {
		correo = strings.Join(r.Correos, ", ")
	}
	for _, f := range []struct{ etiqueta, valor string }{
		{"Acudiente", r.Acudiente}, {"Jugador", r.Jugador}, {"Club", r.Club},
		{"Año", strconv.Itoa(r.Anio)}, {"Móvil", movil}, {"Correo", correo},
	} {
		p, err := buscar(b, f.etiqueta)
		if err != nil {
			return nil, err
		}
		if _, err := replaceInParagraph(p, matchX, f.valor); err != nil {
			return nil, err
		}
	}

	// --- Tablas
	tbls := bodyTables(b)
	if len(tbls) < 2 {
		return nil, genErr("Debe tener la tabla de costos y la tabla de pagos.")
	}
	costos, pagos := tbls[0], tbls[1]

	// Tabla de costos: valores desde la configuración + beca del jugador en amarillo
	resaltada := false
	for i, tr := range rows(costos) {
		if i == 0 {
			continue
		}
		cs := cells(tr)
		if len(cs) < 2 {
			continue
		}
		etiqueta := strings.ToLower(strings.TrimSpace(cellText(cs[0])))
		var clave domain.BecaKey
		switch m := reBecaPct.FindStringSubmatch(etiqueta); {
		case strings.Contains(etiqueta, "sin beca"):
			clave = domain.BecaSinBeca
		case m != nil:
			clave = domain.BecaKey(m[1])
		case strings.Contains(etiqueta, "acompa"):
			clave = domain.BecaAcompanante
		default:
			continue
		}
		bc, ok := cfg.Beca(clave)
		if !ok {
			continue
		}
		v, err := euros(bc.Total)
		if err != nil {
			return nil, err
		}
		texto := "€ " + FormatEuros(v)
		if clave == "100" {
			texto += " admón."
		}
		if err := ponerTextoCelda(cs[1], texto); err != nil {
			return nil, err
		}
		if clave == r.Beca {
			resaltarFila(tr)
			resaltada = true
		}
	}
	if !resaltada {
		return nil, genErr("No encontré la fila de la beca en la tabla de costos.")
	}

	// Programación de pagos (1 a 3 columnas según la beca)
	if hasMerges(pagos) {
		return nil, genErr("La tabla de pagos tiene celdas combinadas; no se puede quitar columnas.")
	}
	prs := rows(pagos)
	if len(prs) < 2 {
		return nil, genErr("La tabla de pagos no tiene el campo XXX del valor.")
	}
	n := len(plan)
	for i := 0; i < n; i++ {
		col := i + 1
		c0, c1 := cells(prs[0]), cells(prs[1])
		if col >= len(c0) || col >= len(c1) {
			return nil, genErr("La tabla de pagos no tiene el campo XXX del valor.")
		}
		if err := replaceOrFail(firstP(c0[col]), matchXXX, FormatEuros(plan[i]), "La tabla de pagos no tiene el campo XXX del valor."); err != nil {
			return nil, err
		}
		if err := replaceOrFail(firstP(c1[col]), matchFechaX, in.FechasPago[i], "La tabla de pagos no tiene el campo XX/XX/XXXX de la fecha."); err != nil {
			return nil, err
		}
	}
	for col := tableColumns(pagos) - 1; col > n; col-- {
		quitarColumna(pagos, col)
	}

	// --- Control final: no debe quedar ningún campo sin reemplazar
	if _, found := matchPlaceholder([]rune(todoElTexto(b))); found {
		return nil, genErr("Quedaron campos «XXX» sin reemplazar en el documento.")
	}
	return writeDocument(zr, root.bytes())
}

// firstP devuelve el primer párrafo de una celda (o uno vacío si no tiene).
func firstP(tc *node) *node {
	if ps := tc.els("w:p"); len(ps) > 0 {
		return ps[0]
	}
	return newEl("w:p")
}

// ValidateTemplate port de _revisar_plantilla (app.py): mismos controles y mismos mensajes.
func (Renderer) ValidateTemplate(data []byte) string {
	_, xmlData, err := readDocument(data)
	if err != nil {
		return "No pude abrir el archivo; ¿es un Word válido?"
	}
	root, err := parseXML(xmlData)
	if err != nil || body(root) == nil {
		return "No pude abrir el archivo; ¿es un Word válido?"
	}
	b := body(root)
	var textos []string
	for _, p := range bodyParagraphs(b) {
		textos = append(textos, paragraphText(p))
	}
	any := func(f func(string) bool) bool {
		for _, t := range textos {
			if f(t) {
				return true
			}
		}
		return false
	}
	if !any(esEncabezado) {
		return "No encontré el encabezado con el torneo «XXXX» y la fecha «XX/XX/XXXX» en la misma línea."
	}
	if !any(func(t string) bool { _, ok := matchFechaX([]rune(t)); return ok }) {
		return "No encontré la fecha «XX/XX/XXXX» del encabezado."
	}
	for _, etiqueta := range []string{"Acudiente", "Jugador", "Club", "Año", "Móvil", "Correo"} {
		if !any(func(t string) bool {
			_, ok := matchX([]rune(t))
			return startsWithTrim(t, etiqueta) && ok
		}) {
			return fmt.Sprintf("No encontré el campo «%s: XXXX».", etiqueta)
		}
	}
	tbls := bodyTables(b)
	if len(tbls) < 2 {
		return "Debe tener la tabla de costos y la tabla de pagos."
	}
	costos, pagos := tbls[0], tbls[1]
	if tableColumns(pagos) != 1+domain.MaxPagos || len(rows(pagos)) != 2 {
		return "La tabla de «Programación Pagos» debe tener 2 filas y 4 columnas (PAGOS + 3 pagos)."
	}
	var labels []string
	for _, tr := range rows(costos) {
		if cs := cells(tr); len(cs) > 0 {
			labels = append(labels, strings.ToLower(cellText(cs[0])))
		}
	}
	joined := strings.ReplaceAll(strings.Join(labels, " "), " %", "%")
	for _, esperado := range []string{"sin beca", "30%", "50", "70%", "100%", "acompa"} {
		if !strings.Contains(joined, esperado) {
			return "La tabla de costos debe tener las filas: Sin Beca, 30%, 50%, 70%, 100% y Acompañante."
		}
	}
	return ""
}
