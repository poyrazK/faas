# ADR 1220: Native application circuit-breaker qualification

Date: 2026-10-13
Status: Accepted

## Context

Shared retry-budget qualification limits amplification, but does not establish
whether an application stops dependency pressure after repeated failures or safely
coordinates recovery probes. Production platform reliability policies remain
separate from the customer application fixture.

## Decision

Add a twenty-second native case with a circuit breaker shared within one dedicated
read caller process. Two consecutive failures open it; a monotonic three-second
cooldown permits one half-open probe. Each dependency attempt has a 1.5-second
deadline. Open and probe-busy callers fail fast with zero dependency attempts.
Cooldown expiry is lazy and requires no background timer.

Use the existing verified loopback TLS SSLRequest fault. Require an initial trip,
six rejected open callers, one failed half-open probe alongside four rejected
callers, and a canceled probe that releases its slot/timer and reopens conservatively.
After clearing the fault, require a successful probe to close the circuit and
restore two normal reads. Guard settlement by generation so late closed-state
results cannot reset newer open state.

Bind state/generation transitions, single-probe observations, stable caller/provider
identities, exact independent attempt/TLS/fault counts, joined requests and cleared
deadline timers into a run-bound proof. Independently verify backend retirement
after phases and cleanup. Private controls stop the fixture but cannot manipulate
breaker state. No production reliability policy, schema or ledger mutation changes.

Raise the gate to 22 cases and verdict version 13. Local real PostgreSQL tests
execute exact sources and proof verification; a portable regression preserves open
state after late success. Designated native x86_64 KVM qualification remains required.

## Consequences

The gate covers application fail-fast behavior, bounded recovery probes, reopening,
cancellation and healthy recovery. Its scope is one application instance rather
than platform-wide or cross-process circuit coordination.
