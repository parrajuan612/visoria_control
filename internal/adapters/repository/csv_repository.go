package repository

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"visoria-control/internal/core/domain"
)

type csvTournamentRepository struct {
	mapaBecas       map[string]domain.Pricing
	listaTorneos    []map[string]interface{}
	listaCategorias []map[string]interface{}
}

func NewCSVTournamentRepository() *csvTournamentRepository {
	return &csvTournamentRepository{
		mapaBecas:       make(map[string]domain.Pricing),
		listaTorneos:    []map[string]interface{}{},
		listaCategorias: []map[string]interface{}{},
	}
}

func (r *csvTournamentRepository) LoadConfigFromCSV(ctx context.Context, csvURL string) error {
	resp, err := http.Get(csvURL)
	if err != nil {
		return fmt.Errorf("error descargando CSV: %v", err)
	}
	defer resp.Body.Close()

	reader := csv.NewReader(resp.Body)
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true // Permite caracteres raros en el CSV

	filas, err := reader.ReadAll()
	if err != nil {
		return fmt.Errorf("error leyendo CSV: %v", err)
	}

	// Limpiar mapas
	r.mapaBecas = make(map[string]domain.Pricing)
	r.listaTorneos = []map[string]interface{}{}
	r.listaCategorias = []map[string]interface{}{}

	// Función helper para limpiar basura invisible de las celdas
	clean := func(s string) string {
		s = strings.ReplaceAll(s, "\r", "")
		s = strings.ReplaceAll(s, "\n", "")
		return strings.TrimSpace(s)
	}

	for i, fila := range filas {
		if i == 0 {
			continue
		}

		// TABLA 1: Becas (Columnas 0 a 4)
		if len(fila) > 0 {
			becaNombre := clean(fila[0])
			if becaNombre != "" {
				r.mapaBecas[becaNombre] = domain.Pricing{
					Total: clean(fila[1]),
					Pago1: clean(fila[2]),
					Pago2: clean(fila[3]),
					Pago3: clean(fila[4]),
				}
			}
		}

		// TABLA 2: Torneos (Columnas 5 a 6)
		if len(fila) > 6 {
			aniosTorneo := clean(fila[5])
			if aniosTorneo != "" {
				r.listaTorneos = append(r.listaTorneos, map[string]interface{}{
					"rango":  aniosTorneo,
					"nombre": clean(fila[6]),
				})
			}
		}

		// TABLA 3: Categorías (Columnas 7 a 8)
		if len(fila) > 8 {
			aniosCategoria := clean(fila[7])
			if aniosCategoria != "" {
				r.listaCategorias = append(r.listaCategorias, map[string]interface{}{
					"rango":  aniosCategoria,
					"nombre": clean(fila[8]),
				})
			}
		}
	}

	// Imprimimos en la terminal para estar 100% seguros de que se cargó todo
	fmt.Printf("✅ Cerebro Sincronizado: %d Becas, %d Torneos, %d Categorias\n", len(r.mapaBecas), len(r.listaTorneos), len(r.listaCategorias))

	return nil
}

func (r *csvTournamentRepository) GetTournamentForPlayer(ctx context.Context, birthYear int, scholarship string) (domain.TournamentInfo, error) {
	var info domain.TournamentInfo

	if precios, existe := r.mapaBecas[scholarship]; existe {
		info.Pricing = precios
	} else {
		info.Pricing = domain.Pricing{Total: "No definido"}
	}

	for _, t := range r.listaTorneos {
		if estaEnRangoGion(birthYear, t["rango"].(string)) {
			info.Name = t["nombre"].(string)
			break
		}
	}

	for _, c := range r.listaCategorias {
		if estaEnRango(birthYear, c["rango"].(string)) {
			info.Category = c["nombre"].(string)
			break
		}
	}

	return info, nil
}

func estaEnRangoGion(anio int, rangoStr string) bool {
	partes := strings.Split(rangoStr, "-")
	if len(partes) == 2 {
		min, _ := strconv.Atoi(strings.TrimSpace(partes[0]))
		max, _ := strconv.Atoi(strings.TrimSpace(partes[1]))
		return anio >= min && anio <= max
	}
	return false
}

func estaEnRango(anio int, rangoStr string) bool {
	partes := strings.Split(strings.ReplaceAll(rangoStr, " ", ""), "-")
	for _, p := range partes {
		val, err := strconv.Atoi(p)
		if err == nil && val == anio {
			return true
		}
	}
	return estaEnRangoGion(anio, rangoStr)
}
