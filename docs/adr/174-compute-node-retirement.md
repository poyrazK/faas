# ADR-174 — Terminal compute-node retirement

- **Status:** accepted
- **Date:** 2026-09-11
- **Decision:** Add a terminal `retired` compute-node lifecycle reached only by a durable schedd-owned operator intent after maintenance and a zero-live-instance check.
- **Why:** `unavailable` means temporarily unhealthy and is intentionally eligible for automatic heartbeat recovery. Reusing it for decommissioning can silently return removed capacity to placement.

## Context

Operators can drain and reactivate nodes, but the old default DELETE behavior
only set `active=false`, which maps to `unavailable`. The recovery controller
may later move that row through `recovering` to `active`. Hard deletion also
discarded the node identity before recording an operator reason or proving the
row was unused.

## Decision

`retired` is a non-admitting, non-recoverable terminal lifecycle. The supported
workflow is:

1. drain the node to `maintenance`;
2. submit `node_retire` with recent MFA step-up, `--yes`, and an explicit reason;
3. schedd re-reads the node and verifies zero live instances before applying
   `maintenance → retired`;
4. retain the row and workload history for audit.

vmmd self-registration preserves an existing retired lifecycle. Operator
enrollment and legacy active toggles refuse to reactivate it. The legacy DELETE
route submits the same retirement intent. `?hard=1` is cleanup only for a
non-default retired row with no app or instance references; the state-layer
delete repeats that check atomically.

## Consequences

- Retirement is outside the customer deployment and wake paths.
- Request and terminal outcome remain trace-linked in the existing operator
  intent and audit streams.
- Reusing a retired name is refused; replacement hardware receives a new node
  identity.
- Restoring a retired node requires a reviewed database repair, not the routine
  activation endpoint.

## Rejected alternatives

- **Reuse `unavailable`:** conflicts with automatic recovery semantics.
- **Hard-delete every decommissioned row:** destroys useful identity and audit
  history and is fragile around foreign keys.
- **Reuse `maintenance`:** leaves no distinction between a reversible upgrade
  hold and permanent decommissioning.
