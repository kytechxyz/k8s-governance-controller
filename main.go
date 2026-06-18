package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	// 1. Read the request body.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to read body: %v", err), http.StatusBadRequest)
		return
	}

	// 2. Decode the incoming AdmissionReview.
	var review admissionv1.AdmissionReview
	if err := json.Unmarshal(body, &review); err != nil {
		http.Error(w, fmt.Sprintf("failed to decode AdmissionReview: %v", err), http.StatusBadRequest)
		return
	}
	if review.Request == nil {
		http.Error(w, "AdmissionReview request is nil", http.StatusBadRequest)
		return
	}

	// 3. Build the response. For now: allow everything.
	//    The UID MUST echo the request UID back, or the API server rejects the response.
	response := &admissionv1.AdmissionResponse{
		UID:     review.Request.UID,
		Allowed: true,
	}

	// 4. Wrap the response in an AdmissionReview envelope.
	out := admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			Kind:       "AdmissionReview",
			APIVersion: "admission.k8s.io/v1",
		},
		Response: response,
	}

	// 5. Marshal and send.
	respBytes, err := json.Marshal(out)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to encode response: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(respBytes)

	log.Printf("processed admission request uid=%s kind=%s namespace=%s allowed=%t",
		review.Request.UID, review.Request.Kind.Kind, review.Request.Namespace, response.Allowed)
}