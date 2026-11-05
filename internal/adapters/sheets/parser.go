// Package sheets lee la configuración maestra publicada como CSV desde Google
// Sheets. Los encabezados se detectan por nombre (no por posición), así que el
// cliente puede reordenar o agregar columnas sin romper la lectura.
package sheets

import (
	"encoding/csv"
	"fmt"
	"io"
	"strings"
	"unicode"

	"visoria-control/internal/domain"
)

// maxHeaderScan: filas iniciales donde se busca el encabezado.
const maxHeaderScan = 10

// Alias de encabezados (ya normalizados: mayúsculas, sin tildes ni '#', espacios simples).
var (
	aliasBeca      = []string{"BECA", "BECAS"}
	aliasTotal     = []string{"V/TOTAL", "TOTAL", "V TOTAL", "VALOR TOTAL", "VALOR", "PRECIO"}
	aliasPago      = [domain.MaxPagos][]string{{"PAGO 1", "PAGO1", "1ER PAGO", "PRIMER PAGO"}, {"PAGO 2", "PAGO2", "2DO PAGO", "SEGUNDO PAGO"}, {"PAGO 3", "PAGO3", "3ER PAGO", "TERCER PAGO"}}
	aliasAnios     = []string{"ANOS", "ANO", "ANIOS", "ANIO", "NACIMIENTO"}
	aliasTorneo    = []string{"TORNEO", "TORNEOS"}
	aliasCategoria = []string{"CATEGORIA", "CATEGORIAS"}
)

func in(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// clean quita saltos de línea y espacios repetidos de una celda.
func clean(s string) string {
	return strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " ")
}

type columns struct {
	beca, total                int
	pago                       [domain.MaxPagos]int
	torneoAnios, torneoNombre  int
	categoriaAnios, categoriaN int
}

func newColumns() columns {
	c := columns{beca: -1, total: -1, torneoAnios: -1, torneoNombre: -1, categoriaAnios: -1, categoriaN: -1}
	for i := range c.pago {
		c.pago[i] = -1
	}
	return c
}

// findHeader busca la fila de encabezados y mapea cada columna.
func findHeader(rows [][]string) (int, columns, bool) {
	for r := 0; r < len(rows) && r < maxHeaderScan; r++ {
		norm := make([]string, len(rows[r]))
		hasBeca := false
		for i, cell := range rows[r] {
			norm[i] = domain.NormalizeHeader(cell)
			if in(aliasBeca, norm[i]) {
				hasBeca = true
			}
		}
		if !hasBeca {
			continue
		}
		c := newColumns()
		for i, h := range norm {
			switch {
			case in(aliasBeca, h) && c.beca < 0:
				c.beca = i
			case in(aliasTotal, h) && c.total < 0:
				c.total = i
			case in(aliasAnios, h):
				// "AÑOS" va junto a su nombre: a la derecha ("AÑOS | TORNEO") o a la izquierda.
				for _, j := range []int{i + 1, i - 1} {
					if j < 0 || j >= len(norm) {
						continue
					}
					if in(aliasTorneo, norm[j]) && c.torneoAnios < 0 {
						c.torneoAnios, c.torneoNombre = i, j
						break
					}
					if in(aliasCategoria, norm[j]) && c.categoriaAnios < 0 {
						c.categoriaAnios, c.categoriaN = i, j
						break
					}
				}
			}
			for p := range aliasPago {
				if in(aliasPago[p], h) && c.pago[p] < 0 {
					c.pago[p] = i
				}
			}
		}
		return r, c, true
	}
	return 0, columns{}, false
}

func cell(row []string, i int) string {
	if i < 0 || i >= len(row) {
		return ""
	}
	return clean(row[i])
}

