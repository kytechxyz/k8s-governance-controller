# From ulimits to Admission Controllers: Why Resource Governance Is a 30-Year-Old Discipline

*Governance is the architectural prerequisite for FinOps. A controlled experiment — and five failure modes — prove it.*

---

A controlled experiment: enforce resource governance at admission time on a Kubernetes cluster, measure allocation efficiency before and after with Kubecost. The result was a 39% relative improvement — 3.8% to 5.3% cluster efficiency. The absolute cost delta on a 2-node local cluster is negligible. That's not the finding.

The finding is the mechanism. Governance influenced resource behavior before workloads existed. Kubecost simply measured the downstream effect. That shift — from observing cost to engineering it — is the difference between reactive FinOps and deterministic FinOps. The number is lab-scale. The mechanism isn't.

In 2004, a runaway process could consume an entire server. We solved it with `ulimit` — a contract enforced at the OS boundary: you may not consume more than you declared. Then cgroups made the contract hierarchical. Then SELinux made it mandatory. Each generation pushed enforcement earlier in the lifecycle, from post-incident cleanup to admission-time prevention.

Virtualization engineers extended the same discipline to VMs. Resource pool reservations. VM templates with standardized sizing tiers. You didn't provision a virtual machine without declaring its resource contract, because the vCenter efficiency report was meaningless without one. The resource pool is the intellectual ancestor of the Kubernetes admission webhook — the same idea, encoded at a different layer.

Cloud-native platforms broke the contract. A `Deployment` with no `resources` block is the 2024 equivalent of a process with no `ulimit`. The blast radius is a cloud billing cycle instead of a server. The discipline that solved it in 2004 solves it now.

The platform I govern spans both worlds: containerized workloads and KubeVirt virtual machines running on the same Kubernetes control plane. That duality is clarifying, not complicating. When you enforce governance across VMs and containers simultaneously, the principle becomes impossible to miss: resource governance is a platform discipline, not a container feature.

This is the thesis: **governance is not a competing priority to FinOps. It is its architectural prerequisite.** Without admission-time enforcement, cost optimization is reactive observation of chaos — you measure what happened but you cannot engineer what will happen. With governance, FinOps becomes deterministic. You control resource behavior at the moment workloads enter the cluster.

One honest qualifier, stated now rather than buried later: admission-time governance is not an impenetrable gate. A degraded webhook running `failurePolicy: Ignore` fails open. The SLO is the compensating control that detects degradation before it becomes systematic bypass. The thesis is about architectural *position*, not about perfection. The rest of this article is the implementation.

---

## The Architecture: A Unified Admission-Time Control Plane

The first question a senior engineer asks is the right one: why write a Go admission webhook when Kyverno can express resource-limit and label validation as declarative policy? The honest answer has three parts.

First, complex cross-field logic. A rule like "if `team: billing` and `environment: production`, require `cpu.limits` ≥ 1000m" is awkward in Kyverno's JMESPath and natural in Go. The moment validation branches on field relationships, imperative code earns its place. Second, the webhook is in the critical path of every `kubectl apply`; pure in-memory Go validation completes in single-digit milliseconds. Third — I'll state this plainly rather than imply otherwise — a working admission controller is a capability signal that YAML cannot send. TLS bootstrapping, table-driven tests, a distroless image, correct `AdmissionReview` protocol handling: these demonstrate that I can build the infrastructure layer, not only configure it. That's a legitimate reason, stated honestly.

Every `kubectl apply` traverses a synchronous chain before anything persists: kubectl → API server → authentication → authorization → mutating webhooks → validating webhooks → etcd. The governance layer sits at hops 5 and 6. Two facts drive the design: the chain is synchronous — the developer's terminal blocks until the webhook responds — and mutation *always* precedes validation, deterministically. The architecture relies on that ordering: Kyverno injects a default `environment` label before the Go webhook validates its presence.

The protocol has one trap the documentation underplays. The `AdmissionReview` request carries a UID that the response must echo back verbatim:

```go
response := &admissionv1.AdmissionResponse{
    UID:     review.Request.UID,  // echo or the API server drops the response
    Allowed: true,
}
```

Return the wrong UID and the API server discards your response as though the webhook never answered. The request proceeds according to `failurePolicy`. This separates "I followed a tutorial" from "I understand the protocol."

The validation logic draws a deliberate boundary between two failure kinds:

```go
func ValidateResourceLimits(raw []byte) (violations []string, err error) {
    // err        → infrastructure failure: object couldn't be decoded
    // violations → business-logic findings: object is well-formed but non-compliant
}
```

Conflating them produces a webhook that returns HTTP 500 for policy violations — wrong and operationally confusing. One scope note the senior reader will check: this validator targets `Deployments`. A production implementation covers all pod-template-bearing resources — StatefulSets, DaemonSets, Jobs, CronJobs. The Deployment scope is a deliberate Phase 1 boundary, named rather than hidden.

