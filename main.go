package main

import (
	"fmt"
	"log"
	"net/http"
)

const (
	tlsCertPath = "/etc/webhook/certs/server.cert.pem"
	tlsKeyPath  = "/etc/webhook/certs/server.key.pem"
	listenAddr  = ":8443"
)

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", handleHealth)
	mux.HandleFunc("POST /validate", handleValidate)

	log.Printf("starting webhook server on %s (TLS)", listenAddr)
	if err := http.ListenAndServeTLS(listenAddr, tlsCertPath, tlsKeyPath, mux); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "ok")
}

func handleValidate(w http.ResponseWriter, r *http.Request) {
	// t12: admission review decode logic goes here
	w.WriteHeader(http.StatusOK)
}
