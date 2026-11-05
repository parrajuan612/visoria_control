package whatsapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"regexp"
	"strings"
	"time"

	"visoria-control/internal/platform/httpx"
	"visoria-control/internal/ports"
)

const (
	defaultBase = "https://graph.facebook.com"
	maxBody     = 1 << 20
)

// Options configura el cliente de la Cloud API.
type Options struct {
	Token      string
	PhoneID    string // Phone Number ID (no el WABA ID)
	APIVersion string // p. ej. v19.0
	BaseURL    string // vacío = https://graph.facebook.com (cambia en las pruebas)
	HTTP       *http.Client
}

// Client implementa ports.WhatsAppClient.
type Client struct {
	o    Options
	http *http.Client
}

var _ ports.WhatsAppClient = (*Client)(nil)

func NewClient(o Options) *Client {
	if o.BaseURL == "" {
		o.BaseURL = defaultBase
	}
	if o.APIVersion == "" {
		o.APIVersion = "v19.0"
	}
	c := o.HTTP
	if c == nil {
		c = httpx.New(60 * time.Second) // la subida del PDF puede tardar más que un mensaje
	}
	return &Client{o: o, http: c}
}

func (c *Client) endpoint(path string) string {
	return fmt.Sprintf("%s/%s/%s", strings.TrimRight(c.o.BaseURL, "/"), c.o.APIVersion, path)
}

// graphError es el cuerpo de error de la Graph API.
type graphError struct {
	Error struct {
		Message   string `json:"message"`
		Type      string `json:"type"`
		Code      int    `json:"code"`
		Subcode   int    `json:"error_subcode"`
		ErrorData struct {
			Details string `json:"details"`
		} `json:"error_data"`
	} `json:"error"`
}

// do ejecuta la petición y clasifica cualquier error. El token nunca aparece en los errores.
func (c *Client) do(req *http.Request, out any) error {
	req.Header.Set("Authorization", "Bearer "+c.o.Token)
	resp, err := c.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // sin la URL
		}
		return networkError(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if out != nil {
			if err := json.Unmarshal(data, out); err != nil {
				return &ports.ProviderError{Action: ports.ActionReview, Code: "respuesta", Message: "WhatsApp respondió algo inesperado y no se sabe si el mensaje salió.", Raw: trim(string(data))}
			}
		}
		return nil
	}
	return classify(resp.StatusCode, data)
}

