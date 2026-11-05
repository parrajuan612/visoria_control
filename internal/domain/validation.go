package domain

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ValidationOptions son las opciones de una campaña.
type ValidationOptions struct {
	// RequireBothChannels convierte en ERROR que falte (o sea inválido) el móvil o el correo.
	// Por defecto una fila es válida si tiene al menos un canal válido (D3).
	RequireBothChannels bool
}

// Campos de los hallazgos (coinciden con las columnas del Excel).
const (
	CampoJugador   = "Jugador"
	CampoClub      = "Club"
	CampoAcudiente = "Acudiente"
	CampoBeca      = "Beca"
	CampoAnio      = "Año de nacimiento"
	CampoMovil     = "Móvil"
	CampoCorreo    = "Correo"
	CampoCanales   = "Canales"
	CampoDuplicado = "Duplicado"
	CampoTorneo    = "Torneo"
	CampoCategoria = "Categoría"
)

// ---------------------------------------------------------------------------
// Parseo de celdas
// ---------------------------------------------------------------------------

var (
	reAnio4      = regexp.MustCompile(`^(19|20)\d{2}$`)
	reAnioEnDate = regexp.MustCompile(`(?:^|[^0-9])((?:19|20)\d{2})(?:[^0-9]|$)`)
	reNumero     = regexp.MustCompile(`^\d+([.,]\d+)?$`)
	reCientifica = regexp.MustCompile(`^\d+([.,]\d+)?[eE][+-]?\d+$`)
	reEmail      = regexp.MustCompile(`^[A-Za-z0-9._%+'\-]+@[A-Za-z0-9\-]+(\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,}$`)
)

// cleanNumber convierte lo que Excel guarda para un número ("3.2093234E9", "3209323449.0")
// a su forma entera en texto; si no es un número, devuelve la entrada limpia.
func cleanNumber(s string) string {
	t := strings.TrimSpace(s)
	if reCientifica.MatchString(t) {
		if f, err := strconv.ParseFloat(strings.Replace(t, ",", ".", 1), 64); err == nil && f == math.Trunc(f) {
			return strconv.FormatFloat(f, 'f', 0, 64)
		}
	}
	if strings.HasSuffix(t, ".0") && reNumero.MatchString(strings.TrimSuffix(t, ".0")) {
		return strings.TrimSuffix(t, ".0")
	}
	return t
}

// ParseBirthYear interpreta la celda de nacimiento, que debe traer SOLO el año (ej. 2010).
// Por tolerancia acepta una fecha completa (extrae el año y avisa) y un número de serie de Excel.
func ParseBirthYear(s string) (year int, warn string, err error) {
	t := cleanNumber(s)
	switch {
	case t == "":
		return 0, "", fmt.Errorf("falta el año de nacimiento")
	case reAnio4.MatchString(t):
		year, _ = strconv.Atoi(t)
	case reNumero.MatchString(t) && !strings.ContainsAny(t, ".,"):
		// Número de serie de una fecha de Excel (días desde 1899-12-30).
		n, _ := strconv.Atoi(t)
		if n < 10000 || n > 80000 {
			return 0, "", fmt.Errorf("el año de nacimiento «%s» no es válido (use solo el año, ej. 2010)", t)
		}
		year = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC).AddDate(0, 0, n).Year()
		warn = fmt.Sprintf("la celda trae una fecha y no solo el año; se tomó %d (escriba solo el año, ej. 2010)", year)
	default:
		m := reAnioEnDate.FindStringSubmatch(t)
		if m == nil || reNumero.MatchString(t) { // un decimal suelto ("2010.5") no es una fecha
			return 0, "", fmt.Errorf("el año de nacimiento «%s» no es válido (use solo el año, ej. 2010)", t)
		}
		year, _ = strconv.Atoi(m[1])
		warn = fmt.Sprintf("la celda trae «%s» y no solo el año; se tomó %d (escriba solo el año, ej. 2010)", t, year)
	}
	if year < MinBirthYear || year > MaxBirthYear {
		return 0, "", fmt.Errorf("el año de nacimiento %d no es plausible (se esperaba entre %d y %d)", year, MinBirthYear, MaxBirthYear)
	}
	return year, warn, nil
}

// ParseBecaCell interpreta la celda de beca: "50%", "50", "0,5", "SIN BECA", "ACOMPAÑANTE".
func ParseBecaCell(s string) (BecaKey, error) {
	t := cleanNumber(s)
	if t == "" {
		return "", fmt.Errorf("falta la beca")
	}
	// Excel guarda 50 % como 0,5: se convierte a porcentaje.
	if reNumero.MatchString(t) {
		if f, err := strconv.ParseFloat(strings.Replace(t, ",", ".", 1), 64); err == nil && f > 0 && f < 1 {
			t = strconv.Itoa(int(math.Round(f * 100)))
		}
	}
	if k, ok := NormalizeBecaKey(t); ok {
		return k, nil
	}
	return "", fmt.Errorf("la beca «%s» no es válida", strings.TrimSpace(s))
}

