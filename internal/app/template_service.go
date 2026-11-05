package app

import (
	"context"
	"fmt"

	"visoria-control/internal/domain"
	"visoria-control/internal/ports"
)

// TemplateService instala plantillas Word validándolas antes (app.py: /plantilla).
type TemplateService struct {
	store  ports.TemplateStore
	render ports.DocRenderer
}

var _ ports.TemplateUseCase = (*TemplateService)(nil)

func NewTemplateService(store ports.TemplateStore, render ports.DocRenderer) *TemplateService {
	return &TemplateService{store: store, render: render}
}

func (s *TemplateService) List(ctx context.Context) ([]ports.TemplateInfo, error) {
	return s.store.List(ctx)
}

// Install reemplaza la plantilla SOLO si conserva los campos que el programa reemplaza; la anterior
// queda guardada. El mensaje de rechazo es el del cliente.
func (s *TemplateService) Install(ctx context.Context, key string, data []byte) (string, error) {
	if domain.TemplateName(key) == "" {
		return "", input("Plantilla desconocida: %q.", key)
	}
	if msg := s.render.ValidateTemplate(data); msg != "" {
		return "", input("La plantilla no se cambió. %s", msg)
	}
	backup, err := s.store.Put(ctx, key, data)
	if err != nil {
		return "", fmt.Errorf("guardando la plantilla: %w", err)
	}
	return backup, nil
}
