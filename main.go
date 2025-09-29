package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// main.go - Serviço mínimo para consultar o decoder público da NHTSA (vPIC).
// Rotas:
//  - GET /healthz
//  - GET /nhtsa/{vin}   -> usa https://vpic.nhtsa.dot.gov/api/vehicles/DecodeVinValues/{VIN}?format=json
//
// Como usar:
//  go run .
//  GET http://localhost:8080/nhtsa/1HGCM82633A004352

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
	_ = json.NewEncoder(w).Encode(v)
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

// nhtsaHandler expõe /nhtsa/{vin} e retorna campos enxutos + raw
func nhtsaHandler(w http.ResponseWriter, r *http.Request) {
	const prefix = "/nhtsa/"
	if len(r.URL.Path) <= len(prefix) {
		jsonWrite(w, http.StatusBadRequest, map[string]string{"error": "use /nhtsa/{vin}"})
		return
	}
	vin := r.URL.Path[len(prefix):]

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

	jsonWrite(w, http.StatusOK, out)
}

// ===================== Main =====================

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthHandler)
	mux.HandleFunc("/nhtsa/", nhtsaHandler)

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
