package app

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"

	"visoria-control/internal/domain"
	"visoria-control/internal/ports"
)

var unsafeName = regexp.MustCompile(`[^\p{L}\p{N} ._-]+`)

// pdfEntryName: «fila_Nombre.pdf». El número de fila evita colisiones y se limpia el nombre
// para que no pueda escapar de la carpeta al descomprimir (zip-slip).
func pdfEntryName(r domain.Recipient) string {
	name := strings.TrimSpace(unsafeName.ReplaceAllString(r.Jugador, "_"))
	name = strings.Trim(name, "._ ")
	if rs := []rune(name); len(rs) > 80 {
		name = string(rs[:80])
	}
	if name == "" {
		name = r.ID
	}
	return fmt.Sprintf("%03d_%s.pdf", r.Fila, name)
}

// ExportPDFs implementa ports.CampaignUseCase.
func (s *CampaignService) ExportPDFs(ctx context.Context, campaignID string, w io.Writer) (int, error) {
	c, err := s.repo.Get(ctx, campaignID)
	if err != nil {
		return 0, err
	}
	recs, err := s.repo.Recipients(ctx, campaignID)
	if err != nil {
		return 0, err
	}
	var withPDF []domain.Recipient
	for _, r := range recs {
		if r.Documento != nil && r.Documento.Path != "" && r.Documento.Error == "" {
			withPDF = append(withPDF, r)
		}
	}
	if len(withPDF) == 0 {
		return 0, ports.ErrNoPDFs
	}
	zw := zip.NewWriter(w)
	n := 0
	for _, r := range withPDF {
		data, err := s.files.ReadPDF(ctx, c, r.Documento.Path)
		if err != nil {
			return n, fmt.Errorf("leyendo el PDF de la fila %d: %w", r.Fila, err)
		}
		f, err := zw.Create(pdfEntryName(r))
		if err != nil {
			return n, err
		}
		if _, err := f.Write(data); err != nil {
			return n, err
		}
		n++
	}
	return n, zw.Close()
}

// RecipientPDF implementa ports.CampaignUseCase.
func (s *CampaignService) RecipientPDF(ctx context.Context, campaignID, recipientID string) ([]byte, string, error) {
	c, err := s.repo.Get(ctx, campaignID)
	if err != nil {
		return nil, "", err
	}
	recs, err := s.repo.Recipients(ctx, campaignID)
	if err != nil {
		return nil, "", err
	}
	for _, r := range recs {
		if r.ID != recipientID {
			continue
		}
		if r.Documento == nil || r.Documento.Path == "" || r.Documento.Error != "" {
			return nil, "", ports.ErrRecipientNotFound
		}
		data, err := s.files.ReadPDF(ctx, c, r.Documento.Path)
		if err != nil {
			return nil, "", fmt.Errorf("leyendo el PDF: %w", err)
		}
		return data, domain.VisibleFileNames(recs)[r.ID], nil
	}
	return nil, "", ports.ErrRecipientNotFound
}
