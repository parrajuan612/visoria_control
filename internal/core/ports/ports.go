package ports

import (
	"context"
	"mime/multipart"
	"visoria-control/internal/core/domain"
)

// Repository: Lo que la base de datos (o CSV) nos debe proveer
type TournamentRepository interface {
	LoadConfigFromCSV(ctx context.Context, csvURL string) error
	GetTournamentForPlayer(ctx context.Context, birthYear int, scholarship string) (domain.TournamentInfo, error)
}

// External: Servicios externos como PDF o WhatsApp
type PDFGenerator interface {
	Generate(player domain.Player, tournament domain.TournamentInfo) (string, error)
}

type WhatsAppAPI interface {
	SendTemplate(ctx context.Context, phone string, templateName string, language string, components []interface{}) error
}

// Service: Lo que el Handler puede pedirle al negocio
type VisoriaService interface {
	LoadMasterConfig(ctx context.Context, csvURL string) error
	ProcessPlayersExcel(ctx context.Context, file multipart.File) ([]domain.Player, error)
	GenerateDocuments(ctx context.Context, players []domain.Player) ([]string, error)
	DispatchWhatsAppMessages(ctx context.Context, players []domain.Player) error
}
