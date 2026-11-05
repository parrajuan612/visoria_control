package libreoffice

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"visoria-control/internal/ports"
)

// soffice simulado: registra sus argumentos, crea el PDF de cada .docx salvo los que se llamen "*fallo*",
// y con FAKE_MODE=sleep deja un proceso hijo dormido (para probar que se mata el grupo entero).
const fakeSoffice = `#!/bin/sh
echo "$@" > "$FAKE_DIR/args.txt"
if [ "$FAKE_MODE" = "sleep" ]; then
  sleep 30 &
  echo $! > "$FAKE_DIR/hijo.pid"
  wait
  exit 0
fi
outdir=""
while [ $# -gt 0 ]; do
  case "$1" in
    --convert-to) shift 2 ;;
    --outdir) outdir="$2"; shift 2 ;;
    -env:*|--*) shift ;;
    *) b=$(basename "$1" .docx); case "$b" in *fallo*) ;; *) echo "%PDF-fake" > "$outdir/$b.pdf" ;; esac; shift ;;
  esac
done
exit ${FAKE_EXIT:-0}
`

func setupFake(t *testing.T) (bin, dir string) {
	t.Helper()
	dir = t.TempDir()
	bin = filepath.Join(dir, "soffice")
	if err := os.WriteFile(bin, []byte(fakeSoffice), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_DIR", dir)
	return bin, dir
}

func docxs(t *testing.T, names ...string) []string {
	dir := t.TempDir()
	var out []string
	for _, n := range names {
		p := filepath.Join(dir, n+".docx")
		os.WriteFile(p, []byte("x"), 0o644)
		out = append(out, p)
	}
	return out
}

func TestConvertArmaLosArgumentosYLimpiaElPerfil(t *testing.T) {
	bin, dir := setupFake(t)
	work := t.TempDir()
	out := filepath.Join(t.TempDir(), "pdf")
	files := docxs(t, "a", "b")
	if err := New(Options{Binary: bin, WorkDir: work}).Convert(context.Background(), files, out); err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args.txt"))
	a := string(args)
	for _, want := range []string{"-env:UserInstallation=file://" + work + "/lo-perfil-", "--headless", "--norestore", "--nolockcheck", "--convert-to pdf", "--outdir " + out, files[0], files[1]} {
		if !strings.Contains(a, want) {
			t.Errorf("falta %q en los argumentos: %s", want, a)
		}
	}
	for _, n := range []string{"a.pdf", "b.pdf"} {
		if _, err := os.Stat(filepath.Join(out, n)); err != nil {
			t.Errorf("falta %s", n)
		}
	}
	// El perfil temporal se borra siempre.
	if left, _ := filepath.Glob(filepath.Join(work, "lo-perfil-*")); len(left) != 0 {
		t.Errorf("perfil sin limpiar: %v", left)
	}
}

func TestConvertFalloParcial(t *testing.T) {
	bin, _ := setupFake(t)
	out := t.TempDir()
	err := New(Options{Binary: bin}).Convert(context.Background(), docxs(t, "bien", "se_fallo", "otro_fallo", "bien2"), out)
	var ce *ports.ConvertError
	if !errors.As(err, &ce) || strings.Join(ce.Failed, ",") != "se_fallo,otro_fallo" {
		t.Fatalf("error: %v", err)
	}
	// Los que sí se convirtieron quedan (el worker los puede usar).
	for _, n := range []string{"bien.pdf", "bien2.pdf"} {
		if _, err := os.Stat(filepath.Join(out, n)); err != nil {
			t.Errorf("falta %s", n)
		}
	}
}

// Como el cliente: el éxito se decide por los PDF que quedaron, no por el código de salida.
func TestConvertIgnoraElCodigoDeSalidaSiHayPDF(t *testing.T) {
	bin, _ := setupFake(t)
	t.Setenv("FAKE_EXIT", "7")
	if err := New(Options{Binary: bin}).Convert(context.Background(), docxs(t, "a"), t.TempDir()); err != nil {
		t.Errorf("con PDF generado no debe ser error: %v", err)
	}
}

func TestConvertPDFVacioCuentaComoFallo(t *testing.T) {
	bin, _ := setupFake(t)
	out := t.TempDir()
	os.WriteFile(filepath.Join(out, "a.pdf"), nil, 0o644) // un PDF vacío preexistente
	script := "#!/bin/sh\nexit 0\n"                       // y un soffice que no escribe nada
	os.WriteFile(bin, []byte(script), 0o755)
	var ce *ports.ConvertError
	if err := New(Options{Binary: bin}).Convert(context.Background(), docxs(t, "a"), out); !errors.As(err, &ce) {
		t.Errorf("un PDF vacío no es una conversión válida: %v", err)
	}
}

func TestConvertPlazoVencidoMataElGrupoDeProcesos(t *testing.T) {
	bin, dir := setupFake(t)
	t.Setenv("FAKE_MODE", "sleep")
	start := time.Now()
	err := New(Options{Binary: bin, Timeout: 400 * time.Millisecond}).Convert(context.Background(), docxs(t, "a"), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "tardó más") {
		t.Fatalf("debía vencer el plazo: %v", err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("tardó demasiado en cancelar: %v", d)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "hijo.pid"))
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	if pid == 0 {
		t.Fatal("el hijo no arrancó")
	}
	time.Sleep(200 * time.Millisecond)
	if err := syscall.Kill(pid, 0); err == nil {
		// aún existe (o es zombi): se confirma que no sigue vivo de verdad
		if st, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); !strings.Contains(string(st), ") Z") && len(st) > 0 {
			t.Errorf("el proceso hijo %d sigue vivo tras vencer el plazo", pid)
		}
	}
}

