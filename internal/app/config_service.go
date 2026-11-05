package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"visoria-control/internal/domain"
	"visoria-control/internal/platform/log"
	"visoria-control/internal/ports"
)

// ConfigService sincroniza la configuración maestra desde Google Sheets.
// Solo reemplaza la vigente si la nueva es válida (sin errores).
type ConfigService struct {
	source      ports.ConfigSource
	store       ports.ConfigStore
	applier     ports.TournamentRepository
	defaultURL  string
	validateURL func(string) error
	now         func() time.Time

	mu sync.Mutex // una sincronización a la vez
}

// NewConfigService crea el servicio. defaultURL puede estar vacío; validateURL valida
// las URLs recibidas por la API (por ejemplo, solo hojas de Google).
func NewConfigService(source ports.ConfigSource, store ports.ConfigStore, applier ports.TournamentRepository,
	defaultURL string, validateURL func(string) error) *ConfigService {
	return &ConfigService{source: source, store: store, applier: applier,
		defaultURL: strings.TrimSpace(defaultURL), validateURL: validateURL, now: time.Now}
}

func summarize(cfg domain.MasterConfig, issues []domain.Issue) domain.ConfigSummary {
	c := domain.CountByLevel(issues)
	return domain.ConfigSummary{Becas: len(cfg.Becas), Torneos: len(cfg.Torneos), Categorias: len(cfg.Categorias),
		Errores: c[domain.IssueError], Advertencias: c[domain.IssueWarning]}
}

// resolveURL: la enviada > la guardada > la del entorno.
func (s *ConfigService) resolveURL(ctx context.Context, requested string) (string, error) {
	if requested = strings.TrimSpace(requested); requested != "" {
		if s.validateURL != nil {
			if err := s.validateURL(requested); err != nil {
				return "", fmt.Errorf("%w: %v", ports.ErrInvalidSourceURL, err)
			}
		}
		return requested, nil
	}
	saved, err := s.store.SourceURL(ctx)
	if err != nil {
		return "", err
	}
	if saved != "" {
		return saved, nil
	}
	if s.defaultURL != "" {
		return s.defaultURL, nil
	}
	return "", ports.ErrNoSourceURL
}

// Sync descarga, valida y, si no hay errores, reemplaza la configuración vigente.
// newURL es opcional: si se envía y la configuración resulta válida, queda guardada
// como nueva fuente (así se cambia de hoja sin tocar código ni reiniciar).
//
// Una configuración inválida NO es un error de Go: devuelve Applied=false con los
// hallazgos y deja intacta la vigente.
func (s *ConfigService) Sync(ctx context.Context, newURL string) (domain.SyncResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lg := log.FromContext(ctx)

	url, err := s.resolveURL(ctx, newURL)
	if err != nil {
		return domain.SyncResult{}, err
	}
	cfg, issues, err := s.source.Load(ctx, url)
	if err != nil {
		return domain.SyncResult{}, fmt.Errorf("%w: %v", ports.ErrSourceUnavailable, err)
	}
	// Si la lectura ya falló (columnas faltantes), validar el resto solo agregaría ruido.
	if !domain.HasErrors(issues) {
		issues = append(issues, cfg.Validate()...)
	}
	res := domain.SyncResult{Hash: cfg.Hash(), SyncedAt: s.now().UTC(), Issues: issues, Summary: summarize(cfg, issues)}

	if domain.HasErrors(issues) {
		lg.Warn("configuración rechazada: se conserva la vigente", "errores", res.Summary.Errores)
		return res, nil
	}

	prev, err := s.store.Current(ctx)
	if err != nil {
		return domain.SyncResult{}, err
	}
	snap := domain.ConfigSnapshot{Config: cfg, Hash: res.Hash, SyncedAt: res.SyncedAt, Issues: nonErrors(issues)}
	if err := s.store.Save(ctx, snap); err != nil {
		return domain.SyncResult{}, err
	}
	if strings.TrimSpace(newURL) != "" {
		if err := s.store.SetSourceURL(ctx, url); err != nil {
			return domain.SyncResult{}, err
		}
	}
	s.applier.Apply(cfg)
	res.Applied = true
	res.Changed = prev == nil || prev.Hash != res.Hash
	lg.Info("configuración sincronizada", "version", res.Hash, "cambio", res.Changed,
		"becas", res.Summary.Becas, "torneos", res.Summary.Torneos, "categorias", res.Summary.Categorias,
		"advertencias", res.Summary.Advertencias)
	return res, nil
}

func nonErrors(in []domain.Issue) []domain.Issue {
	out := make([]domain.Issue, 0, len(in))
	for _, i := range in {
		if i.Nivel != domain.IssueError {
			out = append(out, i)
		}
	}
	return out
}

// Restore carga la última configuración válida guardada (al arrancar). Si no hay
// ninguna y existe una URL (entorno o guardada), intenta sincronizar. Nunca impide
// arrancar: devuelve el motivo para que el llamador lo registre.
func (s *ConfigService) Restore(ctx context.Context) error {
	snap, err := s.store.Current(ctx)
	if err != nil {
		return fmt.Errorf("leyendo la configuración guardada: %w", err)
	}
	if snap != nil {
		s.applier.Apply(snap.Config)
		log.FromContext(ctx).Info("configuración restaurada", "version", snap.Hash, "sincronizada_en", snap.SyncedAt)
		return nil
	}
	res, err := s.Sync(ctx, "")
	if errors.Is(err, ports.ErrNoSourceURL) {
		return nil // aún no hay hoja configurada: se hará desde la pantalla
	}
	if err != nil {
		return err
	}
	if !res.Applied {
		return fmt.Errorf("la hoja tiene %d errores: no se aplicó", res.Summary.Errores)
	}
	return nil
}

// Status devuelve la configuración vigente y de dónde viene.
func (s *ConfigService) Status(ctx context.Context) (domain.ConfigStatus, error) {
	snap, err := s.store.Current(ctx)
	if err != nil {
		return domain.ConfigStatus{}, err
	}
	url, err := s.resolveURL(ctx, "")
	if err != nil && !errors.Is(err, ports.ErrNoSourceURL) {
		return domain.ConfigStatus{}, err
	}
	return domain.ConfigStatus{Sincronizada: snap != nil, URL: url, Snapshot: snap}, nil
}
