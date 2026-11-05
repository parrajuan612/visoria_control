package ports

import (
	"context"
	"errors"
	"strings"

	"visoria-control/internal/domain"
)

// DocError es un problema al generar o validar un documento, con un mensaje claro para el
// usuario (los mismos del sistema del cliente).
type DocError struct{ Message string }

func (e *DocError) Error() string { return e.Message }

// DocumentInput son los datos de una propuesta.
type DocumentInput struct {
	Recipient    domain.Recipient
	Torneo       string   // torneo donde se becó (va en el encabezado)
	FechaEmision string   // dd/mm/aaaa
	FechasPago   []string // dd/mm/aaaa; la beca de N pagos usa las N primeras
	Config       domain.MasterConfig
}

// DocRenderer rellena una plantilla Word con los datos de un destinatario.
type DocRenderer interface {
	// Render devuelve el .docx. Los errores de contenido son *DocError.
	Render(ctx context.Context, template []byte, in DocumentInput) ([]byte, error)
	// ValidateTemplate comprueba que la plantilla conserve los campos que se reemplazan.
	// Devuelve "" si está bien o el mensaje para el usuario.
	ValidateTemplate(data []byte) string
}

// PDFConverter convierte .docx a PDF (LibreOffice). Los PDF quedan en outDir con el mismo
// nombre base que cada archivo de entrada.
type PDFConverter interface {
	Convert(ctx context.Context, docxFiles []string, outDir string) error
}

// ErrConverterUnavailable: no hay LibreOffice instalado (o no se encontró el ejecutable).
var ErrConverterUnavailable = errors.New("no se pudo convertir a PDF: LibreOffice no está instalado (gratis: libreoffice.org)")

// ConvertError indica los archivos de un lote que NO quedaron convertidos; los demás sí.
type ConvertError struct {
	Failed []string // nombres base (sin extensión) que no generaron PDF
	Output string   // salida de LibreOffice, recortada (para el log)
}

func (e *ConvertError) Error() string {
	return "no se pudo convertir a PDF: " + strings.Join(e.Failed, ", ")
}

// Workspace da carpetas de trabajo temporales para generar documentos (el núcleo no toca el disco).
type Workspace interface {
	// Open crea una carpeta temporal y devuelve su ruta y la función que la borra con todo su contenido.
	Open() (dir string, cleanup func(), err error)
	// WriteFile escribe un archivo (solo el propietario puede leerlo: trae datos de menores).
	WriteFile(path string, data []byte) error
}
