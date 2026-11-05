package domain

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// CampaignStatus es el estado de una campaña (SDD §3.5).
type CampaignStatus string

const (
	CampaignDraft       CampaignStatus = "DRAFT" // recién importada: se revisa y se corrige
	CampaignReady       CampaignStatus = "READY"
	CampaignDocsRunning CampaignStatus = "DOCS_RUNNING"
	CampaignDocsDone    CampaignStatus = "DOCS_DONE"
	CampaignDispatching CampaignStatus = "DISPATCHING"
	CampaignCompleted   CampaignStatus = "COMPLETED"
	CampaignPaused      CampaignStatus = "PAUSED"
	CampaignCancelled   CampaignStatus = "CANCELLED"
)

// Channel ya está definido en delivery.go (whatsapp | email).

// Campaign agrupa una importación del Excel con sus parámetros. La configuración maestra
// queda congelada (snapshot) al crearla: cambiar la hoja de Google no altera una campaña
// en curso (los recordatorios usan los valores de la propuesta impresa).
type Campaign struct {
	ID          string         `json:"id"`
	Torneo      string         `json:"torneo"`      // torneo donde se becó a los jugadores (va en el encabezado)
	FechasPago  []string       `json:"fechas_pago"` // ISO yyyy-mm-dd; la beca de N pagos usa las N primeras
	Canales     []Channel      `json:"canales"`
	Exigir2     bool           `json:"requerir_ambos_canales"`
	Status      CampaignStatus `json:"status"`
	Config      MasterConfig   `json:"-"` // snapshot inmutable (se guarda aparte)
	ConfigHash  string         `json:"config_version"`
	ExcelNombre string         `json:"excel_nombre"`
	Dir         string         `json:"-"` // carpeta en disco (artefactos y auditoría)
	CreatedBy   string         `json:"creado_por"`
	CreatedAt   time.Time      `json:"creada_en"`
}

// Editable indica si todavía se pueden corregir los destinatarios (SDD §4.3).
func (c Campaign) Editable() bool {
	return c.Status == CampaignDraft || c.Status == CampaignReady || c.Status == CampaignDocsDone
}

// ValidationOptions deriva las opciones de validación de la campaña.
func (c Campaign) ValidationOptions() ValidationOptions {
	return ValidationOptions{RequireBothChannels: c.Exigir2, FechasPago: len(c.FechasPago)}
}

var reSlug = regexp.MustCompile(`[^\p{L}\p{N}]+`)

// DirName es el nombre de la carpeta de la campaña: {fecha}_{torneo}_{id}.
func (c Campaign) DirName() string {
	slug := strings.Trim(reSlug.ReplaceAllString(c.Torneo, "-"), "-")
	if r := []rune(slug); len(r) > 60 {
		slug = strings.Trim(string(r[:60]), "-")
	}
	if slug == "" {
		slug = "torneo"
	}
	return fmt.Sprintf("%s_%s_%s", c.CreatedAt.Format("2006-01-02"), slug, c.ID)
}

// MaxTorneoLen limita el nombre del torneo (va en el encabezado del documento).
const MaxTorneoLen = 120

// NormalizeTorneo valida y limpia el nombre del torneo de origen.
func NormalizeTorneo(s string) (string, error) {
	t := squash(s)
	switch {
	case t == "":
		return "", fmt.Errorf("Escribe el nombre del torneo donde se becó a los jugadores.")
	case len([]rune(t)) > MaxTorneoLen:
		return "", fmt.Errorf("El nombre del torneo es demasiado largo (máximo %d caracteres).", MaxTorneoLen)
	}
	return t, nil
}

// ParseFechasPago interpreta las casillas de fechas (ISO o dd/mm/aaaa) y devuelve ISO.
// Mismas reglas del cliente: se llenan en orden sin huecos, son válidas y no decrecen.
func ParseFechasPago(slots []string) ([]string, error) {
	var out []string
	var times []time.Time
	hayVacia := false
	for i, raw := range slots {
		v := strings.TrimSpace(raw)
		if v == "" {
			hayVacia = true
			continue
		}
		if hayVacia {
			return nil, fmt.Errorf("Fechas de pago: llénalas en orden, sin dejar un pago vacío antes de otro.")
		}
		if i >= MaxPagos {
			return nil, fmt.Errorf("Fechas de pago: el máximo es %d.", MaxPagos)
		}
		d, err := time.Parse("2006-01-02", v)
		if err != nil {
			if d, err = time.Parse("02/01/2006", v); err != nil {
				return nil, fmt.Errorf("Fecha inválida en el pago %d.", i+1)
			}
		}
		out = append(out, d.Format("2006-01-02"))
		times = append(times, d)
	}
	for i := 1; i < len(times); i++ {
		if times[i].Before(times[i-1]) {
			return nil, fmt.Errorf("Las fechas de pago deben estar en orden (1er pago primero).")
		}
	}
	return out, nil
}