Now the enforcement posture — the most important architectural decision in the system, and a deliberate split-brain. The Go webhook runs `failurePolicy: Ignore`: its checks fail **open**. Kyverno's policies run `Enforce`: registry restriction and KubeVirt VM policies fail **closed**. The asymmetry is intentional. The Go webhook governs workload ergonomics — resource limits, attribution labels — where a temporary gap is impactful but recoverable. The security and VM-infrastructure layer fails closed because a bypass there is not recoverable. Enforcement posture follows risk profile. The SLO (below) monitors the opening the Ignore policy creates.

---

## The KubeVirt Layer: Where Virtualization Experience Becomes an Unfair Advantage

There are thousands of engineers writing about Kyverno policy. Very few are writing about governing KubeVirt virtual machines with it, and fewer still from the perspective of decades migrating workloads off RHV and onto OpenShift Virtualization and VMware. The three KubeVirt policies here aren't arbitrary demonstrations. Each encodes an operational discipline virtualization engineers have enforced for twenty years — translated into admission-time policy.

The first requires every VirtualMachine to declare `evictionStrategy: LiveMigrate`. A VM without it gets forcibly terminated when its node drains, instead of live-migrating. This is the vMotion contract, enforced through DRS for years. In KubeVirt it's a Kyverno pattern match:

```yaml
pattern:
  spec:
    template:
      spec:
        evictionStrategy: LiveMigrate
```

But a blanket requirement is wrong in exactly the way that bites you in production. A VM on ReadWriteOnce storage — or a direct-LUN attachment, the kind of pass-through block storage I worked with constantly during RHV-to-VMware and RHV-to-OpenShift migrations — *cannot* live-migrate. Force `LiveMigrate` on such a VM and the node drain hangs indefinitely, waiting for an eviction that can never complete. That failure mode doesn't appear in any Kyverno tutorial, because writing the tutorial doesn't require knowing how direct-LUN storage behaves under node drain. Knowing it requires having watched the drain stall. The production-correct policy exempts those workloads:

```yaml
exclude:
  any:
    - resources:
        selector:
          matchLabels:
            storage-class: direct-lun
```

The lesson generalizes: a governance policy is only as good as the author's knowledge of the edge cases it will encounter. The cloud-native engineer who's never managed pass-through storage writes the blanket policy. The virtualization engineer writes the conditional one — because they've felt the node drain hang.

The second policy enforces `runStrategy: Always`, scoped by label selector to VMs marked `environment: production`. This is the HA restart contract — a crashed production VM restarts automatically; a dev VM can stay down. The label selector makes the policy surgical: it enforces exactly where the operational requirement exists and nowhere else.

The third requires VMs to reference a `VirtualMachineClusterInstancetype` and rejects raw CPU and memory sizing — the VM template discipline. Standard tiers only; ad-hoc sizing rejected. The policy requires both the presence of the instancetype reference *and* the absence of raw sizing, because a VM could declare both, and a policy that only checked for the instancetype would let raw sizing slip through alongside it.

---

## What the Failures Taught: Behavior Is Emergent

Building this framework surfaced five failure modes that don't appear in tutorials. They're worth documenting because they all teach the same lesson: a governance control plane is not a stack of independent tools. Its behavior is emergent — arising from interactions between layers that are documented in no single README.

**Ordering: validating-to-validating is undefined; mutation-to-validation is not.** Between two validating webhooks, Kubernetes guarantees no order — either fires first. The naive lesson is "never depend on ordering." That's wrong, and acting on it costs you a real capability. Mutation *always* precedes validation, and this system relies on it: Kyverno injects `environment: lab` before the Go webhook validates the label's presence. The precise claim: design *validating* webhooks for independence; use mutation-before-validation deliberately. An engineer who flattens this to "ordering is undefined" has designed away a dependency the system actually needs.

**The Enforcement Paradox: a self-governing control plane needs a designed escape hatch.** Every infrastructure tool I installed — Kyverno, KubeVirt, the Prometheus stack, Kubecost — hit the same wall. Each needed a namespace exemption that couldn't exist until after the tool deployed, but the tool couldn't deploy without the exemption. Five tools, one architectural principle: a control plane that governs the cluster it runs in must have a deliberately designed bootstrap escape hatch, or it cannot deploy the webhook that gates the webhook. SELinux solved this with `permissive` mode. The Kubernetes equivalent is a namespace-exemption taxonomy defined *before* enforcement begins. The failure is organizational disguised as technical: enforcement was switched on before the exemption strategy was designed.

