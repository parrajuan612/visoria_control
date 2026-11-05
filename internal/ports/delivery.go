package ports

import (
	"context"
	"fmt"
	"time"

	"visoria-control/internal/domain"
)

// ErrorAction es lo que el sistema debe hacer ante un error de un proveedor (SDD §3.5.2).
type ErrorAction int

const (
	// ActionRetry: error transitorio (429, 5xx, red): reintentar con backoff.
	ActionRetry ErrorAction = iota
	// ActionFail: error permanente de ESTE destinatario (número sin WhatsApp, dirección inexistente).
	ActionFail
	// ActionPause: el problema es de TODA la campaña (token vencido, plantilla rechazada, credenciales
	// de correo): se pausa para no fallar uno por uno.
	ActionPause
	// ActionDefer: cuota diaria agotada (Gmail): se reprograma sin gastar reintentos.
	ActionDefer
	// ActionReview: no se sabe si el mensaje salió (p. ej. 200 sin identificador). NO se reintenta
	// (podría duplicarse): queda en NEEDS_REVIEW para que el operador decida.
	ActionReview
)

// ProviderError es un error clasificado de WhatsApp o del servidor de correo.
type ProviderError struct {
	Action     ErrorAction
	Code       string // código de Meta ("131026") o de SMTP ("5.1.1")
	Message    string // mensaje legible para el operador, en español
	Raw        string // detalle técnico recortado (solo para el log)
	RetryAfter time.Duration
}

func (e *ProviderError) Error() string { return fmt.Sprintf("%s (%s)", e.Message, e.Code) }

// Permanent indica que reintentar no sirve.
func (e *ProviderError) Permanent() bool { return e.Action == ActionFail }

// TemplateMessage es un mensaje de plantilla de WhatsApp con un documento en el encabezado.
type TemplateMessage struct {
	Name        string
	Language    string
	MediaID     string   // PDF ya subido a Meta
	DocFilename string   // nombre visible del PDF
	BodyParams  []string // {{1}}, {{2}}… del cuerpo
}

// WhatsAppClient habla con la Cloud API de Meta. Los errores del servidor son *ProviderError.
type WhatsAppClient interface {
	// UploadMedia sube el PDF y devuelve su media_id (dura 30 días).
	UploadMedia(ctx context.Context, filename string, pdf []byte) (mediaID string, err error)
	// SendTemplate envía la plantilla y devuelve el wamid.
	SendTemplate(ctx context.Context, to string, msg TemplateMessage) (wamid string, err error)
	// CheckToken comprueba que el token y el número sirvan, sin enviar nada.
	CheckToken(ctx context.Context) error
}

// QuotaStore lleva el contador diario de correos (Gmail ≈ 500/día).
type QuotaStore interface {
	// Take reserva un envío del día; false si ya se llegó al límite.
	Take(ctx context.Context, day string, limit int) (bool, error)
}

// DispatchInput son las opciones de un despacho.
type DispatchInput struct {
	Channels []string // "whatsapp", "email" (vacío = ambos)
	By       string
}

// DispatchResult resume lo que se encoló.
type DispatchResult struct {
	Created      int `json:"entregas_creadas"`
	Queued       int `json:"en_cola"`
	Skipped      int `json:"omitidas"`
	NoDocument   int `json:"sin_documento"`
	AlreadyExist int `json:"ya_existian"`
}

// RetryResult resume un reenvío.
type RetryResult struct {
	Requeued  int `json:"reencoladas"`
	Permanent int `json:"fallos_permanentes"` // no se reintentan: el dato está mal
}

// DeliveryView es una entrega lista para mostrarse.
type DeliveryView struct {
	Key         string    `json:"key"`
	RecipientID string    `json:"recipient_id"`
	Fila        int       `json:"fila"`
	Jugador     string    `json:"jugador"`
	Channel     string    `json:"channel"`
	Status      string    `json:"status"`
	Label       string    `json:"label"`
	Error       string    `json:"error,omitempty"`
	Permanent   bool      `json:"permanente,omitempty"`
	Attempts    int       `json:"attempts"`
	At          time.Time `json:"at"`
}

// ChannelProgress son los contadores de un canal.
type ChannelProgress struct {
	Total  int            `json:"total"`
	Counts map[string]int `json:"estados"`
}

// DeliverySnapshot es el estado de los envíos de una campaña.
type DeliverySnapshot struct {
	Status     string                     `json:"status"`
	Progress   map[string]ChannelProgress `json:"progreso"`
	Deliveries []DeliveryView             `json:"entregas"`
	InFlight   int                        `json:"en_curso"`
}

// DispatchUseCase: despacho de las propuestas y seguimiento de las entregas.
type DispatchUseCase interface {
	Dispatch(ctx context.Context, campaignID string, in DispatchInput) (DispatchResult, error)
	// Retry reenvía lo que falló por motivos no permanentes (y lo que quedó en revisión).
	Retry(ctx context.Context, campaignID string) (RetryResult, error)
	// Resume reanuda una campaña pausada (p. ej. tras corregir el token).
	Resume(ctx context.Context, campaignID string) error
	Snapshot(ctx context.Context, campaignID string) (DeliverySnapshot, error)
}

// ProposalMail son los datos de una propuesta por correo.
type ProposalMail struct {
	To          []string
	Acudiente   string
	Jugador     string
	Beca        domain.BecaKey
	Torneo      string
	FileName    string // nombre visible del PDF adjunto
	PDF         []byte
	DeliveryKey string
}

// MailComposer arma el correo de una propuesta (textos y HTML del cliente).
type MailComposer interface {
	Compose(p ProposalMail) EmailMessage
}

// Limiter espera hasta que se pueda enviar el siguiente mensaje.
type Limiter interface {
	Wait(ctx context.Context) error
}
