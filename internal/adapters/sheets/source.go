package sheets

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"visoria-control/internal/domain"
	"visoria-control/internal/platform/httpx"
	"visoria-control/internal/ports"
)

const maxCSVBytes = 2 << 20 // 2 MiB: una hoja de configuración real pesa unos pocos KB

// ErrInvalidURL: la URL no es una hoja publicada de Google Sheets.
var ErrInvalidURL = errors.New("URL no permitida")

// googleHost indica si el host puede servir una hoja publicada. Se acepta
// docs.google.com y los dominios googleusercontent.com a los que Google redirige.
func googleHost(h string) bool {
	h = strings.ToLower(h)
	return h == "docs.google.com" || h == "googleusercontent.com" || strings.HasSuffix(h, ".googleusercontent.com")
}

// ValidateGoogleSheetsURL exige https y una hoja publicada de Google. Impide que el
// endpoint de sincronización se use para llamar a otros servidores (SSRF).
func ValidateGoogleSheetsURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return fmt.Errorf("%w: no es una URL válida", ErrInvalidURL)
	}
	switch {
	case u.Scheme != "https":
		return fmt.Errorf("%w: debe empezar por https://", ErrInvalidURL)
	case u.User != nil:
		return fmt.Errorf("%w: no puede incluir usuario ni contraseña", ErrInvalidURL)
	case strings.ToLower(u.Hostname()) != "docs.google.com" || u.Port() != "":
		return fmt.Errorf("%w: debe ser un enlace de Google Sheets (docs.google.com)", ErrInvalidURL)
	case !strings.HasPrefix(u.Path, "/spreadsheets/"):
		return fmt.Errorf("%w: debe ser un enlace de hoja de cálculo (docs.google.com/spreadsheets/…)", ErrInvalidURL)
	}
	return nil
}

// Source descarga la hoja publicada y la interpreta.
type Source struct {
	client *http.Client
	// allow decide si una URL (inicial o de redirección) puede consultarse.
	allow func(*url.URL) bool
}

var _ ports.ConfigSource = (*Source)(nil)

// NewSource restringe las consultas (y sus redirecciones) a Google por https.
func NewSource() *Source {
	return newSource(func(u *url.URL) bool { return u.Scheme == "https" && googleHost(u.Hostname()) })
}

func newSource(allow func(*url.URL) bool) *Source {
	c := *httpx.New(httpx.LongTimeout)
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("demasiadas redirecciones")
		}
		if !allow(req.URL) {
			return fmt.Errorf("redirección a un servidor no permitido (%s)", req.URL.Hostname())
		}
		return nil
	}
	return &Source{client: &c, allow: allow}
}

func (s *Source) Load(ctx context.Context, rawURL string) (domain.MasterConfig, []domain.Issue, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !s.allow(u) {
		return domain.MasterConfig{}, nil, ErrInvalidURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return domain.MasterConfig{}, nil, fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}
	req.Header.Set("Accept", "text/csv")

	resp, err := s.client.Do(req)
	if err != nil {
		// Se omite la URL del error (url.Error la incluye): el enlace publicado no debe quedar en logs.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return domain.MasterConfig{}, nil, fmt.Errorf("no se pudo descargar la hoja: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return domain.MasterConfig{}, nil, fmt.Errorf("Google respondió HTTP %d: la hoja no existe o no está publicada en la web", resp.StatusCode)
	}
	if mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); mt == "text/html" {
		return domain.MasterConfig{}, nil, errors.New("Google devolvió una página web y no un CSV: publique la hoja con Archivo → Compartir → Publicar en la web → formato CSV")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxCSVBytes+1))
	if err != nil {
		return domain.MasterConfig{}, nil, fmt.Errorf("error leyendo la hoja: %w", err)
	}
	if len(data) > maxCSVBytes {
		return domain.MasterConfig{}, nil, fmt.Errorf("la hoja supera el tamaño máximo de %d MiB", maxCSVBytes>>20)
	}

	cfg, issues := Parse(strings.NewReader(string(data)))
	return cfg, issues, nil
}
