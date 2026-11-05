package domain

// RecipientState es el estado de validación de una fila del Excel.
type RecipientState string

const (
	RecipientValid   RecipientState = "VALID"   // lista para enviar por al menos un canal
	RecipientInvalid RecipientState = "INVALID" // tiene errores: no se envía hasta corregirla
	RecipientOmitted RecipientState = "OMITTED" // SIN BECA: no se genera propuesta
)

// RawRow es una fila del Excel tal como viene (texto), antes de validar.
type RawRow struct {
	Fila      int    `json:"fila"` // número de fila en la hoja (para los mensajes)
	Jugador   string `json:"jugador"`
	Club      string `json:"club"`
	Beca      string `json:"beca"`
	Anio      string `json:"anio"` // año de nacimiento (solo el año)
	Acudiente string `json:"acudiente"`
	Movil     string `json:"movil"`
	Correo    string `json:"correo"`
}

// Recipient es una fila ya validada y normalizada.
type Recipient struct {
	ID        string         `json:"id"` // estable: no depende del nombre del PDF
	Fila      int            `json:"fila"`
	Jugador   string         `json:"jugador"`
	Club      string         `json:"club"`
	Acudiente string         `json:"acudiente"`
	Beca      BecaKey        `json:"beca"`
	Anio      int            `json:"anio"`
	Plantilla string         `json:"plantilla"`
	Categoria string         `json:"categoria"`
	Torneos   []string       `json:"torneos"`
	Correos   []string       `json:"correos"`
	Movil     string         `json:"movil"` // E.164 sin "+": 57XXXXXXXXXX ("" si no hay)
	Issues    []Issue        `json:"issues"`
	Estado    RecipientState `json:"estado"`
	Editado   bool           `json:"editado"` // true si se corrigió en pantalla (auditado)
}

// HasWhatsApp / HasEmail indican qué canales tiene datos válidos.
func (r Recipient) HasWhatsApp() bool { return r.Movil != "" }
func (r Recipient) HasEmail() bool    { return len(r.Correos) > 0 }

// BatchSummary cuenta el resultado de validar un archivo.
type BatchSummary struct {
	Total        int `json:"total"`
	Validas      int `json:"validas"`
	ConErrores   int `json:"con_errores"`
	Omitidas     int `json:"omitidas"`
	Advertencias int `json:"con_advertencias"` // filas válidas con al menos una advertencia
}

// Summarize cuenta las filas por estado.
func Summarize(rs []Recipient) BatchSummary {
	s := BatchSummary{Total: len(rs)}
	for _, r := range rs {
		switch r.Estado {
		case RecipientValid:
			s.Validas++
			for _, i := range r.Issues {
				if i.Nivel == IssueWarning {
					s.Advertencias++
					break
				}
			}
		case RecipientInvalid:
			s.ConErrores++
		case RecipientOmitted:
			s.Omitidas++
		}
	}
	return s
}
