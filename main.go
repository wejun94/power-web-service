package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// ===================== Estruturas =====================

type NHTSAResponse struct {
	Count          int                      `json:"Count"`
	Message        string                   `json:"Message"`
	SearchCriteria string                   `json:"SearchCriteria"`
	Results        []map[string]interface{} `json:"Results"`
}

// ===================== Helpers =====================

func jsonWrite(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	data, err := json.MarshalIndent(v, "", "	") //identação com dois espaços
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(data)
	//_ = json.NewEncoder(w).Encode(v)
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	jsonWrite(w, http.StatusOK, map[string]any{
		"status": "ok",
		"time":   time.Now().Format(time.RFC3339),
	})
}

// ===================== NHTSA (vPIC) client =====================

func fetchNHTSA(vin string) (*NHTSAResponse, error) {
	if vin == "" {
		return nil, fmt.Errorf("vin vazio")
	}
	url := fmt.Sprintf("https://vpic.nhtsa.dot.gov/api/vehicles/DecodeVinValues/%s?format=json", vin)

	client := &http.Client{Timeout: 12 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nhtsa status %d; body: %s", resp.StatusCode, string(body))
	}

	var nr NHTSAResponse
	if err := json.Unmarshal(body, &nr); err != nil {
		return nil, fmt.Errorf("unmarshal nhtsa json: %w", err)
	}
	return &nr, nil
}

// ===================== Banco de dados =====================

func upsertVehicle(db *sql.DB, out map[string]interface{}) error {
	rawJSON, _ := json.Marshal(out["raw"])
	_, err := db.Exec(`
        INSERT INTO vehicles (
            vin, make, model, model_year, manufacturer,
            plant_country, plant_state, body_class, engine_cylinders, fuel_type, raw
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
        ON CONFLICT (vin) DO UPDATE SET
            make=$2, model=$3, model_year=$4, manufacturer=$5,
            plant_country=$6, plant_state=$7, body_class=$8,
            engine_cylinders=$9, fuel_type=$10, raw=$11,
            last_updated=now();
    `,
		out["vin"], out["make"], out["model"], out["model_year"], out["manufacturer"],
		out["plant_country"], out["plant_state"], out["body_class"], out["engine_cylinders"], out["fuel_type"], rawJSON,
	)
	return err
}

func getVehicleByVIN(db *sql.DB, vin string) (map[string]interface{}, error) {
	row := db.QueryRow(`SELECT vin, make, model, model_year, manufacturer,
                               plant_country, plant_state, body_class, engine_cylinders, fuel_type, raw
                        FROM vehicles WHERE vin=$1`, vin)

	var (
		vinVal, make, model, modelYear, manufacturer, plantCountry, plantState, bodyClass, engineCylinders, fuelType string
		raw                                                                                                          []byte
	)
	err := row.Scan(&vinVal, &make, &model, &modelYear, &manufacturer, &plantCountry, &plantState, &bodyClass, &engineCylinders, &fuelType, &raw)
	if err != nil {
		return nil, err
	}

	var rawJSON map[string]interface{}
	_ = json.Unmarshal(raw, &rawJSON)

	return map[string]interface{}{
		"vin":              vinVal,
		"make":             make,
		"model":            model,
		"model_year":       modelYear,
		"manufacturer":     manufacturer,
		"plant_country":    plantCountry,
		"plant_state":      plantState,
		"body_class":       bodyClass,
		"engine_cylinders": engineCylinders,
		"fuel_type":        fuelType,
		"raw":              rawJSON,
	}, nil
}

// ===================== Handlers =====================

// Busca VIN: primeiro no DB, senão chama NHTSA e salva
func nhtsaHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/nhtsa/"
		if len(r.URL.Path) <= len(prefix) {
			jsonWrite(w, http.StatusBadRequest, map[string]string{"error": "use /nhtsa/{vin}"})
			return
		}
		vin := r.URL.Path[len(prefix):]

		// tenta pegar do banco
		if v, err := getVehicleByVIN(db, vin); err == nil {
			jsonWrite(w, http.StatusOK, v)
			return
		}

		// se não achou, chama API
		nresp, err := fetchNHTSA(vin)
		if err != nil {
			jsonWrite(w, http.StatusBadGateway, map[string]any{"vin": vin, "error": err.Error()})
			return
		}

		var flat map[string]interface{}
		if len(nresp.Results) > 0 {
			flat = nresp.Results[0]
		} else {
			flat = map[string]interface{}{}
		}

		out := map[string]interface{}{
			"vin":              vin,
			"make":             flat["Make"],
			"model":            flat["Model"],
			"model_year":       flat["ModelYear"],
			"manufacturer":     flat["Manufacturer"],
			"plant_country":    flat["PlantCountry"],
			"plant_state":      flat["PlantState"],
			"body_class":       flat["BodyClass"],
			"engine_cylinders": flat["EngineCylinders"],
			"fuel_type":        flat["FuelTypePrimary"],
			"raw":              flat,
		}

		// salva no banco
		if err := upsertVehicle(db, out); err != nil {
			log.Println("erro salvando no banco:", err)
		}

		jsonWrite(w, http.StatusOK, out)
	}
}

// Apenas busca no DB
func vehicleHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/vehicles/"
		if len(r.URL.Path) <= len(prefix) {
			jsonWrite(w, http.StatusBadRequest, map[string]string{"error": "use /vehicles/{vin}"})
			return
		}
		vin := r.URL.Path[len(prefix):]

		v, err := getVehicleByVIN(db, vin)
		if err != nil {
			jsonWrite(w, http.StatusNotFound, map[string]string{"error": "vin not found"})
			return
		}
		jsonWrite(w, http.StatusOK, v)
	}
}

// ===================== Main =====================

func main() {
	// conecta ao Postgres
	db, err := sql.Open("pgx", "postgres://wejun:postgres123@localhost:5432/vehicles_db?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// garante que o banco está ok
	if err := db.Ping(); err != nil {
		log.Fatal("erro ao conectar no banco:", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthHandler)
	mux.Handle("/nhtsa/", nhtsaHandler(db))
	mux.Handle("/vehicles/", vehicleHandler(db))

	srv := &http.Server{
		Addr:              ":8080",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       12 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	log.Println("servidor ouvindo em http://localhost:8080 ...")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
