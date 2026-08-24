package external

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"visoria-control/internal/core/domain"
	"visoria-control/internal/core/ports"

	"github.com/jung-kurt/gofpdf"
)

type pdfGenerator struct{}

func NewPDFGenerator() ports.PDFGenerator {
	return &pdfGenerator{}
}

func (g *pdfGenerator) Generate(player domain.Player, tournament domain.TournamentInfo) (string, error) {

	err := os.MkdirAll("uploads/pdfs", os.ModePerm)
	if err != nil {
		return "", err
	}

	fileName := fmt.Sprintf("uploads/pdfs/%s.pdf", strings.ReplaceAll(player.Name, " ", "_"))

	pdf := gofpdf.New("P", "mm", "A4", "")

	// --- MARCA DE AGUA ---
	pdf.SetHeaderFunc(func() {
		pdf.SetAlpha(0.1, "Normal")
		// Si la imagen no existe, esto no fallará, solo no se mostrará
		pdf.ImageOptions("Isologo_6@2x (1).png", 30, 75, 150, 0, false, gofpdf.ImageOptions{ReadDpi: true}, 0, "")
		pdf.SetAlpha(1.0, "Normal")
	})

	pdf.AddPage()
	pdf.SetAutoPageBreak(false, 0)

	// Logo principal
	pdf.ImageOptions("logo.png", 10, 10, 70, 0, false, gofpdf.ImageOptions{ImageType: "PNG", ReadDpi: true}, 0, "")

	// --- ENCABEZADO ---
	pdf.SetY(32)
	pdf.SetFont("Arial", "", 10)
	fechaActual := time.Now().Format("02/01/2006")
	pdf.Cell(0, 4, fmt.Sprintf("BOGOTA - %s", fechaActual)) // Quitamos tilde a Bogotá por seguridad
	pdf.Ln(5)

	pdf.SetFont("Arial", "B", 10)
	pdf.MultiCell(0, 4, tournament.Name, "", "L", false)
	pdf.Ln(4)

	// --- DATOS DEL JUGADOR ---
	colWidth := 40.0
	rowHeight := 4.0

	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(colWidth, rowHeight, "Acudiente:", "", 0, "L", false, 0, "")
	pdf.SetFont("Arial", "", 10)
	pdf.CellFormat(0, rowHeight, player.GuardianName, "", 1, "L", false, 0, "")

	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(colWidth, rowHeight, "Jugador:", "", 0, "L", false, 0, "")
	pdf.SetFont("Arial", "", 10)
	pdf.CellFormat(0, rowHeight, player.Name, "", 1, "L", false, 0, "")

	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(colWidth, rowHeight, "Año Nacimiento:", "", 0, "L", false, 0, "")
	pdf.SetFont("Arial", "", 10)
	pdf.CellFormat(0, rowHeight, fmt.Sprintf("%d", player.BirthYear), "", 1, "L", false, 0, "")

	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(colWidth, rowHeight, "Movil:", "", 0, "L", false, 0, "") // Sin tilde
	pdf.SetFont("Arial", "", 10)
	pdf.CellFormat(0, rowHeight, "(+57) "+player.PrimaryPhone, "", 1, "L", false, 0, "")
	pdf.Ln(4)

	// --- REFERENCIA Y TÍTULO ---
	pdf.SetFont("Arial", "BU", 10)
	pdf.CellFormat(0, rowHeight, "Ref. Propuesta Jugadores seleccionados", "", 1, "L", false, 0, "")
	pdf.Ln(2)

	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(0, rowHeight, "PROGRAMA DE INTERCAMBIO DEPORTIVO", "", 1, "L", false, 0, "")
	pdf.Ln(2)

	// --- CUERPO DEL TEXTO ---
	pdf.SetFont("Arial", "", 10)
	pdf.MultiCell(0, 4, "Consiste en la participacion del ALUMNO - DEPORTISTA en el intercambio deportivo, torneo de futbol en ESPANA.", "", "J", false)
	pdf.SetFont("Arial", "U", 10)
	pdf.CellFormat(0, 4, "solamente participara de un torneo", "", 1, "L", false, 0, "")
	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(0, 4, fmt.Sprintf("- %s", tournament.Name), "", 1, "L", false, 0, "")
	pdf.Ln(2)

	// CATEGORIAS
	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(0, 4, "CATEGORIAS:", "", 1, "L", false, 0, "")
	pdf.SetFont("Arial", "", 10)
	pdf.CellFormat(0, 4, tournament.Category, "", 1, "L", false, 0, "")
	pdf.Ln(2)

	// FECHAS
	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(0, 4, "Fechas para el viaje:", "", 1, "L", false, 0, "")

	// Usamos CellFormat en lugar de Write para evitar el error de parsing de gofpdf
	pdf.CellFormat(0, 4, "SALIDA el 18/03/2027", "", 1, "L", false, 0, "")
	pdf.CellFormat(0, 4, "LLEGADA a Espana al aeropuerto de BARCELONA el 19/03/2027", "", 1, "L", false, 0, "")
	pdf.SetFont("Arial", "", 10)
	pdf.CellFormat(0, 4, ", ingresaran con cena y se recogeran en el aeropuerto.", "", 1, "L", false, 0, "")

	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(0, 4, "REGRESO el 28/03/2027", "", 1, "L", false, 0, "")

	pdf.CellFormat(0, 4, "INCLUYE:", "", 1, "L", false, 0, "")
	pdf.SetFont("Arial", "", 10)
	pdf.MultiCell(0, 4, "Alimentacion - hospedaje - transporte interno en Espana (aeropuerto-torneo-turismo-entrenos-hoteles) - Indumentaria - Inscripcion Torneo - Visitas turisticas...", "", "J", false)

	pdf.Ln(3)

	// --- TABLA DE COSTOS Y BECA ---
	becaNum, _ := strconv.Atoi(strings.ReplaceAll(player.Scholarship, "%", ""))

	pdf.SetFont("Arial", "B", 11)
	pdf.CellFormat(0, 5, fmt.Sprintf("COSTOS: EL JUGADOR OBTUVO BECA DEL %s", player.Scholarship), "", 1, "C", false, 0, "")
	pdf.Ln(1)

	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(80, 5, "BECA", "1", 0, "C", false, 0, "")
	pdf.CellFormat(80, 5, "VALOR EUROS", "1", 1, "C", false, 0, "") // Quitamos el símbolo del euro por ahora para evitar problemas de encoding

	pdf.SetFont("Arial", "", 10)
	filas := [][]string{
		{"Sin Beca", "2.800"},
		{"Beca al 30%", "1.960"},
		{"Beca al 50%", "1.550"},
		{"Beca al 70%", "990"},
		{"Beca al 100%", "200 administracion"},
		{"Acompanante", "1.800"},
	}

	for i, fila := range filas {
		fill := false
		percent := -1
		switch i {
		case 0:
			percent = 0
		case 1:
			percent = 30
		case 2:
			percent = 50
		case 3:
			percent = 70
		case 4:
			percent = 100
		}

		if percent == becaNum {
			pdf.SetFillColor(255, 255, 0)
			fill = true
		}

		pdf.CellFormat(80, 5, fila[0], "1", 0, "L", fill, 0, "")
		pdf.CellFormat(80, 5, fila[1], "1", 1, "C", fill, 0, "")
	}
	pdf.Ln(2)

	pdf.MultiCell(0, 4, "NO INCLUYE: Tiquetes Aereos, Emision del pasaporte, Seguro de viaje.\nLos pagos se deben realizar a la cuenta de ahorros # 22546881826 de BANCOLOMBIA...", "", "J", false)
	pdf.Ln(3)

	// --- TABLA RESPONSIVE DE PAGOS ---
	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(0, 4, "Programacion Pagos:", "", 1, "L", false, 0, "")

	// Leemos los pagos desde Google Sheets
	p1 := tournament.Pricing.Pago1
	// Limpiamos los símbolos raros o euros si los trajera para evitar crasheos
	p1 = strings.ReplaceAll(p1, "€", "")

	pdf.CellFormat(0, 5, fmt.Sprintf("1er pago: %s", p1), "", 1, "L", false, 0, "")

	pdf.Ln(4)
	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(0, 4, "Cordialmente:", "", 1, "L", false, 0, "")

	xFirma := pdf.GetX()
	yFirma := pdf.GetY()
	// Si tienes firma.png, la pone, si no, sigue sin romper
	pdf.ImageOptions("firma.png", xFirma, yFirma-3, 40, 0, false, gofpdf.ImageOptions{ReadDpi: true}, 0, "")

	pdf.SetY(yFirma + 24)
	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(0, 4, "SUYSAN COLMENARES C.", "", 1, "L", false, 0, "")

	err = pdf.OutputFileAndClose(fileName)
	if err != nil {
		return "", err
	}

	return fileName, nil
}
