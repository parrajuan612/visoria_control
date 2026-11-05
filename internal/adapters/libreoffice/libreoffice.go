// Package libreoffice convierte .docx a PDF con LibreOffice en modo headless (SDD §3.3 ADR-7).
// Port de becas/pdf.py: un perfil temporal por tanda (evita bloqueos entre ejecuciones), todos los
// archivos de la tanda en una sola llamada, y se da por convertido lo que dejó su PDF en la salida.
package libreoffice

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"visoria-control/internal/platform/log"
	"visoria-control/internal/ports"
)

const (
	DefaultTimeout = 10 * time.Minute
	maxOutput      = 4 << 10
)

// Options configura el conversor.
type Options struct {
	Binary  string        // ruta de soffice; vacío = SOFFICE_PATH ya resuelto o búsqueda en el PATH
	WorkDir string        // carpeta de los perfiles temporales; vacío = temporal del sistema
	Timeout time.Duration // por tanda; 0 = 10 min
}

type Converter struct {
	opts Options
}

var _ ports.PDFConverter = (*Converter)(nil)

func New(o Options) *Converter {
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	return &Converter{opts: o}
}

// Find busca el ejecutable: el indicado, o soffice/libreoffice/lowriter en el PATH.
func Find(explicit string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("%w (SOFFICE_PATH=%q)", ports.ErrConverterUnavailable, explicit)
		}
		return explicit, nil
	}
	for _, n := range []string{"soffice", "libreoffice", "lowriter"} {
		if p, err := exec.LookPath(n); err == nil {
			return p, nil
		}
	}
	return "", ports.ErrConverterUnavailable
}

// Convert convierte todos los .docx en una sola ejecución de LibreOffice. Devuelve nil si todos
// quedaron en outDir con su nombre base; si no, *ports.ConvertError con los que fallaron.
func (c *Converter) Convert(ctx context.Context, docxFiles []string, outDir string) error {
	if len(docxFiles) == 0 {
		return nil
	}
	bin, err := Find(c.opts.Binary)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		return err
	}
	absOut, err := filepath.Abs(outDir)
	if err != nil {
		return err
	}
	for _, f := range docxFiles {
		if !strings.HasSuffix(strings.ToLower(f), ".docx") {
			return fmt.Errorf("no es un .docx: %s", filepath.Base(f))
		}
		if _, err := os.Stat(f); err != nil {
			return fmt.Errorf("no existe %s", filepath.Base(f))
		}
	}

	// Perfil nuevo por tanda: dos ejecuciones (o una anterior colgada) no se bloquean entre sí.
	profile, err := os.MkdirTemp(c.opts.WorkDir, "lo-perfil-")
	if err != nil {
		return fmt.Errorf("creando el perfil temporal: %w", err)
	}
	defer os.RemoveAll(profile)
	profileURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(profile)}).String()

	args := []string{"-env:UserInstallation=" + profileURL, "--headless", "--norestore", "--nolockcheck",
		"--convert-to", "pdf", "--outdir", absOut}
	for _, f := range docxFiles {
		abs, err := filepath.Abs(f)
		if err != nil {
			return err
		}
		args = append(args, abs)
	}

	runCtx, cancel := context.WithTimeout(ctx, c.opts.Timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, bin, args...)
	// soffice lanza a soffice.bin: al vencer el plazo se mata TODO el grupo de procesos.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out

	start := time.Now()
	runErr := cmd.Run()
	lg := log.FromContext(ctx)
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("LibreOffice tardó más de %s y se canceló", c.opts.Timeout)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Como el cliente: el éxito se decide por los PDF que quedaron, no por el código de salida.
	var failed []string
	for _, f := range docxFiles {
		base := strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))
		if info, err := os.Stat(filepath.Join(absOut, base+".pdf")); err != nil || info.Size() == 0 {
			failed = append(failed, base)
		}
	}
	lg.Info("conversión a PDF", "archivos", len(docxFiles), "fallidos", len(failed), "duracion_ms", time.Since(start).Milliseconds(), "salida_error", runErr != nil)
	if len(failed) > 0 {
		o := strings.TrimSpace(out.String())
		if len(o) > maxOutput {
			o = o[:maxOutput]
		}
		return &ports.ConvertError{Failed: failed, Output: o}
	}
	return nil
}