// CheckFechas comprueba que hay fechas para las becas que realmente vienen en el Excel
// (mensaje del cliente). Solo se miran las filas válidas.
func CheckFechas(cfg MasterConfig, recs []Recipient, nFechas int) error {
	needed := map[BecaKey]int{}
	for _, r := range recs {
		if r.Estado != RecipientValid {
			continue
		}
		if b, ok := cfg.Beca(r.Beca); ok && len(b.Pagos) > needed[r.Beca] {
			needed[r.Beca] = len(b.Pagos)
		}
	}
	for _, b := range cfg.Becas { // orden de la hoja
		n, ok := needed[b.Clave]
		if !ok || n <= nFechas {
			continue
		}
		cuales := "la fecha del 1er pago"
		if n > 1 {
			cuales = fmt.Sprintf("las fechas de los %d primeros pagos", n)
		}
		return fmt.Errorf("Faltan fechas de pago: en tu Excel hay jugadores con «%s», que necesita %s.", BecaLabel(b.Clave), cuales)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Ediciones
// ---------------------------------------------------------------------------

// RecipientPatch son las correcciones que el operador puede hacer sobre las celdas de una fila.
// Un campo nil no se toca; "" borra el contenido.
type RecipientPatch struct {
	Jugador   *string `json:"jugador"`
	Club      *string `json:"club"`
	Beca      *string `json:"beca"`
	Anio      *string `json:"anio"`
	Acudiente *string `json:"acudiente"`
	Movil     *string `json:"movil"`
	Correo    *string `json:"correo"`
}

// FieldChange describe un cambio de celda (para la auditoría).
type FieldChange struct {
	Field string `json:"field"`
	Old   string `json:"old"`
	New   string `json:"new"`
}

// IsEmpty indica si el parche no trae ningún campo.
func (p RecipientPatch) IsEmpty() bool {
	return p.Jugador == nil && p.Club == nil && p.Beca == nil && p.Anio == nil &&
		p.Acudiente == nil && p.Movil == nil && p.Correo == nil
}

// maxCellLen limita lo que se acepta en una celda editada.
const maxCellLen = 300

// Apply devuelve la fila con el parche aplicado y la lista de cambios reales.
func (raw RawRow) Apply(p RecipientPatch) (RawRow, []FieldChange, error) {
	out := raw
	var changes []FieldChange
	set := func(field string, dst *string, v *string) error {
		if v == nil {
			return nil
		}
		nv := strings.TrimSpace(*v)
		if len([]rune(nv)) > maxCellLen {
			return fmt.Errorf("El valor de «%s» es demasiado largo (máximo %d caracteres).", field, maxCellLen)
		}
		if nv != *dst {
			changes = append(changes, FieldChange{Field: field, Old: *dst, New: nv})
			*dst = nv
		}
		return nil
	}
	for _, f := range []struct {
		name string
		dst  *string
		v    *string
	}{
		{"jugador", &out.Jugador, p.Jugador}, {"club", &out.Club, p.Club}, {"beca", &out.Beca, p.Beca},
		{"anio", &out.Anio, p.Anio}, {"acudiente", &out.Acudiente, p.Acudiente},
		{"movil", &out.Movil, p.Movil}, {"correo", &out.Correo, p.Correo},
	} {
		if err := set(f.name, f.dst, f.v); err != nil {
			return raw, nil, err
		}
	}
	return out, changes, nil
}

// CampaignView es la campaña con su resumen de validación (para listados y respuestas).
type CampaignView struct {
	Campaign
	Resumen BatchSummary `json:"resumen"`
}

// AuditEvent es una línea de auditoría append-only (events.jsonl de la campaña).
type AuditEvent struct {
	Time        time.Time `json:"ts"`
	Type        string    `json:"type"` // campaign.created | recipient.edited
	CampaignID  string    `json:"campaign_id"`
	RecipientID string    `json:"recipient_id,omitempty"`
	Fila        int       `json:"fila,omitempty"`
	Field       string    `json:"field,omitempty"`
	Old         string    `json:"old,omitempty"`
	New         string    `json:"new,omitempty"`
	By          string    `json:"by"`
	Detail      string    `json:"detail,omitempty"`
}
