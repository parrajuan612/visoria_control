package email

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math/big"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"visoria-control/internal/ports"
)

// ---- servidor SMTP simulado ----

type fakeSMTP struct {
	ln         net.Listener
	tlsCfg     *tls.Config
	user, pass string
	rcptReject map[string]string // dirección → "550 5.1.1 ..."
	mailReject string
	dataReject string
	mu         sync.Mutex
	messages   [][]byte
	envelopes  [][]string
	sawTLS     bool
	sawAuth    bool
}

func selfSigned(t *testing.T) *tls.Config {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, _ := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
}

func startSMTP(t *testing.T, f *fakeSMTP) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.ln = ln
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeSMTP) port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func (f *fakeSMTP) serve(conn net.Conn) {
	defer conn.Close()
	rd := bufio.NewReader(conn)
	send := func(s string) { fmt.Fprintf(conn, "%s\r\n", s) }
	send("220 localhost ESMTP fake")
	var rcpts []string
	for {
		line, err := rd.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.TrimSpace(line)
		up := strings.ToUpper(cmd)
		switch {
		case strings.HasPrefix(up, "EHLO"), strings.HasPrefix(up, "HELO"):
			send("250-localhost")
			if f.tlsCfg != nil && !f.sawTLS {
				send("250-STARTTLS")
			}
			send("250 AUTH PLAIN")
		case up == "STARTTLS":
			send("220 Ready to start TLS")
			tc := tls.Server(conn, f.tlsCfg)
			if err := tc.Handshake(); err != nil {
				return
			}
			conn, rd, f.sawTLS = tc, bufio.NewReader(tc), true
			send = func(s string) { fmt.Fprintf(tc, "%s\r\n", s) }
		case strings.HasPrefix(up, "AUTH PLAIN"):
			raw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(cmd[len("AUTH PLAIN"):]))
			parts := strings.Split(string(raw), "\x00")
			if len(parts) == 3 && parts[1] == f.user && parts[2] == f.pass {
				f.sawAuth = true
				send("235 2.7.0 Accepted")
			} else {
				send("535 5.7.8 Username and Password not accepted")
			}
		case strings.HasPrefix(up, "MAIL FROM"):
			if f.mailReject != "" {
				send(f.mailReject)
				continue
			}
			send("250 2.1.0 OK")
		case strings.HasPrefix(up, "RCPT TO"):
			addr := strings.Trim(cmd[len("RCPT TO:"):], "<> ")
			if r, ok := f.rcptReject[addr]; ok {
				send(r)
				continue
			}
			rcpts = append(rcpts, addr)
			send("250 2.1.5 OK")
		case up == "DATA":
			if f.dataReject != "" {
				send(f.dataReject)
				continue
			}
			send("354 go ahead")
			var buf bytes.Buffer
			for {
				l, err := rd.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				buf.WriteString(strings.TrimPrefix(l, "."))
			}
			f.mu.Lock()
			f.messages, f.envelopes = append(f.messages, buf.Bytes()), append(f.envelopes, rcpts)
			f.mu.Unlock()
			rcpts = nil
			send("250 2.0.0 OK queued")
		case up == "RSET":
			rcpts = nil
			send("250 OK")
		case up == "QUIT":
			send("221 bye")
			return
		default:
			send("502 not implemented")
		}
	}
}

func (f *fakeSMTP) last(t *testing.T) *mail.Message {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.messages) == 0 {
		t.Fatal("el servidor no recibió ningún mensaje")
	}
	m, err := mail.ReadMessage(bytes.NewReader(f.messages[len(f.messages)-1]))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func plain(f *fakeSMTP) *Sender {
	return NewSender(Options{Host: "127.0.0.1", Port: f.port(), User: "becas@example.com", FromName: "Majestic Intercambio Deportivo", Security: "none", Timeout: 3 * time.Second})
}

func msg() ports.EmailMessage {
	return ports.EmailMessage{To: []string{"papa@example.com"}, Subject: "Propuesta económica – Ñandú – Majestic",
		TextBody: "Hola ñandú", HTMLBody: "<p>Hola <b>ñandú</b></p>", Headers: map[string]string{"X-Visoria-Delivery": "c1:r1:email:proposal"},
		Attachments: []ports.EmailAttachment{{Data: bytes.Repeat([]byte("%PDF-1.4 datos "), 200), Filename: "Ñandú Pérez - BECA 50.pdf", ContentType: "application/pdf"}}}
}

