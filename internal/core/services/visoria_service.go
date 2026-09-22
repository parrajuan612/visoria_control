package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
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

		nombreSeguro := strings.ReplaceAll(p.Name, " ", "_")
		baseURL := os.Getenv("BASE_URL")
		if baseURL == "" {
			baseURL = "https://chatmajestic.duckdns.org"
		}

		pdfURL := fmt.Sprintf("%s/pdfs/%s_%s.pdf", baseURL, nombreSeguro, p.FileID)

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
		err := s.waAPI.SendTemplate(context.Background(), phone, "purchase_receipt_3", "es_CO", components)

		if err != nil {
			msgErr := fmt.Sprintf("❌ Error enviando a %s: %v", p.Name, err)
			fmt.Println(msgErr)
			progressChan <- msgErr
		} else {
			msgOk := fmt.Sprintf("✅ Mensaje enviado a %s", p.Name)
			fmt.Println(msgOk)
			progressChan <- msgOk

			// 2. Notificación y autorregistro en Chatwoot
			textoRegistro := fmt.Sprintf("📄 *Beca enviada por sistema*\n\nHola %s,\nTe hacemos entrega del documento oficial correspondiente a la beca del %s%% asignada a %s.\n\nPDF: %s", p.GuardianName, becaNum, p.Name, pdfURL)

			if errCw := s.registrarEnChatwoot(phone, p.Name, textoRegistro); errCw != nil {
				fmt.Printf("⚠️ Mensaje enviado por WhatsApp pero no se registró en Chatwoot (%s): %v\n", p.Name, errCw)
			} else {
				fmt.Printf("💬 Copia del mensaje registrada con éxito en Chatwoot para %s\n", p.Name)
			}
		}

		time.Sleep(6 * time.Second)
	}

	return nil
}

