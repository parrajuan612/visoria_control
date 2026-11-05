// Package email compone y envía las propuestas por correo (ADR-9: Gmail SMTP). La composición es un
// port de becas/correo.py: texto plano + HTML con el aviso «NO respondas», el botón de WhatsApp y el
// PDF adjunto.
package email

import (
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"

	"visoria-control/internal/domain"
	"visoria-control/internal/ports"
)

// Textos por defecto: los de config.json del cliente. Campos admitidos: {acudiente} {jugador} {beca} {torneo}.
const (
	DefaultSubject = "Propuesta económica – {jugador} – Majestic Intercambio Deportivo"
	DefaultBody    = "Cordial saludo, {acudiente}:\n\n" +
		"Le escribimos de Majestic Intercambio Deportivo. Nos complace informarle que {jugador} obtuvo una beca del {beca} en {torneo}.\n\n" +
		"Adjuntamos la propuesta económica del programa de intercambio deportivo en España, con los valores y las fechas de pago.\n\n" +
		"Cordialmente,\n\n" +
		"Suysan Colmenares C.\n" +
		"Coordinador Programa\n" +
		"Majestic Intercambio Deportivo"
	DefaultWhatsApp        = "3228467206"
	DefaultWhatsAppMessage = "Hola, recibí la beca de Majestic Intercambio deportivo otorgada al jugador {jugador} a mi correo electrónico y quiero saber más información"
)

// Settings son los textos del correo y el WhatsApp de contacto.
type Settings struct {
	Subject, Body             string
	WhatsApp, WhatsAppMessage string
}

// DefaultSettings devuelve los textos del cliente (whatsapp vacío = el del cliente).
func DefaultSettings(whatsapp string) Settings {
	if strings.TrimSpace(whatsapp) == "" {
		whatsapp = DefaultWhatsApp
	}
	return Settings{Subject: DefaultSubject, Body: DefaultBody, WhatsApp: whatsapp, WhatsAppMessage: DefaultWhatsAppMessage}
}

// Data son los datos de una propuesta.
type Data struct {
	To        []string
	Acudiente string
	Jugador   string
	Beca      domain.BecaKey
	Torneo    string // tal como se escribió
	FileName  string // nombre visible del PDF adjunto
	PDF       []byte
	MessageID string
	Delivery  string // clave de la entrega (cabecera X-Visoria-Delivery)
}

var rePlaceholder = regexp.MustCompile(`\{(\w+)\}`)

func fill(text string, vals map[string]string) string {
	return rePlaceholder.ReplaceAllStringFunc(text, func(m string) string {
		if v, ok := vals[m[1:len(m)-1]]; ok {
			return v
		}
		return m
	})
}

// ValidateSettings port de validar_textos: comprueba que haya asunto y cuerpo y que los campos existan.
func ValidateSettings(s Settings) []string {
	var errs []string
	if strings.TrimSpace(s.Subject) == "" {
		errs = append(errs, "Escribe el asunto del correo.")
	}
	if strings.TrimSpace(s.Body) == "" {
		errs = append(errs, "Escribe el texto del correo.")
	}
	if strings.TrimSpace(s.WhatsApp) != "" {
		if _, _, ok := WhatsAppNumber(s.WhatsApp); !ok {
			errs = append(errs, "El número de WhatsApp no es válido (ejemplo: 322 846 7206).")
		} else if strings.TrimSpace(s.WhatsAppMessage) == "" {
			errs = append(errs, "Escribe el mensaje predeterminado de WhatsApp.")
		}
	}
	allowed := map[string]bool{"acudiente": true, "jugador": true, "beca": true, "torneo": true}
	seen := map[string]bool{}
	var unknown []string
	for _, m := range rePlaceholder.FindAllStringSubmatch(s.Subject+s.Body+s.WhatsAppMessage, -1) {
		if !allowed[m[1]] && !seen[m[1]] {
			seen[m[1]] = true
			unknown = append(unknown, "{"+m[1]+"}")
		}
	}
	if len(unknown) > 0 {
		errs = append(errs, "Campos que no existen: "+strings.Join(unknown, ", ")+". Puedes usar: {acudiente}, {jugador}, {beca}, {torneo}.")
	}
	return errs
}