// NormalizePhone devuelve el móvil en formato E.164 sin "+": 57XXXXXXXXXX.
// Acepta "320 932 3449", "(+57) 320-9323449", "573209323449", "3.2093234E9" y números
// internacionales con prefijo "+" o "00". Devuelve "" sin error si la celda está vacía.
func NormalizePhone(s string) (string, error) {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return "", nil
	}
	raw = cleanNumber(raw)
	intl := strings.HasPrefix(raw, "+") || strings.HasPrefix(raw, "00")
	var digits strings.Builder
	for _, r := range raw {
		switch {
		case unicode.IsDigit(r):
			digits.WriteRune(r)
		case unicode.IsSpace(r), strings.ContainsRune("+-().", r):
		default:
			return "", fmt.Errorf("MOVIL «%s» contiene caracteres no válidos", raw)
		}
	}
	d := digits.String()
	if strings.HasPrefix(raw, "00") {
		d = strings.TrimPrefix(d, "00")
	}
	switch {
	case d == "":
		return "", fmt.Errorf("MOVIL «%s» no tiene dígitos", raw)
	case !intl && len(d) == 10:
		if d[0] != '3' {
			return "", fmt.Errorf("MOVIL «%s» no es un móvil colombiano (debe empezar por 3)", raw)
		}
		return "57" + d, nil
	case strings.HasPrefix(d, "57"): // el 57 es solo de Colombia: 57 + 10 dígitos que empiezan por 3
		if len(d) != 12 {
			return "", fmt.Errorf("MOVIL «%s» está incompleto (57 + 10 dígitos)", raw)
		}
		if d[2] != '3' {
			return "", fmt.Errorf("MOVIL «%s» no es un móvil colombiano (después del 57 debe empezar por 3)", raw)
		}
		return d, nil
	case intl && len(d) >= 8 && len(d) <= 15:
		return d, nil // internacional (E.164: hasta 15 dígitos)
	case len(d) < 10:
		return "", fmt.Errorf("MOVIL «%s» está incompleto (10 dígitos)", raw)
	default:
		return "", fmt.Errorf("MOVIL «%s» no tiene un largo válido (10 dígitos, o 57 + 10 dígitos)", raw)
	}
}

// NationalPhone devuelve el móvil colombiano en 10 dígitos (sin 57); los demás se devuelven tal cual.
func NationalPhone(e164 string) string {
	if len(e164) == 12 && strings.HasPrefix(e164, "57") {
		return e164[2:]
	}
	return e164
}

var typoDominios = map[string]string{
	"gmial.com": "gmail.com", "gmai.com": "gmail.com", "gmail.co": "gmail.com", "gamil.com": "gmail.com",
	"gmail.con": "gmail.com", "gmil.com": "gmail.com", "hotmial.com": "hotmail.com", "hotmal.com": "hotmail.com",
	"hotmail.con": "hotmail.com", "outlok.com": "outlook.com", "yaho.com": "yahoo.com", "yahoo.con": "yahoo.com",
}

// ParseEmails separa por ; , o espacios, pasa a minúsculas, quita repetidos y clasifica.
func ParseEmails(s string) (valid, invalid []string) {
	seen := map[string]bool{}
	for _, tok := range strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == ',' || unicode.IsSpace(r) }) {
		e := strings.ToLower(strings.Trim(tok, "<>\"'"))
		if e == "" || seen[e] {
			continue
		}
		seen[e] = true
		if len(e) <= 254 && !strings.Contains(e, "..") && reEmail.MatchString(e) {
			valid = append(valid, e)
		} else {
			invalid = append(invalid, tok)
		}
	}
	return valid, invalid
}

// ---------------------------------------------------------------------------
// Validación de una fila
// ---------------------------------------------------------------------------

func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

