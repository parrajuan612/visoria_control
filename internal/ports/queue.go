package ports

import (
	"context"
	"errors"
	"time"
)

// Colas (SDD §3.7).
const (
	QueueDocs      = "docs"      // generación de PDFs (concurrencia 1: protege la RAM)
	QueueWhatsApp  = "whatsapp"  // wa:send
	QueueEmail     = "email"     // email:send
	QueueSide      = "side"      // chatwoot:note, meta:forward, ops:*
	QueueScheduled = "scheduled" // reminder:fire (programadas con ProcessAt)
)

// Tipos de tarea ya definidos.
const (
	TaskOpsPing   = "ops:ping"   // verificación de punta a punta de la cola
	TaskDocRender = "doc:render" // genera las propuestas (docx -> PDF) de un lote de destinatarios
	TaskWASend    = "wa:send"    // envía una propuesta por WhatsApp
	TaskEmailSend = "email:send" // envía una propuesta por correo
	TaskNote      = "chatwoot:note"
)

var (
	// ErrDuplicateTask: ya existe una tarea con ese TaskID (idempotencia).
	ErrDuplicateTask = errors.New("tarea duplicada")
	// ErrSkipRetry: un handler la devuelve (envuelta) para NO reintentar.
	ErrSkipRetry = errors.New("error permanente: no reintentar")
)

// Task es una unidad de trabajo durable.
type Task struct {
	Type      string
	Payload   []byte
	Queue     string
	TaskID    string    // único: normalmente el deliveryKey
	ProcessAt time.Time // cero = ahora
	MaxRetry  int       // 0 = valor por defecto de la implementación
}

// TaskQueue encola tareas.
type TaskQueue interface {
	Enqueue(ctx context.Context, t Task) error
}

// TaskHandler procesa una tarea. Devolver un error la reintenta con backoff,
// salvo que envuelva ErrSkipRetry.
type TaskHandler func(ctx context.Context, payload []byte) error

// Attempt describe el intento actual de una tarea (para decidir si es el último).
type Attempt struct{ Retried, MaxRetry int }

// IsLast indica que si esta ejecución falla, la tarea no se volverá a intentar.
func (a Attempt) IsLast() bool { return a.MaxRetry > 0 && a.Retried >= a.MaxRetry }

type attemptKey struct{}

// WithAttempt guarda el intento en el contexto (lo hace el adaptador de la cola).
func WithAttempt(ctx context.Context, a Attempt) context.Context {
	return context.WithValue(ctx, attemptKey{}, a)
}

// AttemptFrom devuelve el intento del contexto (cero si no hay).
func AttemptFrom(ctx context.Context) Attempt {
	a, _ := ctx.Value(attemptKey{}).(Attempt)
	return a
}
