package sheets

import (
	"sync/atomic"

	"visoria-control/internal/domain"
	"visoria-control/internal/ports"
)

// Repository guarda en memoria la configuración vigente. La lectura es sin bloqueos (atomic) y el
// reemplazo es completo: no hay estados a medias ni carreras entre una sincronización y las consultas.
type Repository struct {
	cur atomic.Pointer[domain.MasterConfig]
}

var (
	_ ports.ConfigApplier  = (*Repository)(nil)
	_ ports.ConfigProvider = (*Repository)(nil)
)

func NewRepository() *Repository { return &Repository{} }

// Apply reemplaza la configuración vigente.
func (r *Repository) Apply(cfg domain.MasterConfig) { r.cur.Store(&cfg) }

// Current devuelve la configuración vigente (nil si aún no se sincronizó).
func (r *Repository) Current() *domain.MasterConfig { return r.cur.Load() }
