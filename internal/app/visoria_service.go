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

	"github.com/xuri/excelize/v2"
)

// Options agrupa los valores de configuración que necesita el servicio
// (los provee platform/config; el servicio no lee el entorno).
type Options struct {
	BaseURL       string
	WhatsAppPause time.Duration
}

type visoriaService struct {
	repo   ports.TournamentRepository
	pdfGen ports.PDFGenerator
	docs   ports.DocumentStore
	waAPI  ports.WhatsAppAPI
	chat   ports.ChatwootClient
	opts   Options
}

func NewVisoriaService(repo ports.TournamentRepository, pdfGen ports.PDFGenerator, docs ports.DocumentStore, waAPI ports.WhatsAppAPI, chat ports.ChatwootClient, opts Options) ports.VisoriaService {
	return &visoriaService{
		repo:   repo,
		pdfGen: pdfGen,
		docs:   docs,
		waAPI:  waAPI,
		chat:   chat,
		opts:   opts,
	}
}

func (s *visoriaService) ProcessPlayersExcel(ctx context.Context, file multipart.File) ([]domain.Player, error) {

	f, err := excelize.OpenReader(file)
	if err != nil {
		return nil, fmt.Errorf("error al abrir el archivo Excel: %w", err)
	}
	defer f.Close()

	sheetName := f.GetSheetList()[0]
	rows, err := f.GetRows(sheetName)
	if err != nil {
		return nil, fmt.Errorf("error leyendo las filas de la hoja %s: %w", sheetName, err)
	}

	var players []domain.Player

	for i, row := range rows {
		if i == 0 {
			continue
		}
		if len(row) < 4 {
			continue
		}

		getCol := func(idx int) string {
			if idx < len(row) {
				return strings.TrimSpace(row[idx])
			}
			return ""
		}

		becaRaw := getCol(2)
		beca := becaRaw
		if becaRaw != "" && becaRaw != "ACOMPAÑANTE" && becaRaw != "SIN BECA" && !strings.Contains(becaRaw, "%") {
			beca = becaRaw + "%"
		}

		fechaNac := getCol(3)
		var anio int
		var fechaNacFormatted string

		parsedDate := parseBirthDate(fechaNac)
		if !parsedDate.IsZero() {
			anio = parsedDate.Year()
			fechaNacFormatted = parsedDate.Format("02/01/2006")
		} else {
			if len(fechaNac) >= 4 {
				anio, _ = strconv.Atoi(fechaNac[:4])
			}
			fechaNacFormatted = fechaNac
		}

		torneoInfo, _ := s.repo.GetTournamentForPlayer(ctx, anio, beca)

		player := domain.Player{
			Name:         getCol(0),
			Club:         getCol(1),
			GuardianName: getCol(4),
			PrimaryPhone: getCol(5),
			Scholarship:  beca,
			BirthYear:    anio,
			BirthDate:    fechaNacFormatted,
			Status:       "PENDING",
			Tournament:   torneoInfo,
			FileID:       fmt.Sprintf("%d", time.Now().UnixNano()),
		}

		if player.Name == "" || player.PrimaryPhone == "" || anio == 0 {
			player.Status = "INVALID_DATA"
		} else if player.Tournament.Name == "" || player.Tournament.Pricing.Total == "No definido" {
			player.Status = "INVALID_MATCH"
		}

		players = append(players, player)
	}

	return players, nil
}

func parseBirthDate(value string) time.Time {
	formats := []string{
		"01-02-06",
		"01-02-2006",
		"02/01/2006",
		"02-01-2006",
		"2006-01-02",
		"01/02/06",
	}

	for _, format := range formats {
		if date, err := time.Parse(format, value); err == nil {
			return date
		}
	}
	return time.Time{}
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
			progressChan <- fmt.Sprintf("⚠️ Omitido %s: datos inválidos (%s)", p.Name, p.Status)
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
