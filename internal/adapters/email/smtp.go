package email

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"

	"visoria-control/internal/ports"
)

// Options configura el emisor SMTP.
type Options struct {
	Host, User, Password string
	Port                 int
	FromName             string
	// Security: "starttls" (por defecto; Gmail:587), "tls" (465) o "none" (servidor local de pruebas).
	Security string
	Timeout  time.Duration
	TLS      *tls.Config // solo pruebas
}

// Sender implementa ports.EmailSender sobre SMTP.
type Sender struct{ o Options }

var _ ports.EmailSender = (*Sender)(nil)

func NewSender(o Options) *Sender {
	if o.Timeout <= 0 {
		o.Timeout = 30 * time.Second
	}
	if o.Security == "" {
		o.Security = "starttls"
	}
	return &Sender{o: o}
}

func (s *Sender) addr() string { return net.JoinHostPort(s.o.Host, fmt.Sprint(s.o.Port)) }

func (s *Sender) tlsConfig() *tls.Config {
	if s.o.TLS != nil {
		return s.o.TLS
	}
	return &tls.Config{ServerName: s.o.Host, MinVersion: tls.VersionTLS12}
}

// connect abre la conexión y deja al cliente autenticado.
func (s *Sender) connect(ctx context.Context) (*smtp.Client, error) {
	d := net.Dialer{Timeout: s.o.Timeout}
	var conn net.Conn
	var err error
	if s.o.Security == "tls" {
		conn, err = tls.DialWithDialer(&d, "tcp", s.addr(), s.tlsConfig())
	} else {
		conn, err = d.DialContext(ctx, "tcp", s.addr())
	}
	if err != nil {
		return nil, classifyNet(err)
	}
	_ = conn.SetDeadline(time.Now().Add(s.o.Timeout * 4))
	c, err := smtp.NewClient(conn, s.o.Host)
	if err != nil {
		conn.Close()
		return nil, classifyNet(err)
	}
	if s.o.Security == "starttls" {
		if err := c.StartTLS(s.tlsConfig()); err != nil {
			c.Close()
			return nil, classifyNet(err)
		}
	}
	if s.o.Password != "" {
		if err := c.Auth(smtp.PlainAuth("", s.o.User, s.o.Password, s.o.Host)); err != nil {
			c.Close()
			return nil, classify(err)
		}
	}
	return c, nil
}

// Verify abre sesión y cierra sin enviar.
func (s *Sender) Verify(ctx context.Context) error {
	c, err := s.connect(ctx)
	if err != nil {
		return err
	}
	return c.Quit()
}

// Send entrega el mensaje. Devuelve el Message-ID. Los errores son *ports.ProviderError.
func (s *Sender) Send(ctx context.Context, m ports.EmailMessage) (string, error) {
	if len(m.To) == 0 {
		return "", &ports.ProviderError{Action: ports.ActionFail, Code: "sin-destino", Message: "El correo no tiene destinatarios."}
	}
	id := m.MessageID
	if id == "" {
		b := make([]byte, 12)
		_, _ = rand.Read(b)
		id = "<" + hex.EncodeToString(b) + "@" + domainOf(s.o.User) + ">"
	}
	body, err := s.build(m, id)
	if err != nil {
		return "", &ports.ProviderError{Action: ports.ActionFail, Code: "mensaje", Message: err.Error()}
	}
	c, err := s.connect(ctx)
	if err != nil {
		return "", err
	}
	defer c.Close()
	if err := c.Mail(s.o.User); err != nil {
		return "", classify(err)
	}
	refused := 0
	var firstRefusal error
	for _, to := range m.To {
		if err := c.Rcpt(to); err != nil {
			refused++
			if firstRefusal == nil {
				firstRefusal = err
			}
		}
	}
	if refused == len(m.To) { // todas las direcciones rechazadas
		_ = c.Reset()
		return "", classify(firstRefusal)
	}
	w, err := c.Data()
	if err != nil {
		return "", classify(err)
	}
	if _, err := w.Write(body); err != nil {
		w.Close()
		return "", classify(err)
	}
	if err := w.Close(); err != nil {
		return "", classify(err)
	}
	_ = c.Quit()
	return id, nil
}

func domainOf(addr string) string {
	if i := strings.LastIndex(addr, "@"); i >= 0 && i < len(addr)-1 {
		return addr[i+1:]
	}
	return "localhost"
}

// clean impide inyectar cabeceras (saltos de línea) desde datos del Excel.
func clean(s string) string { return strings.NewReplacer("\r", " ", "\n", " ").Replace(s) }

