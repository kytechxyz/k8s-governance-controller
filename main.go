package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kytechxyz/k8s-governance-controller/pkg/validator"
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
	mux.Handle("GET /metrics", promhttp.Handler())

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
	start := time.Now()
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

	// 3. Run the resource-limits validator against the raw object.
	response := &admissionv1.AdmissionResponse{
		UID: review.Request.UID,
	}

	// Route to the right validators based on the object kind.
	var violations []string
	var decodeErr error
	resourceType := strings.ToLower(review.Request.Kind.Kind)

	switch review.Request.Kind.Kind {
	case "Deployment":
		limitViolations, err := validator.ValidateResourceLimits(review.Request.Object.Raw)
		if err != nil {
			decodeErr = err
			break
		}
		labelViolations, err := validator.ValidateRequiredLabels(review.Request.Object.Raw)
		if err != nil {
			decodeErr = err
			break
		}
		violations = append(violations, limitViolations...)
		violations = append(violations, labelViolations...)

	case "Namespace":
		labelViolations, err := validator.ValidateRequiredLabels(review.Request.Object.Raw)
		if err != nil {
			decodeErr = err
			break
		}
		violations = append(violations, labelViolations...)
	}

	// Decide the verdict from what the validators found.
	result := "allowed"
	if decodeErr != nil {
		result = "denied"
		response.Allowed = false
		response.Result = &metav1.Status{
			Message: fmt.Sprintf("validation error: %v", decodeErr),
		}
	} else if len(violations) > 0 {
		result = "denied"
		response.Allowed = false
		response.Result = &metav1.Status{
			Message: "governance policy violations: " + strings.Join(violations, "; "),
		}
		// Increment violation counter per violation type.
		for _, v := range violations {
			violationType := classifyViolation(v)
			validator.ViolationsBlocked.WithLabelValues(violationType, review.Request.Namespace).Inc()
		}
	} else {
		response.Allowed = true
	}

	// Record admission duration with resource type and result labels.
	validator.AdmissionDuration.WithLabelValues(resourceType, result).Observe(time.Since(start).Seconds())

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

// classifyViolation maps a violation message to a short label
// suitable for use in Prometheus metric labels.
func classifyViolation(msg string) string {
	switch {
	case strings.Contains(msg, "resource limits"):
		return "missing_limits"
	case strings.Contains(msg, "label"):
		return "missing_labels"
	default:
		return "policy_violation"
	}
}
