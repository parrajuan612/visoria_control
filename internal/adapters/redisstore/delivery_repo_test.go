package redisstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"visoria-control/internal/domain"
	"visoria-control/internal/ports"

	"github.com/redis/go-redis/v9"
)

// Requiere Redis real: VISORIA_TEST_REDIS_ADDR=127.0.0.1:6380 [VISORIA_TEST_REDIS_PASSWORD=...]
// Usa la DB 14 (exclusiva de tests; se vacía al terminar).
func setup(t *testing.T) (*DeliveryRepo, *redis.Client, string) {
	t.Helper()
	addr := os.Getenv("VISORIA_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("VISORIA_TEST_REDIS_ADDR no definida; se omite el test de integración con Redis")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr, Password: os.Getenv("VISORIA_TEST_REDIS_PASSWORD"), DB: 14})
	t.Cleanup(func() { rdb.Close() })
	repo, err := NewDeliveryRepo(rdb)
	if err != nil {
		t.Fatal(err)
	}
	camp := fmt.Sprintf("t%d", time.Now().UnixNano())
	t.Cleanup(func() { rdb.FlushDB(context.Background()) })
	return repo, rdb, camp
}

var now = time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)

func put(t *testing.T, rdb *redis.Client, camp string, d domain.Delivery) {
	t.Helper()
	b, _ := json.Marshal(d)
	if err := rdb.HSet(context.Background(), deliveriesKey(camp), d.Key, b).Err(); err != nil {
		t.Fatal(err)
	}
}

// Cruce exhaustivo: el script Lua y domain.Apply deben coincidir en TODAS las combinaciones.
func TestLuaCoincideConGoEnTodasLasCombinaciones(t *testing.T) {
	repo, rdb, camp := setup(t)
	ctx := context.Background()
	sources := []domain.Source{domain.SourceWorker, domain.SourceWebhook, domain.SourceIMAP, domain.SourceOperator, domain.SourceSystem}
	n := 0
	for _, ch := range []domain.Channel{domain.ChannelWhatsApp, domain.ChannelEmail} {
		for _, from := range domain.AllStatuses {
			for _, to := range domain.AllStatuses {
				for _, src := range sources {
					n++
					d := domain.NewDelivery(camp, fmt.Sprintf("r%d", n), ch, domain.KindProposal, now)
					d.Status = from
					d.History = []domain.StatusEvent{{Status: from, At: now, Source: domain.SourceSystem}}
					put(t, rdb, camp, d)

					tr := domain.Transition{Key: d.Key, To: to, Source: src, At: now.Add(time.Minute),
						ProviderID: "id-" + d.Key, Detail: "d",
						Error: &domain.ProviderError{Code: "1", Message: "m", Permanent: true}}
					want := d
					wantOut, wantPrev := want.Apply(tr)

					got, err := repo.Transition(ctx, camp, tr)
					if err != nil {
						t.Fatalf("%s %s→%s %s: %v", ch, from, to, src, err)
					}
					if got.Outcome != wantOut || got.Previous != wantPrev {
						t.Fatalf("%s %s→%s %s: Lua=%s/%s Go=%s/%s", ch, from, to, src, got.Outcome, got.Previous, wantOut, wantPrev)
					}
					stored, err := repo.Get(ctx, camp, d.Key)
					if err != nil {
						t.Fatal(err)
					}
					if stored.Status != want.Status || stored.Attempts != want.Attempts ||
						stored.ProviderID != want.ProviderID || len(stored.History) != len(want.History) ||
						(stored.LastError == nil) != (want.LastError == nil) {
						t.Fatalf("%s %s→%s %s: estado guardado difiere\nLua: %+v\nGo:  %+v", ch, from, to, src, stored, want)
					}
				}
			}
		}
	}
	t.Logf("%d combinaciones verificadas", n)
}

func TestCreateIdempotenteYContadores(t *testing.T) {
	repo, rdb, camp := setup(t)
	ctx := context.Background()
	d := domain.NewDelivery(camp, "r1", domain.ChannelWhatsApp, domain.KindProposal, now)
	for i, want := range []bool{true, false} {
		created, err := repo.Create(ctx, camp, d)
		if err != nil || created != want {
			t.Fatalf("intento %d: created=%v err=%v", i, created, err)
		}
	}
	c, _ := rdb.HGetAll(ctx, countersKey(camp)).Result()
	if c["whatsapp:total"] != "1" || c["whatsapp:PENDING"] != "1" {
		t.Errorf("contadores: %v", c)
	}
}