// registrarEnChatwoot busca o crea el contacto, asegura una conversación activa y registra el mensaje saliente
func (s *visoriaService) registrarEnChatwoot(phone string, playerName string, messageContent string) error {
	chatwootURL := os.Getenv("CHATWOOT_URL")
	if chatwootURL == "" {
		chatwootURL = "http://127.0.0.1:3000"
	}

	apiToken := os.Getenv("CHATWOOT_API_TOKEN")
	if apiToken == "" {
		return fmt.Errorf("CHATWOOT_API_TOKEN no está definido en las variables de entorno")
	}

	accountID := os.Getenv("CHATWOOT_ACCOUNT_ID")
	if accountID == "" {
		accountID = "1"
	}

	inboxIDStr := os.Getenv("CHATWOOT_INBOX_ID")
	if inboxIDStr == "" {
		inboxIDStr = "2"
	}
	inboxID, _ := strconv.Atoi(inboxIDStr)

	client := &http.Client{Timeout: 10 * time.Second}

	// 1. Buscar contacto por número telefónico
	searchURL := fmt.Sprintf("%s/api/v1/accounts/%s/contacts/search?q=%s", chatwootURL, accountID, phone)
	req, err := http.NewRequest("GET", searchURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("api_access_token", apiToken)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var searchResult struct {
		Payload []struct {
			ID int `json:"id"`
		} `json:"payload"`
	}

	var contactID int
	if json.NewDecoder(resp.Body).Decode(&searchResult) == nil && len(searchResult.Payload) > 0 {
		contactID = searchResult.Payload[0].ID
	}

	// 2. Si el contacto no existe, crearlo automáticamente
	if contactID == 0 {
		createContactURL := fmt.Sprintf("%s/api/v1/accounts/%s/contacts", chatwootURL, accountID)
		contactPayload := map[string]interface{}{
			"phone_number": "+" + phone,
			"name":         playerName,
			"inbox_id":     inboxID,
		}
		bodyBytes, _ := json.Marshal(contactPayload)
		reqCreate, err := http.NewRequest("POST", createContactURL, bytes.NewBuffer(bodyBytes))
		if err != nil {
			return fmt.Errorf("error creando petición de contacto: %w", err)
		}
		reqCreate.Header.Set("Content-Type", "application/json")
		reqCreate.Header.Set("api_access_token", apiToken)

		respCreate, err := client.Do(reqCreate)
		if err != nil {
			return fmt.Errorf("error al conectar con Chatwoot para crear contacto: %w", err)
		}
		defer respCreate.Body.Close()

		respBodyBytes, errRead := io.ReadAll(respCreate.Body)
		if errRead != nil {
			return fmt.Errorf("error leyendo respuesta de Chatwoot: %w", errRead)
		}

		fmt.Printf("🔍 [DEBUG CHATWOOT] Status: %d | Response: %s\n", respCreate.StatusCode, string(respBodyBytes))

		if respCreate.StatusCode >= 400 {
			return fmt.Errorf("chatwoot rechazó la creación (HTTP %d): %s", respCreate.StatusCode, string(respBodyBytes))
		}

		var createResult struct {
			Payload struct {
				Contact struct {
					ID int `json:"id"`
				} `json:"contact"`
				ID int `json:"id"`
			} `json:"payload"`
			Contact struct {
				ID int `json:"id"`
			} `json:"contact"`
			ID int `json:"id"`
		}

		if err := json.Unmarshal(respBodyBytes, &createResult); err != nil {
			return fmt.Errorf("error decodificando respuesta de Chatwoot: %w", err)
		}

		if createResult.Payload.Contact.ID != 0 {
			contactID = createResult.Payload.Contact.ID
		} else if createResult.Payload.ID != 0 {
			contactID = createResult.Payload.ID
		} else if createResult.Contact.ID != 0 {
			contactID = createResult.Contact.ID
		} else if createResult.ID != 0 {
			contactID = createResult.ID
		}

		if contactID == 0 {
			return fmt.Errorf("no se pudo extraer el ID del contacto del JSON recibido: %s", string(respBodyBytes))
		}
	}

	// 3. Obtener las conversaciones del contacto
	convURL := fmt.Sprintf("%s/api/v1/accounts/%s/contacts/%d/conversations", chatwootURL, accountID, contactID)
	reqConv, err := http.NewRequest("GET", convURL, nil)
	if err != nil {
		return err
	}
	reqConv.Header.Set("api_access_token", apiToken)

	respConv, err := client.Do(reqConv)
	if err != nil {
		return err
	}
	defer respConv.Body.Close()

	var convResult struct {
		Payload []struct {
			ID int `json:"id"`
		} `json:"payload"`
	}

	var conversationID int
	if json.NewDecoder(respConv.Body).Decode(&convResult) == nil && len(convResult.Payload) > 0 {
		conversationID = convResult.Payload[0].ID
	}

	// 4. Si no tiene una conversación activa, crear una nueva
	if conversationID == 0 {
		createConvURL := fmt.Sprintf("%s/api/v1/accounts/%s/conversations", chatwootURL, accountID)
		convPayload := map[string]interface{}{
			"contact_id": contactID,
			"inbox_id":   inboxID,
		}
		bodyBytes, _ := json.Marshal(convPayload)
		reqConvCreate, err := http.NewRequest("POST", createConvURL, bytes.NewBuffer(bodyBytes))
		if err != nil {
			return fmt.Errorf("error creando petición de conversación: %w", err)
		}
		reqConvCreate.Header.Set("Content-Type", "application/json")
		reqConvCreate.Header.Set("api_access_token", apiToken)

		respConvCreate, err := client.Do(reqConvCreate)
		if err != nil {
			return fmt.Errorf("error al conectar con Chatwoot para crear conversación: %w", err)
		}
		defer respConvCreate.Body.Close()

		var newConv struct {
			ID int `json:"id"`
		}
		if err := json.NewDecoder(respConvCreate.Body).Decode(&newConv); err != nil || newConv.ID == 0 {
			return fmt.Errorf("no se pudo crear la conversación en Chatwoot")
		}
		conversationID = newConv.ID
	}

	// 5. Registrar el mensaje saliente en la conversación
	msgURL := fmt.Sprintf("%s/api/v1/accounts/%s/conversations/%d/messages", chatwootURL, accountID, conversationID)
	msgPayload := map[string]interface{}{
		"content":      messageContent,
		"message_type": "outgoing",
		"private":      false,
	}

	bodyBytes, _ := json.Marshal(msgPayload)
	reqMsg, err := http.NewRequest("POST", msgURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return err
	}
	reqMsg.Header.Set("Content-Type", "application/json")
	reqMsg.Header.Set("api_access_token", apiToken)

	respMsg, err := client.Do(reqMsg)
	if err != nil {
		return err
	}
	defer respMsg.Body.Close()

	if respMsg.StatusCode >= 400 {
		return fmt.Errorf("respuesta de Chatwoot API: HTTP %d", respMsg.StatusCode)
	}

	return nil
}
