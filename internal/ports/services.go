package ports

import (
	"context"
	"mime/multipart"

	"visoria-control/internal/domain"
)

// VisoriaService: lo que los adaptadores de entrada (HTTP) pueden pedirle al negocio.
type VisoriaService interface {
	ProcessPlayersExcel(ctx context.Context, file multipart.File) ([]domain.Player, error)
	GenerateDocuments(ctx context.Context, players []domain.Player) ([]string, error)
	DispatchWhatsAppMessages(ctx context.Context, players []domain.Player, progressChan chan<- string) error
}

// ConfigUseCase: sincronización y consulta de la configuración maestra.
type ConfigUseCase interface {
	// Sync sincroniza desde Google Sheets. newURL es opcional (cambia la hoja de origen).
	// Una configuración inválida NO es un error: devuelve Applied=false con los hallazgos.
	Sync(ctx context.Context, newURL string) (domain.SyncResult, error)
	Status(ctx context.Context) (domain.ConfigStatus, error)
}
