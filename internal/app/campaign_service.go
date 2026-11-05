package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"visoria-control/internal/domain"
	"visoria-control/internal/platform/log"
	"visoria-control/internal/ports"
)

// CampaignService implementa ports.CampaignUseCase (SDD §4.2 y §4.3).
type CampaignService struct {
	reader ports.PlayerSheetReader
	cfg    ports.ConfigProvider
	repo   ports.CampaignRepo
	files  ports.CampaignFiles
	newID  func() string
	now    func() time.Time

	locks *keyedLocks // una edición a la vez por campaña (se comparte con la generación de documentos)
}

// keyedLocks da un candado por clave (ID de campaña).
type keyedLocks struct {
	mu sync.Mutex
	m  map[string]*sync.Mutex
}

func newKeyedLocks() *keyedLocks { return &keyedLocks{m: map[string]*sync.Mutex{}} }

// lock bloquea la clave y devuelve la función que la libera.
func (k *keyedLocks) lock(id string) func() {
	k.mu.Lock()
	m, ok := k.m[id]
	if !ok {
		m = &sync.Mutex{}
		k.m[id] = m
	}
	k.mu.Unlock()
	m.Lock()
	return m.Unlock
}

var _ ports.CampaignUseCase = (*CampaignService)(nil)

func NewCampaignService(reader ports.PlayerSheetReader, cfg ports.ConfigProvider, repo ports.CampaignRepo, files ports.CampaignFiles) *CampaignService {
	return &CampaignService{reader: reader, cfg: cfg, repo: repo, files: files, newID: randomID, now: time.Now, locks: newKeyedLocks()}
}

func input(format string, a ...any) error {
	return &ports.InputError{Message: fmt.Sprintf(format, a...)}
}

// Create lee y valida el Excel, congela la configuración vigente y guarda la campaña en DRAFT.
func (s *CampaignService) Create(ctx context.Context, in ports.CreateCampaignInput) (ports.CampaignDetail, error) {
	torneo, err := domain.NormalizeTorneo(in.Torneo)
	if err != nil {
		return ports.CampaignDetail{}, input("%v", err)
	}
	fechas, err := domain.ParseFechasPago(in.FechasPago)
	if err != nil {
		return ports.CampaignDetail{}, input("%v", err)
	}
	cfg := s.cfg.Current()
	if cfg == nil {
		return ports.CampaignDetail{}, ports.ErrNoConfig
	}
	rows, err := s.reader.Read(ctx, bytes.NewReader(in.Data))
	if err != nil {
		return ports.CampaignDetail{}, err
	}

	c := domain.Campaign{
		ID: s.newID(), Torneo: torneo, FechasPago: fechas, Canales: []domain.Channel{domain.ChannelWhatsApp, domain.ChannelEmail},
		Exigir2: in.RequireBoth, Status: domain.CampaignDraft, Config: *cfg, ConfigHash: cfg.Hash(),
		ExcelNombre: in.Filename, CreatedBy: in.By, CreatedAt: s.now().UTC(),
	}
	c.Dir = c.DirName()

	// Como el sistema del cliente: si el Excel trae becas que necesitan más fechas de las dadas, se detiene.
	if err := domain.CheckFechas(*cfg, domain.ValidateBatch(rows, *cfg, domain.ValidationOptions{RequireBothChannels: in.RequireBoth}), len(fechas)); err != nil {
		return ports.CampaignDetail{}, input("%v", err)
	}
	recs := domain.ValidateBatch(rows, *cfg, c.ValidationOptions())
	for i := range recs {
		recs[i].ID = s.newID()
	}
	summary := domain.Summarize(recs)

	// Primero los archivos (original y snapshot); luego Redis, que es lo que hace visible la campaña.
	if err := s.files.SaveOriginal(ctx, c, in.Data); err != nil {
		return ports.CampaignDetail{}, fmt.Errorf("guardando el Excel original: %w", err)
	}
	if err := s.files.SaveSnapshot(ctx, c); err != nil {
		return ports.CampaignDetail{}, fmt.Errorf("guardando la campaña: %w", err)
	}
	if err := s.repo.Create(ctx, c, recs, summary); err != nil {
		return ports.CampaignDetail{}, err
	}
	if err := s.files.Audit(ctx, c, domain.AuditEvent{Time: c.CreatedAt, Type: "campaign.created", CampaignID: c.ID, By: in.By,
		Detail: fmt.Sprintf("torneo=%q filas=%d validas=%d con_errores=%d omitidas=%d version_config=%s archivo=%q",
			c.Torneo, summary.Total, summary.Validas, summary.ConErrores, summary.Omitidas, c.ConfigHash, in.Filename)}); err != nil {
		log.FromContext(ctx).Error("no se pudo escribir la auditoría", "campaign_id", c.ID, "error", err)
	}
	log.FromContext(ctx).Info("campaña creada", "campaign_id", c.ID, "filas", summary.Total, "validas", summary.Validas,
		"con_errores", summary.ConErrores, "omitidas", summary.Omitidas)
	return ports.CampaignDetail{Campaign: domain.CampaignView{Campaign: c, Resumen: summary}, Recipients: recs}, nil
}

