package ports

import (
	"context"
	"errors"

	"visoria-control/internal/domain"
)

// TournamentRepository: consulta de la configuración maestra vigente (en memoria).
type TournamentRepository interface {
	// Apply reemplaza la configuración vigente por completo.
	Apply(cfg domain.MasterConfig)
	GetTournamentForPlayer(ctx context.Context, birthYear int, scholarship string) (domain.TournamentInfo, error)
}

// ErrDeliveryNotFound: no existe la entrega pedida.
var ErrDeliveryNotFound = errors.New("entrega no encontrada")

// TransitionResult es el resultado de una transición atómica.
type TransitionResult struct {
	Outcome  domain.Outcome
	Previous domain.DeliveryStatus
}

// DeliveryRepo guarda las entregas y aplica las transiciones de forma atómica.
type DeliveryRepo interface {
	// Create guarda la entrega en PENDING. Es idempotente: devuelve created=false si ya existía.
	Create(ctx context.Context, campaignID string, d domain.Delivery) (created bool, err error)
	Get(ctx context.Context, campaignID, deliveryKey string) (domain.Delivery, error)
	// Transition aplica las reglas de domain.Decide de forma atómica (compara el estado
	// actual, escribe, ajusta contadores, emite el evento SSE e indexa el ID del proveedor).
	// Devuelve ErrDeliveryNotFound si la entrega no existe.
	Transition(ctx context.Context, campaignID string, t domain.Transition) (TransitionResult, error)
}
