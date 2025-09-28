package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"
)

// ===================== CONFIG =====================

type AppConfig struct {
	JDPUser string
	JDPPass string
	// deixar baseURL configurável facilita apontar para demo vs prod no futuro:
	BaseURL string // ex: "https://www.jdpowerwebservices.com"
}

func newConfig() (AppConfig, error) {
	cfg := AppConfig{
		JDPUser: os.Getenv("JDP_USER"),
		JDPPass: os.Getenv("JDP_PASS"),
		BaseURL: os.Getenv("JDP_BASE_URL"),
	}
	if cfg.BaseURL == "" {
		// padrão: produção (poderíamos permitir demo via env JDP_BASE_URL)
		cfg.BaseURL = "https://www.jdpowerwebservices.com"
	}
	if cfg.JDPUser == "" || cfg.JDPPass == "" {
		log.Println("[aviso] JDP_USER ou JDP_PASS não definidos; use export/setx para definir.")
	}
	return cfg, nil
}

// ===================== MODELOS (Response) =====================
// Baseado nos structs do seu arquivo "Struct for JD Power Integration.txt". :contentReference[oaicite:5]{index=5}

type APIResponse struct {
	GetModelsByVINV2Result struct {
		Status        string  `json:"Status"`
		Models        []Model `json:"Models"`
		VintageModels []Model `json:"VintageModels"`
	} `json:"GetModelsByVINV2Result"`
}

type Model struct {
	Make             string `json:"Make"`
	MakeID           string `json:"MakeID"`
	Year             int    `json:"Year"`
	Model            string `json:"Model"`
	ModelID          int    `json:"ModelID"`
	ModelType        string `json:"ModelType"`
	ModelNo          string `json:"ModelNo"`
	MSRP             int    `json:"MSRP"`
	LowRetail        int    `json:"LowRetail"`
	AverageRetail    int    `json:"AverageRetail"`
	LowTrade         int    `json:"LowTrade"`
	HighTrade        int    `json:"HighTrade"`
	AverageWholesale int    `json:"AverageWholesale"`
	Transmission     string `json:"Transmission"`
	Weight           int    `json:"Weight"`
	EngineCC         int    `json:"EngineCC"`
	Stroke           int    `json:"Stroke"`
	Cylinders        int    `json:"Cylinders"`
}

// ===================== CLIENTE HTTP (fetchVIN) =====================

// fetchVIN chama o endpoint REST GetModelsByVINV2 e retorna o objeto desserializado.
// Endpoint: GET {BaseURL}/UsedPowersportsService.svc/VINV2/{vin} com headers UserName/Password. :contentReference[oaicite:6]{index=6} :contentReference[oaicite:7]{index=7}
func fetchVIN(ctx context.Context, cfg AppConfig, vin string) (*APIResponse, error) {
	if len(vin) == 0 {
		return nil, errors.New("vin vazio")
	}

	// 1) Montar URL
	url := fmt.Sprintf("%s/UsedPowersportsService.svc/VINV2/%s", cfg.BaseURL, vin)

	// 2) Criar requisição com contexto (permite timeout/cancelamento)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("criando request: %w", err)
	}

	// 3) Headers de autenticação (case-sensitive conforme doc) :contentReference[oaicite:8]{index=8}
	req.Header.Add("UserName", cfg.JDPUser)
	// A doc REST sample mostra "password" minúsculo; SOAP usa "Password" maiúsculo.
	// Na prática, servidores costumam aceitar ambos, mas seguiremos o sample REST. :contentReference[oaicite:9]{index=9}
	req.Header.Add("password", cfg.JDPPass)

	// 4) http.Client com timeout (robusto contra travas)
	client := &http.Client{Timeout: 12 * time.Second}

	// 5) Executar
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executando request: %w", err)
	}
	defer resp.Body.Close()

	// 6) Checar status HTTP
	if resp.StatusCode != http.StatusOK {
		// 401/403 → credenciais; 400/404 → VIN inválido etc. A doc lista códigos/erros comuns. :contentReference[oaicite:10]{index=10}
		return nil, fmt.Errorf("status HTTP inesperado: %d", resp.StatusCode)
	}

	// 7) Ler corpo e desserializar JSON (REST retorna JSON) :contentReference[oaicite:11]{index=11}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("lendo corpo: %w", err)
	}
	var apiResp APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("decodificando JSON: %w", err)
	}

	// 8) Validar o campo de Status de negócio (ExactMatch, NoRecordsFound...) :contentReference[oaicite:12]{index=12}
	status := apiResp.GetModelsByVINV2Result.Status
	if status != "ExactMatch" {
		// retornamos erro amigável contendo o status da API
		return nil, fmt.Errorf("status da API: %s", status)
	}

	return &apiResp, nil
}

// ===================== HELPERS HTTP =====================

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

// vinHandler agora chama fetchVIN de verdade.
func vinHandler(cfg AppConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/vin/"
		if len(r.URL.Path) <= len(prefix) {
			jsonWrite(w, http.StatusBadRequest, map[string]string{"error": "vin ausente; use /vin/{vin}"})
			return
		}
		vin := r.URL.Path[len(prefix):]

		// Contexto com timeout por requisição
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()

		resp, err := fetchVIN(ctx, cfg, vin)
		if err != nil {
			jsonWrite(w, http.StatusBadGateway, map[string]any{
				"vin":   vin,
				"error": err.Error(),
			})
			return
		}

		jsonWrite(w, http.StatusOK, resp)
	}
}

func main() {
	cfg, err := newConfig()
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthHandler)
	mux.HandleFunc("/vin/", vinHandler(cfg))

	srv := &http.Server{
		Addr:              ":8080",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	log.Println("servidor ouvindo em http://localhost:8080 ...")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
