package validator

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// AdmissionDuration tracks how long each admission request takes to process.
// A histogram is used rather than a gauge or summary because we need to query
// specific percentile buckets (p50/p95/p99) against a fixed 200ms SLO threshold.
var AdmissionDuration = promauto.NewHistogramVec(
	prometheus.HistogramOpts{
		Name: "governance_admission_duration_seconds",
		Help: "Duration of admission webhook request processing in seconds.",
		Buckets: []float64{
			0.005, // 5ms
			0.010, // 10ms
			0.025, // 25ms
			0.050, // 50ms
			0.100, // 100ms
			0.200, // 200ms  ← SLO threshold
			0.500, // 500ms
		},
	},
	[]string{"resource_type", "result"},
)

// ViolationsBlocked tracks the total number of policy violations blocked
// since the webhook started. A counter (never decreases) is the correct
// instrument — rate() in PromQL derives violations-per-second from it.
var ViolationsBlocked = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "governance_violations_blocked_total",
		Help: "Total number of admission requests denied due to policy violations.",
	},
	[]string{"violation_type", "namespace"},
)
