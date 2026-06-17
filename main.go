package main

import (
"fmt"
"log"
"net/http"
)

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", handleHealth)
	mux.HandleFunc("POST /validate", handleValidate)

	log.Println("starting webhook server on :8443")
	if err := http.ListenAndServe(":8443", mux); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "ok")
}

func handleValidate(w http.ResponseWriter, r *http.Request) {
	// Phase 1: admission review logic goes here
	w.WriteHeader(http.StatusOK)
}
