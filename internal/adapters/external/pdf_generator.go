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

	// 👉 Paso 3: Usamos el ID único para nombrar el archivo físico
	nombreSeguro := strings.ReplaceAll(player.Name, " ", "_")
	fileName := fmt.Sprintf("uploads/pdfs/%s_%s.pdf", nombreSeguro, player.FileID)

	pdf := gofpdf.New("P", "mm", "A4", "")

	tr := pdf.UnicodeTranslatorFromDescriptor("cp1252")

	pdf.SetHeaderFunc(func() {
		pdf.SetAlpha(0.1, "Normal")
		pdf.ImageOptions("Isologo_6@2x (1).png", 30, 75, 150, 0, false, gofpdf.ImageOptions{ReadDpi: true}, 0, "")
		pdf.SetAlpha(1.0, "Normal")
	})

	pdf.AddPage()
	pdf.SetAutoPageBreak(false, 0)

	pdf.ImageOptions("logo.png", 10, 10, 70, 0, false, gofpdf.ImageOptions{ImageType: "PNG", ReadDpi: true}, 0, "")

	pdf.SetY(32)
	pdf.SetFont("Arial", "", 10)
	fechaActual := time.Now().Format("02/01/2006")
	// El "R" alinea el texto a la derecha de la celda que ocupa todo el ancho (0)
	pdf.CellFormat(0, 4, tr(fmt.Sprintf("BOGOTÁ - %s", fechaActual)), "", 1, "R", false, 0, "")
	pdf.Ln(5)

	pdf.SetFont("Arial", "B", 10)
	pdf.MultiCell(0, 4, tr(player.VisoriaLocation), "", "L", false)
	pdf.Ln(4)

	colWidth := 40.0
	rowHeight := 4.0

	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(colWidth, rowHeight, "Acudiente:", "", 0, "L", false, 0, "")
	pdf.SetFont("Arial", "", 10)
	pdf.CellFormat(0, rowHeight, tr(player.GuardianName), "", 1, "L", false, 0, "")

	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(colWidth, rowHeight, "Jugador:", "", 0, "L", false, 0, "")
	pdf.SetFont("Arial", "", 10)
	pdf.CellFormat(0, rowHeight, tr(player.Name), "", 1, "L", false, 0, "")

	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(colWidth, rowHeight, "Club:", "", 0, "L", false, 0, "")
	pdf.SetFont("Arial", "", 10)
	pdf.CellFormat(0, rowHeight, tr(player.Club), "", 1, "L", false, 0, "")

	pdf.SetFont("Arial", "B", 10)
	// Cambiamos la palabra "Año" por "Fecha"
	pdf.CellFormat(colWidth, rowHeight, tr("Fecha de Nacimiento:"), "", 0, "L", false, 0, "")
	pdf.SetFont("Arial", "", 10)
	// Imprimimos el nuevo string directamente
	pdf.CellFormat(0, rowHeight, player.BirthDate, "", 1, "L", false, 0, "")

	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(colWidth, rowHeight, tr("Móvil:"), "", 0, "L", false, 0, "")
	pdf.SetFont("Arial", "", 10)
	pdf.CellFormat(0, rowHeight, "(+57) "+player.PrimaryPhone, "", 1, "L", false, 0, "")
	pdf.Ln(4)

	pdf.SetFont("Arial", "BU", 10)
	pdf.CellFormat(0, rowHeight, "Ref. Propuesta Jugadores seleccionados", "", 1, "C", false, 0, "")
	pdf.Ln(2)

	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(0, rowHeight, "PROGRAMA DE INTERCAMBIO DEPORTIVO", "", 1, "C", false, 0, "")
	pdf.Ln(2)

	pdf.SetFont("Arial", "", 10)
	pdf.MultiCell(0, 4, tr("Consiste en la participación del ALUMNO - DEPORTISTA en el intercambio deportivo, torneo de fútbol en ESPAÑA."), "", "J", false)
	pdf.SetFont("Arial", "U", 10)
	pdf.CellFormat(0, 4, tr("solamente participará de un torneo"), "", 1, "L", false, 0, "")

	// 👉 CAMBIO AQUÍ: Usamos MultiCell para soportar múltiples torneos con saltos de línea
	pdf.SetFont("Arial", "B", 10)
	pdf.MultiCell(0, 4, tr(fmt.Sprintf("- %s", tournament.Name)), "", "L", false)
	pdf.Ln(2)

	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(0, 4, "CATEGORIAS:", "", 1, "L", false, 0, "")
	pdf.SetFont("Arial", "", 10)
	// 👉 CAMBIO AQUÍ: Usamos MultiCell por si las categorías ocupan más de una línea
	pdf.MultiCell(0, 4, tr(tournament.Category), "", "L", false)
	pdf.Ln(2)

	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(0, 4, "Fechas para el viaje:", "", 1, "L", false, 0, "")

	pdf.Write(4, "SALIDA el 18/03/2027\n")

	pdf.Write(4, tr("LLEGADA a España al aeropuerto de BARCELONA el 19/03/2027"))
	pdf.SetFont("Arial", "", 10)
	pdf.Write(4, tr(", ingresarán con cena y se recogerán en el aeropuerto en el transcurso del día máximo hasta las 5:00 pm hora España.\n"))

	pdf.SetFont("Arial", "B", 10)
	pdf.Write(4, "REGRESO el 28/03/2027 ")
	pdf.SetFont("Arial", "", 10)
	pdf.Write(4, tr("salen con Desayuno y almuerzo, los buses los recogeran de nuevo para ir al aeropuerto, se sugiere que los vuelos sean después de las 8:00 pm.\n"))

	pdf.SetFont("Arial", "B", 10)
	pdf.Write(4, "INCLUYE: ")
	pdf.SetFont("Arial", "", 10)
	pdf.Write(4, tr("Alimentación - hospedaje - transporte interno en España (aeropuerto-torneo-turismo-entrenos-hoteles) - Indumentaria - Inscripción Torneo - Visitas turísticas, Visorías por parte de los clubes y las academias que tenemos convenios para que el jugador continúe con su primer proceso en Europa según su desempeño, este puede ser de 30-60-90 días.\n"))

	pdf.Ln(2)
	pdf.MultiCell(0, 4, tr("En el mes de enero/2027 se realizará la pre-temporada en Bogotá. (fechas por confirmar)"), "", "L", false)
	pdf.Ln(3)

	becaNum, _ := strconv.Atoi(strings.ReplaceAll(player.Scholarship, "%", ""))

	pdf.SetFont("Arial", "B", 11)
	pdf.CellFormat(0, 5, fmt.Sprintf("COSTOS: EL JUGADOR OBTUVO BECA DEL %s", player.Scholarship), "", 1, "C", false, 0, "")
	pdf.Ln(1)

	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(80, 5, "BECA", "1", 0, "C", false, 0, "")
	// 👉 INCLUIMOS EL SÍMBOLO € EN EL ENCABEZADO DE LA TABLA
	pdf.CellFormat(80, 5, tr("VALOR € EUROS"), "1", 1, "C", false, 0, "")

	pdf.SetFont("Arial", "", 10)
	filas := [][]string{
		{"Sin Beca", "€ 2.800"},
		{"Beca al 30%", "€ 1.960"},
		{"Beca al 50%", "€ 1.550"},
		{"Beca al 70%", "€ 990"},
		{"Beca al 100%", "€ 200 administración"},
		{"Acompañante", "€ 1.800"},
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

		pdf.CellFormat(80, 5, tr(fila[0]), "1", 0, "L", fill, 0, "")
		pdf.CellFormat(80, 5, tr(fila[1]), "1", 1, "C", fill, 0, "")
	}
	pdf.Ln(2)

	pdf.SetFont("Arial", "BU", 10) // B = Bold (Negrita), U = Underline (Subrayado)
	pdf.Write(4, "NO INCLUYE: ")
	pdf.SetFont("Arial", "", 10) // Volvemos a la fuente normal
	// Usamos Write de nuevo para que continúe justo al lado y haga el salto de línea automático
	pdf.Write(4, tr("Tiquetes Aéreos, Emisión del pasaporte, Seguro de viaje.\nLos pagos se deben realizar a la cuenta de ahorros # 22546881826 de BANCOLOMBIA o en DAVIVIENDA cuenta de ahorros # 0570008380462534 las dos a nombre de Suysan Colmenares Camargo C.C 79739776. SEGÚN LOS VALORES VENTA DE DIVISAS CAMBIOS VANCOUVER (página web cambiosvancouver.com)\n"))
	pdf.Ln(3)

	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(0, 4, tr("Programación Pagos:"), "", 1, "L", false, 0, "")

	p1 := strings.ReplaceAll(strings.ReplaceAll(tournament.Pricing.Pago1, "€", ""), ",00", "")
	p2 := strings.ReplaceAll(strings.ReplaceAll(tournament.Pricing.Pago2, "€", ""), ",00", "")
	p3 := strings.ReplaceAll(strings.ReplaceAll(tournament.Pricing.Pago3, "€", ""), ",00", "")

	numPagos := 0
	if strings.TrimSpace(p1) != "" {
		numPagos++
	}
	if strings.TrimSpace(p2) != "" {
		numPagos++
	}
	if strings.TrimSpace(p3) != "" {
		numPagos++
	}

	if numPagos > 0 {
		anchoTotal := 180.0
		anchoColumna := anchoTotal / float64(numPagos+1)

		pdf.SetFont("Arial", "B", 10)
		pdf.CellFormat(anchoColumna, 5, "PROGRAMA", "1", 0, "C", false, 0, "")

		// 👉 INCLUIMOS EL SÍMBOLO € AL FINAL DEL MONTO EN LOS PAGOS
		if strings.TrimSpace(p1) != "" {
			pdf.CellFormat(anchoColumna, 5, tr(fmt.Sprintf("1er pago %s€", strings.TrimSpace(p1))), "1", 0, "C", false, 0, "")
		}
		if strings.TrimSpace(p2) != "" {
			pdf.CellFormat(anchoColumna, 5, tr(fmt.Sprintf("2do pago %s€", strings.TrimSpace(p2))), "1", 0, "C", false, 0, "")
		}
		if strings.TrimSpace(p3) != "" {
			pdf.CellFormat(anchoColumna, 5, tr(fmt.Sprintf("3er pago %s€", strings.TrimSpace(p3))), "1", 0, "C", false, 0, "")
		}
		pdf.Ln(5)

		pdf.SetFont("Arial", "", 10)
		pdf.CellFormat(anchoColumna, 5, tr("TORNEO ESPAÑA"), "1", 0, "C", false, 0, "")

		if strings.TrimSpace(p1) != "" {
			fecha1 := "30/09/2026" // Valor por defecto si olvidan llenarlo
			if player.PaymentDate1 != "" {
				fecha1 = player.PaymentDate1
			}
			pdf.CellFormat(anchoColumna, 5, fecha1, "1", 0, "C", false, 0, "")
		}
		if strings.TrimSpace(p2) != "" {
			fecha2 := "30/10/2026"
			if player.PaymentDate2 != "" {
				fecha2 = player.PaymentDate2
			}
			pdf.CellFormat(anchoColumna, 5, fecha2, "1", 0, "C", false, 0, "")
		}
		if strings.TrimSpace(p3) != "" {
			fecha3 := "15/12/2026"
			if player.PaymentDate3 != "" {
				fecha3 = player.PaymentDate3
			}
			pdf.CellFormat(anchoColumna, 5, fecha3, "1", 0, "C", false, 0, "")
		}
		pdf.Ln(6)
	}

	pdf.Ln(1)
	pdf.SetFont("Arial", "B", 9)
	pdf.Write(4, "NOTA - CONDICIONES DEL PROGRAMA: ")
	pdf.SetFont("Arial", "", 9)
	pdf.Write(4, tr("Los valores abonados no serán objeto de devolución en caso de que el deportista finalmente no realice el viaje. No obstante, dichos valores podrán ser reintegrados en servicios correspondientes al programa y permanecerán congelados por un periodo máximo de un (1) año, de acuerdo con las condiciones establecidas por el programa. Asimismo, en caso de que el deportista o su responsable económico no cumpla con las fechas de pago previamente acordadas, el programa no estará obligado a garantizar ni prestar la totalidad de los servicios anteriormente descritos, quedando su prestación sujeta a la disponibilidad y a las condiciones vigentes del programa.\n"))

	pdf.Ln(1)
	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(0, 4, "Cordialmente:", "", 1, "L", false, 0, "")

	xFirma := pdf.GetX()
	yFirma := pdf.GetY()
	pdf.ImageOptions("firma.png", xFirma, yFirma-4, 35, 0, false, gofpdf.ImageOptions{ReadDpi: true}, 0, "")

	// Reducimos un poco el espacio interno del bloque de la firma
	pdf.SetY(yFirma + 12)
	pdf.SetFont("Arial", "B", 10)
	pdf.CellFormat(0, 4, "SUYSAN COLMENARES C.", "", 1, "L", false, 0, "")
	pdf.SetFont("Arial", "", 10)
	pdf.CellFormat(0, 4, "Coordinador Programa", "", 1, "L", false, 0, "")
	pdf.CellFormat(0, 4, tr("Móvil (+57) 3202411029"), "", 1, "L", false, 0, "")

	// 👉 CORRECCIÓN 2: El truco final. Cambiamos de -22 a -15.
	// Esto empuja el bloque de Instagram/Facebook 7 milímetros más abajo, hacia el límite real de la página.
	pdf.SetY(-15)
	pdf.SetFont("Times", "", 11)
	pdf.CellFormat(0, 4, "Instagram @majestic_intercambio", "", 1, "C", false, 0, "")
	pdf.CellFormat(0, 4, "Facebook Majestic Intercambio", "", 1, "C", false, 0, "")
	pdf.CellFormat(0, 4, tr("Móvil +57 3202411029"), "", 1, "C", false, 0, "")

	err = pdf.OutputFileAndClose(fileName)
	if err != nil {
		return "", err
	}

	return fileName, nil
}
