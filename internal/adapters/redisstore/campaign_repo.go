package redisstore

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"visoria-control/internal/domain"
	"visoria-control/internal/ports"

	"github.com/redis/go-redis/v9"
)

// Claves (SDD §3.6.1).
const campaignsKey = keyPrefix + "campaigns" // ZSET id -> creada_en (unix)

func campaignKey(id string) string   { return keyPrefix + "c:" + id }
func recipientsKey(id string) string { return keyPrefix + "c:" + id + ":r" }

// campaignMeta es lo que se guarda en el HASH de la campaña.
// La configuración congelada va en su propio campo (no se devuelve en los listados).
type campaignMeta struct {
	domain.Campaign
	Dir string `json:"dir"`
}

// CampaignRepo implementa ports.CampaignRepo sobre Redis.
type CampaignRepo struct{ rdb *redis.Client }

var _ ports.CampaignRepo = (*CampaignRepo)(nil)

func NewCampaignRepo(rdb *redis.Client) *CampaignRepo { return &CampaignRepo{rdb: rdb} }

func (r *CampaignRepo) Create(ctx context.Context, c domain.Campaign, recs []domain.Recipient, sum domain.BatchSummary) error {
	meta, err := json.Marshal(campaignMeta{Campaign: c, Dir: c.Dir})
	if err != nil {
		return err
	}
	cfg, err := json.Marshal(c.Config)
	if err != nil {
		return err
	}
	summary, _ := json.Marshal(sum)

	fields := make(map[string]any, len(recs))
	for _, rec := range recs {
		b, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		fields[rec.ID] = b
	}
	// MULTI/EXEC: la campaña aparece completa o no aparece.
	_, err = r.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.HSet(ctx, campaignKey(c.ID), "meta", meta, "config", cfg, "summary", summary, "status", string(c.Status))
		if len(fields) > 0 {
			p.HSet(ctx, recipientsKey(c.ID), fields)
		}
		p.ZAdd(ctx, campaignsKey, redis.Z{Score: float64(c.CreatedAt.Unix()), Member: c.ID})
		return nil
	})
	if err != nil {
		return fmt.Errorf("guardando la campaña: %w", err)
	}
	return nil
}

func decodeMeta(raw, cfg string) (domain.Campaign, error) {
	var m campaignMeta
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return domain.Campaign{}, fmt.Errorf("la campaña guardada está corrupta: %w", err)
	}
	c := m.Campaign
	c.Dir = m.Dir
	if cfg != "" {
		if err := json.Unmarshal([]byte(cfg), &c.Config); err != nil {
			return domain.Campaign{}, fmt.Errorf("la configuración de la campaña está corrupta: %w", err)
		}
	}
	return c, nil
}

func (r *CampaignRepo) Get(ctx context.Context, id string) (domain.Campaign, error) {
	vals, err := r.rdb.HMGet(ctx, campaignKey(id), "meta", "config", "status").Result()
	if err != nil {
		return domain.Campaign{}, fmt.Errorf("leyendo la campaña: %w", err)
	}
	meta, _ := vals[0].(string)
	if meta == "" {
		return domain.Campaign{}, ports.ErrCampaignNotFound
	}
	cfg, _ := vals[1].(string)
	c, err := decodeMeta(meta, cfg)
	if err != nil {
		return domain.Campaign{}, err
	}
	if st, _ := vals[2].(string); st != "" { // el estado vigente vive en su propio campo
		c.Status = domain.CampaignStatus(st)
	}
	return c, nil
}

func (r *CampaignRepo) List(ctx context.Context, limit int) ([]domain.CampaignView, error) {
	ids, err := r.rdb.ZRevRange(ctx, campaignsKey, 0, int64(limit)-1).Result()
	if err != nil {
		return nil, fmt.Errorf("listando campañas: %w", err)
	}
	pipe := r.rdb.Pipeline()
	cmds := make([]*redis.SliceCmd, len(ids))
	for i, id := range ids {
		cmds[i] = pipe.HMGet(ctx, campaignKey(id), "meta", "summary", "status") // sin la configuración: es pesada
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("listando campañas: %w", err)
	}
	out := make([]domain.CampaignView, 0, len(ids))
	for _, cmd := range cmds {
		vals := cmd.Val()
		meta, _ := vals[0].(string)
		if meta == "" {
			continue // venció o se borró mientras se listaba
		}
		c, err := decodeMeta(meta, "")
		if err != nil {
			return nil, err
		}
		if st, _ := vals[2].(string); st != "" {
			c.Status = domain.CampaignStatus(st)
		}
		v := domain.CampaignView{Campaign: c}
		if s, _ := vals[1].(string); s != "" {
			_ = json.Unmarshal([]byte(s), &v.Resumen)
		}
		out = append(out, v)
	}
	return out, nil
}

func (r *CampaignRepo) Recipients(ctx context.Context, id string) ([]domain.Recipient, error) {
	all, err := r.rdb.HGetAll(ctx, recipientsKey(id)).Result()
	if err != nil {
		return nil, fmt.Errorf("leyendo destinatarios: %w", err)
	}
	out := make([]domain.Recipient, 0, len(all))
	for rid, raw := range all {
		var rec domain.Recipient
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			return nil, fmt.Errorf("el destinatario %s está corrupto: %w", rid, err)
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Fila < out[j].Fila })
	return out, nil
}

func (r *CampaignRepo) SaveRecipients(ctx context.Context, id string, changed []domain.Recipient, sum domain.BatchSummary) error {
	// Solo se puede escribir en una campaña existente: HSET no debe crear una a medias.
	if n, err := r.rdb.Exists(ctx, campaignKey(id)).Result(); err != nil {
		return err
	} else if n == 0 {
		return ports.ErrCampaignNotFound
	}
	fields := make(map[string]any, len(changed))
	for _, rec := range changed {
		b, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		fields[rec.ID] = b
	}
	summary, _ := json.Marshal(sum)
	_, err := r.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		if len(fields) > 0 {
			p.HSet(ctx, recipientsKey(id), fields)
		}
		p.HSet(ctx, campaignKey(id), "summary", summary)
		return nil
	})
	if err != nil {
		return fmt.Errorf("guardando destinatarios: %w", err)
	}
	return nil
}

// setStatusLua: cambia el estado solo si el actual está entre los permitidos (ARGV[2..]).
// Una campaña antigua sin campo "status" se considera DRAFT.
var setStatusScript = redis.NewScript(`
local cur = redis.call('HGET', KEYS[1], 'status')
if not cur then
  if redis.call('EXISTS', KEYS[1]) == 0 then return -1 end
  cur = 'DRAFT'
end
for i = 2, #ARGV do
  if ARGV[i] == cur then
    redis.call('HSET', KEYS[1], 'status', ARGV[1])
    return 1
  end
end
return 0
`)

func (r *CampaignRepo) SetStatus(ctx context.Context, id string, from []domain.CampaignStatus, to domain.CampaignStatus) (bool, error) {
	args := []any{string(to)}
	for _, f := range from {
		args = append(args, string(f))
	}
	n, err := setStatusScript.Run(ctx, r.rdb, []string{campaignKey(id)}, args...).Int()
	if err != nil {
		return false, fmt.Errorf("cambiando el estado de la campaña: %w", err)
	}
	if n < 0 {
		return false, ports.ErrCampaignNotFound
	}
	return n == 1, nil
}
