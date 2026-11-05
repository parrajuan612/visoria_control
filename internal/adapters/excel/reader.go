// Package excel lee el Excel de jugadores. Las columnas se detectan por el nombre de su
// encabezado (con alias, sin importar tildes, mayúsculas ni el orden), no por su posición:
// el cliente puede reordenar columnas o agregar otras sin romper la lectura.
package excel

import (
	"context"
	"fmt"
	"io"
	"strings"

	"visoria-control/internal/domain"
	"visoria-control/internal/ports"

	"github.com/xuri/excelize/v2"
)

const (
	maxHeaderScan = 10        // filas iniciales donde se busca el encabezado
	maxRows       = 5000      // tope de filas por archivo
	maxUnzip      = 100 << 20 // protege contra archivos "zip bomb"
	maxUnzipXML   = 32 << 20
)

// field es cada dato que se lee de una fila.
type field int

const (
	fJugador field = iota
	fClub
	fBeca
	fAnio
	fAcudiente
	fMovil
	fCorreo
	numFields
)

var fieldNames = [numFields]string{"Jugador", "Club", "Beca", "Año de nacimiento", "Acudiente", "Móvil", "Correo"}

// Alias de encabezados, ya normalizados con domain.NormalizeHeader.
var aliases = [numFields][]string{
	fJugador:   {"NOMBRE", "NOMBRES", "JUGADOR", "NOMBRE JUGADOR", "NOMBRE DEL JUGADOR", "DEPORTISTA", "ATLETA"},
	fClub:      {"CLUB", "EQUIPO", "NOMBRE CLUB", "NOMBRE DEL CLUB"},
	fBeca:      {"BECA", "BECAS", "% BECA", "PORCENTAJE BECA", "PORCENTAJE DE BECA", "PORCENTAJE"},
	fAnio:      {"ANO", "ANOS", "ANO NACIMIENTO", "ANO DE NACIMIENTO", "NACIMIENTO", "FECHA NACIMIENTO", "FECHA DE NACIMIENTO", "ANIO", "ANIO NACIMIENTO", "ANIO DE NACIMIENTO"},
	fAcudiente: {"ACUDIENTE", "NOMBRE ACUDIENTE", "NOMBRE DEL ACUDIENTE", "REPRESENTANTE", "RESPONSABLE", "TUTOR", "PADRE", "PADRE O ACUDIENTE", "PADRE/ACUDIENTE"},
	fMovil:     {"MOVIL", "CELULAR", "TELEFONO", "NUMERO", "WHATSAPP", "TEL", "CEL", "NUMERO CELULAR", "NUMERO DE CELULAR", "MOVIL ACUDIENTE", "CELULAR ACUDIENTE", "TELEFONO ACUDIENTE"},
	fCorreo:    {"CORREO", "CORREO ELECTRONICO", "EMAIL", "E MAIL", "MAIL", "CORREO ACUDIENTE", "EMAIL ACUDIENTE"},
}

// Obligatorias: sin estas columnas no se puede leer el archivo. El móvil y el correo son
// opcionales a nivel de columna (D3: basta con uno de los dos canales por fila), pero
// debe existir al menos una de las dos columnas.
var required = []field{fJugador, fClub, fBeca, fAnio, fAcudiente}

// Reader implementa ports.PlayerSheetReader.
type Reader struct{}

var _ ports.PlayerSheetReader = Reader{}

func NewReader() Reader { return Reader{} }

type headerMap [numFields]int // índice de columna de cada campo (-1 si no está)

func (h headerMap) found() int {
	n := 0
	for _, c := range h {
		if c >= 0 {
			n++
		}
	}
	return n
}

func mapHeader(row []string) headerMap {
	var h headerMap
	for i := range h {
		h[i] = -1
	}
	for col, cell := range row {
		norm := domain.NormalizeHeader(cell)
		if norm == "" {
			continue
		}
		for f := field(0); f < numFields; f++ {
			if h[f] >= 0 {
				continue // gana la primera columna con ese nombre
			}
			for _, a := range aliases[f] {
				if a == norm {
					h[f] = col
					break
				}
			}
		}
	}
	return h
}

type candidate struct {
	sheet string
	row   int // índice de la fila de encabezados
	hdr   headerMap
	rows  [][]string
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ports.ErrInvalidSpreadsheet, fmt.Sprintf(format, a...))
}

// Read abre el .xlsx, localiza los encabezados y devuelve una RawRow por cada fila con datos.
func (Reader) Read(_ context.Context, r io.Reader) ([]domain.RawRow, error) {
	opts := excelize.Options{RawCellValue: true, UnzipSizeLimit: maxUnzip, UnzipXMLSizeLimit: maxUnzipXML}
	f, err := excelize.OpenReader(r, opts)
	if err != nil {
		return nil, invalid("no se pudo abrir el archivo; debe ser un Excel .xlsx (%v)", err)
	}
	defer f.Close()

	// Se elige la hoja y la fila de encabezados que reconozcan más columnas.
	var best *candidate
	for _, sheet := range f.GetSheetList() {
		rows, err := f.GetRows(sheet, opts)
		if err != nil {
			continue
		}
		for i := 0; i < len(rows) && i < maxHeaderScan; i++ {
			h := mapHeader(rows[i])
			if h.found() == 0 {
				continue
			}
			if best == nil || h.found() > best.hdr.found() {
				best = &candidate{sheet: sheet, row: i, hdr: h, rows: rows}
			}
		}
	}
	if best == nil || best.hdr.found() < 3 {
		return nil, invalid("no se encontró la fila de encabezados. Debe tener columnas como: %s", expectedColumns())
	}

	var missing []string
	for _, fld := range required {
		if best.hdr[fld] < 0 {
			missing = append(missing, fieldNames[fld])
		}
	}
	if best.hdr[fMovil] < 0 && best.hdr[fCorreo] < 0 {
		missing = append(missing, "Móvil o Correo (al menos una de las dos)")
	}
	if len(missing) > 0 {
		return nil, invalid("faltan columnas: %s. Se leyeron los encabezados de la hoja «%s» (fila %d)",
			strings.Join(missing, ", "), best.sheet, best.row+1)
	}

	cell := func(row []string, f field) string {
		if c := best.hdr[f]; c >= 0 && c < len(row) {
			return strings.TrimSpace(row[c])
		}
		return ""
	}
	var out []domain.RawRow
	for i := best.row + 1; i < len(best.rows); i++ {
		row := best.rows[i]
		rr := domain.RawRow{
			Fila: i + 1, Jugador: cell(row, fJugador), Club: cell(row, fClub), Beca: cell(row, fBeca),
			Anio: cell(row, fAnio), Acudiente: cell(row, fAcudiente), Movil: cell(row, fMovil), Correo: cell(row, fCorreo),
		}
		if rr == (domain.RawRow{Fila: rr.Fila}) {
			continue // fila vacía (o solo con columnas que no se leen)
		}
		if len(out) >= maxRows {
			return nil, invalid("el archivo supera el máximo de %d filas", maxRows)
		}
		out = append(out, rr)
	}
	if len(out) == 0 {
		return nil, invalid("el archivo no tiene filas de jugadores debajo de los encabezados")
	}
	return out, nil
}

func expectedColumns() string {
	names := make([]string, 0, numFields)
	for _, n := range fieldNames {
		names = append(names, n)
	}
	return strings.Join(names, ", ")
}