func networkError(err error) error {
	msg := "No se pudo conectar con WhatsApp. Se reintentará."
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() || errors.Is(err, context.DeadlineExceeded) {
		msg = "WhatsApp tardó demasiado en responder. Se reintentará."
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	return &ports.ProviderError{Action: ports.ActionRetry, Code: "red", Message: msg, Raw: trim(err.Error())}
}

func trim(s string) string {
	if len(s) > 400 {
		return s[:400]
	}
	return s
}

// classify traduce la respuesta de error de Meta (SDD §3.5.2).
func classify(status int, body []byte) error {
	var g graphError
	_ = json.Unmarshal(body, &g)
	code := g.Error.Code
	raw := trim(strings.TrimSpace(g.Error.Message + " " + g.Error.ErrorData.Details))
	if raw == "" {
		raw = trim(string(body))
	}
	pe := func(a ports.ErrorAction, c, msg string) error {
		return &ports.ProviderError{Action: a, Code: c, Message: msg, Raw: raw}
	}
	cs := fmt.Sprint(code)
	if code == 0 {
		cs = fmt.Sprintf("HTTP %d", status)
	}
	switch {
	case status == http.StatusUnauthorized || code == 190 || code == 102:
		return pe(ports.ActionPause, cs, "El token de WhatsApp no es válido o venció. Genera uno nuevo en Meta y actualízalo en el servidor.")
	case code == 10 || (code >= 200 && code <= 299):
		return pe(ports.ActionPause, cs, "El token de WhatsApp no tiene los permisos necesarios.")
	case code == 131026:
		return pe(ports.ActionFail, cs, "Este número no tiene WhatsApp o no se puede entregar. Verifica el móvil o usa solo correo.")
	case code == 131047 || code == 131051:
		return pe(ports.ActionFail, cs, "WhatsApp no pudo entregar este tipo de mensaje a ese número.")
	case code == 131049:
		return pe(ports.ActionFail, cs, "WhatsApp no entregó el mensaje para proteger la calidad de la conversación.")
	case code == 131048:
		return pe(ports.ActionPause, cs, "WhatsApp limitó los envíos por calidad (demasiados mensajes reportados). Espera antes de continuar.")
	case code == 132000 || code == 132001 || code == 132005 || code == 132007 || code == 132012 || code == 132015 || code == 132016:
		return pe(ports.ActionPause, cs, "La plantilla de WhatsApp no existe, no está aprobada, está pausada o sus parámetros no coinciden. Revisa la plantilla en Meta.")
	case code == 131053 || code == 131052:
		return pe(ports.ActionRetry, cs, "WhatsApp no pudo procesar el PDF. Se reintentará subiéndolo de nuevo.")
	case code == 130429 || code == 131056 || code == 80007 || code == 4 || code == 17 || code == 613 || status == http.StatusTooManyRequests:
		e := pe(ports.ActionRetry, cs, "WhatsApp pidió ir más despacio. Se reintentará.").(*ports.ProviderError)
		e.RetryAfter = time.Minute
		return e
	case code == 131000 || code == 131016 || status >= 500:
		return pe(ports.ActionRetry, cs, "WhatsApp tuvo un problema temporal. Se reintentará.")
	case code == 100:
		return pe(ports.ActionFail, cs, "WhatsApp rechazó los datos del mensaje: "+firstNonEmpty(g.Error.ErrorData.Details, g.Error.Message))
	}
	return pe(ports.ActionFail, cs, "WhatsApp rechazó el mensaje: "+firstNonEmpty(g.Error.ErrorData.Details, g.Error.Message, fmt.Sprintf("HTTP %d", status)))
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// CheckToken: GET /{phone-id} (no envía nada).
func (c *Client) CheckToken(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint(url.PathEscape(c.o.PhoneID))+"?fields=id,display_phone_number", nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// UploadMedia: POST /{phone-id}/media (multipart) → id.
func (c *Client) UploadMedia(ctx context.Context, filename string, pdf []byte) (string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("messaging_product", "whatsapp")
	_ = w.WriteField("type", "application/pdf")
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, strings.NewReplacer(`"`, "", "\r", "", "\n", "").Replace(filename)))
	h.Set("Content-Type", "application/pdf")
	fw, err := w.CreatePart(h)
	if err != nil {
		return "", err
	}
	fw.Write(pdf)
	w.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(url.PathEscape(c.o.PhoneID)+"/media"), &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	var out struct {
		ID string `json:"id"`
	}
	if err := c.do(req, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", &ports.ProviderError{Action: ports.ActionRetry, Code: "media", Message: "WhatsApp no devolvió el identificador del PDF. Se reintentará."}
	}
	return out.ID, nil
}

var reSpaces = regexp.MustCompile(`\s+`)

// param limpia un parámetro del cuerpo: Meta rechaza saltos de línea, tabulaciones y 4+ espacios seguidos.
func param(s string) string {
	s = strings.TrimSpace(reSpaces.ReplaceAllString(s, " "))
	if s == "" {
		return "-" // un parámetro vacío hace que Meta rechace TODO el mensaje
	}
	if r := []rune(s); len(r) > 1000 {
		s = string(r[:1000])
	}
	return s
}

// SendTemplate: POST /{phone-id}/messages con la plantilla y el PDF (por media_id) → wamid.
func (c *Client) SendTemplate(ctx context.Context, to string, m ports.TemplateMessage) (string, error) {
	body := make([]map[string]string, len(m.BodyParams))
	for i, p := range m.BodyParams {
		body[i] = map[string]string{"type": "text", "text": param(p)}
	}
	payload := map[string]any{
		"messaging_product": "whatsapp",
		"to":                to,
		"type":              "template",
		"template": map[string]any{
			"name":     m.Name,
			"language": map[string]string{"code": m.Language},
			"components": []any{
				map[string]any{"type": "header", "parameters": []any{
					map[string]any{"type": "document", "document": map[string]string{"id": m.MediaID, "filename": m.DocFilename}},
				}},
				map[string]any{"type": "body", "parameters": body},
			},
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(url.PathEscape(c.o.PhoneID)+"/messages"), bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	var out struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := c.do(req, &out); err != nil {
		return "", err
	}
	if len(out.Messages) == 0 || out.Messages[0].ID == "" {
		// 200 sin wamid: no se sabe si salió. Se trata como incierto, no como éxito ni como fallo.
		return "", &ports.ProviderError{Action: ports.ActionReview, Code: "sin-wamid", Message: "WhatsApp respondió sin identificador de mensaje: no se sabe si salió. Revisa en WhatsApp antes de reenviar."}
	}
	return out.Messages[0].ID, nil
}
