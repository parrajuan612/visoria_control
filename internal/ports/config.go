package ports

import (
	"context"
	"errors"

	"visoria-control/internal/domain"
)

// ConfigSource descarga y lee la configuración maestra desde su origen (Google Sheets).
// Devuelve los problemas de LECTURA (formatos inválidos); las reglas de negocio las
// comprueba MasterConfig.Validate(). El error es solo de transporte (red, HTTP, no es CSV).
type ConfigSource interface {
	Load(ctx context.Context, url string) (domain.MasterConfig, []domain.Issue, error)
}

// ConfigStore persiste la última configuración VÁLIDA y la URL de origen elegida.
type ConfigStore interface {
	// Current devuelve la última configuración válida, o nil si no hay ninguna.
	Current(ctx context.Context) (*domain.ConfigSnapshot, error)
	Save(ctx context.Context, snap domain.ConfigSnapshot) error
	// SourceURL devuelve la URL guardada ("" si no hay).
	SourceURL(ctx context.Context) (string, error)
	SetSourceURL(ctx context.Context, url string) error
}

// Errores de la sincronización de configuración.
var (
	// ErrNoSourceURL: no hay URL configurada ni en el entorno ni guardada.
	ErrNoSourceURL = errors.New("no hay una URL de Google Sheets configurada")
	// ErrInvalidSourceURL: la URL enviada no es válida.
	ErrInvalidSourceURL = errors.New("URL de Google Sheets no válida")
	// ErrSourceUnavailable: no se pudo descargar o interpretar la hoja como CSV.
	ErrSourceUnavailable = errors.New("no se pudo obtener la hoja de configuración")
)

// ConfigApplier reemplaza la configuración vigente en memoria.
type ConfigApplier interface {
	Apply(cfg domain.MasterConfig)
}
