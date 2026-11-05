package redisstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"visoria-control/internal/ports"

	"github.com/redis/go-redis/v9"
)

// EventPublisher publica eventos en el stream de la campaña (vc:c:{id}:ev, SDD §3.8). Es el mismo
// stream donde la transición de entregas escribe; el endpoint SSE (Fase 7) los reenvía al navegador.
type EventPublisher struct{ rdb *redis.Client }

var _ ports.EventPublisher = (*EventPublisher)(nil)

func NewEventPublisher(rdb *redis.Client) *EventPublisher { return &EventPublisher{rdb: rdb} }

func (p *EventPublisher) Publish(ctx context.Context, campaignID, eventType string, data any) error {
	body, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = p.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: eventsKey(campaignID), MaxLen: streamMaxLen, Approx: true,
		Values: map[string]any{"type": eventType, "data": string(body), "at": time.Now().UTC().Format(time.RFC3339Nano)},
	}).Result()
	if err != nil {
		return fmt.Errorf("publicando el evento %s: %w", eventType, err)
	}
	return nil
}
