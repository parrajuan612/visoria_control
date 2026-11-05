// Package config carga la configuración desde el entorno UNA sola vez, la valida
// al arrancar y la expone como un struct inmutable. Fuera de este paquete no se
// debe llamar a os.Getenv (SDD §3.4).
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Env      string // development | production
	Port     string
	LogLevel string // debug|info|warn|error
	BaseURL  string // URL pública, sin "/" final
	// Orígenes CORS permitidos (BASE_URL + localhost + CORS_ALLOWED_ORIGINS).
	CORSOrigins []string

	WhatsApp WhatsApp
	Chatwoot Chatwoot
	Redis    Redis
	SMTP     SMTP
	IMAP     IMAP

	// Pausa entre envíos de WhatsApp (antes fija en 6 s en el código).
	WhatsAppPause time.Duration
}

type WhatsApp struct {
	Token      string
	PhoneID    string // Phone Number ID (no el WABA ID)
	APIVersion string
}

type Chatwoot struct {
	URL       string
	Token     string
	AccountID string
	InboxID   int
}

// Redis es obligatorio (cola asynq + estado operativo). La contraseña es
// obligatoria en producción.
type Redis struct {
	Addr     string
	Password string
	DB       int
}

// SMTP e IMAP se usarán en la Fase 6 (ADR-9). Son opcionales por ahora.
type SMTP struct {
	Host, User, AppPassword, FromName string
	Port                              int
	DailyQuota                        int
	Pause                             time.Duration
}

func (s SMTP) Enabled() bool { return s.Host != "" && s.User != "" }

type IMAP struct {
	Host         string
	Port         int
	PollInterval time.Duration
}

// Load lee .env (salvo en producción) y el entorno, y valida. Devuelve un único
// error con todos los problemas encontrados para corregirlos de una vez.
func Load() (*Config, error) {
	env := os.Getenv("APP_ENV")
	if env != "production" {
		if err := godotenv.Load(".env"); err != nil {
			slog.Warn("no se pudo cargar .env, se usan las variables del sistema", "error", err)
		}
	}
	return fromLookup(os.Getenv)
}

// fromLookup separa la lectura del entorno para poder probarla sin tocar os.Environ.
func fromLookup(get func(string) string) (*Config, error) {
	var errs []error
	str := func(key, def string) string {
		if v := strings.TrimSpace(get(key)); v != "" {
			return v
		}
		return def
	}
	required := func(key string) string {
		v := strings.TrimSpace(get(key))
		if v == "" {
			errs = append(errs, fmt.Errorf("falta la variable obligatoria %s", key))
		}
		return v
	}
	integer := func(key string, def int) int {
		v := strings.TrimSpace(get(key))
		if v == "" {
			return def
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s debe ser un entero, recibió %q", key, v))
			return def
		}
		return n
	}
	duration := func(key string, def time.Duration) time.Duration {
		v := strings.TrimSpace(get(key))
		if v == "" {
			return def
		}
		d, err := time.ParseDuration(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s debe ser una duración (ej. 6s), recibió %q", key, v))
			return def
		}
		return d
	}

	c := &Config{
		Env:      str("APP_ENV", "development"),
		Port:     str("PORT", "8880"),
		LogLevel: str("LOG_LEVEL", "info"),
		BaseURL:  strings.TrimRight(required("BASE_URL"), "/"),
		WhatsApp: WhatsApp{
			Token:      required("WHATSAPP_TOKEN"),
			PhoneID:    required("WHATSAPP_PHONE_ID"),
			APIVersion: str("WHATSAPP_API_VERSION", "v19.0"),
		},
		Chatwoot: Chatwoot{
			URL:       strings.TrimRight(str("CHATWOOT_URL", "http://127.0.0.1:3000"), "/"),
			Token:     required("CHATWOOT_API_TOKEN"),
			AccountID: str("CHATWOOT_ACCOUNT_ID", "1"),
			InboxID:   integer("CHATWOOT_INBOX_ID", 2),
		},
		Redis: Redis{
			Addr:     required("REDIS_ADDR"),
			Password: str("REDIS_PASSWORD", ""),
			DB:       integer("REDIS_DB", 0),
		},
		SMTP: SMTP{
			Host:        str("SMTP_HOST", ""),
			Port:        integer("SMTP_PORT", 587),
			User:        str("SMTP_USER", ""),
			AppPassword: str("SMTP_APP_PASSWORD", ""),
			FromName:    str("SMTP_FROM_NAME", ""),
			DailyQuota:  integer("SMTP_DAILY_QUOTA", 450),
			Pause:       time.Duration(integer("SMTP_PAUSE_MS", 1500)) * time.Millisecond,
		},
		IMAP: IMAP{
			Host:         str("IMAP_HOST", "imap.gmail.com"),
			Port:         integer("IMAP_PORT", 993),
			PollInterval: time.Duration(integer("IMAP_POLL_MINUTES", 10)) * time.Minute,
		},
		WhatsAppPause: duration("WHATSAPP_PAUSE", 6*time.Second),
	}

	if c.Env == "production" && c.Redis.Password == "" {
		errs = append(errs, errors.New("REDIS_PASSWORD es obligatoria en producción"))
	}
	if c.SMTP.Host != "" && (c.SMTP.User == "" || c.SMTP.AppPassword == "") {
		errs = append(errs, errors.New("SMTP_HOST definido pero faltan SMTP_USER o SMTP_APP_PASSWORD"))
	}

	c.CORSOrigins = []string{"http://localhost:8880"}
	if c.BaseURL != "" {
		c.CORSOrigins = append(c.CORSOrigins, c.BaseURL)
	}
	for _, o := range strings.Split(get("CORS_ALLOWED_ORIGINS"), ",") {
		if o = strings.TrimRight(strings.TrimSpace(o), "/"); o != "" {
			c.CORSOrigins = append(c.CORSOrigins, o)
		}
	}

	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("configuración inválida:\n%w", err)
	}
	return c, nil
}
