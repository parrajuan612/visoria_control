package email

import (
	"strings"
	"testing"

	"visoria-control/internal/domain"
)

func data() Data {
	return Data{To: []string{"a@b.co"}, Acudiente: "MARÍA DEL PILAR PÉREZ", Jugador: "JUAN DE LA CRUZ MUÑOZ", Beca: "50", Torneo: "COPA Majestic Internacional",
		FileName: "JUAN DE LA CRUZ MUÑOZ - BECA 50.pdf", PDF: []byte("%PDF-1.4"), MessageID: "<id@x>", Delivery: "c:r:email:proposal"}
}

func TestComposeConLosTextosDelCliente(t *testing.T) {
	m := Compose(DefaultSettings(""), data())
	if m.Subject != "Propuesta económica – Juan de la Cruz Muñoz – Majestic Intercambio Deportivo" {
		t.Errorf("asunto: %q", m.Subject)
	}
	for _, want := range []string{
		"Cordial saludo, María del Pilar Pérez:",                                         // nombre propio: el Excel viene en mayúsculas
		"Juan de la Cruz Muñoz obtuvo una beca del 50 % en COPA Majestic Internacional.", // «50 %» y el torneo tal como se escribió
		"Suysan Colmenares C.",
		"⚠️ NO respondas este correo. Si quieres más información, escríbenos al WhatsApp 322 846 7206:",
		"https://wa.me/573228467206?text=Hola%2C%20recib%C3%AD%20la%20beca%20de%20Majestic%20Intercambio%20deportivo%20otorgada%20al%20jugador%20Juan%20de%20la%20Cruz%20Mu%C3%B1oz%20a%20mi%20correo%20electr%C3%B3nico%20y%20quiero%20saber%20m%C3%A1s%20informaci%C3%B3n", // idéntico a urllib.quote de Python
	} {
		if !strings.Contains(m.TextBody, want) {
			t.Errorf("el texto no contiene %q:\n%s", want, m.TextBody)
		}
	}
	for _, want := range []string{"MAJESTIC &middot; INTERCAMBIO DEPORTIVO", "NO respondas este correo", "<b>322 846 7206</b>", `href="https://wa.me/573228467206?text=`, "Escríbenos por WhatsApp", `<p style="margin:0 0 14px">Cordial saludo, María del Pilar Pérez:</p>`} {
		if !strings.Contains(m.HTMLBody, want) {
			t.Errorf("el HTML no contiene %q", want)
		}
	}
	if m.MessageID != "<id@x>" || m.Headers["X-Visoria-Delivery"] != "c:r:email:proposal" || len(m.Attachments) != 1 ||
		m.Attachments[0].Filename != "JUAN DE LA CRUZ MUÑOZ - BECA 50.pdf" || m.Attachments[0].ContentType != "application/pdf" || string(m.Attachments[0].Data) != "%PDF-1.4" {
		t.Errorf("adjunto/cabeceras: %+v", m)
	}
}

func TestComposeEscapaYNoInventaCamposNiAviso(t *testing.T) {
	d := data()
	d.Acudiente = `<script>alert(1)</script> & "x"`
	d.Torneo = `Copa <b>1</b>`
	m := Compose(DefaultSettings(""), d)
	if strings.Contains(m.HTMLBody, "<script>") || strings.Contains(m.HTMLBody, "<b>1</b>") || !strings.Contains(m.HTMLBody, "&lt;script&gt;") {
		t.Errorf("el HTML debe escapar los datos del Excel:\n%s", m.HTMLBody)
	}
	// Sin WhatsApp configurado no hay aviso ni botón (como en el cliente).
	s := DefaultSettings("")
	s.WhatsApp = ""
	m = Compose(s, data())
	if strings.Contains(m.HTMLBody, "NO respondas") || strings.Contains(m.TextBody, "NO respondas") || strings.Contains(m.TextBody, "wa.me") {
		t.Error("sin WhatsApp no debe haber aviso")
	}
	// Un campo desconocido queda tal cual; sin beca / acompañante usan su nombre.
	s = DefaultSettings("")
	s.Body = "Hola {acudiente} {raro}"
	if m := Compose(s, data()); !strings.Contains(m.TextBody, "Hola María del Pilar Pérez {raro}") {
		t.Errorf("campo desconocido: %q", m.TextBody)
	}
	d = data()
	d.Beca = domain.BecaAcompanante
	if m := Compose(DefaultSettings(""), d); !strings.Contains(m.TextBody, "beca del ACOMPAÑANTE") {
		t.Errorf("acompañante: %q", m.TextBody)
	}
}

func TestWhatsAppNumber(t *testing.T) {
	for in, want := range map[string][3]string{
		"322 846 7206": {"573228467206", "322 846 7206", "true"}, "3228467206": {"573228467206", "322 846 7206", "true"},
		"+57 322 846 7206": {"573228467206", "322 846 7206", "true"}, "+34 612 345 678": {"34612345678", "+34612345678", "true"},
		"123": {"", "", "false"}, "": {"", "", "false"}, "12345678901234567": {"", "", "false"},
	} {
		d, v, ok := WhatsAppNumber(in)
		if d != want[0] || v != want[1] || (want[2] == "true") != ok {
			t.Errorf("WhatsAppNumber(%q)=%q,%q,%v", in, d, v, ok)
		}
	}
}

func TestValidateSettings(t *testing.T) {
	if errs := ValidateSettings(DefaultSettings("")); len(errs) != 0 {
		t.Errorf("los textos del cliente deben ser válidos: %v", errs)
	}
	cases := map[string]Settings{
		"Escribe el asunto del correo.":                               {Subject: " ", Body: "x"},
		"Escribe el texto del correo.":                                {Subject: "x", Body: ""},
		"El número de WhatsApp no es válido (ejemplo: 322 846 7206).": {Subject: "x", Body: "x", WhatsApp: "12"},
		"Escribe el mensaje predeterminado de WhatsApp.":              {Subject: "x", Body: "x", WhatsApp: "3228467206"},
		"Campos que no existen: {raro}, {otro}.":                      {Subject: "{raro}", Body: "{otro} {raro} {jugador}"},
	}
	for want, s := range cases {
		found := false
		for _, e := range ValidateSettings(s) {
			if strings.HasPrefix(e, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("faltó %q en %v", want, ValidateSettings(s))
		}
	}
}
