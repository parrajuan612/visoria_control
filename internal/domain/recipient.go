package domain

import "time"

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
	Raw       RawRow         `json:"raw"`     // celdas tal como están (originales o ya corregidas)
	// Documento es la propuesta generada (nil = aún no se generó o quedó obsoleta por una edición).
	Documento *Document `json:"documento,omitempty"`
}

// Document es el PDF de la propuesta de un destinatario.
type Document struct {
	Path        string    `json:"path,omitempty"`   // relativo a la carpeta de la campaña: pdf/{id}.pdf
	SHA256      string    `json:"sha256,omitempty"` // huella del PDF
	GeneratedAt time.Time `json:"generado_en"`
	// Error es el motivo si no se pudo generar (mensaje del cliente: «Quedaron campos XXX…», etc.).
	Error string `json:"error,omitempty"`
	// MediaID es el identificador del PDF subido a Meta (WhatsApp) y cuándo vence (30 días): se reutiliza
	// entre reintentos y se vuelve a subir si venció.
	MediaID        string    `json:"media_id,omitempty"`
	MediaExpiresAt time.Time `json:"media_expires_at,omitempty"`
}

// MediaValid indica si el medio subido a Meta sigue vigente (con un margen de 1 día).
func (d Document) MediaValid(now time.Time) bool {
	return d.MediaID != "" && now.Add(24*time.Hour).Before(d.MediaExpiresAt)
}

// HasDocument indica que el PDF existe y se puede enviar.
func (r Recipient) HasDocument() bool {
	return r.Documento != nil && r.Documento.Error == "" && r.Documento.Path != ""
}

// CanDispatch: solo una fila VÁLIDA con su PDF generado puede recibir entregas (SDD §4.4: un
// destinatario sin PDF no puede tener entregas encoladas).
func (r Recipient) CanDispatch() bool { return r.Estado == RecipientValid && r.HasDocument() }

// PendingDocuments son las filas válidas que todavía no tienen un intento de documento.
func PendingDocuments(recs []Recipient) []Recipient {
	var out []Recipient
	for _, r := range recs {
		if r.Estado == RecipientValid && r.Documento == nil {
			out = append(out, r)
		}
	}
	return out
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
