package httpadapter

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"

	"visoria-control/internal/ports"

	"github.com/gin-gonic/gin"
)

type fakeTplUC struct {
	err    error
	backup string
	gotKey string
	gotLen int
}

func (f *fakeTplUC) List(context.Context) ([]ports.TemplateInfo, error) {
	return []ports.TemplateInfo{{Key: "infantil", Name: "Infantil", Installed: true}}, f.err
}
func (f *fakeTplUC) Install(_ context.Context, key string, data []byte) (string, error) {
	f.gotKey, f.gotLen = key, len(data)
	return f.backup, f.err
}

func tplRouter(f *fakeTplUC) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewTemplateHandler(f)
	r.GET("/templates", h.List)
	r.PUT("/templates/:key", h.Install)
	return r
}

func put(r *gin.Engine, key, filename string, content []byte) (int, map[string]any) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if filename != "" {
		fw, _ := w.CreateFormFile("file", filename)
		fw.Write(content)
	}
	w.Close()
	return call(r, "PUT", "/templates/"+key, &buf, w.FormDataContentType())
}

func TestTemplatesList(t *testing.T) {
	code, out := call(tplRouter(&fakeTplUC{}), "GET", "/templates", nil, "")
	if code != 200 || out["templates"] == nil {
		t.Errorf("%d %v", code, out)
	}
	if code, _ := call(tplRouter(&fakeTplUC{err: errors.New("x")}), "GET", "/templates", nil, ""); code != 500 {
		t.Errorf("error interno: %d", code)
	}
}

func TestTemplatesInstall(t *testing.T) {
	f := &fakeTplUC{backup: "infantil_20261105_090000.docx"}
	code, out := put(tplRouter(f), "infantil", "Mi Plantilla.DOCX", []byte("datos"))
	if code != 200 || f.gotKey != "infantil" || f.gotLen != 5 || out["respaldo"] != f.backup ||
		out["message"] != "Plantilla «Infantil» actualizada. La anterior quedó guardada en plantillas/anteriores." {
		t.Errorf("instalación: %d %v", code, out)
	}
	// Primera instalación: sin respaldo.
	if _, out := put(tplRouter(&fakeTplUC{}), "cadete_juvenil", "x.docx", []byte("d")); out["message"] != "Plantilla «Cadete y Juvenil» actualizada." {
		t.Errorf("primera: %v", out)
	}
	// Rechazada por la validación: 400 con el mensaje del cliente.
	bad := &fakeTplUC{err: &ports.InputError{Message: "La plantilla no se cambió. No encontré el campo «Club: XXXX»."}}
	if code, out := put(tplRouter(bad), "infantil", "x.docx", []byte("d")); code != 400 || !strings.HasPrefix(out["error"].(string), "La plantilla no se cambió.") {
		t.Errorf("rechazada: %d %v", code, out)
	}
	if code, out := put(tplRouter(&fakeTplUC{err: errors.New("disco: secreto")}), "infantil", "x.docx", []byte("d")); code != 500 || strings.Contains(out["error"].(string), "secreto") {
		t.Errorf("interno: %d %v", code, out)
	}
	// Entradas inválidas
	r := tplRouter(&fakeTplUC{})
	if code, _ := put(r, "otra", "x.docx", []byte("d")); code != 404 {
		t.Errorf("clave desconocida: %d", code)
	}
	if code, _ := put(r, "infantil", "", nil); code != 400 {
		t.Errorf("sin archivo: %d", code)
	}
	if code, _ := put(r, "infantil", "x.pdf", []byte("d")); code != 400 {
		t.Errorf("extensión: %d", code)
	}
	if code, _ := put(r, "infantil", "x.docx", bytes.Repeat([]byte("x"), maxTemplateUpload+10)); code != 413 {
		t.Errorf("grande: %d", code)
	}
	_ = httptest.NewRecorder
}
