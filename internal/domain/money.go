package domain

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Money es un importe en céntimos de euro (enteros: sin errores de redondeo).
type Money int64

var (
	reMilesComa = regexp.MustCompile(`^(\d{1,3}(\.\d{3})+|\d+)$`) // parte entera con coma decimal
	reMilesSolo = regexp.MustCompile(`^\d{1,3}(\.\d{3})+$`)       // 2.800
	reDecPunto  = regexp.MustCompile(`^\d+\.\d{1,2}$`)            // 930.50
	reEntero    = regexp.MustCompile(`^\d+$`)
	reDecimales = regexp.MustCompile(`^\d{1,2}$`)
)

// ParseMoneyEUR interpreta importes como "€  2.800,00", "930,5", "200" o "2800".
// Formato español: punto de miles, coma decimal. present=false si la celda está vacía.
func ParseMoneyEUR(s string) (m Money, present bool, err error) {
	t := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '€' || r == ' ' {
			return -1
		}
		return r
	}, s)
	if t == "" {
		return 0, false, nil
	}
	if strings.HasPrefix(t, "-") {
		return 0, true, fmt.Errorf("el importe %q es negativo", strings.TrimSpace(s))
	}
	bad := fmt.Errorf("el importe %q no tiene un formato válido (ej. € 1.960,00)", strings.TrimSpace(s))

	var entero, frac string
	switch {
	case strings.Contains(t, ","):
		parts := strings.Split(t, ",")
		if len(parts) != 2 || !reMilesComa.MatchString(parts[0]) || !reDecimales.MatchString(parts[1]) {
			return 0, true, bad
		}
		entero, frac = strings.ReplaceAll(parts[0], ".", ""), parts[1]
	case strings.Contains(t, "."):
		switch {
		case reMilesSolo.MatchString(t):
			entero = strings.ReplaceAll(t, ".", "")
		case reDecPunto.MatchString(t):
			p := strings.Split(t, ".")
			entero, frac = p[0], p[1]
		default:
			return 0, true, bad
		}
	case reEntero.MatchString(t):
		entero = t
	default:
		return 0, true, bad
	}
	if len(frac) == 1 {
		frac += "0"
	}
	if frac == "" {
		frac = "00"
	}
	e, err1 := strconv.ParseInt(entero, 10, 64)
	f, err2 := strconv.ParseInt(frac, 10, 64)
	if err1 != nil || err2 != nil || e > 1_000_000_000 {
		return 0, true, bad
	}
	return Money(e*100 + f), true, nil
}

// FormatEUR da formato español: "€ 2.800,00".
func FormatEUR(m Money) string {
	neg := m < 0
	if neg {
		m = -m
	}
	ent := strconv.FormatInt(int64(m)/100, 10)
	var b strings.Builder
	for i, r := range ent {
		if i > 0 && (len(ent)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}
	s := fmt.Sprintf("€ %s,%02d", b.String(), int64(m)%100)
	if neg {
		return "-" + s
	}
	return s
}
