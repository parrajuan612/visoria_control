// Package redisstore crea el cliente de Redis y verifica que esté configurado
// para ser durable (SDD §3.6.1). El resto de repositorios se agregan aquí.
package redisstore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"visoria-control/internal/platform/config"

	"github.com/redis/go-redis/v9"
)

func New(cfg config.Redis) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	})
}

// Verify comprueba la conexión y la configuración de durabilidad:
//   - maxmemory-policy debe ser noeviction (otra política podría borrar la cola) → error.
//   - appendonly debe ser yes → advertencia (la devuelve en warnings).
//
// Si el servidor deshabilita CONFIG GET, no se puede verificar: se devuelve una advertencia.
func Verify(ctx context.Context, rdb *redis.Client) (warnings []string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("no se pudo conectar a Redis: %w", err)
	}
	get := func(key string) (string, error) {
		res, err := rdb.ConfigGet(ctx, key).Result()
		if err != nil {
			return "", err
		}
		return strings.ToLower(res[key]), nil
	}

	policy, err := get("maxmemory-policy")
	if err != nil {
		return []string{"no se pudo leer la configuración de Redis (CONFIG GET deshabilitado): no se verificó noeviction/AOF"}, nil
	}
	if policy != "noeviction" {
		return nil, fmt.Errorf("redis tiene maxmemory-policy=%q; debe ser noeviction o la cola podría perder tareas", policy)
	}
	if aof, err := get("appendonly"); err == nil && aof != "yes" {
		warnings = append(warnings, "redis sin appendonly=yes: un reinicio de Redis perdería la cola y los estados")
	}
	return warnings, nil
}