func TestEnviaElMensajeCompleto(t *testing.T) {
	f := startSMTP(t, &fakeSMTP{})
	m := msg()
	id, err := plain(f).Send(context.Background(), m)
	if err != nil || !strings.HasSuffix(id, "@example.com>") || !strings.HasPrefix(id, "<") {
		t.Fatalf("Send: %q %v", id, err)
	}
	got := f.last(t)
	if got.Header.Get("Message-ID") != id || got.Header.Get("X-Visoria-Delivery") != "c1:r1:email:proposal" || got.Header.Get("MIME-Version") != "1.0" {
		t.Errorf("cabeceras: %v", got.Header)
	}
	dec := new(mime.WordDecoder)
	if subj, _ := dec.DecodeHeader(got.Header.Get("Subject")); subj != "Propuesta económica – Ñandú – Majestic" {
		t.Errorf("asunto: %q", subj)
	}
	if from, _ := dec.DecodeHeader(got.Header.Get("From")); from != `"Majestic Intercambio Deportivo" <becas@example.com>` {
		t.Errorf("from: %q", from)
	}
	if got.Header.Get("To") != "papa@example.com" || got.Header.Get("Date") == "" {
		t.Errorf("to/date: %v", got.Header)
	}
	if f.envelopes[0][0] != "papa@example.com" {
		t.Errorf("sobre: %v", f.envelopes)
	}
	// Estructura MIME: mixed → (alternative(text, html), PDF)
	mt, params, _ := mime.ParseMediaType(got.Header.Get("Content-Type"))
	if mt != "multipart/mixed" {
		t.Fatalf("tipo: %s", mt)
	}
	mr := multipart.NewReader(got.Body, params["boundary"])
	var gotText, gotHTML string
	var pdf []byte
	var pdfName string
	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}
		pt, pp, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
		switch {
		case pt == "multipart/alternative":
			ar := multipart.NewReader(part, pp["boundary"])
			for {
				ap, err := ar.NextPart()
				if err != nil {
					break
				}
				b, _ := io.ReadAll(quotedprintable.NewReader(ap))
				if strings.HasPrefix(ap.Header.Get("Content-Type"), "text/plain") {
					gotText = string(b)
				} else {
					gotHTML = string(b)
				}
			}
		case pt == "application/pdf":
			raw, _ := io.ReadAll(part)
			pdf, _ = base64.StdEncoding.DecodeString(strings.ReplaceAll(string(raw), "\r\n", ""))
			_, dp, _ := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
			pdfName = dp["filename"]
			if !strings.Contains(part.Header.Get("Content-Disposition"), "attachment") {
				t.Error("el PDF debe ir como adjunto")
			}
		}
	}
	if strings.TrimSpace(gotText) != "Hola ñandú" || !strings.Contains(gotHTML, "<b>ñandú</b>") {
		t.Errorf("cuerpos: %q | %q", gotText, gotHTML)
	}
	if !bytes.Equal(pdf, m.Attachments[0].Data) || pdfName != "Ñandú Pérez - BECA 50.pdf" {
		t.Errorf("adjunto: %d bytes, nombre %q", len(pdf), pdfName)
	}
}

func TestStartTLSyAutenticacion(t *testing.T) {
	cfg := selfSigned(t)
	f := startSMTP(t, &fakeSMTP{tlsCfg: cfg, user: "becas@example.com", pass: "abcdefghijklmnop"})
	s := NewSender(Options{Host: "127.0.0.1", Port: f.port(), User: "becas@example.com", Password: "abcdefghijklmnop", FromName: "M",
		Security: "starttls", Timeout: 3 * time.Second, TLS: &tls.Config{InsecureSkipVerify: true}})
	if _, err := s.Send(context.Background(), msg()); err != nil {
		t.Fatal(err)
	}
	if !f.sawTLS || !f.sawAuth {
		t.Errorf("debe cifrar antes de autenticar: tls=%v auth=%v", f.sawTLS, f.sawAuth)
	}
	if err := s.Verify(context.Background()); err != nil {
		t.Errorf("Verify: %v", err)
	}
	// Contraseña incorrecta → error de credenciales: pausa la campaña (no falla uno por uno).
	bad := NewSender(Options{Host: "127.0.0.1", Port: f.port(), User: "becas@example.com", Password: "mala", Security: "starttls", Timeout: 3 * time.Second, TLS: &tls.Config{InsecureSkipVerify: true}})
	for name, fn := range map[string]func() error{
		"Send":   func() error { _, err := bad.Send(context.Background(), msg()); return err },
		"Verify": func() error { return bad.Verify(context.Background()) },
	} {
		var pe *ports.ProviderError
		if err := fn(); !errors.As(err, &pe) || pe.Action != ports.ActionPause || !strings.Contains(pe.Message, "contraseña de aplicación") || strings.Contains(pe.Message+pe.Raw, "mala") {
			t.Errorf("%s con credenciales malas: %+v", name, err)
		}
	}
}

