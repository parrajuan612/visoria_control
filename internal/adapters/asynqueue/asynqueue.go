// Package asynqueue implementa ports.TaskQueue sobre asynq (ADR-1) y el
// consumidor (worker) con una concurrencia independiente por cola (SDD §3.7).
package asynqueue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"visoria-control/internal/ports"

	"github.com/hibiken/asynq"
)

const defaultMaxRetry = 5

// Client encola tareas.
type Client struct{ c *asynq.Client }

var _ ports.TaskQueue = (*Client)(nil)

func NewClient(opt asynq.RedisConnOpt) *Client { return &Client{c: asynq.NewClient(opt)} }

func (c *Client) Close() error { return c.c.Close() }

func (c *Client) Enqueue(ctx context.Context, t ports.Task) error {
	if t.Type == "" || t.Queue == "" {
		return errors.New("la tarea requiere Type y Queue")
	}
	retry := t.MaxRetry
	if retry == 0 {
		retry = defaultMaxRetry
	}
	opts := []asynq.Option{asynq.Queue(t.Queue), asynq.MaxRetry(retry)}
	if t.TaskID != "" {
		opts = append(opts, asynq.TaskID(t.TaskID))
	}
	if !t.ProcessAt.IsZero() {
		opts = append(opts, asynq.ProcessAt(t.ProcessAt))
	}
	_, err := c.c.EnqueueContext(ctx, asynq.NewTask(t.Type, t.Payload), opts...)
	switch {
	case errors.Is(err, asynq.ErrTaskIDConflict), errors.Is(err, asynq.ErrDuplicateTask):
		return ports.ErrDuplicateTask
	case err != nil:
		return fmt.Errorf("encolando %s: %w", t.Type, err)
	}
	return nil
}

// QueueSpec define la concurrencia de cada cola.
type QueueSpec struct {
	Name        string
	Concurrency int
}

// DefaultQueues sigue el SDD §3.7.
var DefaultQueues = []QueueSpec{
	{ports.QueueDocs, 1},
	{ports.QueueWhatsApp, 4},
	{ports.QueueEmail, 1},
	{ports.QueueSide, 2},
	{ports.QueueScheduled, 1},
}

// Worker consume las colas. Usa un asynq.Server por cola para que cada una
// tenga su propia concurrencia (asynq solo permite una por servidor).
type Worker struct {
	servers []*asynq.Server
	mux     *asynq.ServeMux
	log     *slog.Logger
}

func NewWorker(opt asynq.RedisConnOpt, queues []QueueSpec, lg *slog.Logger) *Worker {
	w := &Worker{mux: asynq.NewServeMux(), log: lg}
	for _, q := range queues {
		q := q
		w.servers = append(w.servers, asynq.NewServer(opt, asynq.Config{
			Concurrency:     q.Concurrency,
			Queues:          map[string]int{q.Name: 1},
			ShutdownTimeout: 30 * time.Second, // SDD §3.7: termina las tareas en curso
			Logger:          slogAdapter{lg.With("component", "asynq", "queue", q.Name)},
			RetryDelayFunc:  asynq.DefaultRetryDelayFunc, // backoff exponencial con jitter
			ErrorHandler: asynq.ErrorHandlerFunc(func(ctx context.Context, t *asynq.Task, err error) {
				retried, _ := asynq.GetRetryCount(ctx)
				max, _ := asynq.GetMaxRetry(ctx)
				lg.Error("tarea fallida", "queue", q.Name, "type", t.Type(), "retry", retried, "max_retry", max, "error", err)
			}),
		}))
	}
	return w
}

// Handle registra el handler de un tipo de tarea.
func (w *Worker) Handle(taskType string, h ports.TaskHandler) {
	w.mux.HandleFunc(taskType, func(ctx context.Context, t *asynq.Task) error {
		err := h(ctx, t.Payload())
		if err != nil && errors.Is(err, ports.ErrSkipRetry) {
			return fmt.Errorf("%w: %w", err, asynq.SkipRetry)
		}
		return err
	})
}

// Start arranca todos los servidores sin bloquear.
func (w *Worker) Start() error {
	for i, s := range w.servers {
		if err := s.Start(w.mux); err != nil {
			for _, started := range w.servers[:i] {
				started.Shutdown()
			}
			return fmt.Errorf("iniciando worker: %w", err)
		}
	}
	return nil
}

// Shutdown deja de tomar tareas y espera las que están en curso.
func (w *Worker) Shutdown() {
	for _, s := range w.servers {
		s.Shutdown()
	}
}

// slogAdapter adapta asynq.Logger a slog.
type slogAdapter struct{ l *slog.Logger }

func (a slogAdapter) Debug(args ...any) { a.l.Debug(fmt.Sprint(args...)) }
func (a slogAdapter) Info(args ...any)  { a.l.Info(fmt.Sprint(args...)) }
func (a slogAdapter) Warn(args ...any)  { a.l.Warn(fmt.Sprint(args...)) }
func (a slogAdapter) Error(args ...any) { a.l.Error(fmt.Sprint(args...)) }
func (a slogAdapter) Fatal(args ...any) { a.l.Error(fmt.Sprint(args...)) }

// RedisOpt arma la conexión de asynq desde la configuración.
func RedisOpt(addr, password string, db int) asynq.RedisConnOpt {
	return asynq.RedisClientOpt{Addr: addr, Password: password, DB: db}
}
