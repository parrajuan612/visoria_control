package app

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"visoria-control/internal/domain"
	"visoria-control/internal/ports"
)

func TestExportPDFs(t *testing.T) {
	e := newCampSvc(t, rows(), campCfg())
	d, err := create(t, e, nil)
	if err != nil {
		t.Fatal(err)
	}
	id := d.Campaign.ID

	// Sin PDFs generados: error claro y nada escrito.
	var buf bytes.Buffer
	if _, err := e.svc.ExportPDFs(context.Background(), id, &buf); !errors.Is(err, ports.ErrNoPDFs) || buf.Len() != 0 {
		t.Fatalf("sin PDFs: err=%v bytes=%d", err, buf.Len())
	}
	if _, err := e.svc.ExportPDFs(context.Background(), "noexiste", &buf); err == nil {
		t.Fatal("campaña inexistente debía fallar")
	}

	// Con PDFs: uno con nombre peligroso, uno con error y otro sin documento.
	recs, _ := e.repo.Recipients(context.Background(), id)
	if len(recs) < 2 {
		t.Fatalf("la prueba necesita al menos 2 filas, hay %d", len(recs))
	}
	recs[0].Jugador = "../../etc/passwd/Ñandú Pérez"
	recs[0].Documento = &domain.Document{Path: "pdf/" + recs[0].ID + ".pdf"}
	e.files.pdfs = map[string][]byte{recs[0].ID: []byte("%PDF-uno")}
	recs[1].Documento = &domain.Document{Path: "pdf/" + recs[1].ID + ".pdf", Error: "falló"}
	if err := e.repo.SaveRecipients(context.Background(), id, recs[:2], domain.Summarize(recs)); err != nil {
		t.Fatal(err)
	}

	buf.Reset()
	n, err := e.svc.ExportPDFs(context.Background(), id, &buf)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 1 {
		t.Fatalf("esperaba 1 archivo, hay %d", len(zr.File))
	}
	name := zr.File[0].Name
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") || !strings.HasSuffix(name, ".pdf") {
		t.Errorf("nombre inseguro en el ZIP: %q", name)
	}
	rc, _ := zr.File[0].Open()
	got, _ := io.ReadAll(rc)
	if string(got) != "%PDF-uno" {
		t.Errorf("contenido distinto: %q", got)
	}
}
