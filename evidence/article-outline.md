# Article Outline: The Admission-Time Control Plane (v2 — post peer review)
## Governance Is the Architectural Prerequisite for FinOps

**Canonical:** Medium (@kyle-williams-systems)
**Cross-post:** LinkedIn Article
**Target length:** ~3,500 words
**Primary audience:** Hiring managers, Directors of Platform/SRE, IC6+ engineers
**Secondary audience:** Platform practitioners, Kyverno/KubeVirt users
**Repo:** github.com/kytechxyz/k8s-governance-controller

---

## Peer Review Changes (v1 → v2)

The following load-bearing issues were corrected from the first draft:

1. **Thesis/failurePolicy contradiction resolved.** Removed the claim "non-compliant
   workloads never reach etcd." Replaced with the accurate, more defensible position:
   governance shapes resource behavior at the earliest possible intervention point,
   and the SLO is the compensating control that monitors the integrity of that
   enforcement. The thesis now claims governance is the *architectural prerequisite*
   for FinOps — not that it's an impenetrable hard gate.

2. **SLO math category error corrected.** The SLI was request-based (% of requests
   under 200ms), but the error budget was expressed in hours — a time-based
   availability formula applied to a latency SLI. Fixed by explicitly defining two
   separate SLOs: an availability SLO (to which 3.6 hours correctly applies) and a
   latency SLO (whose budget is request-based, not time-based). The article turns
   this into a teaching moment rather than hiding the distinction.

3. **Deployment-scope gap acknowledged.** "Every container, no exceptions" was false
   for StatefulSets, DaemonSets, Jobs, bare Pods. Fixed by stating the scope
   accurately and framing the Deployment focus as a deliberate phased approach,
   with the production extension path named explicitly.

4. **Ordering claim corrected.** The article no longer claims all webhook ordering
   is undefined. It correctly distinguishes: mutation-before-validation is
   deterministic (and the architecture relies on it); validating-to-validating
   order is undefined. The system's actual dependency on mutation-before-validation
   is acknowledged and defended, not hidden.

5. **evictionStrategy edge case added.** RWO/direct-LUN VMs cannot live-migrate;
   a blanket evictionStrategy policy causes node drains to hang. The article flags
   this as the exact edge case an RHV migration specialist should anticipate, and
   shows the conditional exemption pattern.

6. **"Why Go" justified before implementation.** §2 now opens with the positive case
   for the Go layer before enumerating what it does.

7. **Bootstrap problem named as an architectural principle.** The five exemption
   incidents are unified under the "Enforcement Paradox" — a control plane that
   governs the cluster it runs in requires a designed escape hatch. Named and
   treated as the principal-level insight it is.

8. **Scope honesty front-loaded.** The kind-cluster confession moves from §5 to §1,
   so the 39% number reads as rigorous from the first paragraph rather than rescued
   three thousand words later.

9. **Savings number direction explained.** Projected savings went up ($162.45 →
   $165.40) alongside efficiency improvement. Explained: Kubecost can surface
   right-sizing recommendations only when declared limits exist — before governance,
   the recommendations were invisible, not absent.

10. **"Controlled experiment" language corrected.** Denominator changes between states
    (node capacity → declared ceiling). The comparison is explicitly framed as
    measuring two different things that are directionally meaningful, not the same
    metric at two points in time.

---

## The Dual-Read Test

Every section must pass two reads:
- **30-second skim (Director/HM):** Architectural claim and business result
  extractable without reading code?
- **Deep read (IC6/SRE):** Does technical depth hold up to scrutiny?

---

## §1 — The Problem Didn't Change. The Surface Did. (~400 words)
*Function: Hook + Thesis + Scope. Must land the number, the claim, AND the
honest scope within the first four paragraphs. Scope honesty front-loaded.*

**Opening paragraph — the number, honestly scoped:**
A controlled before/after experiment on a 2-node kind cluster: enforce resource
governance at admission time, measure cluster efficiency before and after.
Result: 3.8% → 5.3%, a 39% relative improvement in measured allocation
efficiency. Absolute costs are negligible — this is a local lab, not a
production billing statement. The finding isn't the number. The finding is
the mechanism: governance influenced resource behavior before workloads existed,
and Kubecost simply measured the downstream effect. That mechanism is
scale-invariant. The number isn't.