func (s *CampaignService) Get(ctx context.Context, id string) (ports.CampaignDetail, error) {
	c, err := s.repo.Get(ctx, id)
	if err != nil {
		return ports.CampaignDetail{}, err
	}
	recs, err := s.repo.Recipients(ctx, id)
	if err != nil {
		return ports.CampaignDetail{}, err
	}
	return ports.CampaignDetail{Campaign: domain.CampaignView{Campaign: c, Resumen: domain.Summarize(recs)}, Recipients: recs}, nil
}

func (s *CampaignService) List(ctx context.Context) ([]domain.CampaignView, error) {
	return s.repo.List(ctx, 100)
}

// EditRecipient aplica una corrección a las celdas de una fila y vuelve a validar TODO el lote
// con la configuración congelada de la campaña (un duplicado se puede resolver o crear al editar).
func (s *CampaignService) EditRecipient(ctx context.Context, campaignID, recipientID string, patch domain.RecipientPatch, by string) (ports.EditResult, error) {
	defer s.locks.lock(campaignID)()

	c, err := s.repo.Get(ctx, campaignID)
	if err != nil {
		return ports.EditResult{}, err
	}
	if !c.Editable() {
		return ports.EditResult{}, ports.ErrCampaignLocked
	}
	if patch.IsEmpty() {
		return ports.EditResult{}, input("No se envió ningún cambio.")
	}
	recs, err := s.repo.Recipients(ctx, campaignID)
	if err != nil {
		return ports.EditResult{}, err
	}
	idx := -1
	for i, r := range recs {
		if r.ID == recipientID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return ports.EditResult{}, ports.ErrRecipientNotFound
	}

	newRaw, changes, err := recs[idx].Raw.Apply(patch)
	if err != nil {
		return ports.EditResult{}, input("%v", err)
	}
	if len(changes) == 0 { // nada cambió realmente
		return ports.EditResult{Recipient: recs[idx], Otros: []domain.Recipient{}, Resumen: domain.Summarize(recs)}, nil
	}

	raws := make([]domain.RawRow, len(recs))
	for i, r := range recs {
		raws[i] = r.Raw
	}
	raws[idx] = newRaw
	updated := domain.ValidateBatch(raws, c.Config, c.ValidationOptions())

	var changed, otros []domain.Recipient
	for i := range updated {
		updated[i].ID = recs[i].ID
		updated[i].Editado = recs[i].Editado || i == idx
		if i != idx { // el documento de las demás filas sigue siendo válido: su contenido no cambió
			updated[i].Documento = recs[i].Documento
		} // la fila editada queda SIN documento: el PDF anterior ya no corresponde a sus datos (SDD §4.3)
		if i == idx || !sameJSON(updated[i], recs[i]) {
			changed = append(changed, updated[i])
			if i != idx {
				otros = append(otros, updated[i])
			}
		}
	}
	summary := domain.Summarize(updated)
	if err := s.repo.SaveRecipients(ctx, campaignID, changed, summary); err != nil {
		return ports.EditResult{}, err
	}

	now := s.now().UTC()
	for _, ch := range changes {
		ev := domain.AuditEvent{Time: now, Type: "recipient.edited", CampaignID: campaignID, RecipientID: recipientID,
			Fila: recs[idx].Fila, Field: ch.Field, Old: ch.Old, New: ch.New, By: by,
			Detail: fmt.Sprintf("estado %s -> %s", recs[idx].Estado, updated[idx].Estado)}
		if recs[idx].Documento != nil {
			ev.Detail += "; el documento generado quedó obsoleto"
		}
		if err := s.files.Audit(ctx, c, ev); err != nil {
			// La corrección ya está guardada: no se revierte, pero queda registrado el fallo.
			log.FromContext(ctx).Error("no se pudo escribir la auditoría", "campaign_id", campaignID, "error", err)
		}
	}
	if otros == nil {
		otros = []domain.Recipient{}
	}
	return ports.EditResult{Recipient: updated[idx], Otros: otros, Resumen: summary}, nil
}

func sameJSON(a, b domain.Recipient) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return bytes.Equal(ja, jb)
}

// IsNotFound agrupa los errores de "no existe" para que los adaptadores respondan 404.
func IsNotFound(err error) bool {
	return errors.Is(err, ports.ErrCampaignNotFound) || errors.Is(err, ports.ErrRecipientNotFound)
}
