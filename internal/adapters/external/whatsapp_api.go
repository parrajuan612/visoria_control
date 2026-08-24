package external

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"visoria-control/internal/core/ports"
)

type whatsappAPI struct {
	accessToken   string
	phoneNumberID string // ¡OJO! Este es el Phone Number ID, no el WABA ID
}

func NewWhatsAppAPI() ports.WhatsAppAPI {
	// Asegúrate de tener estas variables en tu archivo .env
	return &whatsappAPI{
		accessToken:   os.Getenv("WHATSAPP_TOKEN"),
		phoneNumberID: os.Getenv("WHATSAPP_PHONE_ID"),
	}
}

func (w *whatsappAPI) SendTemplate(ctx context.Context, phone string, templateName string, language string, components []interface{}) error {

	url := fmt.Sprintf("https://graph.facebook.com/v19.0/%s/messages", w.phoneNumberID)

	// Estructura oficial de Meta para enviar plantillas
	payload := map[string]interface{}{
		"messaging_product": "whatsapp",
		"to":                phone,
		"type":              "template",
		"template": map[string]interface{}{
			"name": templateName,
			"language": map[string]string{
				"code": language, // Aquí irá "es_CO"
			},
			"components": components,
		},
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("error construyendo payload de WhatsApp: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("error creando petición HTTP: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+w.accessToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error ejecutando petición HTTP a Meta: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Leer el error de Meta para saber qué falló
		var errResp map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&errResp)
		return fmt.Errorf("API Meta respondió con error (%d): %v", resp.StatusCode, errResp)
	}

	return nil
}
