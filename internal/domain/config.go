package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Años de nacimiento plausibles para jugadores de categorías base.
const (
	MinBirthYear = 1990
	MaxBirthYear = 2035
)

// BecaKey identifica una beca: "sin_beca", "30", "50", "70", "100", "acompanante".
type BecaKey string

const (
	BecaSinBeca     BecaKey = "sin_beca"
	BecaAcompanante BecaKey = "acompanante"
)

// BecasRequeridas son las claves que la hoja debe tener siempre.
var BecasRequeridas = []BecaKey{BecaSinBeca, "30", "50", "70", "100"}

// MaxPagos es el máximo de cuotas de un plan de pagos.
const MaxPagos = 3

// Beca es un precio y su plan de pagos.
type Beca struct {
	Clave  BecaKey `json:"clave"`
	Nombre string  `json:"nombre"` // como aparece en la hoja ("30%", "SIN BECA")
	Total  Money   `json:"total"`
	Pagos  []Money `json:"pagos"` // 1 a 3 cuotas, en orden
	Fila   int     `json:"fila"`  // fila de origen (para mensajes)
}

// YearRange es un rango de años de nacimiento, ambos extremos incluidos.
type YearRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

func (r YearRange) Contains(y int) bool { return y >= r.Min && y <= r.Max }

func (r YearRange) String() string {
	if r.Min == r.Max {
		return strconv.Itoa(r.Min)
	}
	return fmt.Sprintf("%d-%d", r.Min, r.Max)
}

// ParseYearRange interpreta "2008-2010", "2008 – 2010" o "2011".
func ParseYearRange(s string) (YearRange, error) {
	t := strings.NewReplacer("–", "-", "—", "-", " ", "", " ", "").Replace(strings.TrimSpace(s))
	bad := fmt.Errorf("el rango de años %q no es válido (use 2008-2010 o 2011)", strings.TrimSpace(s))
	parts := strings.Split(t, "-")
	if len(parts) < 1 || len(parts) > 2 {
		return YearRange{}, bad
	}
	nums := make([]int, len(parts))
	for i, p := range parts {
		if len(p) != 4 {
			return YearRange{}, bad
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return YearRange{}, bad
		}
		nums[i] = n
	}
	r := YearRange{Min: nums[0], Max: nums[len(nums)-1]}
	if r.Min > r.Max {
		return YearRange{}, fmt.Errorf("el rango de años %q está invertido (el primer año es mayor que el último)", strings.TrimSpace(s))
	}
	return r, nil
}

// TorneoRango asocia un rango de años de nacimiento con un torneo.
// Los rangos PUEDEN solaparse: un jugador participa en todos los torneos que le correspondan.
type TorneoRango struct {
	Anios  YearRange `json:"anios"`
	Nombre string    `json:"nombre"`
	Fila   int       `json:"fila"`
}

// CategoriaRango asocia un rango de años con una categoría y su plantilla Word.
type CategoriaRango struct {
	Anios     YearRange `json:"anios"`
	Nombre    string    `json:"nombre"`
	Plantilla string    `json:"plantilla"` // cadete_juvenil | infantil | benjamin_alevin ("" si no se conoce)
	Fila      int       `json:"fila"`
}

// MasterConfig es la configuración maestra (Google Sheets), inmutable por campaña.
type MasterConfig struct {
	Becas      []Beca           `json:"becas"`
	Torneos    []TorneoRango    `json:"torneos"`
	Categorias []CategoriaRango `json:"categorias"`
}

// Hash es una huella estable del contenido (sin números de fila): detecta cambios reales.
func (c MasterConfig) Hash() string {
	type beca struct {
		C BecaKey
		T Money
		P []Money
	}
	type rango struct {
		A YearRange
		N string
	}
	canon := struct {
		B []beca
		T []rango
		C []rango
	}{}
	for _, b := range c.Becas {
		canon.B = append(canon.B, beca{b.Clave, b.Total, b.Pagos})
	}
	for _, t := range c.Torneos {
		canon.T = append(canon.T, rango{t.Anios, t.Nombre})
	}
	for _, k := range c.Categorias {
		canon.C = append(canon.C, rango{k.Anios, k.Nombre})
	}
	raw, _ := json.Marshal(canon)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

// Beca devuelve la beca por clave.
func (c MasterConfig) Beca(k BecaKey) (Beca, bool) {
	for _, b := range c.Becas {
		if b.Clave == k {
			return b, true
		}
	}
	return Beca{}, false
}

// TorneosParaAnio devuelve los nombres de todos los torneos que incluyen ese año.
func (c MasterConfig) TorneosParaAnio(y int) []string {
	var out []string
	for _, t := range c.Torneos {
		if t.Anios.Contains(y) {
			out = append(out, t.Nombre)
		}
	}
	return out
}

// CategoriasParaAnio devuelve las categorías que incluyen ese año.
func (c MasterConfig) CategoriasParaAnio(y int) []CategoriaRango {
	var out []CategoriaRango
	for _, k := range c.Categorias {
		if k.Anios.Contains(y) {
			out = append(out, k)
		}
	}
	return out
}

// NormalizeText pasa a mayúsculas sin tildes ni espacios repetidos (para comparar).
func NormalizeText(s string) string {
	rep := strings.NewReplacer("Á", "A", "É", "E", "Í", "I", "Ó", "O", "Ú", "U", "Ü", "U", "Ñ", "N")
	s = rep.Replace(strings.ToUpper(strings.TrimSpace(s)))
	return strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " ")
}

