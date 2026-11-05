package domain

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

var (
	reIlegales = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)
	reEspacios = regexp.MustCompile(`\s+`)
)

// SafeFileName port de _nombre_seguro (proceso.py): quita los caracteres que los sistemas de archivos
// no admiten, junta espacios, recorta puntos y espacios de los extremos y limita el largo.
func SafeFileName(text string, max int) string {
	t := reIlegales.ReplaceAllString(text, " ")
	t = strings.Trim(reEspacios.ReplaceAllString(t, " "), " .")
	t = strings.TrimSpace(t)
	if utf8.RuneCountInString(t) > max {
		t = strings.TrimSpace(string([]rune(t)[:max]))
	}
	return t
}

// VisibleFileNames calcula el nombre con que el acudiente ve cada PDF: "{JUGADOR} - BECA {n}.pdf"
// ("SIN BECA" si no tiene). Si dos jugadores darían el mismo nombre se agrega " (2)", " (3)"…
// (sin distinguir mayúsculas), en el orden de las filas del Excel, como _nombre_archivo del cliente.
func VisibleFileNames(recs []Recipient) map[string]string {
	ordered := append([]Recipient(nil), recs...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Fila < ordered[j].Fila })
	used := map[string]bool{}
	out := make(map[string]string, len(recs))
	for _, r := range ordered {
		beca := "BECA " + strings.TrimSuffix(BecaLabel(r.Beca), "%")
		if r.Beca == BecaSinBeca {
			beca = "SIN BECA"
		}
		base := SafeFileName(r.Jugador, 90) + " - " + beca
		name, n := base, 2
		for used[strings.ToLower(name)] {
			name = fmt.Sprintf("%s (%d)", base, n)
			n++
		}
		used[strings.ToLower(name)] = true
		out[r.ID] = name + ".pdf"
	}
	return out
}

var smallWords = map[string]bool{"de": true, "del": true, "la": true, "las": true, "los": true, "y": true}

// ProperName port de nombre_propio (correo.py): "JUAN DE LA CRUZ MUÑOZ" → "Juan de la Cruz Muñoz".
func ProperName(text string) string {
	words := strings.Fields(strings.ToLower(text))
	for i, w := range words {
		if i > 0 && smallWords[w] {
			continue
		}
		r := []rune(w)
		words[i] = strings.ToUpper(string(r[:1])) + string(r[1:])
	}
	return strings.Join(words, " ")
}
