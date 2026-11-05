package ports

import (
	"context"
	"errors"
	"io"

	"visoria-control/internal/domain"
)

// ErrInvalidSpreadsheet: el archivo no es un Excel legible o no tiene las columnas esperadas.
var ErrInvalidSpreadsheet = errors.New("archivo de jugadores inválido")

// ErrNoConfig: aún no hay una configuración maestra válida con la que validar.
var ErrNoConfig = errors.New("no hay una configuración sincronizada: sincronice Google Sheets primero")

// PlayerSheetReader lee el Excel de jugadores y devuelve sus filas sin validar.
// Los errores de formato (no es .xlsx, faltan columnas) envuelven ErrInvalidSpreadsheet.
type PlayerSheetReader interface {
	Read(ctx context.Context, r io.Reader) ([]domain.RawRow, error)
}

// ConfigProvider entrega la configuración maestra vigente (nil si aún no hay).
type ConfigProvider interface {
	Current() *domain.MasterConfig
}