// WhatsAppNumber port de numero_whatsapp: "322 846 7206" → ("573228467206", "322 846 7206").
func WhatsAppNumber(text string) (digits, visible string, ok bool) {
	var b strings.Builder
	for _, r := range text {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	d := b.String()
	if len(d) == 10 && d[0] == '3' {
		d = "57" + d
	}
	if len(d) < 11 || len(d) > 15 {
		return "", "", false
	}
	local := d
	if strings.HasPrefix(d, "57") && len(d) == 12 {
		local = d[2:]
	}
	if len(local) == 10 {
		return d, local[:3] + " " + local[3:6] + " " + local[6:], true
	}
	return d, "+" + d, true
}

// Compose arma el correo de una propuesta.
func Compose(s Settings, d Data) ports.EmailMessage {
	vals := map[string]string{
		"acudiente": domain.ProperName(d.Acudiente),
		"jugador":   domain.ProperName(d.Jugador),
		"beca":      domain.BecaLabel(d.Beca),
		"torneo":    d.Torneo,
	}
	if d.Beca != domain.BecaSinBeca && d.Beca != domain.BecaAcompanante {
		vals["beca"] = string(d.Beca) + " %" // como en el cliente: «50 %»
	}
	subject, body := fill(s.Subject, vals), fill(s.Body, vals)

	var visible, link string
	if num, vis, ok := WhatsAppNumber(s.WhatsApp); ok {
		visible = vis
		link = "https://wa.me/" + num + "?text=" + url.PathEscape(fill(s.WhatsAppMessage, vals))
	}
	text := body
	if link != "" {
		text += "\n\n⚠️ NO respondas este correo. Si quieres más información, escríbenos al WhatsApp " + visible + ":\n" + link
	}
	headers := map[string]string{}
	if d.Delivery != "" {
		headers["X-Visoria-Delivery"] = d.Delivery
	}
	return ports.EmailMessage{
		To: d.To, Subject: subject, TextBody: text, HTMLBody: renderHTML(body, visible, link), MessageID: d.MessageID, Headers: headers,
		Attachments: []ports.EmailAttachment{{Data: d.PDF, Filename: d.FileName, ContentType: "application/pdf"}},
	}
}

var reParagraphs = regexp.MustCompile(`\n\s*\n`)

// renderHTML es el HTML del cliente: estilos en línea y tablas (se ve bien en Gmail y Outlook).
func renderHTML(body, visible, link string) string {
	var paras strings.Builder
	for _, p := range reParagraphs.Split(strings.TrimSpace(body), -1) {
		if strings.TrimSpace(p) == "" {
			continue
		}
		paras.WriteString(`<p style="margin:0 0 14px">` + strings.ReplaceAll(html.EscapeString(p), "\n", "<br>") + `</p>`)
	}
	notice := ""
	if link != "" {
		notice = fmt.Sprintf(`
<tr><td style="padding:6px 28px 28px">
  <table role="presentation" width="100%%" cellpadding="0" cellspacing="0"
         style="background:#fff4d6;border:2px solid #c9a24b;border-radius:10px">
    <tr><td style="padding:20px;text-align:center">
      <div style="font-size:19px;font-weight:bold;color:#b3261e">&#9888;&#65039; NO respondas este correo</div>
      <div style="font-size:15px;margin:8px 0 16px">Si quieres más información, escríbenos al WhatsApp <b>%s</b></div>
      <table role="presentation" cellpadding="0" cellspacing="0" align="center"><tr>
        <td style="background:#25D366;border-radius:8px">
          <a href="%s" target="_blank"
             style="display:inline-block;padding:14px 28px;font-size:17px;font-weight:bold;color:#ffffff;text-decoration:none">
            Escríbenos por WhatsApp</a>
        </td></tr></table>
    </td></tr>
  </table>
</td></tr>`, html.EscapeString(visible), html.EscapeString(link))
	}
	return `<!doctype html>
<html lang="es"><head><meta charset="utf-8"></head><body style="margin:0;padding:0;background:#f5f6fa">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#f5f6fa;padding:24px 0">
<tr><td align="center">
  <table role="presentation" width="600" cellpadding="0" cellspacing="0"
         style="max-width:600px;width:100%;background:#ffffff;border-radius:10px;border-top:6px solid #1f2a5b;
                font-family:Arial,Helvetica,sans-serif;color:#1c2033">
    <tr><td style="padding:22px 28px 4px;font-size:13px;letter-spacing:2px;font-weight:bold;color:#1f2a5b">
      MAJESTIC &middot; INTERCAMBIO DEPORTIVO</td></tr>
    <tr><td style="padding:12px 28px 4px;font-size:15px;line-height:1.55">` + paras.String() + `</td></tr>` + notice + `
  </table>
</td></tr>
</table>
</body></html>`
}

// Composer implementa ports.MailComposer con los textos configurados.
type Composer struct{ S Settings }

var _ ports.MailComposer = Composer{}

func (c Composer) Compose(p ports.ProposalMail) ports.EmailMessage {
	return Compose(c.S, Data{To: p.To, Acudiente: p.Acudiente, Jugador: p.Jugador, Beca: p.Beca, Torneo: p.Torneo,
		FileName: p.FileName, PDF: p.PDF, Delivery: p.DeliveryKey})
}
