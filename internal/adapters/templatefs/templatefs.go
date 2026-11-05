// Package templatefs guarda las plantillas Word en disco: {dir}/{clave}.docx y las versiones
// anteriores en {dir}/anteriores/{clave}_{aaaammdd_hhmmss}.docx (como plantillas/anteriores del cliente).
package templatefs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"visoria-control/internal/domain"
	"visoria-control/internal/ports"
)

type Store struct {
	dir string
	now func() time.Time
	mu  sync.Mutex // un reemplazo a la vez
}

var _ ports.TemplateStore = (*Store)(nil)

func New(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("carpeta de plantillas vacía")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(abs, "anteriores"), 0o750); err != nil {
		return nil, fmt.Errorf("creando %s: %w", abs, err)
	}
	return &Store{dir: abs, now: time.Now}, nil
}

func known(key string) bool { return domain.TemplateName(key) != "" }

func (s *Store) path(key string) string { return filepath.Join(s.dir, key+".docx") }

func (s *Store) Get(_ context.Context, key string) ([]byte, error) {
	if !known(key) {
		return nil, ports.ErrTemplateNotFound
	}
	b, err := os.ReadFile(s.path(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ports.ErrTemplateNotFound
	}
	return b, err
}

func (s *Store) Put(_ context.Context, key string, data []byte) (string, error) {
	if !known(key) {
		return "", fmt.Errorf("plantilla desconocida: %q", key)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	backup := ""
	if _, err := os.Stat(s.path(key)); err == nil {
		stamp := s.now().Format("20060102_150405")
		backup = fmt.Sprintf("%s_%s.docx", key, stamp)
		for n := 2; ; n++ { // dos reemplazos en el mismo segundo no se pisan
			if _, err := os.Stat(filepath.Join(s.dir, "anteriores", backup)); err != nil {
				break
			}
			backup = fmt.Sprintf("%s_%s_%d.docx", key, stamp, n)
		}
		if err := copyFile(s.path(key), filepath.Join(s.dir, "anteriores", backup)); err != nil {
			return "", fmt.Errorf("guardando la versión anterior: %w", err)
		}
	}
	tmp, err := os.CreateTemp(s.dir, ".tmp-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp.Name(), 0o640); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), s.path(key)); err != nil {
		return "", err
	}
	return backup, nil
}

func (s *Store) List(_ context.Context) ([]ports.TemplateInfo, error) {
	out := make([]ports.TemplateInfo, 0, len(domain.TemplateKeys))
	for _, k := range domain.TemplateKeys {
		info := ports.TemplateInfo{Key: k, Name: domain.TemplateName(k)}
		if st, err := os.Stat(s.path(k)); err == nil {
			info.Installed, info.Size, info.ModifiedAt = true, st.Size(), st.ModTime().UTC()
		}
		old, _ := filepath.Glob(filepath.Join(s.dir, "anteriores", k+"_*.docx"))
		info.Versions = len(old)
		out = append(out, info)
	}
	return out, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
