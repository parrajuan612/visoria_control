package main

import (
	"context"
	"log/slog"

	"visoria-control/internal/adapters/asynqueue"
	"visoria-control/internal/app"
	"visoria-control/internal/ports"
)

// registerHandlers conecta cada tipo de tarea con su caso de uso. Las tareas de
// negocio (wa:send, email:send, doc:render…) se registran aquí en las fases 4 a 6.
func registerHandlers(w *asynqueue.Worker, lg *slog.Logger, docs *app.DocumentService, dispatch *app.DispatchService) {
	w.Handle(ports.TaskOpsPing, func(ctx context.Context, payload []byte) error {
		lg.Info("ops:ping recibido", "payload", string(payload))
		return nil
	})
	// Generación de propuestas (cola docs, concurrencia 1): docx en Go -> LibreOffice -> PDF.
	w.Handle(ports.TaskDocRender, docs.HandleRender)
	// Envío (colas whatsapp, email y side): el estado real de cada entrega vive en Redis.
	w.Handle(ports.TaskWASend, dispatch.HandleWhatsApp)
	w.Handle(ports.TaskEmailSend, dispatch.HandleEmail)
	w.Handle(ports.TaskNote, dispatch.HandleNote)
}