func TestClasificaLosErroresSMTP(t *testing.T) {
	cases := []struct {
		name string
		f    *fakeSMTP
		want ports.ErrorAction
		sub  string
	}{
		{"dirección inexistente", &fakeSMTP{rcptReject: map[string]string{"papa@example.com": "550 5.1.1 The email account that you tried to reach does not exist"}}, ports.ActionFail, "no existe"},
		{"dirección rechazada 553", &fakeSMTP{rcptReject: map[string]string{"papa@example.com": "553 5.1.2 bad address"}}, ports.ActionFail, "no existe"},
		{"cuota diaria", &fakeSMTP{dataReject: "550 5.4.5 Daily user sending quota exceeded"}, ports.ActionDefer, "límite diario"},
		{"cuota 452", &fakeSMTP{dataReject: "452 4.5.3 Daily sending limit exceeded"}, ports.ActionDefer, "límite diario"},
		{"temporal 421", &fakeSMTP{dataReject: "421 4.7.0 Try again later"}, ports.ActionRetry, "temporal"},
		{"temporal 451", &fakeSMTP{mailReject: "451 4.3.0 Temporary problem"}, ports.ActionRetry, "temporal"},
		{"spam 5.7.1", &fakeSMTP{dataReject: "554 5.7.1 Message rejected as spam"}, ports.ActionFail, "rechazó"},
	}
	for _, c := range cases {
		f := startSMTP(t, c.f)
		_, err := plain(f).Send(context.Background(), msg())
		var pe *ports.ProviderError
		if !errors.As(err, &pe) || pe.Action != c.want || !strings.Contains(pe.Message, c.sub) {
			t.Errorf("%s: %+v", c.name, err)
		}
	}
}

func TestVariosDestinatarios(t *testing.T) {
	f := startSMTP(t, &fakeSMTP{rcptReject: map[string]string{"malo@example.com": "550 5.1.1 no existe"}})
	m := msg()
	m.To = []string{"malo@example.com", "bueno@example.com"}
	if _, err := plain(f).Send(context.Background(), m); err != nil {
		t.Fatalf("si al menos uno es válido, se envía: %v", err)
	}
	if got := f.envelopes[0]; len(got) != 1 || got[0] != "bueno@example.com" {
		t.Errorf("sobre: %v", got)
	}
	// Todos rechazados → fallo permanente y no se envía nada.
	m.To = []string{"malo@example.com"}
	var pe *ports.ProviderError
	if _, err := plain(f).Send(context.Background(), m); !errors.As(err, &pe) || pe.Action != ports.ActionFail {
		t.Errorf("todos rechazados: %v", err)
	}
	if len(f.messages) != 1 {
		t.Errorf("no debe enviarse a nadie: %d mensajes", len(f.messages))
	}
}

func TestRedYEntradasInvalidas(t *testing.T) {
	// Servidor caído → transitorio.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	s := NewSender(Options{Host: "127.0.0.1", Port: port, User: "a@b.co", Security: "none", Timeout: time.Second})
	var pe *ports.ProviderError
	if _, err := s.Send(context.Background(), msg()); !errors.As(err, &pe) || pe.Action != ports.ActionRetry {
		t.Errorf("servidor caído: %v", err)
	}
	f := startSMTP(t, &fakeSMTP{})
	sender := plain(f)
	m := msg()
	m.To = nil
	if _, err := sender.Send(context.Background(), m); !errors.As(err, &pe) || pe.Action != ports.ActionFail {
		t.Errorf("sin destinatarios: %v", err)
	}
	m = msg()
	m.To = []string{"no-es-correo"}
	if _, err := sender.Send(context.Background(), m); !errors.As(err, &pe) || pe.Action != ports.ActionFail {
		t.Errorf("dirección inválida: %v", err)
	}
	// Inyección de cabeceras: un salto de línea en el asunto o en el nombre del adjunto no crea cabeceras nuevas.
	m = msg()
	m.Subject = "Hola\r\nBcc: espia@evil.com"
	m.Attachments[0].Filename = "a.pdf\r\nX-Evil: 1"
	if _, err := sender.Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	got := f.last(t)
	if got.Header.Get("Bcc") != "" || got.Header.Get("X-Evil") != "" {
		t.Errorf("cabeceras inyectadas: %v", got.Header)
	}
	// Contexto cancelado.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sender.Send(ctx, msg()); err == nil {
		t.Error("contexto cancelado debe fallar")
	}
	if NewSender(Options{}).o.Timeout != 30*time.Second || NewSender(Options{}).o.Security != "starttls" {
		t.Error("valores por defecto")
	}
	if got := domainOf("a@gmail.com"); got != "gmail.com" || domainOf("sin-arroba") != "localhost" {
		t.Error("domainOf")
	}
	_ = strconv.Itoa
}
