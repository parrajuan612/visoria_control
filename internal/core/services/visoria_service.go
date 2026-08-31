package services

import (
	"context"
	"fmt"
	"mime/multipart"
	"strconv"
	"strings"
	"time"

	"visoria-control/internal/core/domain"
	"visoria-control/internal/core/ports"

	"github.com/xuri/excelize/v2"
)

type visoriaService struct {
	repo   ports.TournamentRepository
	pdfGen ports.PDFGenerator
	waAPI  ports.WhatsAppAPI
}

func NewVisoriaService(repo ports.TournamentRepository, pdfGen ports.PDFGenerator, waAPI ports.WhatsAppAPI) ports.VisoriaService {
	return &visoriaService{
		repo:   repo,
		pdfGen: pdfGen,
		waAPI:  waAPI,
	}
}

func (s *visoriaService) LoadMasterConfig(ctx context.Context, csvURL string) error {
	return s.repo.LoadConfigFromCSV(ctx, csvURL)
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
		parsedDate := parseBirthDate(fechaNac)
		if !parsedDate.IsZero() {
			anio = parsedDate.Year()
		} else {
			if len(fechaNac) >= 4 {
				anio, _ = strconv.Atoi(fechaNac[:4])
			}
		}

		torneoInfo, _ := s.repo.GetTournamentForPlayer(ctx, anio, beca)

		player := domain.Player{
			Name:         getCol(0),
			GuardianName: getCol(4),
			PrimaryPhone: getCol(5),
			Scholarship:  beca,
			BirthYear:    anio,
			Status:       "PENDING",
			Tournament:   torneoInfo,
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
			fmt.Printf("Error generando PDF para %s: %v\n", p.Name, err)
			continue
		}
		generatedPaths = append(generatedPaths, path)
	}

	return generatedPaths, nil
}

func (s *visoriaService) DispatchWhatsAppMessages(ctx context.Context, players []domain.Player, progressChan chan<- string) error {
	for i, p := range players {
		if p.Status != "PENDING" {
			continue
		}

		phone := strings.ReplaceAll(p.PrimaryPhone, " ", "")
		phone = strings.ReplaceAll(phone, "+", "")
		if len(phone) == 10 {
			phone = "57" + phone
		}

		msgInicio := fmt.Sprintf("[%d/%d] Enviando WhatsApp a %s...", i+1, len(players), p.Name)
		fmt.Println(msgInicio)
		progressChan <- msgInicio

		becaNum := strings.ReplaceAll(p.Scholarship, "%", "")

		components := []interface{}{
			map[string]interface{}{
				"type": "header",
				"parameters": []interface{}{
					map[string]interface{}{
						"type": "document",
						"document": map[string]string{
							// Le quitamos el "Beca_" al link para que busque el PDF nuevo
							"link": fmt.Sprintf("https://porthole-cross-cassette.ngrok-free.dev/pdfs/%s.pdf", strings.ReplaceAll(p.Name, " ", "_")),
							// El filename sí puede llevar el "Beca_" porque es solo el nombre visual con el que le llega al cliente
							"filename": fmt.Sprintf("Beca_%s.pdf", strings.ReplaceAll(p.Name, " ", "_")),
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

		err := s.waAPI.SendTemplate(context.Background(), phone, "purchase_receipt_3", "es_CO", components)

		if err != nil {
			msgErr := fmt.Sprintf("❌ Error enviando a %s: %v", p.Name, err)
			fmt.Println(msgErr)
			progressChan <- msgErr
		} else {
			msgOk := fmt.Sprintf("✅ Mensaje enviado a %s", p.Name)
			fmt.Println(msgOk)
			progressChan <- msgOk
		}

		time.Sleep(3 * time.Second)
	}

	return nil
}
