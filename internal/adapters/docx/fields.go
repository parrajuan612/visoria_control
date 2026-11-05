package docx

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Port de las expresiones regulares de generador.py. Las posiciones son índices de CARACTERES
// (runes), como en Python, no de bytes.
//
//	RE_X           = X{3,}
//	RE_FECHA_X     = X{2}/X{2}/X{4}
//	RE_TORNEO      = (?:TORNEO\s+)?(?<![/X])X{3,}(?!/)     ← usa lookaround: se implementa a mano
//	RE_PLACEHOLDER = X{3}|XX/
type span struct{ start, end int }

// matcher busca la primera coincidencia en el texto.
type matcher func([]rune) (span, bool)

var (
	reXRe           = regexp.MustCompile(`X{3,}`)
	reFechaXRe      = regexp.MustCompile(`XX/XX/XXXX`)
	rePlaceholderRe = regexp.MustCompile(`XXX|XX/`)
	reXXXRe         = regexp.MustCompile(`XXX`)
)

func fromRegexp(re *regexp.Regexp) matcher {
	return func(text []rune) (span, bool) {
		s := string(text)
		loc := re.FindStringIndex(s)
		if loc == nil {
			return span{}, false
		}
		return span{utf8.RuneCountInString(s[:loc[0]]), utf8.RuneCountInString(s[:loc[1]])}, true
	}
}

var (
	matchX           = fromRegexp(reXRe)
	matchFechaX      = fromRegexp(reFechaXRe)
	matchPlaceholder = fromRegexp(rePlaceholderRe)
	matchXXX         = fromRegexp(reXXXRe) // r"X{3}": las tres primeras X del valor de un pago
)

func isSpace(r rune) bool { return unicode.IsSpace(r) }

// xRunAt: si en j empieza un grupo de X que cumple (?<![/X])X{3,}(?!/), devuelve su fin.
// Replica el retroceso del motor: si tras todas las X viene "/", prueba con una X menos.
func xRunAt(t []rune, j int) (int, bool) {
	if j >= len(t) || t[j] != 'X' {
		return 0, false
	}
	if j > 0 && (t[j-1] == '/' || t[j-1] == 'X') { // (?<![/X])
		return 0, false
	}
	n := 0
	for j+n < len(t) && t[j+n] == 'X' {
		n++
	}
	for m := n; m >= 3; m-- { // X{3,} greedy con retroceso
		e := j + m
		if e >= len(t) || t[e] != '/' { // (?!/)
			return e, true
		}
	}
	return 0, false
}

// matchTorneo implementa (?:TORNEO\s+)?(?<![/X])X{3,}(?!/) buscando de izquierda a derecha
// y probando primero el grupo opcional, como Python.
func matchTorneo(t []rune) (span, bool) {
	const word = "TORNEO"
	w := []rune(word)
	for i := 0; i <= len(t); i++ {
		// con el grupo opcional "TORNEO" + espacios
		if i+len(w) <= len(t) && string(t[i:i+len(w)]) == word {
			j := i + len(w)
			k := j
			for k < len(t) && isSpace(t[k]) {
				k++
			}
			if k > j { // \s+ exige al menos un espacio
				if e, ok := xRunAt(t, k); ok {
					return span{i, e}, true
				}
			}
		}
		// sin el grupo
		if e, ok := xRunAt(t, i); ok {
			return span{i, e}, true
		}
	}
	return span{}, false
}

// esEncabezado: párrafo con el campo del torneo y la fecha de emisión XX/XX/XXXX.
func esEncabezado(text string) bool {
	r := []rune(text)
	_, f := matchFechaX(r)
	_, t := matchTorneo(r)
	return f && t
}

func startsWithTrim(text, prefix string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), prefix)
}
