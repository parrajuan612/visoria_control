package redisstore

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"visoria-control/internal/domain"
	"visoria-control/internal/ports"
)

func sampleCampaign(id string, at time.Time) (domain.Campaign, []domain.Recipient) {
	cfg := domain.MasterConfig{
		Becas:      []domain.Beca{{Clave: "30", Nombre: "30%", Total: 196000, Pagos: []domain.Money{65000, 65000, 66000}, Fila: 3}},
		Torneos:    []domain.TorneoRango{{Anios: domain.YearRange{Min: 2008, Max: 2014}, Nombre: "T"}},
		Categorias: []domain.CategoriaRango{{Anios: domain.YearRange{Min: 2008, Max: 2014}, Nombre: "JUVENIL", Plantilla: "cadete_juvenil"}},
	}
	c := domain.Campaign{ID: id, Torneo: "Copa X", FechasPago: []string{"2026-12-01"}, Status: domain.CampaignDraft, Config: cfg,
		ConfigHash: cfg.Hash(), CreatedBy: "operador", CreatedAt: at, Canales: []domain.Channel{domain.ChannelWhatsApp}}
	c.Dir = c.DirName()
	recs := []domain.Recipient{
		{ID: "r3", Fila: 4, Jugador: "C", Estado: domain.RecipientValid, Raw: domain.RawRow{Fila: 4, Jugador: "C"}},
		{ID: "r1", Fila: 2, Jugador: "A", Estado: domain.RecipientInvalid, Issues: []domain.Issue{{Nivel: domain.IssueError, Campo: "X", Mensaje: "m"}}},
		{ID: "r2", Fila: 3, Jugador: "B", Estado: domain.RecipientOmitted},
	}
	return c, recs
}

func TestCampaignRepo(t *testing.T) {
	_, rdb, _ := setup(t)
	repo := NewCampaignRepo(rdb)
	ctx := context.Background()
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)

	if _, err := repo.Get(ctx, "nope"); !errors.Is(err, ports.ErrCampaignNotFound) {
		t.Fatalf("inexistente: %v", err)
	}
	c, recs := sampleCampaign("camp1", now)
	sum := domain.Summarize(recs)
	if err := repo.Create(ctx, c, recs, sum); err != nil {
		t.Fatal(err)
	}

	// Ida y vuelta: incluye la configuración congelada y la carpeta.
	got, err := repo.Get(ctx, "camp1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Torneo != "Copa X" || got.Dir != c.Dir || got.Status != domain.CampaignDraft || got.Config.Hash() != c.Config.Hash() ||
		len(got.FechasPago) != 1 || !got.CreatedAt.Equal(now) || got.ConfigHash != c.ConfigHash {
		t.Errorf("campaña: %+v", got)
	}
	// Los destinatarios vuelven ordenados por fila.
	rs, err := repo.Recipients(ctx, "camp1")
	if err != nil || len(rs) != 3 || rs[0].ID != "r1" || rs[1].ID != "r2" || rs[2].ID != "r3" || len(rs[0].Issues) != 1 {
		t.Errorf("destinatarios: %+v %v", rs, err)
	}

	// SaveRecipients solo reemplaza lo indicado y actualiza el resumen.
	rs[0].Estado, rs[0].Issues, rs[0].Editado = domain.RecipientValid, nil, true
	newSum := domain.Summarize([]domain.Recipient{rs[0], rs[1], rs[2]})
	if err := repo.SaveRecipients(ctx, "camp1", []domain.Recipient{rs[0]}, newSum); err != nil {
		t.Fatal(err)
	}
	rs2, _ := repo.Recipients(ctx, "camp1")
	if rs2[0].Estado != domain.RecipientValid || !rs2[0].Editado || rs2[2].ID != "r3" || len(rs2) != 3 {
		t.Errorf("tras guardar: %+v", rs2)
	}

	// Un segundo lote para comprobar el orden del listado (más reciente primero) y que el resumen persiste.
	c2, recs2 := sampleCampaign("camp2", now.Add(time.Hour))
	if err := repo.Create(ctx, c2, recs2, domain.Summarize(recs2)); err != nil {
		t.Fatal(err)
	}
	list, err := repo.List(ctx, 10)
	if err != nil || len(list) != 2 || list[0].ID != "camp2" || list[1].ID != "camp1" {
		t.Fatalf("listado: %+v %v", list, err)
	}
	if list[1].Resumen.Validas != 2 || list[1].Resumen.ConErrores != 0 || list[1].Resumen.Omitidas != 1 {
		t.Errorf("el listado debe traer el resumen actualizado: %+v", list[1].Resumen)
	}
	if len(list[0].Config.Becas) != 0 {
		t.Error("el listado no debe traer la configuración (es pesada)")
	}
	if l, _ := repo.List(ctx, 1); len(l) != 1 || l[0].ID != "camp2" {
		t.Errorf("límite: %+v", l)
	}

	// No se puede escribir en una campaña que no existe (HSET crearía una a medias).
	if err := repo.SaveRecipients(ctx, "fantasma", rs, newSum); !errors.Is(err, ports.ErrCampaignNotFound) {
		t.Errorf("campaña inexistente: %v", err)
	}
	if n, _ := rdb.Exists(ctx, recipientsKey("fantasma")).Result(); n != 0 {
		t.Error("no debe quedar basura en Redis")
	}
	// JSON corrupto → error claro, sin pánico.
	rdb.HSet(ctx, campaignKey("camp1"), "meta", "{roto")
	if _, err := repo.Get(ctx, "camp1"); err == nil {
		t.Error("meta corrupta debe fallar")
	}
}

func TestCampaignRepoGrande(t *testing.T) {
	_, rdb, _ := setup(t)
	repo := NewCampaignRepo(rdb)
	ctx := context.Background()
	c, _ := sampleCampaign("grande", time.Now())
	var recs []domain.Recipient
	for i := 0; i < 5000; i++ {
		recs = append(recs, domain.Recipient{ID: fmt.Sprintf("id%05d", i), Fila: i + 2, Jugador: "J", Estado: domain.RecipientValid})
	}
	if err := repo.Create(ctx, c, recs, domain.Summarize(recs)); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Recipients(ctx, "grande")
	if err != nil || len(got) != 5000 || got[0].Fila != 2 || got[4999].Fila != 5001 {
		t.Errorf("5000 filas: %d %v", len(got), err)
	}
}
