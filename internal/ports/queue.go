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
