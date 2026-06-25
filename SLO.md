# Service Level Objective: Governance Admission Webhook

## Overview

This document defines the formal reliability commitment for the Kubernetes
governance admission webhook. The webhook sits in the synchronous admission
path of every Deployment and Namespace operation — meaning every `kubectl apply`
blocks until the webhook responds. Its latency is therefore a direct component
of cluster-wide API responsiveness, and warrants a formal SLO.

## Service Level Indicator (SLI)

**The percentage of admission requests processed in under 200ms.**

Measured from the Prometheus histogram `governance_admission_duration_seconds`,
which records the wall-clock duration of each call to the validation handler,
labeled by `resource_type` (deployment/namespace) and `result` (allowed/denied).

The 200ms threshold is the human-perception boundary: requests completing under
200ms feel instantaneous to a developer running `kubectl apply`. For a webhook
performing pure in-memory JSON validation with no I/O, 200ms is a generous
ceiling — observed p99 latency in normal operation is single-digit milliseconds.

## Service Level Objective (SLO)

**99.5% of admission requests processed in under 200ms, over a rolling 30-day window.**

### PromQL

```promql
sum(rate(governance_admission_duration_seconds_bucket{le="0.2"}[30d]))
/
sum(rate(governance_admission_duration_seconds_count[30d]))
```

This returns the fraction of requests that fell into the ≤200ms bucket over the
window. The SLO is met when this value is ≥ 0.995.

## Error Budget

An SLO of 99.5% implies an error budget of 0.5% — the proportion of requests
permitted to exceed 200ms without breaching the objective.

| Quantity                  | Value          |
| ------------------------- | -------------- |
| Window                    | 30 days        |
| Total seconds in window   | 2,592,000      |
| Error budget (0.5%)       | 12,960 seconds |
| **Error budget in hours** | **3.6 hours**  |

Over any rolling 30-day window, the webhook may serve slow (>200ms) responses
for a cumulative 3.6 hours before the error budget is exhausted.

## Error Budget Policy

- **Budget remaining:** feature work proceeds normally. New policies, new
  validators, and image changes ship as planned.
- **Budget exhausted:** a deployment freeze takes effect on the webhook itself.
  No new features are added until reliability work brings latency back within
  objective and the rolling window recovers headroom.

This mirrors the traditional change-freeze discipline applied during incident
recovery — but quantified against a measured budget rather than triggered by
subjective judgment.

## Failure Mode Context

The webhook is registered with `failurePolicy: Ignore`. If the webhook becomes
unreachable or exceeds the API server's webhook timeout (default 10s), the API
server admits the request without governance evaluation rather than blocking
cluster operations. This is a deliberate availability-over-enforcement tradeoff:
a degraded webhook must not become a cluster-wide outage. Sustained latency
approaching the timeout is therefore not only an SLO concern but a silent
governance-bypass risk, making this SLO a security signal as well as a
performance one.

## Traditional Infrastructure Parallel

This SLO formalizes the same discipline applied to enterprise storage and
compute SLAs — but corrects their central flaw. Legacy RDBMS-backed monitoring
rolled high-frequency data into hourly averages after a few days, mathematically
erasing the sub-second latency micro-bursts that caused real incidents. The
Prometheus histogram backing this SLO retains full-resolution bucket data across
the entire 30-day window, so a 500ms spike at 2am remains visible — and
accountable against the error budget — for the full retention period.