// Parse lee el CSV y devuelve la configuración junto con los problemas de lectura
// (formatos de importe o de años inválidos, columnas faltantes). Las reglas de
// negocio se verifican después con MasterConfig.Validate().
func Parse(r io.Reader) (domain.MasterConfig, []domain.Issue) {
	var cfg domain.MasterConfig
	var issues []domain.Issue
	addErr := func(campo, msg string, a ...any) {
		issues = append(issues, domain.Issue{Nivel: domain.IssueError, Campo: campo, Mensaje: fmt.Sprintf(msg, a...)})
	}
	addWarn := func(campo, msg string, a ...any) {
		issues = append(issues, domain.Issue{Nivel: domain.IssueWarning, Campo: campo, Mensaje: fmt.Sprintf(msg, a...)})
	}

	rd := csv.NewReader(r)
	rd.FieldsPerRecord = -1
	rd.LazyQuotes = true
	rows, err := rd.ReadAll()
	if err != nil {
		addErr("Archivo", "no se pudo leer el CSV: %v", err)
		return cfg, issues
	}

	hdr, col, ok := findHeader(rows)
	if !ok {
		addErr("Encabezados", "no se encontró la fila de encabezados (debe tener la columna «BECA» entre las primeras %d filas)", maxHeaderScan)
		return cfg, issues
	}
	// Columnas imprescindibles de cada tabla; si falta alguna, se avisa cuál y cómo debe llamarse.
	need := func(idx int, tabla, nombre string, aliases []string) {
		if idx < 0 {
			addErr("Encabezados · "+tabla, "no se encontró la columna «%s» (se acepta: %s)", nombre, strings.Join(aliases, ", "))
		}
	}
	need(col.total, "Becas", "V/TOTAL", aliasTotal)
	for p := range col.pago {
		need(col.pago[p], "Becas", fmt.Sprintf("PAGO # %d", p+1), aliasPago[p])
	}
	if col.torneoAnios < 0 {
		addErr("Encabezados · Torneos", "no se encontró el par de columnas «AÑOS» + «TORNEO» (una junto a la otra)")
	}
	if col.categoriaAnios < 0 {
		addErr("Encabezados · Categorías", "no se encontró el par de columnas «AÑOS» + «CATEGORIA» (una junto a la otra)")
	}
	if domain.HasErrors(issues) {
		return cfg, issues
	}

	for i := hdr + 1; i < len(rows); i++ {
		row, n := rows[i], i+1 // n = número de fila en la hoja

		// --- Tabla de becas ---
		if nombre := cell(row, col.beca); nombre != "" {
			if b, okb := parseBeca(row, col, nombre, n, addErr, addWarn); okb {
				cfg.Becas = append(cfg.Becas, b)
			}
		} else if cell(row, col.total) != "" || cell(row, col.pago[0]) != "" {
			addWarn(fmt.Sprintf("Becas · fila %d", n), "hay importes sin el nombre de la beca; se ignora la fila")
		}

		// --- Tabla de torneos ---
		anios, nombre := cell(row, col.torneoAnios), cell(row, col.torneoNombre)
		if anios != "" || nombre != "" {
			campo := fmt.Sprintf("Torneos · fila %d", n)
			if rg, err := domain.ParseYearRange(anios); err != nil {
				addErr(campo, "%v", err)
			} else {
				cfg.Torneos = append(cfg.Torneos, domain.TorneoRango{Anios: rg, Nombre: nombre, Fila: n})
			}
		}

		// --- Tabla de categorías ---
		anios, nombre = cell(row, col.categoriaAnios), cell(row, col.categoriaN)
		if anios != "" || nombre != "" {
			campo := fmt.Sprintf("Categorías · fila %d", n)
			if rg, err := domain.ParseYearRange(anios); err != nil {
				addErr(campo, "%v", err)
			} else {
				cfg.Categorias = append(cfg.Categorias, domain.CategoriaRango{
					Anios: rg, Nombre: nombre, Plantilla: domain.PlantillaParaCategoria(nombre), Fila: n})
			}
		}
	}
	return cfg, issues
}

func parseBeca(row []string, col columns, nombre string, n int, addErr, addWarn func(string, string, ...any)) (domain.Beca, bool) {
	campo := fmt.Sprintf("Becas · fila %d (%s)", n, nombre)
	key, ok := domain.NormalizeBecaKey(nombre)
	if !ok {
		addWarn(campo, "beca no reconocida (use SIN BECA, 30%%, 50%%, 70%%, 100%% o ACOMPAÑANTE); se ignora")
		return domain.Beca{}, false
	}
	b := domain.Beca{Clave: key, Nombre: nombre, Fila: n}
	failed := false

	total, present, err := domain.ParseMoneyEUR(cell(row, col.total))
	switch {
	case err != nil:
		addErr(campo, "V/TOTAL: %v", err)
		failed = true
	case !present:
		addErr(campo, "no tiene precio total (V/TOTAL vacío)")
		failed = true
	}
	b.Total = total

	// Los pagos deben ser consecutivos desde el #1: un hueco es un error.
	gap := 0
	for p := range col.pago {
		m, present, err := domain.ParseMoneyEUR(cell(row, col.pago[p]))
		switch {
		case err != nil:
			addErr(campo, "PAGO # %d: %v", p+1, err)
			failed = true
		case !present:
			if gap == 0 {
				gap = p + 1
			}
		case gap != 0:
			addErr(campo, "el PAGO # %d tiene valor pero el PAGO # %d está vacío", p+1, gap)
			failed = true
		default:
			b.Pagos = append(b.Pagos, m)
		}
	}
	return b, !failed
}
