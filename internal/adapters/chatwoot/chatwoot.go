package chatwoot

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type ChatwootClient struct {
	BaseURL    string // p. ej. "http://127.0.0.1:3000" (llamada local dentro del servidor)
	AccountID  int    // ID de cuenta de Chatwoot (normalmente es 1)
	APIToken   string // Token de acceso de agente/bot
	HTTPClient *http.Client
}

type MessagePayload struct {
	Content     string `json:"content"`
	MessageType string `json:"message_type"` // Debe ser "outgoing"
	Private     bool   `json:"private"`      // false para que sea visible
}

func NewChatwootClient(baseURL string, accountID int, apiToken string) *ChatwootClient {
	return &ChatwootClient{
		BaseURL:   baseURL,
		AccountID: accountID,
		APIToken:  apiToken,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// RegistrarMensajeSaliente publica en Chatwoot el mensaje que acaba de enviar tu sistema
func (c *ChatwootClient) RegistrarMensajeSaliente(conversationID int, contenido string) error {
	url := fmt.Sprintf("%s/api/v1/accounts/%d/conversations/%d/messages", c.BaseURL, c.AccountID, conversationID)

	payload := MessagePayload{
		Content:     contenido,
		MessageType: "outgoing",
		Private:     false,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("error al serializar JSON: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return fmt.Errorf("error creando petición HTTP: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api_access_token", c.APIToken)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("error enviando petición a Chatwoot: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Chatwoot respondió con código de estado HTTP %d", resp.StatusCode)
	}

	return nil
}
