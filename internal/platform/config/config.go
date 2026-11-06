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
	// DataDir es la carpeta de artefactos y auditoría de las campañas (EXCEL_ORIGINAL.xlsx, events.jsonl…).
	DataDir string
	BaseURL string // URL pública, sin "/" final
	// Orígenes CORS permitidos (BASE_URL + localhost + CORS_ALLOWED_ORIGINS).
	CORSOrigins []string

	// SheetsURL es la hoja publicada (CSV) por defecto. Opcional: el operador puede
	// enviar otra desde la API (POST /api/v1/config/sync) sin reiniciar.
	SheetsURL string

	Docs Docs

	// DispatchAllowlist: si no está vacía, SOLO se envía a estos móviles y correos (el resto se omite).
	// Es una válvula de seguridad para las pruebas con datos reales.
	DispatchAllowlist []string

	WhatsApp WhatsApp
	Chatwoot Chatwoot
	Redis    Redis
	SMTP     SMTP
	IMAP     IMAP

	// Enlaces firmados a PDF/ZIP (LINK_SECRET, mínimo 32 caracteres; vacío = sin enlaces) y vigencia de los de Chatwoot.
	LinkSecret  string
	PDFLinkDays int

	// Pausa entre envíos de WhatsApp (antes fija en 6 s en el código).
	WhatsAppPause time.Duration
}

// Docs configura la generación de propuestas (Word -> PDF con LibreOffice).
type Docs struct {
	SofficePath string        // vacío = se busca soffice/libreoffice en el PATH
	BatchSize   int           // destinatarios por tanda de conversión (SDD: 25)
	Timeout     time.Duration // plazo por tanda (SDD: 10 min)
	WorkDir     string        // carpeta de trabajo; vacía = temporal del sistema. LibreOffice por snap no lee /tmp.
}

type WhatsApp struct {
	Token      string
	PhoneID    string // Phone Number ID (no el WABA ID)
	APIVersion string
	Template   string  // plantilla aprobada de la propuesta
	Language   string  // idioma de la plantilla
	RatePerSec float64 // mensajes por segundo (muy por debajo del límite de Meta)
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

// SMTP: correo por Gmail (ADR-9). El canal se habilita con SMTP_USER y SMTP_APP_PASSWORD.
type SMTP struct {
	Host, User, AppPassword, FromName string
	Port                              int
	Security                          string // starttls (por defecto) | tls | none (servidor local de pruebas)
	DailyQuota                        int
	Pause                             time.Duration
	WhatsAppContact                   string // WhatsApp del botón «Escríbenos» (vacío = el del cliente)
}

func (s SMTP) Enabled() bool {
	return s.Host != "" && s.User != "" && (s.AppPassword != "" || s.Security == "none")
}

type IMAP struct {
	Host         string
	Port         int
	PollInterval time.Duration
}

func csv(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
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
	float := func(key string, def float64) float64 {
		v := strings.TrimSpace(get(key))
		if v == "" {
			return def
		}
		n, err := strconv.ParseFloat(strings.Replace(v, ",", ".", 1), 64)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s debe ser un número, recibió %q", key, v))
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
		Env:         str("APP_ENV", "development"),
		Port:        str("PORT", "8880"),
		LogLevel:    str("LOG_LEVEL", "info"),
		DataDir:     str("DATA_DIR", "data"),
		BaseURL:     strings.TrimRight(required("BASE_URL"), "/"),
		SheetsURL:   str("SHEETS_CSV_URL", ""),
		LinkSecret:  str("LINK_SECRET", ""),
		PDFLinkDays: integer("PDF_LINK_DAYS", 30),
		WhatsApp: WhatsApp{
			Token:      required("WHATSAPP_TOKEN"),
			PhoneID:    required("WHATSAPP_PHONE_ID"),
			APIVersion: str("WHATSAPP_API_VERSION", "v19.0"),
			Template:   str("WHATSAPP_TEMPLATE", "purchase_receipt_3"),
			Language:   str("WHATSAPP_LANGUAGE", "es_CO"),
			RatePerSec: float("WHATSAPP_RATE_PER_SEC", 5),
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
			Host:            str("SMTP_HOST", "smtp.gmail.com"),
			Port:            integer("SMTP_PORT", 587),
			User:            str("SMTP_USER", ""),
			AppPassword:     strings.Join(strings.Fields(get("SMTP_APP_PASSWORD")), ""), // Google la muestra como «abcd efgh ijkl mnop»
			FromName:        str("SMTP_FROM_NAME", "Majestic Intercambio Deportivo"),
			Security:        strings.ToLower(str("SMTP_SECURITY", "starttls")),
			DailyQuota:      integer("SMTP_DAILY_QUOTA", 450),
			Pause:           time.Duration(integer("SMTP_PAUSE_MS", 1500)) * time.Millisecond,
			WhatsAppContact: str("MAIL_WHATSAPP", ""),
		},
		IMAP: IMAP{
			Host:         str("IMAP_HOST", "imap.gmail.com"),
			Port:         integer("IMAP_PORT", 993),
			PollInterval: time.Duration(integer("IMAP_POLL_MINUTES", 10)) * time.Minute,
		},
		Docs: Docs{
			SofficePath: str("SOFFICE_PATH", ""),
			BatchSize:   integer("DOCS_BATCH_SIZE", 25),
			Timeout:     duration("DOCS_TIMEOUT", 10*time.Minute),
			WorkDir:     str("DOCS_WORKDIR", ""),
		},
		DispatchAllowlist: csv(get("DISPATCH_ALLOWLIST")),
		WhatsAppPause:     duration("WHATSAPP_PAUSE", 6*time.Second),
	}

	if c.Docs.BatchSize < 1 || c.Docs.BatchSize > 100 {
		errs = append(errs, fmt.Errorf("DOCS_BATCH_SIZE debe estar entre 1 y 100, recibió %d", c.Docs.BatchSize))
	}
	if c.Env == "production" && c.Redis.Password == "" {
		errs = append(errs, errors.New("REDIS_PASSWORD es obligatoria en producción"))
	}
	if c.SMTP.User != "" && c.SMTP.AppPassword == "" && c.SMTP.Security != "none" {
		errs = append(errs, errors.New("SMTP_USER definido pero falta SMTP_APP_PASSWORD (la contraseña de aplicación de Gmail)"))
	}
	switch c.SMTP.Security {
	case "starttls", "tls", "none":
	default:
		errs = append(errs, fmt.Errorf("SMTP_SECURITY debe ser starttls, tls o none, recibió %q", c.SMTP.Security))
	}
	if c.WhatsApp.RatePerSec <= 0 || c.WhatsApp.RatePerSec > 50 {
		errs = append(errs, fmt.Errorf("WHATSAPP_RATE_PER_SEC debe estar entre 0 y 50, recibió %v", c.WhatsApp.RatePerSec))
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