**Governance applies to governance tooling.** The Tier 3 audit-mode PolicyReport flagged the governance webhook's own Deployment against two of its own policies — unapproved registry image, missing owner label. This is correct, and it's the most credible result in the project. A governance framework that exempts its own tooling isn't governance — it's theater.

**Nil-safety at the intersection of two schemas you don't own.** The instancetype policy's deny rule crashed on compliant VMs because `length(@)` on an absent `domain.cpu` field throws rather than returning zero. The fix:

```yaml
key: "{{ request.object.spec.template.spec.domain.cpu || '' }}"
operator: NotEquals
value: ""
```

When writing policy against a CRD you don't own, test explicitly for absent fields. The failure lives at the intersection of Kyverno's nil semantics and KubeVirt's optional schema fields — undocumented by either system independently.

**"Discovered but dropped": configured is not working.** Prometheus found the webhook's metrics endpoint and silently discarded it. The Service carried no metadata labels — only a pod selector. Prometheus's relabeling `keep` rule reads `__meta_kubernetes_service_label_app` from Service metadata. No metadata labels, no match, no scrape — with no error, just absence. The ServiceMonitor, Service metadata, and Prometheus relabeling interact to produce a result none of the three documents alone. Debug the system, not the components.

**What the five share.** The behavior lives in the seams. The parts were all configured correctly. The behavior still surprised me. That gap is the entire discipline.

---

## Making the System Legible: Two SLOs, Not One

A governance webhook you cannot measure is a governance webhook you cannot trust. It's synchronous in the path of every `kubectl apply` — its latency is the developer's experience, and its availability is the integrity of enforcement itself. The most common mistake in webhook SLO design is treating it as a single objective. It is two, and conflating them produces math an SRE will catch on first read.

The instrumentation is a Prometheus histogram recording each admission decision's duration, labeled by resource type and result. A histogram — not a gauge or counter — because the question is distributional. One precision note: quantile estimation interpolates within buckets, so "percent under 200ms" is exact only if a bucket boundary sits at 200ms. The bucket list `[0.005, 0.01, 0.025, 0.05, 0.1, 0.2, 0.5]` places a boundary at `0.2` exactly — not by accident.

**The availability SLO is time-based.** Target: the webhook is reachable and serving decisions 99.5% of the time over a rolling 30-day window. This is the objective to which an error budget in *hours* correctly applies:

```
30 days = 2,592,000 seconds
error budget = 0.5% × 2,592,000 = 12,960 seconds = 3.6 hours
```

The signal that this budget is burning is dangerous precisely because it's invisible: as latency climbs toward the API server's webhook timeout, requests time out and, because the webhook runs `failurePolicy: Ignore`, those requests are admitted *without governance*. Availability degradation doesn't produce errors. It produces silent enforcement bypass.

**The latency SLO is request-based, and its budget cannot be expressed in hours.** Target: 99.5% of admission requests complete in under 200ms. The budget is 0.5% *of requests* — not 0.5% of time. You cannot convert a request-based budget to hours without knowing request rate. On a low-volume cluster, a single slow request swings the percentage violently. That is the correct behavior of a request-counted budget, and stating it plainly is the difference between understanding SLOs and reciting them.

Why 200ms? The human-perception threshold — below it, `kubectl apply` feels instant. For pure in-memory validation with no I/O, observed p99 is single-digit milliseconds. The objective is deliberately generous: any sustained breach is signal, because the steady state has an order of magnitude of headroom.

`failurePolicy: Ignore` means the latency SLO is also a governance-integrity metric. A p99 trending toward the timeout is a webhook trending toward silent bypass. The SLO fires that signal while there is still time to act. Without it, degraded governance surfaces as a cost or security incident weeks later, with no telemetry to explain it.

There is a direct line from this to the storage SLAs of the enterprise virtualization era — with one decisive improvement. SNMP-polled monitoring rolled five-minute samples into hourly averages after a week. The 500-millisecond latency burst that caused the 2 a.m. incident was mathematically smoothed into a healthy average by Friday, and you could no longer prove the SAN caused the outage. The Prometheus histogram keeps full-resolution bucket data for the entire retention window. The burst at 2 a.m. is still in the error-budget calculation on day 30. The accountability discipline is the same one I practiced for years; the toolchain finally stopped destroying its own evidence.

---

## The Experiment: FinOps as a Downstream Effect

Two cluster states, the same three Deployments, the same measurement tool. First state: governance webhook disabled, workloads carry no resource declarations — the common condition where a team ships `image: nginx` with no `resources` block. Second state: webhook live, same workloads redeployed with declared CPU and memory limits, plus the cost-center and team labels that make spend attributable.

Kubecost measured both:

| | Cluster Efficiency | Surfaced Monthly Savings |
|---|---|---|
| Before (ungoverned) | 3.8% | $162.45 |
| After (governed) | 5.3% | $165.40 |

