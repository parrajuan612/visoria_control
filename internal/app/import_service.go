package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"

	"visoria-control/internal/domain"
	"visoria-control/internal/platform/log"
	"visoria-control/internal/ports"
)

// ImportService convierte el Excel de jugadores en destinatarios validados contra la
// configuración maestra vigente (SDD §4.2).
type ImportService struct {
	reader ports.PlayerSheetReader
	cfg    ports.ConfigProvider
	newID  func() string
}

func NewImportService(reader ports.PlayerSheetReader, cfg ports.ConfigProvider) *ImportService {
	return &ImportService{reader: reader, cfg: cfg, newID: randomID}
}

// randomID genera un ID estable y opaco para el destinatario (no depende del nombre).
func randomID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ImportResult es el resultado de importar un archivo.
type ImportResult struct {
	Recipients    []domain.Recipient
	Summary       domain.BatchSummary
	ConfigVersion string // huella de la configuración con la que se validó
}

// Import lee y valida el archivo. Los errores de formato envuelven ports.ErrInvalidSpreadsheet;
// si no hay configuración sincronizada devuelve ports.ErrNoConfig.
func (s *ImportService) Import(ctx context.Context, r io.Reader, opt domain.ValidationOptions) (ImportResult, error) {
	cfg := s.cfg.Current()
	if cfg == nil {
		return ImportResult{}, ports.ErrNoConfig
	}
	rows, err := s.reader.Read(ctx, r)
	if err != nil {
		return ImportResult{}, err
	}
	rs := domain.ValidateBatch(rows, *cfg, opt)
	for i := range rs {
		rs[i].ID = s.newID()
	}
	res := ImportResult{Recipients: rs, Summary: domain.Summarize(rs), ConfigVersion: cfg.Hash()}
	log.FromContext(ctx).Info("excel importado", "filas", res.Summary.Total, "validas", res.Summary.Validas,
		"con_errores", res.Summary.ConErrores, "omitidas", res.Summary.Omitidas, "version_config", res.ConfigVersion)
	return res, nil
}
