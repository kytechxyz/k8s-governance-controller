# k8s-governance-controller

A Kubernetes governance framework demonstrating admission-time resource enforcement, KubeVirt VM policy, SRE observability, and FinOps cost measurement. A quarter-long personal lab project, not deployed in any production environment.

**The thesis:** governance is the architectural prerequisite for FinOps. Without admission-time enforcement, cost optimization is reactive observation of chaos. With it, FinOps becomes deterministic — you control resource behavior at the moment workloads enter the cluster, and cost measurement follows from that controlled state.

A controlled before/after experiment validated this: enforcing resource bounds at admission time improved measured cluster allocation efficiency from 3.8% to 5.3% — a 39% relative improvement. The efficiency metric was previously unmeasurable because unconstrained workloads have no declared ceiling to measure against.

→ **[Read the full architecture writeup on Medium](https://medium.com/@kyle-williams-systems/from-ulimits-to-admission-controllers-why-resource-governance-is-a-30-year-old-discipline-0200426b19a3)**

---

## How This Was Built

Built as a personal lab project with AI-assisted development. I set the design, the policies, the failure-mode experiments, and the tests, and reviewed every change; AI sped up the implementation.

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
├── cmd/                              # Main entry point (reserved)
├── pkg/
│   ├── validator/
│   │   ├── limits.go                 # Resource limits validator
│   │   ├── limits_test.go            # Table-driven tests (5 cases)
│   │   ├── labels.go                 # Required labels validator
│   │   ├── labels_test.go            # Table-driven tests (4 cases)
│   │   └── metrics.go                # Prometheus instrumentation
│   └── webhook/                      # Webhook handler (reserved)
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

Kyverno v1.19.0 (Helm chart 3.9.0) provides mutation, declarative validation, and KubeVirt-aware policy that the Go webhook cannot express.

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

**Working directory:** all commands below must be run from the repo root.

```bash
git clone git@github.com:kytechxyz/k8s-governance-controller.git
cd k8s-governance-controller
```

> **zsh note:** do not paste multi-line commands in zsh — the PS2 continuation glyph injects literal characters into the command. Type multi-line commands directly or use the file-based approach shown below.

---

### Step 1 — Create the Cluster

A `kind-config.yaml` is included in the repo root:

```bash
kind create cluster --name go-dev-cluster --config kind-config.yaml
kubectl get nodes  # both nodes should show Ready
```

---

### Step 2 — TLS Certificates

The webhook requires a self-signed cert with SANs matching the in-cluster Service DNS name.

```bash
# Create the certs directory — not tracked by git
mkdir -p certs

# Generate cert — type this command directly, do not paste
openssl req -x509 -newkey rsa:4096 -keyout certs/server.key.pem \
  -out certs/server.cert.pem -days 365 -nodes \
  -subj "/CN=governance-webhook.default.svc" \
  -addext "subjectAltName=DNS:governance-webhook.default.svc,DNS:governance-webhook.default.svc.cluster.local"
```

> **Rebuilding with existing certs:** if `certs/` already exists from a prior install, check the cert expiry before reusing:
>
> ```bash
> openssl x509 -enddate -noout -in certs/server.cert.pem
> ```
>
> If the cert is still valid, you can skip regeneration — but the `caBundle` in `manifests/webhook-config.yaml` must match the cert currently in `certs/`. See **Cert Rotation** below if they differ.

Update `manifests/webhook-config.yaml` with the base64-encoded cert:

```bash
cat certs/server.cert.pem | base64 | tr -d '\n'
# Paste the output as the caBundle value in manifests/webhook-config.yaml
```

---

### Step 3 — Verify Namespace Exemptions

**Before installing any infrastructure tools**, confirm the exemption lists are in place. The Enforcement Paradox — a control plane that governs the cluster it runs in cannot bootstrap itself without a designed escape hatch — will block every infrastructure install if exemptions aren't defined first.

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
# Confirm no stale cached image from a prior build
docker images | grep k8s-governance-controller

# Build fresh (from repo root — Dockerfile must be present)
docker build -t k8s-governance-controller:v0.2.0 .

# Load into kind cluster
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

> **Version pin:** `--version 3.8.1` pins to the version this project was built against. Kyverno v1.19+ deprecates `ClusterPolicy` in favor of `ValidatingPolicy`/`MutatingPolicy`. The existing policies work but will require migration in a future release.

---

### Step 7 — Install KubeVirt

KubeVirt CRDs are required for Tier 2 policies. Two steps are required: the operator manifest registers one CRD and deploys the operator; the CR triggers the operator to dynamically register all VM-related CRDs.

```bash
# Step 7a — Install operator (registers kubevirts.kubevirt.io CRD, deploys virt-operator)
kubectl apply -f https://github.com/kubevirt/kubevirt/releases/download/v1.8.4/kubevirt-operator.yaml

# Wait for operator pods to be ready
kubectl wait --for=condition=ready pod -l kubevirt.io=virt-operator -n kubevirt --timeout=120s

# Step 7b — Apply CR (triggers operator to register all VM CRDs)
kubectl apply -f https://github.com/kubevirt/kubevirt/releases/download/v1.8.4/kubevirt-cr.yaml

# Wait for CRDs to register — expect 19
kubectl get crds | grep kubevirt.io | wc -l
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
# Should be rejected by Kyverno registry restriction
kubectl apply -f manifests/test-bad-deployment.yaml

# Should be admitted — compliant VM (ErrorUnschedulable status is expected in kind — no hardware virt)
kubectl apply -f manifests/tests/kyverno/test-vm-compliant.yaml
kubectl get vm

# Check metrics — generate traffic that reaches the Go webhook first
# (test-bad-deployment is caught by Kyverno before the Go webhook; use a registry-approved image)
kubectl port-forward deployment/governance-webhook 8443:8443 &
curl -k https://localhost:8443/metrics 2>/dev/null | grep governance_
```

---

### Cert Rotation

When certs are regenerated, the `caBundle` in `webhook-config.yaml` **must be updated** before redeploying. If it doesn't match the serving cert, the API server rejects the TLS handshake and `failurePolicy: Ignore` silently admits all requests without governance evaluation. The only signal is `tls: bad certificate` errors in the webhook logs.

```bash
# 1 — Regenerate certs (Steps 2 above)
# 2 — Update caBundle
NEW_CA=$(cat certs/server.cert.pem | base64 | tr -d '\n')
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

- **Cert rotation** — keeping `ValidatingWebhookConfiguration.caBundle` synchronized with the serving cert
- **HA replicas + PodDisruptionBudget** — so the availability SLO is defensible
- **Extended validation scope** — StatefulSets, DaemonSets, Jobs, CronJobs, `initContainers`, `ephemeralContainers`
- **`dryRun` handling** — so server-side dry runs don't record phantom violations

---

## Author

Kyle Williams, Principal Platform / Infrastructure Engineer. Experience spanning enterprise virtualization, Kubernetes, OpenShift, and KubeVirt.

[GitHub](https://github.com/kytechxyz) · [LinkedIn](https://www.linkedin.com/in/kyle-williams-systems/) · [Medium](https://medium.com/@kyle-williams-systems)