func TestConvertCancelacion(t *testing.T) {
	bin, _ := setupFake(t)
	t.Setenv("FAKE_MODE", "sleep")
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(300 * time.Millisecond); cancel() }()
	err := New(Options{Binary: bin}).Convert(ctx, docxs(t, "a"), t.TempDir())
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cancelación: %v", err)
	}
}

func TestConvertEntradasInvalidas(t *testing.T) {
	bin, _ := setupFake(t)
	c := New(Options{Binary: bin})
	ctx := context.Background()
	if err := c.Convert(ctx, nil, t.TempDir()); err != nil {
		t.Errorf("lista vacía: %v", err)
	}
	if err := c.Convert(ctx, []string{"/no/existe.docx"}, t.TempDir()); err == nil || !strings.Contains(err.Error(), "no existe") {
		t.Errorf("archivo inexistente: %v", err)
	}
	p := filepath.Join(t.TempDir(), "x.txt")
	os.WriteFile(p, []byte("x"), 0o644)
	if err := c.Convert(ctx, []string{p}, t.TempDir()); err == nil || !strings.Contains(err.Error(), "no es un .docx") {
		t.Errorf("no docx: %v", err)
	}
}

func TestFindYNoDisponible(t *testing.T) {
	if _, err := Find("/no/existe/soffice"); !errors.Is(err, ports.ErrConverterUnavailable) {
		t.Errorf("explícito inexistente: %v", err)
	}
	t.Setenv("PATH", t.TempDir()) // PATH vacío de ejecutables
	if _, err := Find(""); !errors.Is(err, ports.ErrConverterUnavailable) {
		t.Errorf("sin LibreOffice: %v", err)
	}
	if err := New(Options{}).Convert(context.Background(), docxs(t, "a"), t.TempDir()); !errors.Is(err, ports.ErrConverterUnavailable) {
		t.Errorf("Convert sin LibreOffice: %v", err)
	}
	bin, _ := setupFake(t)
	if got, err := Find(bin); err != nil || got != bin {
		t.Errorf("explícito: %q %v", got, err)
	}
	t.Setenv("PATH", filepath.Dir(bin))
	if got, err := Find(""); err != nil || got != bin {
		t.Errorf("en el PATH: %q %v", got, err)
	}
}

func TestPorDefectoUsa10Minutos(t *testing.T) {
	if New(Options{}).opts.Timeout != 10*time.Minute {
		t.Error("el plazo por defecto es de 10 minutos (SDD)")
	}
	_ = exec.ErrNotFound
}
