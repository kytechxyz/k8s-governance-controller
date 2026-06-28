# k8s-governance-controller

A production-grade Kubernetes governance framework demonstrating admission-time resource enforcement, KubeVirt VM policy, SRE observability, and FinOps cost measurement — built as a quarter-long portfolio project by a Principal Engineer with 29 years of enterprise infrastructure experience.

**The thesis:** governance is the architectural prerequisite for FinOps. Without admission-time enforcement, cost optimization is reactive observation of chaos. With it, FinOps becomes deterministic — you control resource behavior at the moment workloads enter the cluster, and cost measurement follows from that controlled state.

A controlled before/after experiment validated this: enforcing resource bounds at admission time improved measured cluster allocation efficiency from 3.8% to 5.3% — a 39% relative improvement. The efficiency metric was previously unmeasurable because unconstrained workloads have no declared ceiling to measure against.

→ **[Read the full architecture writeup on Medium](https://medium.com/@kyle-williams-systems/from-ulimits-to-admission-controllers-why-resource-governance-is-a-30-year-old-discipline-0200426b19a3)**

---

## Architecture

The framework is a single admission-time control plane with three cooperating layers:

```
kubectl apply
      │
      ▼
┌─────────────────────────────────────────────────────┐
│              Kubernetes API Server                   │
└──────────┬──────────────────────┬───────────────────┘
           │                      │
           ▼                      ▼
┌──────────────────┐   ┌─────────────────────────────┐
│  Kyverno         │   │  Go Admission Webhook        │
│  (Mutating)      │   │  (Validating)                │
│                  │   │                              │
│  • Inject        │   │  • Resource limits           │
│    environment   │   │    (all containers)          │
│    label         │   │  • Required labels           │
│  • (runs first,  │   │  • failurePolicy: Ignore     │
│    deterministic)│   │    (fail open — ergonomics)  │
└──────────────────┘   └──────────────────────────────┘
           │
           ▼
┌─────────────────────────────────────────────────────┐
│  Kyverno (Validating)          failurePolicy: Enforce│
│                                                      │
│  Tier 1: Registry restriction (all Deployments)      │
│  Tier 2: KubeVirt VM policies (VirtualMachine CRD)  │
│  Tier 3: Audit-mode PolicyReport observability       │
└─────────────────────────────────────────────────────┘
           │
           ▼
         etcd
```

**Enforcement posture is deliberately split-brain:**

- Go webhook: `failurePolicy: Ignore` — governs workload ergonomics (resource limits, attribution labels). A temporary gap is recoverable. A cluster-wide outage from a hard-failing webhook is not.
- Kyverno: `Enforce` — governs security posture (registry restriction) and VM infrastructure. These fail closed because a bypass is not recoverable.

The SLO (see below) monitors the opening that `Ignore` creates — detecting degradation before it becomes systematic bypass.

---

## Repository Structure

```
k8s-governance-controller/
├── cmd/                          # Main entry point
├── pkg/
│   └── validator/
│       ├── limits.go             # Resource limits validator
│       ├── labels.go             # Required labels validator
│       └── metrics.go            # Prometheus instrumentation
├── manifests/
│   ├── webhook.yaml              # Deployment + Service
│   ├── webhook-config.yaml       # ValidatingWebhookConfiguration
│   ├── servicemonitor.yaml       # Prometheus ServiceMonitor
│   ├── baseline-constrained.yaml # FinOps experiment — governed state
│   ├── baseline-unconstrained.yaml # FinOps experiment — baseline state
│   ├── kyverno/
│   │   ├── tier1/                # Baseline governance policies
│   │   ├── tier2/                # KubeVirt VM policies
│   │   └── tier3/                # Audit-mode observability
│   └── tests/
│       └── kyverno/              # Test manifests for all policies
├── dashboards/
│   └── governance-webhook.json  # Grafana dashboard (3 panels)
├── evidence/
│   ├── cost-impact.md            # FinOps experiment analysis
│   ├── kubecost-before.png       # Efficiency 3.8% (ungoverned)
│   ├── kubecost-after.png        # Efficiency 5.3% (governed)
│   ├── kubecost-cost-allocation.csv
│   └── article-outline.md        # Article architecture + peer review
├── SLO.md                        # Formal SLI/SLO/Error Budget definition
├── main.go                       # Webhook server (HTTPS, /validate, /metrics)
├── Dockerfile                    # Multi-stage, distroless final image
└── certs/                        # TLS certs (gitignored)
```

---

## The Go Admission Webhook

A TLS-secured validating admission webhook written in Go. Runs on `:8443`, serves three endpoints:

| Endpoint    | Method | Purpose                 |
| ----------- | ------ | ----------------------- |
| `/validate` | POST   | AdmissionReview handler |
| `/metrics`  | GET    | Prometheus metrics      |
| `/healthz`  | GET    | Liveness probe          |

### What it validates

**`ValidateResourceLimits`** — every container in `spec.template.spec.containers` must declare both `cpu` and `memory` under `resources.limits`. Scope: Deployments. A production extension covers all pod-template-bearing resources (StatefulSets, DaemonSets, Jobs, CronJobs).

**`ValidateRequiredLabels`** — every Deployment must carry three labels: `cost-center`, `team`, `environment`. These are the FinOps attribution fields that make Kubecost namespace-level cost breakdown meaningful.

### Design decisions

**Violations vs. errors are distinct return types:**

```go
func ValidateResourceLimits(raw []byte) (violations []string, err error)
// err        → infrastructure failure (decode failed)
// violations → business-logic findings (policy non-compliance)
```

Conflating them produces a webhook that returns HTTP 500 for policy violations. Keeping them distinct means non-compliance is always surfaced as a clear rejection message, never a server error.

**UID echo is required:**

```go
response := &admissionv1.AdmissionResponse{
    UID:     review.Request.UID, // Must echo — omit and the API server drops the response
    Allowed: true,
}
```

**Table-driven tests with multi-element cases:**
Nine tests across two validators. Multi-container test cases are required to prove loop correctness — a single-container test can pass even with a broken `return` instead of `continue` in the validation loop.

### Build

```bash
# Build binary
CGO_ENABLED=0 GOOS=linux go build -o webhook .

# Run tests
go test ./pkg/validator/... -v

# Build image (multi-stage, distroless)
docker build -t k8s-governance-controller:v0.2.0 .
```

---

## Kyverno Policies

Kyverno v1.14.1 provides mutation, declarative validation, and KubeVirt-aware policy that the Go webhook cannot express.

### Tier 1 — Baseline Governance

**`mutate-inject-environment.yaml`**
Mutating policy. Injects `environment: lab` on any Deployment that does not already declare an environment label, using Kyverno's conditional patch syntax `+(environment): lab`. Runs in the mutating pass — upstream of the Go webhook — so the label is present before validation fires.

**`validate-registry-restriction.yaml`**
Enforce mode. Rejects any Deployment whose containers reference images outside `registry.kytechxyz.io/*`. Demonstrates defence in depth: registry control that the Go webhook's validating pass cannot provide, because rejection logic cannot be expressed in mutation order.

### Tier 2 — KubeVirt VM Governance

These policies govern `VirtualMachine` objects against the KubeVirt CRD schema. Each maps a traditional RHV/VMware operational discipline to admission-time enforcement.

**`validate-vm-eviction-strategy.yaml`**
Requires `spec.template.spec.evictionStrategy: LiveMigrate`. The vMotion contract: a VM without it gets forcibly terminated during node maintenance instead of live-migrated.

> **Production edge case:** VMs on ReadWriteOnce storage or direct-LUN attachments cannot live-migrate — the storage is not shareable across nodes. Forcing `LiveMigrate` on such VMs causes node drains to hang indefinitely. The production-correct policy exempts these workloads via `storage-class: direct-lun` label selector. This edge case surfaces only from operational experience with RHV and VMware direct-LUN migrations; it does not appear in KubeVirt or Kyverno documentation.

**`validate-vm-run-strategy.yaml`**
Requires `spec.runStrategy: Always` on VMs labeled `environment: production`. Label-scoped: development VMs are exempt. Proven with asymmetric test — production VM rejected, dev VM admitted — demonstrating that the selector fires precisely where the HA requirement exists and nowhere else.

**`validate-vm-instancetype.yaml`**
Dual-rule policy:

- Rule 1: requires `spec.instancetype.kind: VirtualMachineClusterInstancetype` (standardized sizing tier)
- Rule 2: denies raw `domain.cpu` or `domain.memory` declarations

Both rules are required because a VM could declare both an instancetype and raw sizing simultaneously. A policy checking only for instancetype presence would pass such a VM. The null-safe JMESPath pattern:

```yaml
key: "{{ request.object.spec.template.spec.domain.cpu || '' }}"
operator: NotEquals
value: ""
```

The `|| ''` fallback is required — `length(@)` on an absent field throws rather than returning zero.

### Tier 3 — Policy Observability

**`audit-require-owner-label.yaml`**
Audit mode with `background: true`. Demonstrates the audit→enforce progression: violations recorded in `PolicyReport` without blocking workloads, allowing blast-radius measurement before hardening. The background scanner retroactively flagged pre-existing workloads — including the governance webhook's own Deployment against two of its own policies. A governance framework that exempts its own tooling is theater. These findings are kept honest in `evidence/`.

---

## SRE Observability

### Prometheus Instrumentation

Two instruments, both in `pkg/validator/metrics.go`:

**`governance_admission_duration_seconds`** — HistogramVec

```
Labels: resource_type (deployment), result (allowed|denied)
Buckets: [0.005, 0.01, 0.025, 0.05, 0.1, 0.2, 0.5]
```

Bucket boundaries are deliberate: `0.2` aligns exactly with the latency SLO threshold. Histogram quantile interpolation is only exact at bucket boundaries.

**`governance_violations_blocked_total`** — CounterVec

```
Labels: violation_type (missing_limits|missing_labels), namespace
```

### Two SLOs — Not One

A common error in webhook SLO design is conflating availability and latency into a single objective. They have different budget mechanics.

**Availability SLO:** 99.5% uptime over a rolling 30-day window.

```
Error budget = 0.5% × 2,592,000s = 12,960s = 3.6 hours
```

As webhook latency climbs toward the API server timeout, `failurePolicy: Ignore` begins admitting requests without governance — silently. The availability SLO detects this before it becomes systematic bypass.

**Latency SLO:** 99.5% of admission requests complete in under 200ms.

```
Error budget = 0.5% of requests (request-based — cannot be expressed in hours)
```

The budget is request-volume-dependent. On a low-volume cluster, a single slow request spikes the percentage. That is the correct behavior of a request-counted budget.

See [`SLO.md`](SLO.md) for the full formal definition.

### Grafana Dashboard

Three panels exported to `dashboards/governance-webhook.json`:

- Admission latency p50/p95/p99 with 200ms SLO threshold line
- Violations blocked per hour by violation type
- Violation rate % of total requests

### ServiceMonitor

`manifests/servicemonitor.yaml` configures Prometheus scraping via the kube-prometheus-stack operator. Key lesson from implementation: the `keep` relabeling rule evaluates `__meta_kubernetes_service_label_app` against the **Service's metadata labels**, not its pod selector. A Service with no metadata labels is discovered and immediately dropped. Add `labels: {app: governance-webhook}` to the Service metadata — not just the selector.

---

## FinOps Experiment

### Method

Two cluster states, same three Deployments, Kubecost measurement:

**Baseline (ungoverned):** webhook disabled, workloads with no resource declarations. Kubecost allocation model falls back to node-level capacity as the denominator.

**Governed:** webhook live, same workloads redeployed with declared CPU/memory limits and cost attribution labels. Denominator becomes declared limit.

### Results

| State      | Cluster Efficiency | Surfaced Savings |
| ---------- | ------------------ | ---------------- |
| Ungoverned | 3.8%               | $162.45/mo       |
| Governed   | 5.3%               | $165.40/mo       |

### Honest interpretation

This is not a strictly controlled experiment — the efficiency denominator changes between states (node capacity vs. declared limit). 3.8% and 5.3% are not the same metric at two points in time. What the comparison demonstrates: governance makes the efficiency metric meaningful. Without declared bounds, there is no denominator that means anything and no efficiency number worth acting on.

The savings figure rose ($162.45 → $165.40) because Kubecost gained measurement resolution: unconstrained workloads have no right-sizing signal, so recommendations were invisible rather than small. Once limits were declared, Kubecost could surface specific reductions.

See [`evidence/cost-impact.md`](evidence/cost-impact.md) for the full analysis.

---

## Failure Modes Documented

Five real failure modes encountered during implementation, each teaching the same architectural lesson: behavior in a governance control plane is emergent — arising from layer interactions, not individual components.

1. **Validating-to-validating webhook ordering is undefined; mutation-to-validation is not.** The system relies on mutation preceding validation (deterministic). It does not depend on which validating webhook fires first (non-deterministic).

2. **The Enforcement Paradox.** A control plane governing the cluster it runs in requires a designed escape hatch. Every infrastructure tool installed (Kyverno, KubeVirt, Prometheus, Kubecost) hit the same bootstrap deadlock: namespace exemption required before the tool deployed; tool couldn't deploy without it. Five incidents, one principle.

3. **Governance applies to governance tooling.** The audit-mode PolicyReport flagged the governance webhook itself. Kept honest. A framework that exempts its own tooling isn't governance.

4. **JMESPath nil-safety at schema boundaries.** `length(@)` on an absent field throws. Use `{{ field || '' }}` with `NotEquals ""` when writing policy against optional CRD fields.

5. **ServiceMonitor "discovered but dropped."** Prometheus relabeling evaluates Service metadata labels, not the pod selector. "ServiceMonitor created" and "metrics flowing" are different states.

---

## Running Locally

### Prerequisites

- [kind](https://kind.sigs.k8s.io/) — local Kubernetes cluster
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [helm](https://helm.sh/)
- [Go 1.26+](https://golang.org/)
- [Docker](https://www.docker.com/)

### Cluster Setup

```bash
# Create 2-node kind cluster
kind create cluster --name go-dev-cluster --config - <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
  - role: worker
EOF
```

### TLS Certificates

The webhook requires a self-signed cert with correct SANs:

```bash
openssl req -x509 -newkey rsa:4096 -keyout certs/server.key.pem \
  -out certs/server.cert.pem -days 365 -nodes \
  -subj "/CN=governance-webhook.default.svc" \
  -addext "subjectAltName=DNS:governance-webhook.default.svc,DNS:governance-webhook.default.svc.cluster.local"
```

Update the `caBundle` in `manifests/webhook-config.yaml`:

```bash
cat certs/server.cert.pem | base64 | tr -d '\n'
```

### Deploy

```bash
# Build and load image
docker build -t k8s-governance-controller:v0.2.0 .
kind load docker-image k8s-governance-controller:v0.2.0 --name go-dev-cluster

# Deploy webhook
kubectl apply -f manifests/webhook.yaml
kubectl apply -f manifests/webhook-config.yaml

# Install Kyverno
helm repo add kyverno https://kyverno.github.io/kyverno/
helm upgrade --install kyverno kyverno/kyverno -n kyverno --create-namespace

# Apply governance policies
kubectl apply -f manifests/kyverno/tier1/
kubectl apply -f manifests/kyverno/tier2/
kubectl apply -f manifests/kyverno/tier3/
```

### Verify

```bash
# Should be rejected — missing labels and limits
kubectl apply -f manifests/test-bad-deployment.yaml

# Should be admitted — compliant
kubectl apply -f manifests/tests/kyverno/test-vm-compliant.yaml

# Check metrics
kubectl port-forward deployment/governance-webhook 8443:8443 &
curl -k https://localhost:8443/metrics | grep governance_
```

---

## Production Hardening (Not Implemented Here)

This is a portfolio-grade demonstration, not a production deployment. Production hardening requires:

- **Cert rotation** — keeping `ValidatingWebhookConfiguration.caBundle` synchronized with the serving cert
- **HA replicas + PodDisruptionBudget** — so the availability SLO is defensible
- **Extended validation scope** — StatefulSets, DaemonSets, Jobs, CronJobs, `initContainers`, `ephemeralContainers`
- **`dryRun` handling** — so server-side dry runs don't record phantom violations

---

## Related Work

This project is part of [The Single Thread](https://github.com/kytechxyz) — a quarter-long portfolio project building cloud-native platform engineering artifacts.

**Phase 1 — ESO + Vault:** [github.com/kytechxyz/eso-vault-lab](https://github.com/kytechxyz/eso-vault-lab) — External Secrets Operator v2.6.0 with HashiCorp Vault Kubernetes authentication, documented failure modes, and a CI pipeline.

**Phase 2 — Kubernetes Governance (this repo):** Go admission webhook, three-tier Kyverno policy framework, KubeVirt VM governance, Prometheus SLO instrumentation, and Kubecost FinOps integration.

## Author

Kyle Williams — Principal Platform / Infrastructure Engineer  
29 years in enterprise infrastructure: HP-UX, Solaris, AIX → VMware, Red Hat Virtualization → Kubernetes, OpenShift Virtualization, KubeVirt.

[GitHub](https://github.com/kytechxyz) · [LinkedIn](https://www.linkedin.com/in/kyle-williams-systems/) · [Medium](https://medium.com/@kyle-williams-systems)

**Article:** [From ulimits to Admission Controllers: Why Resource Governance Is a 30-Year-Old Discipline](https://medium.com/@kyle-williams-systems/from-ulimits-to-admission-controllers-why-resource-governance-is-a-30-year-old-discipline-0200426b19a3)