func TestFlujoDesordenadoContadoresEventosEIndices(t *testing.T) {
	repo, rdb, camp := setup(t)
	ctx := context.Background()
	d := domain.NewDelivery(camp, "r1", domain.ChannelWhatsApp, domain.KindProposal, now)
	repo.Create(ctx, camp, d)

	step := func(to domain.DeliveryStatus, src domain.Source, id string) ports.TransitionResult {
		res, err := repo.Transition(ctx, camp, domain.Transition{Key: d.Key, To: to, Source: src, At: now, ProviderID: id})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	step(domain.StatusQueued, domain.SourceSystem, "")
	step(domain.StatusSending, domain.SourceWorker, "")
	if r := step(domain.StatusAccepted, domain.SourceWorker, "wamid.X"); r.Outcome != domain.OutcomeApplied {
		t.Fatalf("accepted: %+v", r)
	}
	// llegan desordenados: read, luego delivered y sent atrasados, y un duplicado
	step(domain.StatusRead, domain.SourceWebhook, "")
	for _, late := range []domain.DeliveryStatus{domain.StatusDelivered, domain.StatusSent, domain.StatusRead} {
		if r := step(late, domain.SourceWebhook, ""); r.Outcome != domain.OutcomeIgnored {
			t.Errorf("%s tardío debía ignorarse: %+v", late, r)
		}
	}
	got, _ := repo.Get(ctx, camp, d.Key)
	if got.Status != domain.StatusRead || got.ProviderID != "wamid.X" || got.Attempts != 1 {
		t.Errorf("estado final: %+v", got)
	}

	c, _ := rdb.HGetAll(ctx, countersKey(camp)).Result()
	if c["whatsapp:READ"] != "1" || c["whatsapp:PENDING"] != "0" || c["whatsapp:ACCEPTED"] != "0" || c["whatsapp:total"] != "1" {
		t.Errorf("contadores: %v", c)
	}
	// Solo las transiciones APLICADAS emiten evento: QUEUED, SENDING, ACCEPTED, READ.
	if n, _ := rdb.XLen(ctx, eventsKey(camp)).Result(); n != 4 {
		t.Errorf("eventos=%d, quería 4", n)
	}
	if v, _ := rdb.Get(ctx, WamidKey("wamid.X")).Result(); v != d.Key {
		t.Errorf("índice wamid = %q", v)
	}
	if ttl, _ := rdb.TTL(ctx, WamidKey("wamid.X")).Result(); ttl < 59*24*time.Hour {
		t.Errorf("ttl wamid = %v", ttl)
	}
}

func TestCorreoIndexaMessageIDYNoPuedeEntregarse(t *testing.T) {
	repo, rdb, camp := setup(t)
	ctx := context.Background()
	d := domain.NewDelivery(camp, "r1", domain.ChannelEmail, domain.KindProposal, now)
	repo.Create(ctx, camp, d)
	for _, s := range []domain.DeliveryStatus{domain.StatusQueued, domain.StatusSending} {
		repo.Transition(ctx, camp, domain.Transition{Key: d.Key, To: s, Source: domain.SourceWorker, At: now})
	}
	repo.Transition(ctx, camp, domain.Transition{Key: d.Key, To: domain.StatusAccepted, Source: domain.SourceWorker, At: now, ProviderID: "<m1@x>"})
	if v, _ := rdb.Get(ctx, MsgIDKey("<m1@x>")).Result(); v != d.Key {
		t.Errorf("índice msgid = %q", v)
	}
	if r, _ := repo.Transition(ctx, camp, domain.Transition{Key: d.Key, To: domain.StatusDelivered, Source: domain.SourceWebhook, At: now}); r.Outcome != domain.OutcomeRejected {
		t.Errorf("un correo no puede quedar DELIVERED: %+v", r)
	}
	// El rebote sí lo manda a FAILED con el motivo.
	r, _ := repo.Transition(ctx, camp, domain.Transition{Key: d.Key, To: domain.StatusFailed, Source: domain.SourceIMAP, At: now,
		Error: &domain.ProviderError{Code: "5.1.1", Message: "La dirección no existe", Permanent: true}})
	got, _ := repo.Get(ctx, camp, d.Key)
	if r.Outcome != domain.OutcomeApplied || got.LastError == nil || got.LastError.Code != "5.1.1" {
		t.Errorf("rebote: %+v %+v", r, got)
	}
}

// Dos workers (o un worker y un webhook) compitiendo: solo una transición se aplica.
func TestConcurrenciaSoloUnaGana(t *testing.T) {
	repo, rdb, camp := setup(t)
	ctx := context.Background()
	d := domain.NewDelivery(camp, "r1", domain.ChannelWhatsApp, domain.KindProposal, now)
	repo.Create(ctx, camp, d)
	for _, s := range []domain.DeliveryStatus{domain.StatusQueued} {
		repo.Transition(ctx, camp, domain.Transition{Key: d.Key, To: s, Source: domain.SourceSystem, At: now})
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	applied := 0
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := repo.Transition(ctx, camp, domain.Transition{Key: d.Key, To: domain.StatusSending, Source: domain.SourceWorker, At: now})
			if err != nil {
				t.Error(err)
				return
			}
			if r.Outcome == domain.OutcomeApplied {
				mu.Lock()
				applied++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	got, _ := repo.Get(ctx, camp, d.Key)
	c, _ := rdb.HGetAll(ctx, countersKey(camp)).Result()
	if applied != 1 || got.Attempts != 1 || c["whatsapp:SENDING"] != "1" || c["whatsapp:QUEUED"] != "0" {
		t.Errorf("applied=%d attempts=%d contadores=%v", applied, got.Attempts, c)
	}
}

func TestErroresDeEntrada(t *testing.T) {
	repo, _, camp := setup(t)
	ctx := context.Background()
	if _, err := repo.Transition(ctx, camp, domain.Transition{Key: "nope", To: domain.StatusQueued, Source: domain.SourceSystem}); !errors.Is(err, ports.ErrDeliveryNotFound) {
		t.Errorf("esperaba ErrDeliveryNotFound, fue %v", err)
	}
	if _, err := repo.Get(ctx, camp, "nope"); !errors.Is(err, ports.ErrDeliveryNotFound) {
		t.Errorf("Get: %v", err)
	}
	if _, err := repo.Transition(ctx, camp, domain.Transition{Key: "k", To: "RARO", Source: domain.SourceSystem}); err == nil {
		t.Error("estado inválido debe fallar antes de llamar a Redis")
	}
}

func TestListYCounters(t *testing.T) {
	repo, _, camp := setup(t)
	ctx := context.Background()
	for _, r := range []string{"r2", "r1"} {
		d := domain.NewDelivery(camp, r, domain.ChannelWhatsApp, domain.KindProposal, now)
		repo.Create(ctx, camp, d)
	}
	e := domain.NewDelivery(camp, "r1", domain.ChannelEmail, domain.KindProposal, now)
	repo.Create(ctx, camp, e)
	repo.Transition(ctx, camp, domain.Transition{Key: e.Key, To: domain.StatusSkipped, Source: domain.SourceSystem, At: now, Detail: "sin correo"})

	list, err := repo.List(ctx, camp)
	if err != nil || len(list) != 3 {
		t.Fatalf("List: %v %v", list, err)
	}
	if list[0].Key > list[1].Key || list[1].Key > list[2].Key {
		t.Errorf("deben venir ordenadas por clave: %v %v %v", list[0].Key, list[1].Key, list[2].Key)
	}
	c, err := repo.Counters(ctx, camp)
	if err != nil || c["whatsapp:total"] != 2 || c["whatsapp:PENDING"] != 2 || c["email:total"] != 1 || c["email:SKIPPED"] != 1 || c["email:PENDING"] != 0 {
		t.Errorf("contadores: %v %v", c, err)
	}
	if l, err := repo.List(ctx, "otra"); err != nil || len(l) != 0 {
		t.Errorf("campaña vacía: %v %v", l, err)
	}
}

func TestCuotaDiariaAtomica(t *testing.T) {
	_, rdb, camp := setup(t)
	q := NewQuota(rdb)
	ctx := context.Background()
	day := "d" + camp
	for i := 0; i < 3; i++ {
		if ok, err := q.Take(ctx, day, 3); err != nil || !ok {
			t.Fatalf("envío %d: %v %v", i, ok, err)
		}
	}
	if ok, _ := q.Take(ctx, day, 3); ok {
		t.Error("el 4.º envío del día debe rechazarse")
	}
	// El contador no se pasa del límite aunque insistan.
	if n, _ := rdb.Get(ctx, keyPrefix+"quota:gmail:"+day).Int(); n != 3 {
		t.Errorf("contador=%d", n)
	}
	if ttl, _ := rdb.TTL(ctx, keyPrefix+"quota:gmail:"+day).Result(); ttl <= 0 || ttl > 48*time.Hour {
		t.Errorf("ttl=%v", ttl)
	}
	// Carrera: 60 intentos con límite 10 → exactamente 10.
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < 60; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if good, err := q.Take(ctx, day+"b", 10); err == nil && good {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ok != 10 {
		t.Errorf("concedidos=%d, debían ser 10", ok)
	}
}
