# FaasRuntimeCollectorCapacityRefusals

## Symptom

One apid has recorded an estimated three or more capacity refusals per five-minute
window for five consecutive minutes. Each refusal returns HTTP 429
`obs_runtime_busy` before reading inventory or Prometheus.

## Check

1. Open **Gregale Runtime Diagnostics** for the alert's **apid instance**.
   The warning's **Runtime Diagnostics** link opens that view. Compare
   **Capacity refusals by apid (5m)** and **Active collections by apid**.
   Brief saturation can occur between scrapes.
2. Check for overlapping deployment verifiers and collectors using that apid.
   Stagger their polls and honor `Retry-After` when retrying direct API reads.
   For a focused check, select just the diagnostic groups you need.
3. If refusals coincide with slow collections, follow the
   [deadline runbook](FaasRuntimeCollectorDeadlines.md) to check the source
   bottleneck.

## Verify

Confirm the refusal counter levels off while expected verifier polls resume
returning evidence. The warning clears when the five-minute increase falls
below the threshold. See [runtime collector overload](../ops/post-deployment-monitoring.md#runtime-collector-overload).
