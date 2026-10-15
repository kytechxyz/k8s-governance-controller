# k8s-governance-controller

A Kubernetes governance framework demonstrating admission-time resource enforcement, KubeVirt VM policy, SRE observability, and FinOps cost measurement. A quarter-long personal lab project, not deployed in any production environment.

**The thesis:** governance is the architectural prerequisite for FinOps. Without admission-time enforcement, cost optimization is reactive observation of chaos. With it, you control resource behavior at the moment workloads enter the cluster, and cost measurement follows from that controlled state.

A before/after comparison on a lab cluster supports this: once resource bounds were enforced at admission, Kubecost's efficiency metric became meaningful — it moved from 3.8% to 5.3%, though the two numbers use different denominators and aren't directly comparable (see [Honest interpretation](#honest-interpretation)).

→ **[Read the full architecture writeup on Medium](https://medium.com/@kyle-williams-systems/from-ulimits-to-admission-controllers-why-resource-governance-is-a-30-year-old-discipline-0200426b19a3)**

### Where to look first

- `pkg/validator/` — the Go validators and their table-driven tests
- `manifests/kyverno/` — three tiers of policy, including KubeVirt VM governance
- [`SLO.md`](SLO.md) — availability and latency SLOs for the webhook
- [Failure Modes Documented](#failure-modes-documented) — what broke and what it taught

### How this was built

A personal lab project built with AI-assisted development. I set the design, the policies, the failure-mode experiments and the tests, and reviewed every change; AI sped up the implementation.

---

## Architecture

The framework is a single admission-time control plane with three cooperating layers:

```
kubectl apply
      │
      ▼
┌──────────────────────────────────────────────────────┐
│               Kubernetes API Server                   │
└──────────────────────────┬───────────────────────────┘
                           │
                           ▼  1. Mutating phase (runs first)
┌──────────────────────────────────────────────────────┐
│  Kyverno (Mutating)                                   │
│  • Inject environment label if missing                │
└──────────────────────────┬───────────────────────────┘
                           │
                           ▼  2. Validating phase (order between webhooks is undefined)
┌───────────────────────────┐   ┌──────────────────────────────┐
│  Go Admission Webhook     │   │  Kyverno (Validating)         │
│  failurePolicy: Ignore    │   │  validationFailureAction:     │
│                           │   │    Enforce                    │
│  • Resource limits        │   │  failurePolicy: Fail          │
│    (all containers)       │   │                               │
│  • Required labels        │   │  Tier 1: Registry restriction │
│                           │   │  Tier 2: KubeVirt VM policies │
│                           │   │  Tier 3: Audit-mode reports   │
└─────────────┬─────────────┘   └───────────────┬──────────────┘
              └──────────────┬──────────────────┘
                             ▼  admitted only if every validator allows
                           etcd
```

**Enforcement posture is split by design:**

- Go webhook: `failurePolicy: Ignore` (fail open) — governs workload ergonomics (resource limits, attribution labels). A temporary gap is recoverable. A cluster-wide outage from a hard-failing webhook is not.
- Kyverno: `validationFailureAction: Enforce` with `failurePolicy: Fail` (fail closed) — governs security posture (registry restriction) and VM infrastructure. A bypass here is not recoverable, so these block on violation and on webhook failure.

The SLOs (see below) watch the opening that `Ignore` creates — detecting degradation before it becomes systematic bypass.

---

## Repository Structure

```
k8s-governance-controller/
├── cmd/                              # Main entry point (planned)
├── pkg/
│   ├── validator/
│   │   ├── limits.go                 # Resource limits validator
│   │   ├── limits_test.go            # Table-driven tests (5 cases)
│   │   ├── labels.go                 # Required labels validator
│   │   ├── labels_test.go            # Table-driven tests (4 cases)
│   │   └── metrics.go                # Prometheus instrumentation
│   └── webhook/                      # Webhook handler (planned)
├── manifests/
│   ├── webhook.yaml                  # Deployment + Service
│   ├── webhook-config.yaml           # ValidatingWebhookConfiguration
│   ├── servicemonitor.yaml           # Prometheus ServiceMonitor
│   ├── baseline-constrained.yaml     # FinOps experiment — governed state
│   ├── baseline-unconstrained.yaml   # FinOps experiment — baseline state
│   ├── test-bad-deployment.yaml      # Rejects: missing labels + limits
│   ├── test-bad-registry.yaml        # Rejects: unapproved registry
│   ├── test-mutate-environment.yaml  # Admits: proves label injection
│   ├── kyverno/
│   │   ├── tier1/
│   │   │   ├── mutate-inject-environment.yaml    # Inject environment:lab
│   │   │   └── validate-registry-restriction.yaml # Registry allowlist
│   │   ├── tier2/
│   │   │   ├── validate-vm-eviction-strategy.yaml # Require LiveMigrate
│   │   │   ├── validate-vm-run-strategy.yaml      # Require Always (prod)
│   │   │   └── validate-vm-instancetype.yaml      # Require instancetype
│   │   └── tier3/
│   │       └── audit-require-owner-label.yaml     # Audit-mode PolicyReport
│   └── tests/
│       └── kyverno/
│           ├── test-vm-compliant.yaml
│           ├── test-vm-compliant-instancetype.yaml
│           ├── test-vm-no-eviction-strategy.yaml
│           ├── test-vm-no-instancetype.yaml
│           ├── test-vm-dev-no-runstrategy.yaml
│           └── test-vm-production-no-runstrategy.yaml
├── dashboards/
│   └── governance-webhook.json       # Grafana dashboard (3 panels)
├── evidence/
│   ├── cost-impact.md                # FinOps experiment analysis
│   ├── kubecost-before.png           # Efficiency 3.8% (ungoverned)
│   ├── kubecost-after.png            # Efficiency 5.3% (governed)
│   └── kubecost-cost-allocation.csv
├── tests/
│   └── kyverno/                      # Integration test manifests
├── kind-config.yaml                  # 2-node kind cluster definition
├── SLO.md                            # Formal SLI/SLO/Error Budget definition
├── main.go                           # Webhook server (HTTPS, /validate, /metrics)
├── Dockerfile                        # Multi-stage, distroless final image
└── certs/                            # TLS certs (gitignored)
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

**`ValidateResourceLimits`** — every container in `spec.template.spec.containers` must declare both `cpu` and `memory` under `resources.limits`. Scope: Deployments. A production extension would cover all pod-template-bearing resources (StatefulSets, DaemonSets, Jobs, CronJobs).

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
    UID:     review.Request.UID, // Must echo — omit and the API server rejects the response
    Allowed: true,
}
```

**Table-driven tests with multi-element cases:**
Nine tests across two validators. Multi-container test cases are required to prove loop correctness — a single-container test can pass even with a broken `return` instead of `continue` in the validation loop.

### Build

```bash
# Build binary
CGO_ENABLED=0 GOOS=linux go build -o webhook .

# Build image (multi-stage, distroless)
docker build -t k8s-governance-controller:v0.2.0 .
```

---

## Testing

### Go Unit Tests

```bash
go test ./pkg/validator/... -v
```

Nine table-driven tests across two validators:

- **`TestValidateResourceLimits`** (5 cases) — compliant Deployment, missing limits block, missing CPU only, missing memory only, multi-container with one broken container. The multi-container case proves the loop collects all violations rather than returning on the first one found.
- **`TestValidateRequiredLabels`** (4 cases) — all labels present, one label missing entirely, one label present but empty, two problems at once.

Tests assert violation _count_, not message text, so wording changes don't break the suite.

### Kyverno Policy Tests

Test manifests live in `manifests/tests/kyverno/`. Apply them against a running cluster with policies loaded. Add `--dry-run=server` to run the full admission chain without creating anything:

```bash
# Should be rejected — missing evictionStrategy
kubectl apply --dry-run=server -f manifests/tests/kyverno/test-vm-no-eviction-strategy.yaml

# Should be admitted — compliant VM
kubectl apply --dry-run=server -f manifests/tests/kyverno/test-vm-compliant.yaml

# Should be rejected — production VM missing runStrategy: Always
kubectl apply --dry-run=server -f manifests/tests/kyverno/test-vm-production-no-runstrategy.yaml

# Should be admitted — dev VM, runStrategy policy doesn't apply
kubectl apply --dry-run=server -f manifests/tests/kyverno/test-vm-dev-no-runstrategy.yaml

# Should be rejected — no instancetype, raw CPU/memory declared
kubectl apply --dry-run=server -f manifests/tests/kyverno/test-vm-no-instancetype.yaml

# Should be admitted — instancetype reference present, no raw sizing
kubectl apply --dry-run=server -f manifests/tests/kyverno/test-vm-compliant-instancetype.yaml
```

The asymmetric cases (production rejected, dev admitted) prove that label-scoped policies fire precisely where the requirement exists and nowhere else.

---

## Kyverno Policies

Kyverno (chart version pinned in Step 6) provides mutation, declarative validation, and KubeVirt-aware policy that would take far more code to express in the Go webhook — and can be changed without a code release.

### Tier 1 — Baseline Governance

**`mutate-inject-environment.yaml`**
Mutating policy. Injects `environment: lab` on any Deployment that does not already declare an environment label, using Kyverno's conditional patch syntax `+(environment): lab`. Runs in the mutating phase — before any validating webhook — so the label is present before validation fires.

**`validate-registry-restriction.yaml`**
Enforce mode. Rejects any Deployment whose containers reference images outside `registry.kytechxyz.io/*`. Kept in Kyverno rather than Go because it's security posture: it should fail closed, and the allowlist should be changeable as declarative policy rather than code.

### Tier 2 — KubeVirt VM Governance

These policies govern `VirtualMachine` objects against the KubeVirt CRD schema. Each maps a traditional RHV/VMware operational discipline to admission-time enforcement.

**`validate-vm-eviction-strategy.yaml`**
Requires `spec.template.spec.evictionStrategy: LiveMigrate`. The vMotion contract: a VM without it is shut down during node maintenance instead of live-migrated.

> **Edge case (not implemented here):** VMs on ReadWriteOnce storage or direct-LUN attachments cannot live-migrate — the storage is not shared across nodes. Forcing `LiveMigrate` on such VMs can block node drains. A production version of this policy would exempt those workloads, for example by a storage label selector.

**`validate-vm-run-strategy.yaml`**
Requires `spec.runStrategy: Always` on VMs labeled `environment: production`. Label-scoped: development VMs are exempt. Proven with an asymmetric test — production VM rejected, dev VM admitted.

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

The `|| ''` fallback is required — referencing an absent field fails the variable substitution rather than returning empty.

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

**Availability SLO:** 99.5% over a rolling 30-day window.

```
Error budget = 0.5% × 2,592,000s = 12,960s = 3.6 hours
```

**Latency SLO:** 99.5% of admission requests complete in under 200ms.

```
Error budget = 0.5% of requests (request-based — cannot be expressed in hours)
```

The budget is request-volume-dependent. On a low-volume cluster, a single slow request spikes the percentage. That is the correct behavior of a request-counted budget.

**The blind spot:** with `failurePolicy: Ignore`, a request that times out or fails TLS never reaches the webhook — so the webhook's own metrics can't see it. Fail-open is measured from the API server side (for example `apiserver_admission_webhook_fail_open_count` and `apiserver_admission_webhook_admission_duration_seconds`).

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

This is not a strictly controlled experiment — the efficiency denominator changes between states (node capacity vs. declared limit). 3.8% and 5.3% are not the same metric at two points in time, so the 39% relative change is not a like-for-like improvement. What the comparison demonstrates: governance makes the efficiency metric meaningful. Without declared bounds, there is no denominator that means anything and no efficiency number worth acting on.

The savings figure rose ($162.45 → $165.40) because Kubecost gained measurement resolution: unconstrained workloads have no right-sizing signal, so recommendations were invisible rather than small. Once limits were declared, Kubecost could surface specific reductions.

See [`evidence/cost-impact.md`](evidence/cost-impact.md) for the full analysis.

---

## Failure Modes Documented

Five real failure modes encountered during implementation, each teaching the same architectural lesson: behavior in a governance control plane is emergent — arising from layer interactions, not individual components.

1. **Validating-to-validating webhook ordering is undefined; mutation-to-validation is not.** The system relies on mutation preceding validation (deterministic). It does not depend on which validating webhook fires first (non-deterministic).

2. **The Enforcement Paradox.** A control plane governing the cluster it runs in requires a designed escape hatch. Every infrastructure tool installed (Kyverno, KubeVirt, Prometheus, Kubecost) hit the same bootstrap deadlock: namespace exemption required before the tool deployed; tool couldn't deploy without it. Five incidents, one principle.

3. **Governance applies to governance tooling.** The audit-mode PolicyReport flagged the governance webhook itself. Kept honest. A framework that exempts its own tooling isn't governance.

4. **JMESPath nil-safety at schema boundaries.** Referencing an absent field fails substitution. Use `{{ field || '' }}` with `NotEquals ""` when writing policy against optional CRD fields.

5. **ServiceMonitor "discovered but dropped."** Prometheus relabeling evaluates Service metadata labels, not the pod selector. "ServiceMonitor created" and "metrics flowing" are different states.

---

## Running Locally

### Prerequisites

- [kind](https://kind.sigs.k8s.io/) — local Kubernetes cluster
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [helm](https://helm.sh/)
- [Go](https://golang.org/) (version per `go.mod`)
- [Docker](https://www.docker.com/)

All commands below run from the repo root.

```bash
git clone git@github.com:kytechxyz/k8s-governance-controller.git
cd k8s-governance-controller
```

---

### Step 1 — Create the Cluster

```bash
kind create cluster --name go-dev-cluster --config kind-config.yaml
kubectl get nodes  # both nodes should show Ready
```

---

### Step 2 — TLS Certificates

The webhook requires a self-signed cert with SANs matching the in-cluster Service DNS name.

```bash
mkdir -p certs   # not tracked by git

openssl req -x509 -newkey rsa:4096 -keyout certs/server.key.pem \
  -out certs/server.cert.pem -days 365 -nodes \
  -subj "/CN=governance-webhook.default.svc" \
  -addext "subjectAltName=DNS:governance-webhook.default.svc,DNS:governance-webhook.default.svc.cluster.local"
```

> **Rebuilding with existing certs:** check the expiry before reusing:
>
> ```bash
> openssl x509 -enddate -noout -in certs/server.cert.pem
> ```
>
> If the cert is still valid you can skip regeneration — but the `caBundle` in `manifests/webhook-config.yaml` must match the cert in `certs/`. See **Cert Rotation** below if they differ.

Update `manifests/webhook-config.yaml` with the base64-encoded cert:

```bash
base64 < certs/server.cert.pem | tr -d '\n'
# Paste the output as the caBundle value in manifests/webhook-config.yaml
```

---

### Step 3 — Verify Namespace Exemptions

**Before installing any infrastructure tools**, confirm the exemption lists are in place. Without them, the Enforcement Paradox blocks every infrastructure install.

```bash
# Verify webhook exemptions
grep -A 12 "namespaceSelector" manifests/webhook-config.yaml

# Verify Kyverno registry exemptions
grep -A 12 "exclude" manifests/kyverno/tier1/validate-registry-restriction.yaml
```

Both should include: `kyverno`, `kube-system`, `kubevirt`, `monitoring`, `kubecost`, `cert-manager`, `local-path-storage`.

---

### Step 4 — Build and Load Image

```bash
docker build -t k8s-governance-controller:v0.2.0 .
kind load docker-image k8s-governance-controller:v0.2.0 --name go-dev-cluster
```

---

### Step 5 — Deploy Webhook

```bash
kubectl apply -f manifests/webhook.yaml
kubectl apply -f manifests/webhook-config.yaml
kubectl rollout status deployment/governance-webhook
```

Verify the webhook is healthy and the cert is accepted:

```bash
kubectl logs -l app=governance-webhook --tail=5
# Should show: "starting webhook server on :8443 (TLS)"
# TLS handshake errors mean the caBundle doesn't match the serving cert — see Cert Rotation below
```

---

### Step 6 — Install Kyverno

```bash
helm repo add kyverno https://kyverno.github.io/kyverno/ 2>/dev/null || true
helm repo update
helm upgrade --install kyverno kyverno/kyverno \
  --version 3.8.1 \
  -n kyverno --create-namespace
kubectl get pods -n kyverno  # all 4 controllers should reach Running
```

> **Version pin:** `--version 3.8.1` is the chart this project was built against. Newer Kyverno releases introduce `ValidatingPolicy`/`MutatingPolicy` types alongside `ClusterPolicy`; these policies may need migration in a future release.

---

### Step 7 — Install KubeVirt

KubeVirt CRDs are required for Tier 2 policies. The operator manifest registers one CRD and deploys the operator; the CR triggers the operator to register all VM-related CRDs.

```bash
# 7a — Install operator
kubectl apply -f https://github.com/kubevirt/kubevirt/releases/download/v1.8.4/kubevirt-operator.yaml
kubectl wait --for=condition=ready pod -l kubevirt.io=virt-operator -n kubevirt --timeout=120s

# 7b — Apply CR (registers all VM CRDs)
kubectl apply -f https://github.com/kubevirt/kubevirt/releases/download/v1.8.4/kubevirt-cr.yaml
kubectl get crds | grep kubevirt.io
```

---

### Step 8 — Apply Governance Policies

```bash
kubectl apply -f manifests/kyverno/tier1/
kubectl apply -f manifests/kyverno/tier2/
kubectl apply -f manifests/kyverno/tier3/

# Verify all policies are Ready
kubectl get clusterpolicy
```

---

### Step 9 — Verify

```bash
# Should be rejected by the Kyverno registry restriction
kubectl apply -f manifests/test-bad-registry.yaml

# Should be rejected by the Go webhook — missing labels and limits
kubectl apply -f manifests/test-bad-deployment.yaml

# Should be admitted — compliant VM (ErrorUnschedulable is expected in kind — no hardware virtualization)
kubectl apply -f manifests/tests/kyverno/test-vm-compliant.yaml
kubectl get vm

# Check metrics
kubectl port-forward deployment/governance-webhook 8443:8443 &
curl -k https://localhost:8443/metrics 2>/dev/null | grep governance_
```

---

### Cert Rotation

When certs are regenerated, the `caBundle` in `webhook-config.yaml` **must be updated** before redeploying. If it doesn't match the serving cert, the API server fails the TLS handshake and `failurePolicy: Ignore` silently admits all requests without governance evaluation. Signals: `tls: bad certificate` in the webhook logs, and fail-open counts in the API server metrics.

```bash
# 1 — Regenerate certs (Step 2)
# 2 — Update caBundle
NEW_CA=$(base64 < certs/server.cert.pem | tr -d '\n')
# Paste NEW_CA into manifests/webhook-config.yaml as the caBundle value
kubectl apply -f manifests/webhook-config.yaml

# 3 — Verify dates match — both notBefore values must be identical
kubectl get validatingwebhookconfiguration governance-webhook \
  -o jsonpath='{.webhooks[0].clientConfig.caBundle}' | base64 -d | openssl x509 -noout -dates
echo | openssl s_client -connect localhost:8443 2>/dev/null | openssl x509 -noout -dates

# 4 — Confirm TLS errors are gone
kubectl logs -l app=governance-webhook --tail=10
```

---

## Production Hardening (Not Implemented Here)

This is a lab project, not a production deployment. Production hardening requires:

- **Automated cert rotation** — for example cert-manager with CA injection, keeping `caBundle` synchronized with the serving cert
- **HA replicas + PodDisruptionBudget** — so the availability SLO is defensible
- **Extended validation scope** — StatefulSets, DaemonSets, Jobs, CronJobs, `initContainers`, `ephemeralContainers`
- **`dryRun` handling** — so server-side dry runs don't record phantom violations
- **Policy tests in CI** — `kyverno test` against compliant and non-compliant fixtures on every policy change, including workloads that arrive by migration rather than by `kubectl apply`

---

## Author

Kyle Williams, Principal Platform / Infrastructure Engineer. Experience spanning enterprise virtualization, Kubernetes, OpenShift, and KubeVirt.

[GitHub](https://github.com/kytechxyz) · [LinkedIn](https://www.linkedin.com/in/kyle-williams-systems/) · [Medium](https://medium.com/@kyle-williams-systems)