Before stating what this proves, I want to state what it doesn't — because the honesty is the point. This is not a strictly controlled experiment. The efficiency denominator changes between states: with no declared limits, Kubecost falls back to node-level capacity as the ceiling; with limits declared, the ceiling is the declared limit. Those are different denominators — 3.8% and 5.3% are not the same metric sampled twice. What the comparison demonstrates is not "efficiency rose 1.5 points." It is something more fundamental: **governance is what makes the efficiency metric meaningful at all.**

Without declared resource bounds, there is no denominator that means anything, nothing to right-size toward, and no efficiency number worth acting on. FinOps in that state is archaeology — you sift through cost reports describing chaos that already happened. Once governance forces a declared ceiling, the efficiency ratio becomes actionable: it measures the gap between what a workload reserved and what it actually uses. Governance shaped resource behavior *before the workloads existed*. Kubecost merely measured the consequence. That is the shift from reactive FinOps to deterministic FinOps.

The surfaced-savings figure rose — from $162.45 to $165.40 — which looks backwards until you understand the model. Before governance, Kubecost couldn't surface right-sizing recommendations for unconstrained workloads because a workload whose ceiling is "the entire node" has no meaningful target to size toward. The recommendations weren't small; they were *invisible*. Once limits were declared, Kubecost could compare reservation against actual usage and surface specific reductions. Savings went up because the system gained measurement resolution it never had — not because efficiency degraded.

On Red Hat Virtualization — and on the VMware estates — you enforced resource declarations at provisioning time: reservations, limits, templated VM sizing. A utilization report was meaningless if VMs floated against raw host capacity with no declared contract. You declared the contract up front so the measurement downstream would mean something. The enforcement boundary has moved — from RHV-M and vCenter to the Kubernetes admission chain. The principle has not moved at all.

A word on scale, stated precisely because imprecision here is where FinOps claims lose credibility. The allocation *arithmetic* is scale-invariant: a workload without a declared limit cannot be right-sized, on a two-node lab or a two-thousand-node fleet. But production efficiency *dynamics* are not scale-invariant — bin-packing, HPA/VPA interaction, burstable versus Guaranteed QoS, spot capacity, and multi-tenancy all introduce variables this lab doesn't model. What transfers is the mechanism and the methodology: declare bounds at admission, then measure the gap. On a cluster with real compute spend, the financial impact scales with the spend. The measurement procedure is identical to what I did here.

---

## The Architecture That Outlasts the Tools

`ulimit` to cgroups to SELinux to admission controllers. Four layers of the same idea, across three decades: a resource contract enforced as early in the lifecycle as the platform allows. The surface kept changing — processes became containers, containers joined virtual machines on a shared control plane — but the discipline never did. Resource governance is not a feature of Kyverno or a property of Kubernetes. It is a platform engineering practice that predates both and will outlast them.

This implementation is portfolio-grade, not production, and the difference is a specific list. Production hardening requires:

- **Cert rotation** — keeping the `ValidatingWebhookConfiguration` caBundle synchronized with the serving cert as it rolls
- **HA replicas + PodDisruptionBudget** — so the availability SLO is defensible, not aspirational
- **Expanded validation scope** — StatefulSets, DaemonSets, Jobs, CronJobs, `initContainers`, `ephemeralContainers`
- **`dryRun` handling** — so server-side dry runs don't record phantom violations

None of that is exotic. All of it is necessary. An engineer who names the gaps without being asked is more trustworthy than one who lets you discover them.

The deepest lesson is underneath all of it. A governance control plane has no meaningful component-by-component view. The ServiceMonitor doesn't work without the Service label. The eviction policy misfires without storage-class awareness. The Go webhook's fail-open posture only makes sense alongside Kyverno's fail-closed one — together they form a tiered enforcement contract that neither specifies alone. The behavior lives in the seams. The job is to reason about the whole.

And this is where the virtualization background stops being a footnote and becomes the point. Cloud-native engineers are trained to think in ephemeral containers — cattle, not pets, gone in seconds. Virtualization engineers are trained to think in long-lived resource commitments — machines that hold state for years and must be accounted for the whole time. KubeVirt collapses those two worlds onto one control plane and does not let you choose between the mental models. It demands governance that satisfies both at once. That is the hard, interesting center of platform engineering right now — the exact seam where three decades of enterprise infrastructure experience meets a modern Kubernetes API. The two skill sets aren't a sequence — old then new. They're a single discipline, finally converging on one platform.

The complete implementation — Go admission webhook, three-tier Kyverno policy set, KubeVirt VM governance, Prometheus instrumentation, the dual-SLO definition, the Grafana dashboard, the Kubecost integration, and an evidence directory that documents the failures as honestly as the successes — is on GitHub: **github.com/kytechxyz/k8s-governance-controller**.
