package redisstore

import (
	"context"
	"fmt"

	"visoria-control/internal/ports"

	"github.com/redis/go-redis/v9"
)

// Quota implementa ports.QuotaStore: vc:quota:gmail:{aaaammdd} con vencimiento de 48 h.
type Quota struct{ rdb *redis.Client }

var _ ports.QuotaStore = (*Quota)(nil)

func NewQuota(rdb *redis.Client) *Quota { return &Quota{rdb: rdb} }

var takeScript = redis.NewScript(`
local n = redis.call('INCR', KEYS[1])
if n == 1 then redis.call('EXPIRE', KEYS[1], 172800) end
if n > tonumber(ARGV[1]) then
  redis.call('DECR', KEYS[1])
  return 0
end
return 1
`)

// Take reserva un envío del día (atómico: nunca se pasa del límite aunque haya varios workers).
func (q *Quota) Take(ctx context.Context, day string, limit int) (bool, error) {
	n, err := takeScript.Run(ctx, q.rdb, []string{keyPrefix + "quota:gmail:" + day}, limit).Int()
	if err != nil {
		return false, fmt.Errorf("cuota diaria: %w", err)
	}
	return n == 1, nil
}