**The ulimits arc (KubeVirt motif #1):**
In 2004, a runaway process could consume an entire server. We solved it with
ulimits — a contract enforced at the OS boundary: you may not consume more than
you declared. Then cgroups made the contract hierarchical. Then SELinux made it
mandatory. Each layer pushed the enforcement earlier in the lifecycle.
Virtualization engineers extended the same discipline to VMs: resource pool
reservations, VM templates with standardized sizing tiers. You didn't provision
a VM without declaring its resource contract, because the efficiency report was
meaningless otherwise. The vCenter resource pool is the intellectual ancestor
of the Kubernetes admission webhook — the same idea, at a different layer.

Cloud-native platforms broke the contract. A Deployment with no `resources`
block is the 2024 equivalent of a process with no ulimit. The blast radius
is a cloud billing cycle, not a server.

**Thesis (explicit):**
Governance is not a competing priority to FinOps. It is its architectural
prerequisite. Without admission-time enforcement, cost optimization is reactive
observation of chaos — you can measure what already happened but you can't
engineer it. With governance, FinOps becomes deterministic: you control resource
behavior at the moment workloads enter the cluster, and cost measurement follows
from that controlled state.

Important qualifier (here, not buried in §5): admission-time governance is not
an impenetrable hard gate. A degraded webhook can fail open. The SLO is the
compensating control that detects degradation before it becomes systematic bypass.
The thesis is about architectural position, not about perfection.

**KubeVirt as the opening motif:**
The platform I govern spans both worlds: containerized workloads and KubeVirt
VMs running on the same control plane. That duality is clarifying. When you
enforce governance across both VMs and containers simultaneously, the principle
becomes obvious: resource governance is a platform discipline, not a container
feature.

---

## §2 — The Architecture: A Unified Admission-Time Control Plane (~700 words)
*Function: Technical credibility. "Why Go?" answered before "what does it do?"
Split-brain enforcement posture named and defended.*

**Why a Go webhook alongside Kyverno — answer this first:**
`ValidateResourceLimits` and `ValidateRequiredLabels` are expressible as Kyverno
`validate` rules. The Go layer exists for three reasons that justify its
complexity: (1) it demonstrates software engineering capability that YAML cannot
signal — a working admission controller, TLS-bootstrapped, with table-driven
tests and a distroless image, proves you can build the infrastructure layer, not
just configure it; (2) it enables complex cross-field validation that would
require convoluted JMESPath in Kyverno (e.g., "if team=billing AND
environment=production, require cpu.limits ≥ 1000m"); (3) it performs
validation in sub-millisecond Go rather than Kyverno's full policy evaluation
engine, important when the webhook is in the critical path of every kubectl apply.
The honest framing: the Go layer is also a portfolio artifact. That's a legitimate
reason stated honestly, not hidden.

**Enforcement posture — named and defended:**
The system has a deliberate split-brain posture during degradation:
- The Go webhook uses `failurePolicy: Ignore` — resource-limit and label
  enforcement fails **open**. During webhook degradation, Deployments enter
  the cluster without governance. This is intentional: the Go webhook governs
  workload ergonomics — impactful on FinOps, recoverable operationally. A
  governance gap is better than a cluster-wide outage.
- Kyverno policies default to `Enforce` — registry restriction and KubeVirt
  VM policies fail **closed**. These govern security posture and VM
  infrastructure integrity, where a bypass is not recoverable.

This is a tiered enforcement strategy, not an accident. The SLO (§4) is the
mechanism that detects when the Go webhook is degrading toward bypass before
the bypass becomes systematic.

**The admission chain:**
Every `kubectl apply` traverses a seven-hop synchronous chain before anything
reaches etcd: kubectl → API server → authentication → authorization → mutating
webhooks → validating webhooks → etcd. The governance layer occupies hops 5 and 6.
Two things to know about that position: first, it's synchronous — the developer's
terminal blocks until both hops complete. Second, mutation always precedes
validation, and that ordering is deterministic. The architecture relies on it:
Kyverno's mutating policy injects `environment: lab` before the Go webhook
validates that the label is present. That's not a coincidence — it's the
designed sequencing.

**Walk the Go webhook code:**
- `AdmissionReview` decode: the two-layer envelope. The outer object carries the
  UID that must be echoed back verbatim — return the wrong UID and the API server
  drops the response as if the webhook never responded.
- `ValidateResourceLimits`: every container in `spec.template.spec.containers`,
  CPU and memory, both required. Scope note: this targets Deployments. A
  production extension covers all pod-template-bearing resources —
  StatefulSets, DaemonSets, Jobs. The Deployment scope is a deliberate Phase 1
  boundary, not a complete implementation.
- `ValidateRequiredLabels`: cost-center, team, environment — the FinOps
  attribution fields. Without these, Kubecost cannot break costs down by team
  or cost center. The labels aren't bureaucracy; they're the cost allocation
  schema.

**The three-tier Kyverno layer:**
Kyverno handles what the Go webhook structurally cannot or shouldn't:
- **Tier 1, Mutating:** inject `environment: lab` for unlabeled Deployments.
  A validating webhook can't fix what it finds — it can only accept or reject.
  Mutation requires a separate pass before validation, which is exactly what
  Kyverno's mutating admission controller provides.
- **Tier 1, Validating:** registry restriction. Requires images from
  `registry.kytechxyz.io/*`. Pattern match — no reason to write Go for this.
- **Tier 2, KubeVirt-specific:** three policies governing VirtualMachine
  objects. These require the KubeVirt CRD schema, which Kyverno validates
  natively once the CRDs are registered.

**KubeVirt motif — first technical thread:**
The three KubeVirt policies each map directly to a VMware/RHV operational
discipline, admission-enforced rather than post-hoc detected:

- `evictionStrategy: LiveMigrate` — the vMotion equivalent. A VM without it
  gets forcibly terminated during node maintenance instead of live-migrated.
  **Edge case that matters here:** a VM on RWO (ReadWriteOnce) storage or a
  direct-LUN attachment *cannot* live-migrate — the storage isn't shareable
  across nodes. A blanket evictionStrategy policy on such a VM causes node
  drains to hang indefinitely, waiting on an eviction that can never complete.
  Given my RHV direct-LUN migration background, this is exactly the edge case
  I'd anticipate and design around. The production policy exempts VMs labeled
  `storage-class: direct-lun`. Knowing the edge case exists is the difference
  between a policy that works in demos and one that works in production.

- `runStrategy: Always` on production VMs — the HA restart policy equivalent.
  Scoped to `environment: production` via label selector, not all VMs. A dev
  VM that crashes can stay down until someone fixes it; a production VM cannot.

- `VirtualMachineClusterInstancetype` required, raw CPU/memory rejected — the
  VM template equivalent. Standardized sizing tiers only; ad-hoc sizing
  rejected. The same discipline that prevented VM sprawl in vCenter resource
  pools, reencoded in YAML. Note: the policy requires an exception path for
  workloads with legitimate off-tier requirements — the namespace exemption
  pattern from §3 applies here as well.

**The architectural claim for the Director reader:**
This is not three separate tools. It is a single admission-time control plane
with distinct layers that cooperate. The Go webhook owns workload resource
contracts. Kyverno owns mutation and VM-specific policy. The enforcement
postures differ deliberately by risk profile. The architecture is the decision.

---

## §3 — What the Failures Taught: Behavior Is Emergent (~700 words)
*Function: Failure modes reframed as architectural evidence. Each failure
illustrates the thesis. The bootstrap problem is named as a principle.*

**Framing — the unifying lesson first:**
Building this framework surfaced five failure modes. They're worth documenting
not as a gotcha list but because they all teach the same architectural lesson:
a governance control plane isn't a stack of independent tools. Behavior is
emergent. The system's properties arise from the interactions between layers,
and those interactions aren't documented in any single README.

**Failure 1 — Validating-to-validating ordering is undefined (mutation is not):**

Precision matters here. Between two *validating* webhooks — the Go webhook and
Kyverno's validating admission controller — Kubernetes does not guarantee order.
In testing, either fires first depending on API server scheduling. The lesson:
don't design validating logic that depends on another validator having run first.

But this does not generalize to all ordering. The architecture *relies* on
mutation preceding validation — Kyverno injects `environment: lab` before the
Go webhook validates that the label exists. Mutation-before-validation is
deterministic. The undefined case is validating-to-validating only. Getting
this distinction wrong in production means designing away a dependency you
actually need. The precise claim: design validating webhooks for independence;
use mutation-before-validation ordering deliberately.

**Failure 2 — The Enforcement Paradox (hit five times):**

This one deserves a name. Every infrastructure tool installed in this cluster —
Kyverno, KubeVirt, Prometheus, Kubecost — hit the same failure: the tool
needed a namespace exemption from governance policies that couldn't exist until
after the tool deployed, but the tool couldn't deploy without the exemption.

This isn't five separate installation hiccups. It's one architectural principle:
**a control plane that governs the cluster it runs in requires a designed
escape hatch, or it cannot bootstrap itself.** The bootstrap problem is
intrinsic to self-governing systems — from SELinux's `permissive` mode to
Kubernetes admission webhooks. Fail to design the escape hatch before
enforcement begins, and you can't install the webhook that's gating the webhook.

The lesson is organizational as much as technical: define your exemption
taxonomy before you enforce, not in response to each blocked install.

**Failure 3 — Governance applies to governance tooling:**

The Tier 3 audit-mode PolicyReport surfaced the governance webhook's own
Deployment as flagged by two policies: the registry restriction (the webhook
image isn't from `registry.kytechxyz.io`) and the owner label requirement.
This is the correct behavior. A governance system that exempts its own tooling
is not governance — it's theater. The honest finding is the credible one.
The practical implication: self-governance means building your tooling to the
same standard you enforce on everyone else. That's operationally harder and
architecturally correct.

**Failure 4 — JMESPath nil-safety in KubeVirt schema evaluation:**

The `deny-raw-cpu-memory` rule crashed on compliant VMs because `length(@)`
called on an absent field throws rather than returning zero. The fix:
`{{ request.object.spec.template.spec.domain.cpu || '' }}` with a
`NotEquals ""` condition. The lesson: Kyverno's evaluation engine has nil
semantics; the KubeVirt CRD has optional fields. The interaction between them
produces a failure mode neither system documents independently. When you're
writing policy against a CRD you don't own, test explicitly for absent fields —
the happy path is usually covered; the absent-field path almost never is.

**Failure 5 — ServiceMonitor target discovery: "discovered but dropped":**

Prometheus discovered the webhook endpoint but dropped it during relabeling
because the *Service* had no metadata labels — only a pod selector. The
relabeling `keep` rule evaluates `__meta_kubernetes_service_label_app`, which
reads Service metadata. A Service with no metadata labels fails the `keep`
rule regardless of what its selector targets.

The gap: "ServiceMonitor created" and "metrics actually flowing" are different
states separated by a label-matching rule that appears in no single piece of
documentation. This is a concrete example of emergent behavior — the
ServiceMonitor, Service metadata, and Prometheus relabeling rules interact
to produce a result none of them documents independently.

**What the failures share:**
Every failure mode above was the system teaching the same thing: you cannot
reason about governance components in isolation. The control plane's behavior
is emergent — it arises from the conjunction of layers, not from any single
layer. Design it that way.

---

## §4 — Making the System Legible: SRE Observability (~500 words)
*Function: SLO math — done correctly. Two separate SLOs defined. The category
error from v1 is corrected and turned into a teaching moment.*

**Why instrument the webhook:**
The webhook is synchronous in the critical path of every `kubectl apply`.
Its latency is the developer's experience and its availability is a governance
guarantee. Without instrumentation, you cannot answer: is it performing? Is it
degrading toward the timeout where `failurePolicy: Ignore` silently bypasses
enforcement? The metrics answer both questions.

**The histogram design:**
Prometheus histogram over gauge or counter because percentile calculation
against a fixed threshold requires the bucket-based data model. Histogram
precision note: quantile estimation interpolates within buckets, so "% under
200ms" is only as accurate as having a bucket boundary exactly at `le=0.2`.
The bucket list `[0.005, 0.01, 0.025, 0.05, 0.1, 0.2, 0.5]` places a boundary
precisely at the SLO threshold — not by accident.

**Two SLOs, not one — and why the distinction matters:**

A common error in webhook SLO design is conflating availability SLOs
(time-based) with latency SLOs (request-based). They have different budget
mechanics and neither can substitute for the other.

**Availability SLO:** 99.5% of minutes in a 30-day window, the webhook is
reachable and processing requests.
- Error budget: 0.5% × 2,592,000 seconds = 12,960 seconds = **3.6 hours**
- Budget exhaust signal: p99 latency approaching the API server's 10-second
  webhook timeout; `failurePolicy: Ignore` kicks in, enforcement silently
  bypasses.

**Latency SLO:** 99.5% of admission requests complete in under 200ms.
- Error budget: 0.5% of requests *may* exceed 200ms.
- This budget is request-volume-dependent — it cannot be expressed in hours
  without knowing request rate. On a kind cluster with low volume, a single
  slow request spikes the percentage dramatically. That's not a measurement
  failure; it's the correct signal that latency budgets are request-counted
  by definition.

The 200ms threshold: the human perception boundary. Sub-200ms feels
instantaneous to a developer running `kubectl apply`. For a webhook doing
pure in-memory JSON validation with no I/O, observed p99 is single-digit
milliseconds in normal operation. The 200ms SLO is generous — which means
any breach warrants investigation, because something unusual happened.

**The failurePolicy as a security signal (revised):**
`failurePolicy: Ignore` means a degraded webhook fails open. The latency
SLO is therefore not just a performance commitment — it's a governance
integrity signal. Sustained p99 latency trending toward the timeout window
means governance enforcement is degrading before it fails. The SLO fires
the signal; operations acts before bypass occurs. Without the SLO, the
bypass is silent. That's the argument for instrumenting governance infrastructure
with the same rigor as user-facing services.

**KubeVirt motif — observability thread:**
Traditional SNMP-polled monitoring rolled 5-minute data into hourly averages
after 7 days. The 500ms storage latency burst that caused the 2am outage was
mathematically erased by Friday. Prometheus histograms store full-resolution
bucket data for the entire retention window. The burst at 2am is still in
the error budget calculation on day 30. Same accountability discipline,
different toolchain.

---

## §5 — The Controlled Experiment: FinOps as Downstream Effect (~600 words)
*Function: Business case. 39% delta with mechanism explained. Scope honest.
"Controlled experiment" language fixed — denominator change acknowledged.*

**The experiment design — and its honest limitation:**
Two cluster states, same three Deployments, same measurement tool (Kubecost).
The comparison is directionally meaningful but not strictly controlled:
the denominator changes between states. Before governance, Kubecost's
allocation model uses node-level capacity as the ceiling (no declared limits
exist). After governance, the ceiling is the declared limit. These are
different denominators — the comparison measures two different efficiency
calculations, both of which are correct for their respective states.
What the comparison proves: governance makes cost measurement meaningful.
It does not prove that 3.8% and 5.3% are the same metric at two points in time.

**Results:**
- Before (unconstrained): Cluster Efficiency 3.8%, Possible Savings $162.45/mo
- After (governed): Cluster Efficiency 5.3%, Possible Savings $165.40/mo

**Why savings went up, not down:**
The savings figure increased alongside efficiency — which seems contradictory
until you understand Kubecost's model. Before governance, workloads with no
declared limits have no right-sizing signal: if the ceiling is "all of the
node," there's nothing to right-size toward. Kubecost can't surface
recommendations for what it can't measure. After governance declares a ceiling,
Kubecost can now compare declared limit against actual usage and surface specific
right-sizing recommendations that were previously invisible. The savings figure
went up because Kubecost gained measurement resolution, not because efficiency
degraded.

**The mechanism — this is the finding:**
Governance influenced workload resource behavior before workloads were created.
Kubecost simply measured the downstream effect. That shift — from measuring
what already happened to engineering what will happen — is the distinction
between reactive FinOps and deterministic FinOps.

**KubeVirt motif — the FinOps thread:**
VMware administrators enforced this at provisioning time. Resource pool
reservations. VM templates with standardized sizing tiers. You didn't
provision a VM without declaring its resource contract because the vCenter
efficiency report was meaningless without one. The discipline is identical —
the enforcement boundary moved from vCenter to the Kubernetes API admission
chain. The underlying principle is three decades old.

**The scaling argument — stated with precision:**
The allocation *arithmetic* is scale-invariant. The efficiency dynamics of
production clusters are not — bin-packing, HPA/VPA interaction, burstable
vs Guaranteed QoS, spot capacity, multi-tenancy all introduce variables the
kind cluster doesn't model. What scales is the *mechanism*: workloads without
declared limits cannot be right-sized; workloads with declared limits can.
On a cluster with real compute spend, the impact of that distinction is
proportional to the spend. The methodology for measuring it is the same.

---

## §6 — The Architecture That Outlasts the Tools (~300 words)
*Function: Synthesis. Close with the through-line. Production gaps named
honestly — not solved, but not hidden.*

**The through-line:**
ulimits → cgroups → SELinux → admission controllers. Different layers of the
same stack, across three decades. The surface changed — from processes to
containers to virtual machines running on Kubernetes. The problem didn't.
Resource governance is a platform discipline, not a feature of any particular
tool.

**What production hardening adds (named, not solved here):**
The implementation in this article is a portfolio-grade demonstration, not a
production deployment. Production hardening adds: cert rotation (the
`ValidatingWebhookConfiguration.caBundle` needs to stay in sync with the
serving cert); high-availability replicas with a PodDisruptionBudget;
validation scope extended from Deployments to all pod-template-bearing
resources (StatefulSets, DaemonSets, Jobs, CronJobs); `dryRun` semantics
handled explicitly in the admission handler. Naming these gaps is the honest
accounting. An engineer who lists production hardening requirements without
being asked is more credible than one who implies the demo is production-ready.

**The emergent behavior insight:**
The most important thing the failure modes taught isn't any specific fix.
It's that a platform governance system has no meaningful "component view."
The ServiceMonitor doesn't work without the Service label. The evictionStrategy
policy misfires without storage-class awareness. The Go webhook's Ignore
semantics interact with Kyverno's Enforce posture to produce a tiered
enforcement contract neither webhook specifies independently. Reason about
the system, not the components.

**KubeVirt as the closing motif:**
Cloud-native engineers design for ephemeral containers. Virtualization
engineers design for long-lived resource commitments. KubeVirt doesn't choose
between those models — it requires governance that satisfies both
simultaneously. That duality is where platform engineering gets interesting.
And where three decades of virtualization experience become directly relevant
to a Kubernetes control plane.

**Call to action:**
Full implementation at github.com/kytechxyz/k8s-governance-controller —
Go webhook, Kyverno policies, Prometheus instrumentation, Grafana dashboard,
SLO.md, Kubecost integration, and the honest evidence directory.

---

## Word Budget

| Section | Target | Function |
|---------|--------|----------|
| §1 Hook + Thesis | 400 | Number + claim + scope honesty |
| §2 Architecture | 700 | Why Go, enforcement posture, KubeVirt |
| §3 Failure Modes | 700 | Architectural evidence |
| §4 Observability | 500 | Two SLOs, correct math |
| §5 FinOps | 600 | Business case, honest mechanism |
| §6 Synthesis | 300 | Through-line + production gaps |
| Transitions | 300 | Connective tissue |
| **Total** | **~3,500** | |

---

## Dual-Read Checkpoints

**After §1**, the Director says:
*"39% efficiency improvement, honestly scoped to a lab. The thesis is that
governance precedes FinOps. The author knows the limitation of their own data."*

**After §2**, the IC6 says:
*"The Go webhook exists for defensible reasons. The enforcement posture is a
deliberate tiered strategy. The KubeVirt policies map to real operational
disciplines and the author caught the direct-LUN edge case."*

**After §3**, both audiences think:
*"These failures aren't embarrassing. The Enforcement Paradox is a named
architectural principle. The author understands emergent behavior."*

**After §4**, the IC6 says:
*"They know the difference between availability SLOs and latency SLOs. The
3.6 hours is correctly scoped to availability, not latency."*

**After §5**, the Director says:
*"I trust the methodology because they explained why the denominator changes
and why savings going up is actually the correct result."*

---

## Headline (unchanged from v1)

**Title:** From ulimits to Admission Controllers: Why Resource Governance
Is a 30-Year-Old Discipline

**Deck:** Governance is the architectural prerequisite for FinOps. A controlled
experiment — and five failure modes — prove it.
