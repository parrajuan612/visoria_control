// Package campaignfs guarda en disco los artefactos de una campaña (SDD §3.6.2):
//
//	{base}/campaigns/{fecha}_{torneo}_{id}/
//	    EXCEL_ORIGINAL.xlsx   el archivo tal como se subió
//	    campaign.json         campaña + configuración congelada (para reconstruir)
//	    events.jsonl          auditoría append-only: una línea JSON por evento
package campaignfs

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"visoria-control/internal/domain"
	"visoria-control/internal/ports"
)

const (
	dirMode  = 0o750
	fileMode = 0o640
)

type Store struct {
	base string
	mu   sync.Mutex // serializa las escrituras de auditoría (un evento = una línea completa)
}

var _ ports.CampaignFiles = (*Store)(nil)

// New crea el almacén bajo base (se crea si no existe).
func New(base string) (*Store, error) {
	if strings.TrimSpace(base) == "" {
		return nil, errors.New("DATA_DIR vacío")
	}
	abs, err := filepath.Abs(base)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(abs, "campaigns"), dirMode); err != nil {
		return nil, fmt.Errorf("creando %s: %w", abs, err)
	}
	return &Store{base: abs}, nil
}

// dir devuelve la carpeta de la campaña, garantizando que no salga de la base.
func (s *Store) dir(c domain.Campaign) (string, error) {
	if c.Dir == "" || c.Dir == "." || c.Dir == ".." || strings.ContainsAny(c.Dir, `/\`) || c.Dir != filepath.Base(c.Dir) {
		return "", fmt.Errorf("carpeta de campaña no válida: %q", c.Dir)
	}
	d := filepath.Join(s.base, "campaigns", c.Dir)
	if err := os.MkdirAll(d, dirMode); err != nil {
		return "", err
	}
	return d, nil
}

// writeAtomic escribe en un temporal y renombra: un fallo no deja el archivo a medias.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), fileMode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (s *Store) SaveOriginal(_ context.Context, c domain.Campaign, data []byte) error {
	d, err := s.dir(c)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(d, "EXCEL_ORIGINAL.xlsx"), data)
}

func (s *Store) SaveSnapshot(_ context.Context, c domain.Campaign) error {
	d, err := s.dir(c)
	if err != nil {
		return err
	}
	// La configuración congelada vive en el snapshot (el JSON de la campaña no la incluye).
	body, err := json.MarshalIndent(struct {
		domain.Campaign
		Config domain.MasterConfig `json:"config"`
	}{c, c.Config}, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(d, "campaign.json"), body)
}

// Audit agrega una línea a events.jsonl. Solo agrega: nunca reescribe ni borra.
func (s *Store) Audit(_ context.Context, c domain.Campaign, e domain.AuditEvent) error {
	d, err := s.dir(c)
	if err != nil {
		return err
	}
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(filepath.Join(d, "events.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, fileMode)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	w.Write(line)
	w.WriteByte('\n')
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil { // el evento queda en disco antes de responder al operador
		f.Close()
		return err
	}
	return f.Close()
}

var reRecipientID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// SavePDF copia el PDF a pdf/{id}.pdf (atómico) y devuelve su ruta relativa y su SHA-256.
// El nombre interno es el ID estable del destinatario; el nombre visible se calcula al adjuntar.
func (s *Store) SavePDF(_ context.Context, c domain.Campaign, recipientID, src string) (string, string, error) {
	if !reRecipientID.MatchString(recipientID) {
		return "", "", fmt.Errorf("ID de destinatario no válido: %q", recipientID)
	}
	d, err := s.dir(c)
	if err != nil {
		return "", "", err
	}
	in, err := os.Open(src)
	if err != nil {
		return "", "", err
	}
	defer in.Close()
	head := make([]byte, 5)
	if n, _ := io.ReadFull(in, head); n < 5 || string(head) != "%PDF-" {
		return "", "", fmt.Errorf("el archivo generado no es un PDF válido")
	}
	if _, err := in.Seek(0, io.SeekStart); err != nil {
		return "", "", err
	}
	dstDir := filepath.Join(d, "pdf")
	if err := os.MkdirAll(dstDir, dirMode); err != nil {
		return "", "", err
	}
	tmp, err := os.CreateTemp(dstDir, ".tmp-*")
	if err != nil {
		return "", "", err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), in); err != nil {
		tmp.Close()
		return "", "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", "", err
	}
	if err := tmp.Close(); err != nil {
		return "", "", err
	}
	if err := os.Chmod(tmp.Name(), fileMode); err != nil {
		return "", "", err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dstDir, recipientID+".pdf")); err != nil {
		return "", "", err
	}
	return "pdf/" + recipientID + ".pdf", hex.EncodeToString(h.Sum(nil)), nil
}

const maxPDFBytes = 25 << 20

// ReadPDF lee un PDF ya guardado. La ruta debe ser la devuelta por SavePDF (pdf/{id}.pdf).
func (s *Store) ReadPDF(_ context.Context, c domain.Campaign, rel string) ([]byte, error) {
	if !strings.HasPrefix(rel, "pdf/") || strings.Contains(rel, "..") || strings.Count(rel, "/") != 1 || strings.Contains(rel, `\`) {
		return nil, fmt.Errorf("ruta de PDF no válida: %q", rel)
	}
	d, err := s.dir(c)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Join(d, rel))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxPDFBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxPDFBytes {
		return nil, fmt.Errorf("el PDF supera el tamaño máximo")
	}
	return data, nil
}
