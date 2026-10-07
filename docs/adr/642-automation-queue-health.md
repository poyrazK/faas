# ADR-642: Automation queue health

- **Status:** accepted
- **Date:** 2026-10-07
- **Extends:** ADR-641 and the automation health API
- **Decision:** Add an optional `queue` snapshot to automation health. Historical
  counts retain their existing inclusive creation window. Queue diagnostics use
  the current ledger, independently of that window, and expose an observation
  timestamp, waiting/due/stale counts, oldest due age, app dispatch occupancy and
  app/tenant limits, plus fixed aggregate waiting-reason counts.
- **Reasons:** Give each pending, parked or stale run exactly one primary reason:
  `scheduled`, `retry_backoff`, `parked_wait`, `app_capacity`, `tenant_capacity`,
  `workflow_capacity`, or `ready`. Future deadlines take precedence; then apply
  app, tenant and captured definition concurrency checks in that order. A future
  pending run is in retry backoff only if a pending step's retry deadline equals
  the persisted run wake. Other future wakes are scheduled. Due parked waits
  and expired running leases enter dispatch admission checks. Live running
  claims are excluded from waiting counts. Native Customer Operations custody
  remains outside public automation queue diagnostics.
- **Capacity:** App occupancy counts live automation claims across all workflows
  and tenants in the owned app. Expired leases do not consume dispatch capacity.
  Definition concurrency includes parked waits and started pending retries,
  subtracting a candidate's own occupancy exactly as dispatch admission does.
  Memory admission and diagnostics share their capacity calculation. PostgreSQL
  diagnostics apply the same lease and candidate predicates as fair dispatch.
- **Consistency:** Read PostgreSQL health in one read-only repeatable-read
  transaction with a five-second deadline and one database observation time.
  Memory health holds the store mutex and captures one current timestamp. Queue
  reason counts sum to waiting count; due count includes blocked due runs and
  lease recovery. Age starts at eligibility (wake or expired lease), bounded
  below by creation time, and is zero if nothing is due.
- **Privacy and bounds:** Return aggregates only for the authorized app and
  selected automation. Never return customer payloads, error strings, tenant
  identifiers, or other accounts' dispatcher occupancy. There are seven fixed
  reason keys, independent of backlog size. No new persistent state is required.
- **Limits:** `ready` means the observed dispatch admission checks pass. It does
  not promise immediate handler execution or prove global worker saturation.
  Fair turns, runtime gates, action budgets and invocation capacity still apply.
  `parked_wait` groups intentional timers, conditions, events and callbacks;
  inspect run steps for details. This snapshot provides neither queue position
  nor a completion-time estimate and can change immediately after observation.
- **Compatibility:** Preserve existing active/queued counts and response fields.
  New APIs return the optional queue object; clients tolerate older responses
  without it. CLI displays current diagnostics and reports their absence on
  older servers. OpenAPI and generated Go-facing DTO, Node and Python models
  carry the additive shape. Apply ADR-641's worker rollout before relying on
  the reported dispatch budgets as enforcement guarantees.

This is a read-only control-plane feature; it introduces no VM lifecycle changes.
