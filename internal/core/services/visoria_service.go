package services

import (
	"context"
	"fmt"
	"mime/multipart"
	"strconv"
	"strings"
	"time" // ¡Añadir este!

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

// Estos los llenaremos en el próximo sprint
func (s *visoriaService) ProcessPlayersExcel(ctx context.Context, file multipart.File) ([]domain.Player, error) {

	// Abrimos el archivo excel usando la librería
	f, err := excelize.OpenReader(file)
	if err != nil {
		return nil, fmt.Errorf("error al abrir el archivo Excel: %w", err)
	}
	defer f.Close()

	// Asumimos que los datos están en la primera hoja ("Sheet1" o "Hoja1")
	sheetName := f.GetSheetList()[0]
	rows, err := f.GetRows(sheetName)
	if err != nil {
		return nil, fmt.Errorf("error leyendo las filas de la hoja %s: %w", sheetName, err)
	}

	var players []domain.Player

	// Iteramos sobre las filas del Excel del cliente
	for i, row := range rows {
		if i == 0 {
			continue // Saltar cabeceras
		}
		if len(row) < 4 {
			continue // Al menos necesitamos hasta la fecha de nacimiento
		}

		// Helper para no salirnos de los límites del arreglo
		getCol := func(idx int) string {
			if idx < len(row) {
				return strings.TrimSpace(row[idx])
			}
			return ""
		}

		// 1. Extraer Beca
		becaRaw := getCol(2)
		beca := becaRaw
		if becaRaw != "" && becaRaw != "ACOMPAÑANTE" && becaRaw != "SIN BECA" && !strings.Contains(becaRaw, "%") {
			beca = becaRaw + "%"
		}

		// 2. Extraer Año de Nacimiento (USANDO TU LÓGICA ROBUSTA)
		fechaNac := getCol(3)
		var anio int
		parsedDate := parseBirthDate(fechaNac)
		if !parsedDate.IsZero() {
			anio = parsedDate.Year()
		} else {
			// Intento salvavidas por si solo escribieron el año directo "2013"
			if len(fechaNac) >= 4 {
				anio, _ = strconv.Atoi(fechaNac[:4])
			}
		}

		// AQUÍ SUCEDE EL CRUCE DE DATOS CON EL GOOGLE SHEETS
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

		// VALIDACIONES DE NEGOCIO IMPORTANTES
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
		"01/02/06", // Añadido por si acaso
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
		if p.Status != "PENDING" { // Solo generar para los válidos
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

func (s *visoriaService) DispatchWhatsAppMessages(ctx context.Context, players []domain.Player) error {
	for i, p := range players {
		if p.Status != "PENDING" {
			continue
		}

		// 1. Limpieza y formato del número de teléfono
		phone := strings.ReplaceAll(p.PrimaryPhone, " ", "")
		phone = strings.ReplaceAll(phone, "+", "")
		// Si el número tiene 10 dígitos (típico celular colombiano), le agregamos el código de país 57
		if len(phone) == 10 {
			phone = "57" + phone
		}

		fmt.Printf("[%d/%d] Enviando WhatsApp a %s (%s)...\n", i+1, len(players), p.Name, phone)

		// 2. Configuración de Componentes de la Plantilla
		// IMPORTANTE: Si tu plantilla en Meta NO tiene variables (como {{1}}),
		// puedes dejar components vacío: components := []interface{}{}

		// Ejemplo ASUMIENDO que tu plantilla tiene 1 variable para el nombre del acudiente:
		// Hola {{1}}, te escribimos para...
		components := []interface{}{
			map[string]interface{}{
				"type": "body",
				"parameters": []interface{}{
					map[string]string{
						"type": "text",
						"text": p.GuardianName, // O p.Name si la plantilla saluda al jugador
					},
				},
			},
		}

		// 3. Envío usando el nombre exacto de la plantilla y el idioma
		// Asegúrate de que el nombre de la plantilla sea exactamente como está en Meta
		err := s.waAPI.SendTemplate(ctx, phone, "envio_certificado_beca", "es_CO", components)

		if err != nil {
			fmt.Printf("❌ Error enviando a %s: %v\n", p.Name, err)
		} else {
			fmt.Printf("✅ Mensaje enviado a %s\n", p.Name)
		}

		// Pausa de seguridad (Meta rate limits)
		time.Sleep(3 * time.Second)
	}

	return nil
}
