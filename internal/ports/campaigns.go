package ports

import (
	"context"
	"errors"

	"visoria-control/internal/domain"
)

var (
	ErrCampaignNotFound  = errors.New("campaña no encontrada")
	ErrRecipientNotFound = errors.New("destinatario no encontrado")
	// ErrCampaignLocked: la campaña ya no admite correcciones (se está enviando o terminó).
	ErrCampaignLocked = errors.New("la campaña ya no se puede editar")
	// ErrGenerationRunning: ya se están generando las propuestas de la campaña.
	ErrGenerationRunning = errors.New("Ya se están generando las propuestas de esta campaña.")
	// ErrDispatchRunning: la campaña ya se despachó o está en curso.
	ErrDispatchRunning = errors.New("Esta campaña ya se despachó. Para reenviar lo que falló use «Reenviar fallidos».")
	// ErrCampaignPaused: hay que reanudar antes de continuar.
	ErrCampaignPaused = errors.New("La campaña está en pausa. Corrija el problema y reanúdela.")
)

// InputError es un dato de entrada inválido; su mensaje es para mostrarlo al usuario tal cual.
type InputError struct{ Message string }

func (e *InputError) Error() string { return e.Message }

// CampaignRepo guarda las campañas y sus destinatarios (Redis; SDD §3.6.1).
type CampaignRepo interface {
	// Create guarda la campaña, su snapshot de configuración y los destinatarios de forma atómica.
	Create(ctx context.Context, c domain.Campaign, recs []domain.Recipient, summary domain.BatchSummary) error
	Get(ctx context.Context, id string) (domain.Campaign, error)
	// List devuelve las campañas más recientes primero.
	List(ctx context.Context, limit int) ([]domain.CampaignView, error)
	// Recipients devuelve los destinatarios ordenados por fila del Excel.
	Recipients(ctx context.Context, id string) ([]domain.Recipient, error)
	// SaveRecipients reemplaza solo los destinatarios indicados y actualiza el resumen.
	SaveRecipients(ctx context.Context, id string, changed []domain.Recipient, summary domain.BatchSummary) error
	// SetStatus cambia el estado solo si el actual está en "from" (compara y cambia de forma atómica).
	// Devuelve false si no se cambió.
	SetStatus(ctx context.Context, id string, from []domain.CampaignStatus, to domain.CampaignStatus) (bool, error)
}

// CampaignFiles escribe los artefactos y la auditoría de una campaña en disco (SDD §3.6.2).
type CampaignFiles interface {
	SaveOriginal(ctx context.Context, c domain.Campaign, data []byte) error  // EXCEL_ORIGINAL.xlsx
	SaveSnapshot(ctx context.Context, c domain.Campaign) error               // campaign.json (con la configuración)
	Audit(ctx context.Context, c domain.Campaign, e domain.AuditEvent) error // events.jsonl, solo agrega
	// SavePDF guarda el PDF de un destinatario en pdf/{id}.pdf (copia desde src) y devuelve su ruta
	// relativa y su SHA-256. Rechaza archivos vacíos o que no sean PDF.
	SavePDF(ctx context.Context, c domain.Campaign, recipientID, src string) (rel, sha256 string, err error)
	// ReadPDF lee el PDF guardado (ruta relativa a la carpeta de la campaña).
	ReadPDF(ctx context.Context, c domain.Campaign, rel string) ([]byte, error)
}

// CreateCampaignInput son los datos para crear una campaña desde el Excel.
type CreateCampaignInput struct {
	Data        []byte // contenido del .xlsx
	Filename    string
	Torneo      string
	FechasPago  []string // casillas de pago 1..3 (ISO o dd/mm/aaaa; las vacías van al final)
	RequireBoth bool     // exigir móvil y correo
	By          string
}

// CampaignDetail es una campaña con todos sus destinatarios.
type CampaignDetail struct {
	Campaign   domain.CampaignView `json:"campaign"`
	Recipients []domain.Recipient  `json:"recipients"`
}

// EditResult es el resultado de corregir un destinatario.
type EditResult struct {
	Recipient domain.Recipient `json:"recipient"`
	// Otros son las demás filas cuyo estado cambió por la corrección (p. ej. un duplicado que se resolvió).
	Otros   []domain.Recipient  `json:"otros_cambios"`
	Resumen domain.BatchSummary `json:"resumen"`
}

// CampaignUseCase: crear, consultar y corregir campañas.
type CampaignUseCase interface {
	Create(ctx context.Context, in CreateCampaignInput) (CampaignDetail, error)
	Get(ctx context.Context, id string) (CampaignDetail, error)
	List(ctx context.Context) ([]domain.CampaignView, error)
	EditRecipient(ctx context.Context, campaignID, recipientID string, patch domain.RecipientPatch, by string) (EditResult, error)
}
