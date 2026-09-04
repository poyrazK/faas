# ADR-140 · Public-release backend hardening foundation

- **Status:** proposed
- **Date:** 2026-09-03
- **Decision:** Ship a focused backend hardening slice before adding more public
  platform surface area: admission-only warm placement hints, bounded freshness
  for builder residency data, and cancellation-safe gateway stream teardown.
- **Why:** Public operation depends on failure paths being conservative and
  deterministic. A failed admission must not influence later placement; an
  unreachable schedd must not leave builderd trusting an old low-residency
  value; and a disconnected streaming client must not leave request-body or
  bridge goroutines alive until a server timeout.
- **Consequences:** Warm affinity is written and broadcast only after the
  instance row and ledger admission succeed. A residency value older than 30
  seconds denies the opportunistic builder slot while preserving the
  guaranteed slot. Gateway HTTP and raw-stream bodies are tied to the derived
  stream context, closable bodies are actively closed on cancellation, and all
  receiver exits wait for the body copier to finish. No schema, wire, or
  deployment changes are required.
- **Rejected alternatives:** Retaining indefinitely stale capacity data keeps
  the fast path but is unsafe during a schedd outage. Recording warm affinity
  before admission is lower latency but can steer bursts to a node that never
  accepted the instance. Relying only on `io.Reader` cancellation leaves
  real network bodies blocked because `Read` has no context contract.

## Verification

- Unit regression tests cover stale residency fail-closed behavior, admission
  rejection without a warm hint, and closing a blocked request body.
- Focused gateway, scheduler, and builder tests run under the race detector;
  the full portable Go suite remains the merge gate.

## Follow-up release gates

This ADR is a foundation, not the complete public-release checklist. The next
gates remain durable event replay/outbox semantics, M9 two-node and leak-drill
acceptance, service-replica convergence, real OTLP metrics export, and the
remaining account-export/state-layer scale work.