// ValidateRow valida y normaliza una fila contra la configuración vigente.
// Reglas (D3 y SDD §3.9): jugador, club, acudiente, beca y año son obligatorios (error);
// "SIN BECA" omite la fila; la fila es válida con al menos un canal válido (móvil o correo).
func ValidateRow(raw RawRow, cfg MasterConfig, opt ValidationOptions) Recipient {
	r := Recipient{
		Fila: raw.Fila, Jugador: squash(raw.Jugador), Club: squash(raw.Club), Acudiente: squash(raw.Acudiente),
		Torneos: []string{}, Correos: []string{}, Issues: []Issue{},
	}
	add := func(n IssueLevel, campo, msg string, a ...any) {
		r.Issues = append(r.Issues, Issue{Nivel: n, Campo: campo, Mensaje: fmt.Sprintf(msg, a...)})
	}

	// --- Obligatorios de texto ---
	for _, f := range []struct{ val, campo string }{{r.Jugador, CampoJugador}, {r.Club, CampoClub}, {r.Acudiente, CampoAcudiente}} {
		if f.val == "" {
			add(IssueError, f.campo, "falta el dato (es obligatorio)")
		}
	}

	// --- Beca ---
	key, err := ParseBecaCell(raw.Beca)
	if err != nil {
		add(IssueError, CampoBeca, "%v", err)
	} else {
		r.Beca = key
		if _, ok := cfg.Beca(key); !ok && key != BecaSinBeca {
			add(IssueError, CampoBeca, "la beca «%s» no existe en la configuración de Google Sheets", BecaLabel(key))
		}
	}
	if r.Beca == BecaSinBeca && !HasErrors(r.Issues) {
		add(IssueInfo, CampoBeca, "SIN BECA: no se genera propuesta para esta fila")
		r.Estado = RecipientOmitted
		return r
	}

	// --- Año → categoría, plantilla y torneos ---
	year, warn, err := ParseBirthYear(raw.Anio)
	if err != nil {
		add(IssueError, CampoAnio, "%v", err)
	} else {
		r.Anio = year
		if warn != "" {
			add(IssueWarning, CampoAnio, "%s", warn)
		}
		if cats := cfg.CategoriasParaAnio(year); len(cats) == 0 {
			add(IssueError, CampoCategoria, "no hay una categoría configurada para el año %d", year)
		} else {
			r.Categoria, r.Plantilla = cats[0].Nombre, cats[0].Plantilla
			if r.Plantilla == "" {
				add(IssueError, CampoCategoria, "la categoría «%s» no tiene una plantilla Word asociada", r.Categoria)
			}
		}
		if t := cfg.TorneosParaAnio(year); len(t) == 0 {
			add(IssueError, CampoTorneo, "no hay un torneo configurado para el año %d", year)
		} else {
			r.Torneos = t
		}
	}

	// --- Canales de envío ---
	validateChannels(&r, raw, opt, add)

	if HasErrors(r.Issues) {
		r.Estado = RecipientInvalid
	} else {
		r.Estado = RecipientValid
	}
	return r
}

func validateChannels(r *Recipient, raw RawRow, opt ValidationOptions, add func(IssueLevel, string, string, ...any)) {
	// Móvil
	var movilProblem string
	if tel, err := NormalizePhone(raw.Movil); err != nil {
		movilProblem = err.Error()
	} else if tel == "" {
		movilProblem = "no tiene móvil"
	} else {
		r.Movil = tel
	}
	// Correo(s)
	var correoProblem string
	valid, invalid := ParseEmails(raw.Correo)
	r.Correos = append(r.Correos, valid...)
	if strings.TrimSpace(raw.Correo) == "" {
		correoProblem = "no tiene correo"
	} else if len(valid) == 0 {
		correoProblem = fmt.Sprintf("el correo «%s» no es válido", strings.Join(invalid, " "))
	}
	for _, e := range valid { // errores de digitación frecuentes en el dominio
		if at := strings.LastIndex(e, "@"); at > 0 {
			if fix, ok := typoDominios[e[at+1:]]; ok {
				add(IssueWarning, CampoCorreo, "«%s»: ¿quiso decir @%s?", e, fix)
			}
		}
	}
	if len(valid) > 0 && len(invalid) > 0 {
		add(IssueWarning, CampoCorreo, "se ignora el correo no válido «%s»", strings.Join(invalid, " "))
	}

	waOK, emOK := r.Movil != "", len(r.Correos) > 0
	switch {
	case !waOK && !emOK:
		if strings.TrimSpace(raw.Movil) != "" {
			add(IssueError, CampoMovil, "%s", movilProblem)
		}
		if strings.TrimSpace(raw.Correo) != "" {
			add(IssueError, CampoCorreo, "%s", correoProblem)
		}
		add(IssueError, CampoCanales, "no tiene ningún canal de envío válido (necesita un móvil o un correo)")
	case opt.RequireBothChannels && !(waOK && emOK):
		if !waOK {
			add(IssueError, CampoMovil, "%s (la campaña exige móvil y correo)", movilProblem)
		} else {
			add(IssueError, CampoCorreo, "%s (la campaña exige móvil y correo)", correoProblem)
		}
	case !waOK:
		add(IssueWarning, CampoMovil, "%s: solo recibirá correo", movilProblem)
	case !emOK:
		add(IssueWarning, CampoCorreo, "%s: solo recibirá WhatsApp", correoProblem)
	}
}

// ValidateBatch valida todas las filas y marca los duplicados (mismo jugador y año de nacimiento):
// la primera aparición queda como está y las siguientes pasan a error para no enviar dos veces.
func ValidateBatch(rows []RawRow, cfg MasterConfig, opt ValidationOptions) []Recipient {
	out := make([]Recipient, len(rows))
	first := map[string]int{} // clave → fila de la primera aparición
	for i, raw := range rows {
		r := ValidateRow(raw, cfg, opt)
		if r.Jugador != "" && r.Anio != 0 && r.Estado != RecipientOmitted {
			key := NormalizeText(r.Jugador) + "|" + strconv.Itoa(r.Anio)
			if prev, dup := first[key]; dup {
				r.Issues = append(r.Issues, Issue{Nivel: IssueError, Campo: CampoDuplicado,
					Mensaje: fmt.Sprintf("«%s» (%d) ya aparece en la fila %d", r.Jugador, r.Anio, prev)})
				r.Estado = RecipientInvalid
			} else {
				first[key] = r.Fila
			}
		}
		out[i] = r
	}
	return out
}
