package redisstore

import (
	"context"
	"testing"
	"time"

	"visoria-control/internal/domain"
)

func TestConfigStore(t *testing.T) {
	_, rdb, _ := setup(t)
	s := NewConfigStore(rdb)
	ctx := context.Background()

	if snap, err := s.Current(ctx); err != nil || snap != nil {
		t.Fatalf("vacío: %v %v", snap, err)
	}
	if u, err := s.SourceURL(ctx); err != nil || u != "" {
		t.Fatalf("url vacía: %q %v", u, err)
	}

	in := domain.ConfigSnapshot{
		Hash: "abc", SyncedAt: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC),
		Config: domain.MasterConfig{
			Becas:      []domain.Beca{{Clave: "30", Nombre: "30%", Total: 196000, Pagos: []domain.Money{65000, 65000, 66000}, Fila: 3}},
			Torneos:    []domain.TorneoRango{{Anios: domain.YearRange{Min: 2008, Max: 2014}, Nombre: "T", Fila: 2}},
			Categorias: []domain.CategoriaRango{{Anios: domain.YearRange{Min: 2008, Max: 2010}, Nombre: "JUVENIL", Plantilla: "cadete_juvenil"}},
		},
		Issues: []domain.Issue{{Nivel: domain.IssueInfo, Campo: "Torneos", Mensaje: "x"}},
	}
	if err := s.Save(ctx, in); err != nil {
		t.Fatal(err)
	}
	out, err := s.Current(ctx)
	if err != nil || out == nil {
		t.Fatalf("Current: %v %v", out, err)
	}
	if out.Hash != "abc" || !out.SyncedAt.Equal(in.SyncedAt) || out.Config.Hash() != in.Config.Hash() || len(out.Issues) != 1 {
		t.Errorf("ida y vuelta: %+v", out)
	}
	if err := s.SetSourceURL(ctx, "https://docs.google.com/spreadsheets/x"); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.SourceURL(ctx); u != "https://docs.google.com/spreadsheets/x" {
		t.Errorf("url=%q", u)
	}
	// JSON corrupto → error claro, no pánico
	rdb.Set(ctx, configCurrentKey, "{no es json", 0)
	if _, err := s.Current(ctx); err == nil {
		t.Error("debía fallar con JSON corrupto")
	}
}
