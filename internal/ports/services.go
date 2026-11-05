package ports

import (
	"context"
	"mime/multipart"

	"visoria-control/internal/domain"
)

// VisoriaService: lo que los adaptadores de entrada (HTTP) pueden pedirle al negocio.
type VisoriaService interface {
	LoadMasterConfig(ctx context.Context, csvURL string) error
	ProcessPlayersExcel(ctx context.Context, file multipart.File) ([]domain.Player, error)
	GenerateDocuments(ctx context.Context, players []domain.Player) ([]string, error)
	DispatchWhatsAppMessages(ctx context.Context, players []domain.Player, progressChan chan<- string) error
}
