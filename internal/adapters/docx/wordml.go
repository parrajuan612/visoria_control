package docx

import (
	"strings"
)

// Modelo de WordprocessingML equivalente al que usa python-docx en generador.py.

// runText replica CT_R.text de python-docx 1.1: w:t, w:tab, w:br/w:cr, w:noBreakHyphen y w:ptab.
func runText(r *node) string {
	var b strings.Builder
	for _, c := range r.Children {
		if c.Kind != kElement {
			continue
		}
		switch c.Name {
		case "w:t":
			for _, tc := range c.Children {
				if tc.Kind == kText {
					b.WriteString(tc.Text)
				}
			}
		case "w:tab", "w:ptab":
			b.WriteByte('\t')
		case "w:br":
			if t, ok := c.attr("w:type"); !ok || t == "textWrapping" {
				b.WriteByte('\n')
			}
		case "w:cr":
			b.WriteByte('\n')
		case "w:noBreakHyphen":
			b.WriteByte('-')
		}
	}
	return b.String()
}

// objetos que viven DENTRO de un run y que reescribir su texto destruiría.
var runObjects = map[string]bool{
	"w:drawing": true, "w:pict": true, "w:object": true, "w:fldChar": true, "w:instrText": true,
	"w:footnoteReference": true, "w:endnoteReference": true, "w:sym": true, "w:commentReference": true,
}

// setRunText replica CT_R.text = ...: borra el contenido (menos w:rPr) y lo vuelve a crear:
// "\t" → w:tab, "\n"/"\r" → w:br, el resto → w:t (con xml:space="preserve" si hace falta).
// A diferencia de python-docx, NO borra en silencio dibujos ni campos: devuelve error.
func setRunText(r *node, text string) error {
	for _, c := range r.Children {
		if c.Kind == kElement && runObjects[c.Name] {
			return genErr("Un campo de la plantilla comparte línea con un objeto (imagen o campo de Word) y no se puede reemplazar sin dañarlo.")
		}
	}
	keep := r.Children[:0:0]
	for _, c := range r.Children {
		if c.Kind == kElement && c.Name == "w:rPr" {
			keep = append(keep, c)
		}
	}
	r.Children = keep

	var buf strings.Builder
	flush := func() {
		if buf.Len() == 0 {
			return
		}
		s := buf.String()
		t := newEl("w:t")
		if strings.TrimSpace(s) != s {
			t.Attrs = append(t.Attrs, attr{"xml:space", "preserve"})
		}
		t.Children = []*node{{Kind: kText, Text: s, parent: t}}
		t.parent = r
		r.Children = append(r.Children, t)
		buf.Reset()
	}
	add := func(name string) {
		flush()
		e := newEl(name)
		e.parent = r
		r.Children = append(r.Children, e)
	}
	for _, ch := range text {
		switch ch {
		case '\t':
			add("w:tab")
		case '\n', '\r':
			add("w:br")
		default:
			buf.WriteRune(ch)
		}
	}
	flush()
	return nil
}

// runs devuelve los w:r DIRECTOS del párrafo (Paragraph.runs de python-docx).
func runs(p *node) []*node { return p.els("w:r") }

// paragraphText replica Paragraph.text (1.1): runs y enlaces, en orden.
func paragraphText(p *node) string {
	var b strings.Builder
	for _, c := range p.Children {
		if c.Kind != kElement {
			continue
		}
		switch c.Name {
		case "w:r":
			b.WriteString(runText(c))
		case "w:hyperlink":
			for _, r := range c.els("w:r") {
				b.WriteString(runText(r))
			}
		}
	}
	return b.String()
}

// replaceInRuns port literal de _reemplazar(): reemplaza la primera coincidencia aunque Word la
// haya partido en varios runs. El texto nuevo toma el formato del primer run afectado; el resto
// de runs solo pierde la parte reemplazada. Devuelve false si no hay coincidencia.
func replaceInRuns(texts []string, m matcher, nuevo string) ([]string, bool) {
	rs := make([][]rune, len(texts))
	var all []rune
	for i, t := range texts {
		rs[i] = []rune(t)
		all = append(all, rs[i]...)
	}
	loc, ok := m(all)
	if !ok {
		return texts, false
	}
	ini, fin := loc.start, loc.end
	out := make([]string, len(texts))
	pos, primero := 0, true
	for i, t := range rs {
		rIni, rFin := pos, pos+len(t)
		pos = rFin
		if rFin <= ini || rIni >= fin {
			out[i] = string(t)
			continue
		}
		a, b := max(ini, rIni)-rIni, min(fin, rFin)-rIni
		if primero {
			out[i] = string(t[:a]) + nuevo + string(t[b:])
			primero = false
		} else {
			out[i] = string(t[:a]) + string(t[b:])
		}
	}
	return out, true
}

// replaceInParagraph aplica replaceInRuns sobre los runs de un párrafo y reescribe solo los que cambian.
func replaceInParagraph(p *node, m matcher, nuevo string) (bool, error) {
	rs := runs(p)
	old := make([]string, len(rs))
	for i, r := range rs {
		old[i] = runText(r)
	}
	out, ok := replaceInRuns(old, m, nuevo)
	if !ok {
		return false, nil
	}
	for i, r := range rs {
		if out[i] != old[i] {
			if err := setRunText(r, out[i]); err != nil {
				return false, err
			}
		}
	}
	return true, nil
}

// ---- tablas ----

func body(root *node) *node {
	if d := root.first("w:document"); d != nil {
		return d.first("w:body")
	}
	return nil
}

func bodyParagraphs(b *node) []*node { return b.els("w:p") } // Document.paragraphs: solo los del cuerpo
func bodyTables(b *node) []*node     { return b.els("w:tbl") }

func rows(tbl *node) []*node { return tbl.els("w:tr") }
func cells(tr *node) []*node { return tr.els("w:tc") }

// cellText replica _Cell.text: los párrafos de la celda unidos por "\n".
func cellText(tc *node) string {
	ps := tc.els("w:p")
	parts := make([]string, len(ps))
	for i, p := range ps {
		parts[i] = paragraphText(p)
	}
	return strings.Join(parts, "\n")
}

// tableColumns replica len(table.columns): las columnas de w:tblGrid.
func tableColumns(tbl *node) int {
	if g := tbl.first("w:tblGrid"); g != nil {
		return len(g.els("w:gridCol"))
	}
	return 0
}

// hasMerges indica si la tabla usa celdas combinadas (no soportadas al quitar columnas).
func hasMerges(tbl *node) bool {
	for _, tr := range rows(tbl) {
		for _, tc := range cells(tr) {
			if pr := tc.first("w:tcPr"); pr != nil {
				if g := pr.first("w:gridSpan"); g != nil {
					if v, _ := g.attr("w:val"); v != "" && v != "1" {
						return true
					}
				}
				if pr.first("w:vMerge") != nil || pr.first("w:hMerge") != nil {
					return true
				}
			}
		}
	}
	return false
}
