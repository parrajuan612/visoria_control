package main

import (
	"context"
	"log/slog"

	"visoria-control/internal/adapters/asynqueue"
	"visoria-control/internal/ports"
)

// registerHandlers conecta cada tipo de tarea con su caso de uso. Las tareas de
// negocio (wa:send, email:send, doc:render…) se registran aquí en las fases 4 a 6.
func registerHandlers(w *asynqueue.Worker, lg *slog.Logger) {
	w.Handle(ports.TaskOpsPing, func(ctx context.Context, payload []byte) error {
		lg.Info("ops:ping recibido", "payload", string(payload))
		return nil
	})
}
