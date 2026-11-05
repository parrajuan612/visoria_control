package sheets

import (
	"context"
	"strings"
	"sync/atomic"

	"visoria-control/internal/domain"
	"visoria-control/internal/ports"
)

// Repository guarda en memoria la configuración vigente. La lectura es sin
// bloqueos (atomic) y el reemplazo es completo: no hay estados a medias ni
// carreras entre una sincronización y las consultas en curso.
type Repository struct {
	cur atomic.Pointer[domain.MasterConfig]
}

var _ ports.TournamentRepository = (*Repository)(nil)

func NewRepository() *Repository { return &Repository{} }

// Apply reemplaza la configuración vigente.
func (r *Repository) Apply(cfg domain.MasterConfig) { r.cur.Store(&cfg) }

// Current devuelve la configuración vigente (nil si aún no se sincronizó).
func (r *Repository) Current() *domain.MasterConfig { return r.cur.Load() }

// GetTournamentForPlayer arma lo que necesita el flujo de propuestas actual:
// precios y plan de pagos de la beca, todos los torneos y categorías del año.
// Si la beca no existe devuelve Pricing.Total = "No definido".
func (r *Repository) GetTournamentForPlayer(_ context.Context, birthYear int, scholarship string) (domain.TournamentInfo, error) {
	var info domain.TournamentInfo
	cfg := r.cur.Load()
	if cfg == nil {
		info.Pricing = domain.Pricing{Total: "No definido"}
		return info, nil
	}

	info.Pricing = domain.Pricing{Total: "No definido"}
	if key, ok := domain.NormalizeBecaKey(scholarship); ok {
		if b, found := cfg.Beca(key); found {
			pago := func(i int) string {
				if i < len(b.Pagos) {
					return domain.FormatEUR(b.Pagos[i])
				}
				return ""
			}
			info.Pricing = domain.Pricing{Total: domain.FormatEUR(b.Total), Pago1: pago(0), Pago2: pago(1), Pago3: pago(2)}
		}
	}

	info.Name = strings.Join(cfg.TorneosParaAnio(birthYear), "\n- ")
	var cats []string
	for _, c := range cfg.CategoriasParaAnio(birthYear) {
		cats = append(cats, c.Nombre)
	}
	info.Category = strings.Join(cats, " / ")
	return info, nil
}
