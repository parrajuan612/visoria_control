package ports

import (
	"context"
	"time"
)

// ChatwootClient deja constancia en Chatwoot de lo enviado al acudiente.
// La implementación publica una NOTA PRIVADA (nunca un mensaje al acudiente).
type ChatwootClient interface {
	RegisterNote(ctx context.Context, phone, contactName, content string) error
}

// EmailAttachment es un archivo adjunto.
type EmailAttachment struct {
	Data        []byte // contenido
	Filename    string // nombre visible, p. ej. "JUGADOR - BECA 50.pdf"
	ContentType string // por defecto application/pdf
}

// EmailMessage es un correo listo para enviar (ADR-9).
type EmailMessage struct {
	To          []string
	Subject     string
	TextBody    string
	HTMLBody    string
	Attachments []EmailAttachment
	// MessageID determinista (derivado del deliveryKey) para correlacionar rebotes.
	MessageID string
	// Headers extra, p. ej. X-Visoria-Delivery.
	Headers map[string]string
}

// EmailSender envía correos. SMTP respondió OK significa "aceptado", no "entregado".
// La implementación debe clasificar los errores (permanente / transitorio / cuota).
type EmailSender interface {
	// Send devuelve el Message-ID efectivo del mensaje aceptado. Los errores del servidor son *ProviderError.
	Send(ctx context.Context, msg EmailMessage) (messageID string, err error)
	// Verify comprueba la conexión y las credenciales sin enviar nada.
	Verify(ctx context.Context) error
}

// Bounce es un rebote (DSN) detectado en el buzón.
type Bounce struct {
	OriginalMessageID string    // Message-ID del correo que rebotó
	StatusCode        string    // p. ej. "5.1.1"
	Reason            string    // texto legible del motivo
	ReceivedAt        time.Time // cuándo llegó el DSN
}

// BounceSource consulta los rebotes (IMAP en la Fase 6).
type BounceSource interface {
	// FetchBounces devuelve los rebotes recibidos desde "since" y no procesados antes.
	FetchBounces(ctx context.Context, since time.Time) ([]Bounce, error)
	// MarkProcessed mueve/etiqueta los DSN ya procesados para no repetirlos.
	MarkProcessed(ctx context.Context, bounces []Bounce) error
}
