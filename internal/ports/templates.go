package ports

import (
	"context"
	"errors"
	"time"
)

// ErrTemplateNotFound: falta el archivo de una plantilla.
var ErrTemplateNotFound = errors.New("plantilla no encontrada")

// TemplateInfo describe una plantilla instalada.
type TemplateInfo struct {
	Key        string    `json:"clave"`
	Name       string    `json:"nombre"`
	Installed  bool      `json:"instalada"`
	Size       int64     `json:"tamano,omitempty"`
	ModifiedAt time.Time `json:"modificada_en,omitempty"`
	Versions   int       `json:"versiones_anteriores"`
}

// TemplateStore guarda las plantillas Word y las versiones anteriores (plantillas/anteriores).
type TemplateStore interface {
	// Get devuelve el .docx de la plantilla (ErrTemplateNotFound si falta).
	Get(ctx context.Context, key string) ([]byte, error)
	// Put reemplaza la plantilla y guarda la anterior en anteriores/{clave}_{fecha}.docx.
	// Devuelve el nombre del respaldo ("" si no había una anterior).
	Put(ctx context.Context, key string, data []byte) (backup string, err error)
	List(ctx context.Context) ([]TemplateInfo, error)
}

// EventPublisher publica eventos de una campaña en su stream (Redis Streams; los lee el SSE de la Fase 7).
type EventPublisher interface {
	Publish(ctx context.Context, campaignID, eventType string, data any) error
}

// TemplateUseCase: instalar (con validación) y consultar las plantillas.
type TemplateUseCase interface {
	List(ctx context.Context) ([]TemplateInfo, error)
	// Install valida y reemplaza una plantilla. Un problema de la plantilla es *InputError con el mensaje del cliente.
	Install(ctx context.Context, key string, data []byte) (backup string, err error)
}

// GenerateResult es el resultado de pedir la generación de documentos.
type GenerateResult struct {
	Queued  int `json:"encolados"` // propuestas por generar
	Batches int `json:"lotes"`
	Pending int `json:"pendientes"` // filas válidas sin documento antes de pedirlo
}

// DocumentUseCase: generación de las propuestas en PDF de una campaña.
type DocumentUseCase interface {
	// Generate encola la generación de los documentos que faltan (SDD §4.4).
	Generate(ctx context.Context, campaignID string) (GenerateResult, error)
}
