package ports

import (
	"context"

	"visoria-control/internal/domain"
)

// TournamentRepository: configuración maestra (hoy CSV de Google Sheets).
type TournamentRepository interface {
	LoadConfigFromCSV(ctx context.Context, csvURL string) error
	GetTournamentForPlayer(ctx context.Context, birthYear int, scholarship string) (domain.TournamentInfo, error)
}