// NormalizeHeader normaliza el encabezado de una columna para compararlo con alias:
// mayúsculas, sin tildes, sin signos ("PAGO # 1" → "PAGO 1", "AÑOS" → "ANOS").
func NormalizeHeader(s string) string {
	s = strings.TrimPrefix(s, "\uFEFF")
	s = strings.NewReplacer("#", " ", ".", " ", ":", " ", "º", "", "°", "", "_", " ", "-", " ").Replace(s)
	return NormalizeText(s)
}

// NormalizeBecaKey traduce el nombre de la hoja a una clave: "SIN BECA"→sin_beca,
// "30%"→"30", "ACOMPAÑANTE"→acompanante.
func NormalizeBecaKey(nombre string) (BecaKey, bool) {
	n := NormalizeText(nombre)
	switch n {
	case "SIN BECA", "SINBECA", "SIN_BECA":
		return BecaSinBeca, true
	case "ACOMPANANTE", "ACOMPANANTES":
		return BecaAcompanante, true
	}
	d := strings.TrimSpace(strings.TrimSuffix(n, "%"))
	if v, err := strconv.Atoi(d); err == nil && v >= 1 && v <= 100 {
		return BecaKey(strconv.Itoa(v)), true
	}
	return "", false
}

// PlantillaParaCategoria devuelve la plantilla Word de una categoría ("" si no se conoce).
func PlantillaParaCategoria(nombre string) string {
	switch NormalizeText(nombre) {
	case "JUVENIL", "CADETE":
		return "cadete_juvenil"
	case "INFANTIL":
		return "infantil"
	case "ALEVIN", "BENJAMIN":
		return "benjamin_alevin"
	}
	return ""
}

// ---------------------------------------------------------------------------
// Validación
// ---------------------------------------------------------------------------

// Validate revisa la configuración y devuelve los hallazgos (errores, advertencias, info).
// Una configuración con algún error NO debe reemplazar a la vigente.
func (c MasterConfig) Validate() []Issue {
	var out []Issue
	add := func(n IssueLevel, campo, msg string, a ...any) {
		out = append(out, Issue{Nivel: n, Campo: campo, Mensaje: fmt.Sprintf(msg, a...)})
	}
	where := func(hoja string, fila int, nombre string) string {
		switch {
		case fila > 0 && nombre != "":
			return fmt.Sprintf("%s · fila %d (%s)", hoja, fila, nombre)
		case fila > 0:
			return fmt.Sprintf("%s · fila %d", hoja, fila)
		}
		return hoja
	}

	// --- Becas ---
	if len(c.Becas) == 0 {
		add(IssueError, "Becas", "no hay ninguna beca definida")
	}
	seen := map[BecaKey]int{}
	for _, b := range c.Becas {
		w := where("Becas", b.Fila, b.Nombre)
		if prev, dup := seen[b.Clave]; dup {
			add(IssueError, w, "la beca %q está repetida (ya aparece en la fila %d)", b.Nombre, prev)
		}
		seen[b.Clave] = b.Fila
		if b.Total <= 0 {
			add(IssueError, w, "el precio total debe ser mayor que cero")
		}
		if len(b.Pagos) == 0 {
			add(IssueError, w, "no tiene ningún pago definido")
		}
		if len(b.Pagos) > MaxPagos {
			add(IssueError, w, "tiene %d pagos y el máximo es %d", len(b.Pagos), MaxPagos)
		}
		var sum Money
		for i, p := range b.Pagos {
			if p <= 0 {
				add(IssueError, w, "el pago #%d debe ser mayor que cero", i+1)
			}
			sum += p
		}
		if len(b.Pagos) > 0 && b.Total > 0 && sum != b.Total {
			add(IssueError, w, "los pagos suman %s pero el precio total es %s (diferencia %s)",
				FormatEUR(sum), FormatEUR(b.Total), FormatEUR(abs(sum-b.Total)))
		}
	}
	for _, req := range BecasRequeridas {
		if _, ok := seen[req]; !ok {
			add(IssueError, "Becas", "falta la beca %q, que es obligatoria", BecaLabel(req))
		}
	}

	// --- Torneos ---
	if len(c.Torneos) == 0 {
		add(IssueError, "Torneos", "no hay ningún torneo definido")
	}
	for _, t := range c.Torneos {
		w := where("Torneos", t.Fila, t.Anios.String())
		if t.Nombre == "" {
			add(IssueError, w, "el torneo no tiene nombre")
		}
		if t.Anios.Min < MinBirthYear || t.Anios.Max > MaxBirthYear {
			add(IssueError, w, "los años %s están fuera del rango plausible (%d-%d)", t.Anios, MinBirthYear, MaxBirthYear)
		}
	}
	solapes := map[int][]string{}
	for i, a := range c.Torneos {
		for _, b := range c.Torneos[i+1:] {
			for y := max(a.Anios.Min, b.Anios.Min); y <= min(a.Anios.Max, b.Anios.Max); y++ {
				solapes[y] = appendUnique(solapes[y], a.Nombre, b.Nombre)
			}
		}
	}
	if len(solapes) > 0 {
		anios := sortedKeys(solapes)
		add(IssueInfo, "Torneos", "los años %s están en más de un torneo: a esos jugadores se les listarán todos (esto es normal si es intencional)", compactYears(anios))
	}

	// --- Categorías ---
	if len(c.Categorias) == 0 {
		add(IssueError, "Categorías", "no hay ninguna categoría definida")
	}
	for _, k := range c.Categorias {
		w := where("Categorías", k.Fila, k.Nombre)
		if k.Nombre == "" {
			add(IssueError, w, "la categoría no tiene nombre")
		}
		if k.Anios.Min < MinBirthYear || k.Anios.Max > MaxBirthYear {
			add(IssueError, w, "los años %s están fuera del rango plausible (%d-%d)", k.Anios, MinBirthYear, MaxBirthYear)
		}
		if k.Nombre != "" && k.Plantilla == "" {
			add(IssueWarning, w, "la categoría %q no tiene una plantilla Word asociada; no se podrán generar propuestas para esos años", k.Nombre)
		}
	}
	// Un año en dos categorías haría ambigua la plantilla (regla del cliente).
	for i, a := range c.Categorias {
		for _, b := range c.Categorias[i+1:] {
			lo, hi := max(a.Anios.Min, b.Anios.Min), min(a.Anios.Max, b.Anios.Max)
			if lo <= hi {
				add(IssueError, "Categorías", "los años %s están en dos categorías (%s y %s); cada año debe tener una sola",
					YearRange{lo, hi}, a.Nombre, b.Nombre)
			}
		}
	}

	// --- Cobertura cruzada ---
	catYears, torYears := yearSet(c.Categorias), yearSetT(c.Torneos)
	if sin := missing(catYears, torYears); len(sin) > 0 {
		add(IssueWarning, "Torneos", "los años %s tienen categoría pero ningún torneo", compactYears(sin))
	}
	if sin := missing(torYears, catYears); len(sin) > 0 {
		add(IssueWarning, "Categorías", "los años %s tienen torneo pero ninguna categoría", compactYears(sin))
	}
	return out
}

