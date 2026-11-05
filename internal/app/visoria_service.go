package app

import (
	"context"
	"fmt"
	"mime/multipart"
	"strconv"
	"strings"
	"time"

	"visoria-control/internal/domain"
	"visoria-control/internal/platform/log"
	"visoria-control/internal/ports"
)

// Options agrupa los valores de configuración que necesita el servicio
// (los provee platform/config; el servicio no lee el entorno).
type Options struct {
	BaseURL       string
	WhatsAppPause time.Duration
}

type visoriaService struct {
	repo     ports.TournamentRepository
	pdfGen   ports.PDFGenerator
	docs     ports.DocumentStore
	waAPI    ports.WhatsAppAPI
	chat     ports.ChatwootClient
	importer *ImportService
	opts     Options
}

func NewVisoriaService(repo ports.TournamentRepository, pdfGen ports.PDFGenerator, docs ports.DocumentStore, waAPI ports.WhatsAppAPI, chat ports.ChatwootClient, importer *ImportService, opts Options) ports.VisoriaService {
	return &visoriaService{
		repo:     repo,
		pdfGen:   pdfGen,
		docs:     docs,
		waAPI:    waAPI,
		chat:     chat,
		importer: importer,
		opts:     opts,
	}
}

func (s *visoriaService) ProcessPlayersExcel(ctx context.Context, file multipart.File) ([]domain.Player, domain.BatchSummary, error) {
	res, err := s.importer.Import(ctx, file, domain.ValidationOptions{})
	if err != nil {
		return nil, domain.BatchSummary{}, err
	}
	players := make([]domain.Player, 0, len(res.Recipients))
	for _, r := range res.Recipients {
		players = append(players, s.toPlayer(ctx, r))
	}
	return players, res.Summary, nil
}

// toPlayer adapta un destinatario validado al modelo del flujo actual (se retira cuando
// el flujo pase a las colas, Fases 4-5).
func (s *visoriaService) toPlayer(ctx context.Context, r domain.Recipient) domain.Player {
	p := domain.Player{
		Name:         r.Jugador,
		Club:         r.Club,
		GuardianName: r.Acudiente,
		PrimaryPhone: domain.NationalPhone(r.Movil),
		Email:        strings.Join(r.Correos, ", "),
		Scholarship:  domain.BecaLabel(r.Beca),
		BirthYear:    r.Anio,
		FileID:       r.ID,
		Issues:       r.Issues,
	}
	if r.Anio != 0 {
		p.BirthDate = strconv.Itoa(r.Anio)
		p.Tournament, _ = s.repo.GetTournamentForPlayer(ctx, r.Anio, p.Scholarship)
	}
	switch r.Estado {
	case domain.RecipientValid:
		p.Status = "PENDING"
	case domain.RecipientOmitted:
		p.Status = "OMITTED"
	default:
		p.Status = "INVALID_DATA"
		for _, i := range r.Issues {
			if i.Nivel == domain.IssueError && (i.Campo == domain.CampoTorneo || i.Campo == domain.CampoCategoria) {
				p.Status = "INVALID_MATCH"
			}
		}
	}
	return p
}

func (s *visoriaService) GenerateDocuments(ctx context.Context, players []domain.Player) ([]string, error) {
	var generatedPaths []string

	for _, p := range players {
		if p.Status != "PENDING" {
			continue
		}

		path, err := s.pdfGen.Generate(p, p.Tournament)
		if err != nil {
			log.FromContext(ctx).Error("error generando PDF", "file_id", p.FileID, "error", err)
			continue
		}
		generatedPaths = append(generatedPaths, path)
	}

	return generatedPaths, nil
}

func (s *visoriaService) DispatchWhatsAppMessages(ctx context.Context, players []domain.Player, progressChan chan<- string) error {
	lg := log.FromContext(ctx)
	for i, p := range players {
		if p.Status != "PENDING" {
			motivo := "datos inválidos"
			if p.Status == "OMITTED" {
				motivo = "SIN BECA, no se genera propuesta"
			}
			progressChan <- fmt.Sprintf("⚠️ Omitido %s: %s", p.Name, motivo)
			continue
		}

		if p.PrimaryPhone == "" { // solo tiene correo; el envío por correo llega en la Fase 6
			progressChan <- fmt.Sprintf("⚠️ Omitido %s: no tiene móvil (el envío por correo aún no está disponible)", p.Name)
			continue
		}

		// Nunca enviar si el PDF no existe: Meta respondería 200 y fallaría después al descargarlo.
		if ok, errPDF := s.docs.Exists(ctx, p); errPDF != nil || !ok {
			if errPDF != nil {
				lg.Error("no se pudo verificar el PDF", "file_id", p.FileID, "error", errPDF)
			}
			progressChan <- fmt.Sprintf("❌ Error: no se envió a %s porque su PDF no existe o no se generó", p.Name)
			continue
		}

		phone := strings.ReplaceAll(p.PrimaryPhone, " ", "")
		phone = strings.ReplaceAll(phone, "+", "")
		if len(phone) == 10 {
			phone = "57" + phone
		}

		msgInicio := fmt.Sprintf("[%d/%d] Enviando WhatsApp a %s...", i+1, len(players), p.Name)
		lg.Info("enviando whatsapp", "file_id", p.FileID, "index", i+1, "total", len(players))
		progressChan <- msgInicio

		becaNum := strings.ReplaceAll(p.Scholarship, "%", "")

		nombreSeguro := strings.ReplaceAll(p.Name, " ", "_")
		pdfURL := fmt.Sprintf("%s/pdfs/%s", s.opts.BaseURL, s.docs.FileName(p))

		components := []interface{}{
			map[string]interface{}{
				"type": "header",
				"parameters": []interface{}{
					map[string]interface{}{
						"type": "document",
						"document": map[string]string{
							"link":     pdfURL,
							"filename": fmt.Sprintf("Beca_%s.pdf", nombreSeguro),
						},
					},
				},
			},
			map[string]interface{}{
				"type": "body",
				"parameters": []interface{}{
					map[string]string{"type": "text", "text": p.GuardianName},
					map[string]string{"type": "text", "text": becaNum},
					map[string]string{"type": "text", "text": p.Name},
				},
			},
		}

		// 1. Envío de plantilla oficial por Meta API
		err := s.waAPI.SendTemplate(ctx, phone, "purchase_receipt_3", "es_CO", components)

		if err != nil {
			msgErr := fmt.Sprintf("❌ Error enviando a %s: %v", p.Name, err)
			lg.Error("fallo envío whatsapp", "file_id", p.FileID, "error", err)
			progressChan <- msgErr
		} else {
			msgOk := fmt.Sprintf("✅ Mensaje enviado a %s", p.Name)
			lg.Info("whatsapp aceptado por Meta", "file_id", p.FileID)
			progressChan <- msgOk

			// 2. Notificación y autorregistro en Chatwoot
			textoRegistro := fmt.Sprintf("📄 *Beca enviada por sistema*\n\nHola %s,\nTe hacemos entrega del documento oficial correspondiente a la beca del %s%% asignada a %s.\n\nPDF: %s", p.GuardianName, becaNum, p.Name, pdfURL)

			if errCw := s.chat.RegisterNote(ctx, phone, p.Name, textoRegistro); errCw != nil {
				lg.Warn("whatsapp enviado pero no registrado en Chatwoot", "file_id", p.FileID, "error", errCw)
			} else {
				lg.Info("nota registrada en Chatwoot", "file_id", p.FileID)
			}
		}

		time.Sleep(s.opts.WhatsAppPause)
	}

	return nil
}
