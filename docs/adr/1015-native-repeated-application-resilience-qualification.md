# 1015: Qualify repeated application outage and recovery

Date: 2026-10-13

Status: Accepted

## Context

One outage does not establish that retry windows renew correctly or that resources
remain bounded after repeated recovery within one application lifetime.

## Decision

Add three outage/recovery cycles using the existing sealed combined application
fixture and actual PostgreSQL SSLRequest stalls. Keep the real 30-second retry
window and prove a failed request before expiry cannot refill it. Each subsequent
window admits two retries. Require stable identities, exact cumulative policy and
permit counters, zero live resources and independent database retirement after
each phase. Bound health latency during all three outages.

Extend the designated native KVM acceptance gate to 25 cases and verdict version
16. Portable real-PostgreSQL coverage validates the driver and proof verifier but
does not qualify native VM execution. No production policy defaults change.

## Consequences

Acceptance takes roughly one additional minute because expiry uses elapsed time.
Proofs expose monotonic retry-window age and remaining time without credentials
or reset controls. Failures preserve evidence and run independent cleanup.
