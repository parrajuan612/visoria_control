package config

import (
	"strings"
	"testing"
	"time"
)

func lookup(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func validEnv() map[string]string {
	return map[string]string{
		"BASE_URL":           "https://ejemplo.test/",
		"WHATSAPP_TOKEN":     "tok",
		"WHATSAPP_PHONE_ID":  "123",
		"CHATWOOT_API_TOKEN": "cw",
	}
}

func TestFaltanObligatorias(t *testing.T) {
	_, err := fromLookup(lookup(map[string]string{}))
	if err == nil {
		t.Fatal("se esperaba error")
	}
	for _, k := range []string{"BASE_URL", "WHATSAPP_TOKEN", "WHATSAPP_PHONE_ID", "CHATWOOT_API_TOKEN"} {
		if !strings.Contains(err.Error(), k) {
			t.Errorf("el error debe mencionar %s: %v", k, err)
		}
	}
}

func TestDefaultsYNormalizacion(t *testing.T) {
	c, err := fromLookup(lookup(validEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if c.BaseURL != "https://ejemplo.test" {
		t.Errorf("BaseURL = %q", c.BaseURL)
	}
	if c.Port != "8880" || c.Chatwoot.InboxID != 2 || c.Chatwoot.AccountID != "1" {
		t.Errorf("defaults inesperados: %+v", c)
	}
	if c.WhatsAppPause != 6*time.Second {
		t.Errorf("pausa = %v", c.WhatsAppPause)
	}
	if c.Redis.Enabled() || c.SMTP.Enabled() {
		t.Error("Redis y SMTP deben estar deshabilitados por defecto")
	}
}

func TestCORSOrigenes(t *testing.T) {
	e := validEnv()
	e["CORS_ALLOWED_ORIGINS"] = " https://a.test/ , ,https://b.test"
	c, err := fromLookup(lookup(e))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(c.CORSOrigins, ",")
	want := "http://localhost:8880,https://ejemplo.test,https://a.test,https://b.test"
	if got != want {
		t.Errorf("CORS = %s, quería %s", got, want)
	}
}

func TestValoresInvalidos(t *testing.T) {
	e := validEnv()
	e["CHATWOOT_INBOX_ID"] = "dos"
	e["WHATSAPP_PAUSE"] = "rápido"
	e["SMTP_HOST"] = "smtp.gmail.com"
	_, err := fromLookup(lookup(e))
	if err == nil {
		t.Fatal("se esperaba error")
	}
	for _, s := range []string{"CHATWOOT_INBOX_ID", "WHATSAPP_PAUSE", "SMTP_USER"} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("falta %s en: %v", s, err)
		}
	}
}
