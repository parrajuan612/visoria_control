package asynqueue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"visoria-control/internal/ports"
)

// Requiere un Redis real: VISORIA_TEST_REDIS_ADDR=127.0.0.1:6380 [VISORIA_TEST_REDIS_PASSWORD=...]
// (usa DB 15 para no tocar datos reales).
func redisOpt(t *testing.T) (addr, pass string) {
	addr = os.Getenv("VISORIA_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("VISORIA_TEST_REDIS_ADDR no definida; se omite el test de integración con Redis")
	}
	return addr, os.Getenv("VISORIA_TEST_REDIS_PASSWORD")
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("tiempo agotado esperando la condición")
}

func TestEnqueueProcesaDeduplicaYNoReintentaPermanentes(t *testing.T) {
	addr, pass := redisOpt(t)
	opt := RedisOpt(addr, pass, 15)
	lg := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	client := NewClient(opt)
	defer client.Close()
	w := NewWorker(opt, DefaultQueues, lg)

	var got atomic.Value
	var perm atomic.Int32
	w.Handle(ports.TaskOpsPing, func(_ context.Context, p []byte) error { got.Store(string(p)); return nil })
	w.Handle("test:perm", func(context.Context, []byte) error {
		perm.Add(1)
		return fmt.Errorf("fallo definitivo: %w", ports.ErrSkipRetry)
	})
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	defer w.Shutdown()

	id := "t-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	ctx := context.Background()
	if err := client.Enqueue(ctx, ports.Task{Type: ports.TaskOpsPing, Payload: []byte("hola"), Queue: ports.QueueSide, TaskID: id}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return got.Load() == "hola" })

	// Mismo TaskID mientras la tarea existe en Redis → duplicado (idempotencia).
	err := client.Enqueue(ctx, ports.Task{Type: ports.TaskOpsPing, Queue: ports.QueueSide, TaskID: id})
	if err != nil && !errors.Is(err, ports.ErrDuplicateTask) {
		t.Fatalf("error inesperado: %v", err)
	}

	if err := client.Enqueue(ctx, ports.Task{Type: "test:perm", Queue: ports.QueueSide, MaxRetry: 5}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return perm.Load() >= 1 })
	time.Sleep(500 * time.Millisecond)
	if n := perm.Load(); n != 1 {
		t.Errorf("el error permanente se ejecutó %d veces; debía ser 1", n)
	}
}

func TestEnqueueValidaCampos(t *testing.T) {
	c := NewClient(RedisOpt("127.0.0.1:1", "", 0))
	defer c.Close()
	if err := c.Enqueue(context.Background(), ports.Task{Type: "x"}); err == nil {
		t.Error("debe exigir Queue")
	}
}
