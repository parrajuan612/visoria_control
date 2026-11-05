package redisstore

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"visoria-control/internal/domain"
	"visoria-control/internal/ports"

	"github.com/redis/go-redis/v9"
)

//go:embed transition.lua
var transitionLua string

const (
	streamMaxLen   = 20000
	wamidIndexTTL  = 60 * 24 * time.Hour
	msgidIndexTTL  = 30 * 24 * time.Hour
	keyPrefix      = "vc:"
	wamidKeyPrefix = keyPrefix + "wamid:"
	msgidKeyPrefix = keyPrefix + "msgid:"
)

// Claves de una campaña (SDD §3.6.1).
func deliveriesKey(campaignID string) string { return keyPrefix + "c:" + campaignID + ":d" }
func countersKey(campaignID string) string   { return keyPrefix + "c:" + campaignID + ":n" }
func eventsKey(campaignID string) string     { return keyPrefix + "c:" + campaignID + ":ev" }

// WamidKey / MsgIDKey correlacionan el ID del proveedor con la entrega.
func WamidKey(wamid string) string { return wamidKeyPrefix + wamid }
func MsgIDKey(id string) string    { return msgidKeyPrefix + id }

// DeliveryRepo implementa ports.DeliveryRepo sobre Redis.
type DeliveryRepo struct {
	rdb        *redis.Client
	transition *redis.Script
	create     *redis.Script
}

var _ ports.DeliveryRepo = (*DeliveryRepo)(nil)

const createLua = `
-- KEYS[1]=deliveries, KEYS[2]=counters. ARGV: 1 key, 2 json, 3 channel, 4 status
if redis.call('HSETNX', KEYS[1], ARGV[1], ARGV[2]) == 0 then return 0 end
redis.call('HINCRBY', KEYS[2], ARGV[3] .. ':total', 1)
redis.call('HINCRBY', KEYS[2], ARGV[3] .. ':' .. ARGV[4], 1)
return 1
`

func NewDeliveryRepo(rdb *redis.Client) (*DeliveryRepo, error) {
	rules, err := domain.Rules()
	if err != nil {
		return nil, fmt.Errorf("serializando reglas de transición: %w", err)
	}
	src := strings.Replace(transitionLua, "__RULES__", string(rules), 1)
	return &DeliveryRepo{
		rdb:        rdb,
		transition: redis.NewScript(src),
		create:     redis.NewScript(createLua),
	}, nil
}

func (r *DeliveryRepo) Create(ctx context.Context, campaignID string, d domain.Delivery) (bool, error) {
	body, err := json.Marshal(d)
	if err != nil {
		return false, err
	}
	n, err := r.create.Run(ctx, r.rdb, []string{deliveriesKey(campaignID), countersKey(campaignID)},
		d.Key, string(body), string(d.Channel), string(d.Status)).Int()
	if err != nil {
		return false, fmt.Errorf("creando entrega: %w", err)
	}
	return n == 1, nil
}

func (r *DeliveryRepo) Get(ctx context.Context, campaignID, deliveryKey string) (domain.Delivery, error) {
	raw, err := r.rdb.HGet(ctx, deliveriesKey(campaignID), deliveryKey).Result()
	if errors.Is(err, redis.Nil) {
		return domain.Delivery{}, ports.ErrDeliveryNotFound
	}
	if err != nil {
		return domain.Delivery{}, err
	}
	var d domain.Delivery
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return domain.Delivery{}, fmt.Errorf("entrega %s corrupta: %w", deliveryKey, err)
	}
	return d, nil
}

func (r *DeliveryRepo) Transition(ctx context.Context, campaignID string, t domain.Transition) (ports.TransitionResult, error) {
	if err := t.Validate(); err != nil {
		return ports.TransitionResult{}, err
	}
	at := t.At
	if at.IsZero() {
		at = time.Now()
	}
	errJSON := ""
	if t.Error != nil {
		b, err := json.Marshal(t.Error)
		if err != nil {
			return ports.TransitionResult{}, err
		}
		errJSON = string(b)
	}
	res, err := r.transition.Run(ctx, r.rdb,
		[]string{deliveriesKey(campaignID), countersKey(campaignID), eventsKey(campaignID)},
		t.Key, string(t.To), string(t.Source), at.UTC().Format(time.RFC3339Nano),
		t.ProviderID, errJSON, t.Detail, streamMaxLen,
		wamidKeyPrefix, int(wamidIndexTTL.Seconds()), msgidKeyPrefix, int(msgidIndexTTL.Seconds()),
	).Slice()
	if err != nil {
		return ports.TransitionResult{}, fmt.Errorf("transición %s→%s: %w", t.Key, t.To, err)
	}
	code, _ := res[0].(string)
	prev, _ := res[1].(string)
	if code == "NOT_FOUND" {
		return ports.TransitionResult{}, ports.ErrDeliveryNotFound
	}
	return ports.TransitionResult{Outcome: domain.Outcome(code), Previous: domain.DeliveryStatus(prev)}, nil
}