// BecaLabel es el texto de una beca como se muestra: "SIN BECA", "ACOMPAÑANTE" o "30%".
func BecaLabel(k BecaKey) string {
	switch k {
	case BecaSinBeca:
		return "SIN BECA"
	case BecaAcompanante:
		return "ACOMPAÑANTE"
	}
	return string(k) + "%"
}

func abs(m Money) Money {
	if m < 0 {
		return -m
	}
	return m
}

func appendUnique(l []string, vals ...string) []string {
	for _, v := range vals {
		found := false
		for _, x := range l {
			if x == v {
				found = true
				break
			}
		}
		if !found {
			l = append(l, v)
		}
	}
	return l
}

func sortedKeys(m map[int][]string) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func yearSet(cs []CategoriaRango) map[int]bool {
	s := map[int]bool{}
	for _, c := range cs {
		for y := c.Anios.Min; y <= c.Anios.Max && y-c.Anios.Min < 200; y++ {
			s[y] = true
		}
	}
	return s
}

func yearSetT(ts []TorneoRango) map[int]bool {
	s := map[int]bool{}
	for _, t := range ts {
		for y := t.Anios.Min; y <= t.Anios.Max && y-t.Anios.Min < 200; y++ {
			s[y] = true
		}
	}
	return s
}

// missing devuelve los años de a que no están en b, ordenados.
func missing(a, b map[int]bool) []int {
	var out []int
	for y := range a {
		if !b[y] {
			out = append(out, y)
		}
	}
	sort.Ints(out)
	return out
}

// compactYears: [2008 2009 2010 2013] → "2008-2010, 2013".
func compactYears(ys []int) string {
	if len(ys) == 0 {
		return ""
	}
	var parts []string
	start, prev := ys[0], ys[0]
	flush := func() {
		parts = append(parts, YearRange{start, prev}.String())
	}
	for _, y := range ys[1:] {
		if y == prev+1 {
			prev = y
			continue
		}
		flush()
		start, prev = y, y
	}
	flush()
	return strings.Join(parts, ", ")
}

// ConfigSnapshot es la configuración válida vigente con su metadato de sincronización.
type ConfigSnapshot struct {
	Config   MasterConfig `json:"config"`
	Hash     string       `json:"hash"`
	SyncedAt time.Time    `json:"synced_at"`
	// Issues son los hallazgos no bloqueantes (advertencias e info) de esa sincronización.
	Issues []Issue `json:"issues"`
}
