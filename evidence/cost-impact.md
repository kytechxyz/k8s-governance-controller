# Cost Impact Analysis: Governance-Driven Resource Efficiency

## Executive Summary

This analysis quantifies the FinOps impact of enforcing resource governance at
admission time. Using Kubecost against a controlled before/after experiment, the
governance framework improved measured cluster efficiency from **3.8% to 5.3%** —
a **39% relative improvement** — by forcing every workload to declare explicit
CPU and memory bounds before admission.

The headline finding is not a single dollar figure. It is that **unconstrained
workloads make cost measurement itself impossible**, and that admission-time
governance is the precondition for any meaningful FinOps practice.

## Experimental Method

A controlled comparison was run on the governance cluster:

**Baseline ("before"):** Three Deployments (5 pods total) were admitted with the
governance webhook disabled and no resource limits declared. This reproduces the
common real-world state: developers ship `image: nginx` with no `resources` block.

**Governed ("after"):** The same three workloads were redeployed with the webhook
re-enabled, forcing each to declare CPU/memory requests and limits and pass
cost-center labeling before admission.

Kubecost observed both states and produced allocation and efficiency metrics for
each.

## Results

| Metric                             | Before (Unconstrained) | After (Governed) | Change                   |
| ---------------------------------- | ---------------------- | ---------------- | ------------------------ |
| Cluster Efficiency                 | 3.8%                   | 5.3%             | +1.5 pts (+39% relative) |
| Default NS Efficiency              | not measurable\*       | 1.5%             | newly measurable         |
| Projected Monthly Savings Surfaced | $162.45                | $165.40          | +$2.95                   |

\* Before governance, the `default` namespace efficiency could not be meaningfully
calculated — see the analysis below.

## The Core Insight: Why Unconstrained Workloads Break FinOps

This is the finding that matters, and it is counterintuitive.

When a container declares no resource limits, Kubernetes does not assume it needs
nothing — it allows it to consume whatever the node offers. Kubecost, in turn, has
no declared ceiling to measure against, so its allocation model falls back to
node-level capacity. The result is an efficiency calculation with a meaningless
denominator: a huge allocation figure against tiny actual usage, producing the
3.8% "efficiency" number that tells you nothing actionable.

Once governance forces a declared limit, the allocation denominator becomes the
_declared ceiling_ rather than the _entire node_. The efficiency ratio becomes a
real, actionable signal: it now answers "how much of what this workload reserved
is it actually using?" — the question right-sizing depends on.

**Governance is therefore not a competing concern to FinOps. It is its
precondition.** You cannot right-size what was never sized in the first place.

## The Traditional Infrastructure Parallel

This maps directly to capacity management on enterprise virtualization estates.
In a VMware or RHV environment, a VM provisioned with no reservation or limit
floats against host capacity; the hypervisor's utilization metrics become noise
because there is no declared baseline to measure efficiency against. The
discipline of mandating resource reservations at provisioning time — through
vCenter resource pools and templates — is exactly what this admission webhook
enforces for cloud-native workloads. The tool changed from vCenter to a Go
admission controller; the principle of "no workload enters without declared
bounds" did not.

## What This Demonstrates — and What It Does Not

**Demonstrated:**

- A working FinOps measurement pipeline (Kubecost integrated with the live
  governance cluster)
- A real, reproducible efficiency improvement attributable directly to admission
  enforcement
- The methodology to quantify idle spend and surface right-sizing opportunities

**Not claimed:**

- A specific large dollar saving. This experiment ran on a local kind cluster
  where absolute costs are negligible. The efficiency _delta_ and the
  _measurement methodology_ are the transferable results; the absolute figures
  are lab-scale by design.

## Scaling the Methodology

On a production cluster, the same pipeline produces the actionable number that
matters. The procedure is identical:

1. Measure baseline cluster efficiency with Kubecost.
2. Enforce admission-time resource governance.
3. Re-measure; the efficiency delta multiplied by the cluster's actual monthly
   compute spend is the reclaimable figure.

A cluster running at 3.8% efficiency on, for example, $40,000/month of compute is
carrying the vast majority of that spend as idle, allocated-but-unused capacity.
Driving efficiency upward through enforced right-sizing converts directly to
reclaimed budget — and unlike a one-time cleanup, admission enforcement prevents
the waste from re-accumulating, because non-compliant workloads never enter the
cluster in the first place.

