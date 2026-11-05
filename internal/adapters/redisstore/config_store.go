package redisstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"visoria-control/internal/domain"
	"visoria-control/internal/ports"

	"github.com/redis/go-redis/v9"
)

// Claves de la configuración maestra (SDD §3.6.1).
const (
	configCurrentKey = keyPrefix + "cfg:current" // JSON del ConfigSnapshot vigente
	configURLKey     = keyPrefix + "cfg:url"     // URL de la hoja elegida por el operador
)

// ConfigStore guarda la última configuración válida y la URL de origen.
type ConfigStore struct{ rdb *redis.Client }

var _ ports.ConfigStore = (*ConfigStore)(nil)

func NewConfigStore(rdb *redis.Client) *ConfigStore { return &ConfigStore{rdb: rdb} }

func (s *ConfigStore) Current(ctx context.Context) (*domain.ConfigSnapshot, error) {
	raw, err := s.rdb.Get(ctx, configCurrentKey).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("leyendo la configuración: %w", err)
	}
	var snap domain.ConfigSnapshot
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		return nil, fmt.Errorf("la configuración guardada está corrupta: %w", err)
	}
	return &snap, nil
}

// Save reemplaza la configuración vigente (sin TTL: es la fuente operativa).
func (s *ConfigStore) Save(ctx context.Context, snap domain.ConfigSnapshot) error {
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	if err := s.rdb.Set(ctx, configCurrentKey, raw, 0).Err(); err != nil {
		return fmt.Errorf("guardando la configuración: %w", err)
	}
	return nil
}

func (s *ConfigStore) SourceURL(ctx context.Context) (string, error) {
	v, err := s.rdb.Get(ctx, configURLKey).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("leyendo la URL de la hoja: %w", err)
	}
	return v, nil
}

func (s *ConfigStore) SetSourceURL(ctx context.Context, url string) error {
	if err := s.rdb.Set(ctx, configURLKey, url, 0).Err(); err != nil {
		return fmt.Errorf("guardando la URL de la hoja: %w", err)
	}
	return nil
}
