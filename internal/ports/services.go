package ports

import (
	"context"

	"visoria-control/internal/domain"
)

// ConfigUseCase: sincronización y consulta de la configuración maestra.
type ConfigUseCase interface {
	// Sync sincroniza desde Google Sheets. newURL es opcional (cambia la hoja de origen).
	// Una configuración inválida NO es un error: devuelve Applied=false con los hallazgos.
	Sync(ctx context.Context, newURL string) (domain.SyncResult, error)
	Status(ctx context.Context) (domain.ConfigStatus, error)
}
