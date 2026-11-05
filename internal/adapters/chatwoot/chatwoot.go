// Package chatwoot es el único cliente de la API de Chatwoot. Implementa el
// puerto ports.ChatwootClient: deja constancia (nota privada) de lo enviado al
// acudiente sin que Chatwoot intente reenviarlo por WhatsApp.
package chatwoot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"visoria-control/internal/platform/log"
	"visoria-control/internal/ports"
)

const maxBody = 1 << 20 // 1 MiB por respuesta

type Client struct {
	baseURL   string
	accountID string
	inboxID   int
	token     string
	http      *http.Client

	mu       sync.RWMutex
	contacts map[string]int // teléfono (solo dígitos) -> contactID
}

var _ ports.ChatwootClient = (*Client)(nil)

func New(baseURL, accountID string, inboxID int, token string, httpClient *http.Client) *Client {
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		accountID: accountID,
		inboxID:   inboxID,
		token:     token,
		http:      httpClient,
		contacts:  make(map[string]int),
	}
}

// StatusError representa una respuesta HTTP de error de Chatwoot.
type StatusError struct {
	Op     string
	Status int
}

func (e *StatusError) Error() string { return fmt.Sprintf("chatwoot %s: HTTP %d", e.Op, e.Status) }

// RegisterNote busca (o crea) el contacto por teléfono, busca (o crea) su
// conversación abierta en el inbox configurado y publica una NOTA PRIVADA.
func (c *Client) RegisterNote(ctx context.Context, phone, contactName, content string) error {
	phone = digits(phone)
	lg := log.FromContext(ctx)

	contactID, cached := c.cachedContact(phone)
	if !cached {
		var err error
		if contactID, err = c.findOrCreateContact(ctx, phone, contactName); err != nil {
			return err
		}
		c.storeContact(phone, contactID)
	}

	convID, err := c.findOrCreateConversation(ctx, contactID)
	if err != nil {
		// Un contacto en caché pudo borrarse en Chatwoot: reintentar una vez sin caché.
		var se *StatusError
		if cached && errors.As(err, &se) && se.Status == http.StatusNotFound {
			c.forgetContact(phone)
			if contactID, err = c.findOrCreateContact(ctx, phone, contactName); err != nil {
				return err
			}
			c.storeContact(phone, contactID)
			convID, err = c.findOrCreateConversation(ctx, contactID)
		}
		if err != nil {
			return err
		}
	}

	if err := c.postPrivateNote(ctx, convID, content); err != nil {
		return err
	}
	lg.Debug("chatwoot: nota privada publicada", "contact_id", contactID, "conversation_id", convID)
	return nil
}

func (c *Client) findOrCreateContact(ctx context.Context, phone, name string) (int, error) {
	// q codificado en URL; Chatwoot hace búsqueda parcial, por eso se exige coincidencia exacta.
	var search struct {
		Payload []struct {
			ID    int    `json:"id"`
			Phone string `json:"phone_number"`
		} `json:"payload"`
	}
	path := "/contacts/search?q=" + url.QueryEscape(phone)
	if err := c.do(ctx, "buscar contacto", http.MethodGet, path, nil, &search); err != nil {
		return 0, err
	}
	for _, p := range search.Payload {
		if digits(p.Phone) == phone {
			return p.ID, nil
		}
	}

	var created struct {
		ID      int `json:"id"`
		Contact struct {
			ID int `json:"id"`
		} `json:"contact"`
		Payload struct {
			ID      int `json:"id"`
			Contact struct {
				ID int `json:"id"`
			} `json:"contact"`
		} `json:"payload"`
	}
	body := map[string]any{"phone_number": "+" + phone, "name": name, "inbox_id": c.inboxID}
	if err := c.do(ctx, "crear contacto", http.MethodPost, "/contacts", body, &created); err != nil {
		return 0, err
	}
	// Según la versión de Chatwoot el ID viene en distintos niveles.
	for _, id := range []int{created.Payload.Contact.ID, created.Payload.ID, created.Contact.ID, created.ID} {
		if id != 0 {
			return id, nil
		}
	}
	return 0, fmt.Errorf("chatwoot crear contacto: la respuesta no incluye el ID")
}

// findOrCreateConversation usa la conversación ABIERTA del inbox configurado.
func (c *Client) findOrCreateConversation(ctx context.Context, contactID int) (int, error) {
	var list struct {
		Payload []struct {
			ID      int    `json:"id"`
			InboxID int    `json:"inbox_id"`
			Status  string `json:"status"`
		} `json:"payload"`
	}
	if err := c.do(ctx, "listar conversaciones", http.MethodGet,
		fmt.Sprintf("/contacts/%d/conversations", contactID), nil, &list); err != nil {
		return 0, err
	}
	for _, cv := range list.Payload {
		if cv.InboxID == c.inboxID && cv.Status == "open" {
			return cv.ID, nil
		}
	}

	var created struct {
		ID int `json:"id"`
	}
	body := map[string]any{"contact_id": contactID, "inbox_id": c.inboxID}
	if err := c.do(ctx, "crear conversación", http.MethodPost, "/conversations", body, &created); err != nil {
		return 0, err
	}
	if created.ID == 0 {
		return 0, fmt.Errorf("chatwoot crear conversación: la respuesta no incluye el ID")
	}
	return created.ID, nil
}

func (c *Client) postPrivateNote(ctx context.Context, conversationID int, content string) error {
	body := map[string]any{
		"content":      content,
		"message_type": "outgoing",
		"private":      true, // nota interna: nunca se envía al acudiente (D7)
	}
	return c.do(ctx, "publicar nota", http.MethodPost,
		fmt.Sprintf("/conversations/%d/messages", conversationID), body, nil)
}

// do ejecuta una llamada a /api/v1/accounts/{id}{path}. Si out != nil decodifica el JSON.
func (c *Client) do(ctx context.Context, op, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("chatwoot %s: %w", op, err)
		}
		rd = bytes.NewReader(b)
	}
	endpoint := fmt.Sprintf("%s/api/v1/accounts/%s%s", c.baseURL, url.PathEscape(c.accountID), path)
	req, err := http.NewRequestWithContext(ctx, method, endpoint, rd)
	if err != nil {
		return fmt.Errorf("chatwoot %s: %w", op, err)
	}
	req.Header.Set("api_access_token", c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("chatwoot %s: %w", op, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("chatwoot %s: leyendo respuesta: %w", op, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// No se incluye el cuerpo en el error: puede traer datos personales.
		return &StatusError{Op: op, Status: resp.StatusCode}
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("chatwoot %s: respuesta no válida: %w", op, err)
		}
	}
	return nil
}

func (c *Client) cachedContact(phone string) (int, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	id, ok := c.contacts[phone]
	return id, ok
}
func (c *Client) storeContact(phone string, id int) {
	c.mu.Lock()
	c.contacts[phone] = id
	c.mu.Unlock()
}
func (c *Client) forgetContact(phone string) {
	c.mu.Lock()
	delete(c.contacts, phone)
	c.mu.Unlock()
}

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