func (s *Sender) build(m ports.EmailMessage, id string) ([]byte, error) {
	for _, to := range m.To {
		if _, err := mail.ParseAddress(to); err != nil {
			return nil, fmt.Errorf("la dirección %q no es válida", clean(to))
		}
	}
	from := mail.Address{Name: s.o.FromName, Address: s.o.User}
	var b bytes.Buffer
	hdr := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	hdr("From", from.String())
	hdr("To", strings.Join(m.To, ", "))
	hdr("Subject", mime.QEncoding.Encode("utf-8", clean(m.Subject)))
	hdr("Date", time.Now().Format(time.RFC1123Z))
	hdr("Message-ID", clean(id))
	hdr("MIME-Version", "1.0")
	for k, v := range m.Headers {
		hdr(clean(k), clean(v))
	}

	mixed := multipart.NewWriter(&b)
	hdr("Content-Type", "multipart/mixed; boundary="+mixed.Boundary())
	b.WriteString("\r\n")

	// texto + HTML (alternativas)
	altBuf := &bytes.Buffer{}
	alt := multipart.NewWriter(altBuf)
	for _, p := range []struct{ ctype, body string }{{"text/plain", m.TextBody}, {"text/html", m.HTMLBody}} {
		if p.body == "" {
			continue
		}
		pw, err := alt.CreatePart(textproto.MIMEHeader{"Content-Type": {p.ctype + "; charset=utf-8"}, "Content-Transfer-Encoding": {"quoted-printable"}})
		if err != nil {
			return nil, err
		}
		q := quotedprintable.NewWriter(pw)
		q.Write([]byte(p.body))
		q.Close()
	}
	alt.Close()
	part, err := mixed.CreatePart(textproto.MIMEHeader{"Content-Type": {"multipart/alternative; boundary=" + alt.Boundary()}})
	if err != nil {
		return nil, err
	}
	part.Write(altBuf.Bytes())

	for _, a := range m.Attachments {
		ct := a.ContentType
		if ct == "" {
			ct = "application/pdf"
		}
		name := clean(a.Filename)
		pw, err := mixed.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {mime.FormatMediaType(ct, map[string]string{"name": name})},
			"Content-Disposition":       {mime.FormatMediaType("attachment", map[string]string{"filename": name})},
			"Content-Transfer-Encoding": {"base64"},
		})
		if err != nil {
			return nil, err
		}
		enc := base64.StdEncoding.EncodeToString(a.Data)
		for len(enc) > 76 {
			pw.Write([]byte(enc[:76] + "\r\n"))
			enc = enc[76:]
		}
		pw.Write([]byte(enc + "\r\n"))
	}
	mixed.Close()
	return b.Bytes(), nil
}

// ---- clasificación de errores SMTP (SDD §3.5.2) ----

func classifyNet(err error) error {
	if errors.Is(err, context.Canceled) {
		return err
	}
	return &ports.ProviderError{Action: ports.ActionRetry, Code: "red", Message: "No se pudo conectar con el servidor de correo. Se reintentará.", Raw: trunc(err.Error())}
}

func trunc(s string) string {
	if len(s) > 300 {
		return s[:300]
	}
	return s
}

func classify(err error) error {
	var te *textproto.Error
	if !errors.As(err, &te) {
		return classifyNet(err)
	}
	raw := trunc(fmt.Sprintf("%d %s", te.Code, te.Msg))
	low := strings.ToLower(te.Msg)
	pe := func(a ports.ErrorAction, code, msg string) error {
		return &ports.ProviderError{Action: a, Code: code, Message: msg, Raw: raw}
	}
	code := fmt.Sprint(te.Code)
	if f := strings.Fields(te.Msg); len(f) > 0 && len(f[0]) >= 5 && f[0][1] == '.' {
		code = f[0] // código mejorado: 5.1.1, 5.4.5…
	}
	switch {
	case te.Code == 535 || te.Code == 534 || te.Code == 530 || te.Code == 538:
		return pe(ports.ActionPause, code, "El servidor de correo no aceptó la cuenta o la contraseña de aplicación. Revisa SMTP_USER y SMTP_APP_PASSWORD (debe ser la contraseña de aplicación de 16 letras, no la normal).")
	case strings.Contains(low, "daily user sending") || strings.Contains(low, "sending limit") || strings.Contains(low, "5.4.5") || te.Code == 452 && strings.Contains(low, "4.5.3"):
		return pe(ports.ActionDefer, code, "Se alcanzó el límite diario de Gmail; continúa mañana.")
	case te.Code >= 400 && te.Code < 500:
		return pe(ports.ActionRetry, code, "El servidor de correo respondió con un error temporal. Se reintentará.")
	case te.Code == 550 || te.Code == 551 || te.Code == 553 || te.Code == 501 || te.Code == 511:
		return pe(ports.ActionFail, code, "La dirección de correo no existe o fue rechazada.")
	case te.Code >= 500:
		return pe(ports.ActionFail, code, "El servidor de correo rechazó el mensaje: "+trunc(te.Msg))
	}
	return pe(ports.ActionRetry, code, "Error inesperado del servidor de correo. Se reintentará.")
}
