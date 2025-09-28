package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
)

// AppConfig guarda configurações sensíveis e parâmetros do app.
// Aqui começamos simples: só usuário e senha da API vindos do ambiente.
type AppConfig struct {
	JDPUser string
	JDPPass string
	// Em bloco futuro poderíamos ter: BaseURL, Timeout, Retry etc.
}

// newConfig lê variáveis de ambiente e monta a AppConfig.
// Regra de ouro: nada hard-coded que seja credencial.
func newConfig() (AppConfig, error) {
	cfg := AppConfig{
		JDPUser: os.Getenv("JDP_USER"),
		JDPPass: os.Getenv("JDP_PASS"),
	}
	// validação mínima: avisa se faltou algo essencial
	if cfg.JDPUser == "" || cfg.JDPPass == "" {
		log.Println("[aviso] JDP_USER ou JDP_PASS não definidos; use export/setx para definir.")
	}
	return cfg, nil
}

// jsonWrite é um helper para responder JSON com status e payload.
func jsonWrite(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// healthHandler: endpoint simples para testarmos no Postman.
// GET /healthz -> {"status":"ok","time":"..."}
func healthHandler(w http.ResponseWriter, r *http.Request) {
	jsonWrite(w, http.StatusOK, map[string]any{
		"status": "ok",
		"time":   time.Now().Format(time.RFC3339),
	})
}

// vinHandler: por enquanto é só um stub.
// No Bloco 2 vamos implementar a chamada REST real.
// Padrão de rota: /vin/{vin}  (ex.: /vin/JS1GR7MA8J2100001)
func vinHandler(cfg AppConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// extrai o VIN do path bruto. Como estamos usando ServeMux,
		// pegamos tudo após "/vin/".
		path := r.URL.Path
		const prefix = "/vin/"
		if len(path) <= len(prefix) {
			jsonWrite(w, http.StatusBadRequest, map[string]string{
				"error": "vin ausente; use /vin/{vin}",
			})
			return
		}
		vin := path[len(prefix):]

		// Neste bloco, só mostramos que recebemos o VIN e que
		// as credenciais estão carregadas. No próximo, chamaremos a API.
		jsonWrite(w, http.StatusOK, map[string]any{
			"vin":         vin,
			"implemented": false,
			"message":     "stub: chamada à API será implementada no Bloco 2",
			"hasCreds":    (cfg.JDPUser != "" && cfg.JDPPass != ""),
		})
	}
}

func main() {
	// 1) carrega config
	cfg, err := newConfig()
	if err != nil {
		log.Fatal(err)
	}

	// 2) cria um mux (roteador simples da stdlib)
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthHandler)
	mux.HandleFunc("/vin/", vinHandler(cfg))

	// 3) servidor com timeouts sensatos (boa prática desde o início)
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
