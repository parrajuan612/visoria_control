package domain

import "time"

// SyncResult es el resultado de una sincronización.
type SyncResult struct {
	Applied  bool          `json:"aplicada"` // la nueva configuración quedó vigente
	Changed  bool          `json:"cambio"`   // difiere de la anterior
	Hash     string        `json:"version"`  // huella de la configuración leída
	SyncedAt time.Time     `json:"sincronizada_en"`
	Issues   []Issue       `json:"hallazgos"`
	Summary  ConfigSummary `json:"resumen"`
}

// ConfigSummary son los conteos de la configuración leída.
type ConfigSummary struct {
	Becas        int `json:"becas"`
	Torneos      int `json:"torneos"`
	Categorias   int `json:"categorias"`
	Errores      int `json:"errores"`
	Advertencias int `json:"advertencias"`
}

// ConfigStatus describe la configuración vigente.
type ConfigStatus struct {
	Sincronizada bool            `json:"sincronizada"`
	URL          string          `json:"url,omitempty"`
	Snapshot     *ConfigSnapshot `json:"-"`
}
