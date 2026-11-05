package domain

import (
	"strings"
	"testing"
	"time"
)

func TestSafeFileName(t *testing.T) {
	cases := map[string]string{
		`JUAN <PÉREZ>: "el" / grande? | *`: "JUAN PÉREZ el grande",
		"  ..punto y espacios..  ":         "punto y espacios",
		"tab\tsalto\ny\x00nulo":            "tab salto y nulo",
		"Ñandú Óscar 😀":                    "Ñandú Óscar 😀",
		"":                                 "",
	}
	for in, want := range cases {
		if got := SafeFileName(in, 90); got != want {
			t.Errorf("SafeFileName(%q)=%q quería %q", in, got, want)
		}
	}
	long := strings.Repeat("a", 120) + " b"
	if got := SafeFileName(long, 90); len([]rune(got)) != 90 {
		t.Errorf("debe recortar a 90: %d", len([]rune(got)))
	}
	if got := SafeFileName(strings.Repeat("ñ", 89)+" ñ", 90); got != strings.Repeat("ñ", 89) {
		t.Errorf("el recorte no deja espacios al final: %q", got)
	}
}

func TestVisibleFileNames(t *testing.T) {
	recs := []Recipient{
		{ID: "c", Fila: 4, Jugador: "ana gómez", Beca: "50"},
		{ID: "a", Fila: 2, Jugador: "Ana Gómez", Beca: "50"},
		{ID: "b", Fila: 3, Jugador: "ANA GOMEZ/", Beca: "30"},
		{ID: "d", Fila: 5, Jugador: "Luis", Beca: BecaSinBeca},
		{ID: "e", Fila: 6, Jugador: "Ana Gómez", Beca: "50"},
	}
	got := VisibleFileNames(recs)
	want := map[string]string{
		"a": "Ana Gómez - BECA 50.pdf", "b": "ANA GOMEZ - BECA 30.pdf",
		"c": "ana gómez - BECA 50 (2).pdf", // mismo nombre sin distinguir mayúsculas: se numera por fila
		"d": "Luis - SIN BECA.pdf", "e": "Ana Gómez - BECA 50 (3).pdf",
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: %q, quería %q", id, got[id], w)
		}
	}
}

func TestProperName(t *testing.T) {
	for in, want := range map[string]string{
		"JUAN DE LA CRUZ MUÑOZ": "Juan de la Cruz Muñoz", "  maría   del pilar ": "María del Pilar", "de la rosa": "De la Rosa",
		"ÑANDÚ y ÓSCAR": "Ñandú y Óscar", "": "",
	} {
		if got := ProperName(in); got != want {
			t.Errorf("ProperName(%q)=%q quería %q", in, got, want)
		}
	}
}

func TestStatusLabelEHonestidad(t *testing.T) {
	if StatusLabel(ChannelEmail, StatusAccepted) != "Aceptado por Gmail" || StatusLabel(ChannelWhatsApp, StatusAccepted) != "Aceptado por WhatsApp" {
		t.Error("etiquetas de ACCEPTED")
	}
	for _, s := range AllStatuses {
		if StatusLabel(ChannelWhatsApp, s) == "" || StatusLabel(ChannelWhatsApp, s) == string(s) {
			t.Errorf("falta etiqueta para %s", s)
		}
	}
	if StatusLabel(ChannelEmail, "RARO") != "RARO" {
		t.Error("estado desconocido")
	}
	for s, want := range map[DeliveryStatus]bool{StatusPending: true, StatusQueued: true, StatusSending: true, StatusRetryWait: true,
		StatusAccepted: false, StatusFailed: false, StatusSkipped: false, StatusNeedsReview: false, StatusDelivered: false} {
		if IsInFlight(s) != want {
			t.Errorf("IsInFlight(%s)", s)
		}
	}
}

func TestMediaValid(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		d    Document
		want bool
	}{
		{Document{}, false},
		{Document{MediaID: "m", MediaExpiresAt: now.Add(10 * 24 * time.Hour)}, true},
		{Document{MediaID: "m", MediaExpiresAt: now.Add(12 * time.Hour)}, false}, // menos de 1 día de margen
		{Document{MediaID: "m", MediaExpiresAt: now.Add(-time.Hour)}, false},
		{Document{MediaExpiresAt: now.Add(10 * 24 * time.Hour)}, false},
	} {
		if c.d.MediaValid(now) != c.want {
			t.Errorf("%+v", c.d)
		}
	}
}

func TestParseDeliveryKey(t *testing.T) {
	key := NewDeliveryKey("camp1", "rec1", ChannelEmail, KindProposal)
	c, r, ch, k, err := ParseDeliveryKey(key)
	if err != nil || c != "camp1" || r != "rec1" || ch != ChannelEmail || k != KindProposal {
		t.Errorf("%q %q %q %q %v", c, r, ch, k, err)
	}
	for _, bad := range []string{"", "a:b:c", "a:b:whatsapp:proposal:extra", ":r:whatsapp:proposal", "c::whatsapp:proposal", "c:r:sms:proposal"} {
		if _, _, _, _, err := ParseDeliveryKey(bad); err == nil {
			t.Errorf("%q debía rechazarse", bad)
		}
	}
}
