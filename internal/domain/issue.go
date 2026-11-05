package domain

// IssueLevel es la gravedad de un hallazgo de validación.
type IssueLevel string

const (
	IssueError   IssueLevel = "error"       // impide usar el dato
	IssueWarning IssueLevel = "advertencia" // se puede usar, pero conviene revisarlo
	IssueInfo    IssueLevel = "info"        // solo informativo
)

// Issue es un hallazgo de validación (configuración o filas del Excel).
type Issue struct {
	Nivel   IssueLevel `json:"nivel"`
	Campo   string     `json:"campo"` // dónde: "Becas · fila 3 (30%)"
	Mensaje string     `json:"mensaje"`
}

// HasErrors indica si algún hallazgo es de nivel error.
func HasErrors(issues []Issue) bool {
	for _, i := range issues {
		if i.Nivel == IssueError {
			return true
		}
	}
	return false
}

// CountByLevel cuenta los hallazgos por nivel.
func CountByLevel(issues []Issue) map[IssueLevel]int {
	out := map[IssueLevel]int{}
	for _, i := range issues {
		out[i.Nivel]++
	}
	return out
}
