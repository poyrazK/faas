# ADR-801: Consumer routing circuit breakers

- **Status:** accepted
- **Date:** 2026-10-08
- **Decision:** Offer opt-in, durable circuit breakers for individual application event consumers.
- **Why:** Repeated routing failures need isolation and controlled recovery without blocking healthy consumers or consuming every recipient's retry budget during an outage.

## Policy and state

Operational configuration lives separately from acceptance-time recipient retry
snapshots. Defaults are a 50% failure threshold over 300 seconds, at least 20
outcomes, a 60-second cooldown, three successful probes, and 60 seconds of
recovery capped at ten routing permits per second. Bounds are enforced by the
API and database. Enable, replacement, and manual reset start a fresh window.

States are closed, open, half_open, and draining. Closed circuits count
successful invocation admissions and routing failures, including retryable
pending failures, from retained publication and backfill routing history.
Filtered outcomes, operator replay markers, capacity waits, subscription
control waits, and invocation execution results do not count. Window scans
are limited to once per consumer per second. Compacted windows do not trigger
threshold-based opening or complete recovery; status exposes incomplete history.

Opening stops new routing and persists a cooldown. Work available after the
cooldown advances the circuit to half_open and reserves one probe with its
routing claim token and lease. A successful admission counts toward recovery.
A routing failure reopens the circuit; an expired probe without a durable
outcome reopens it with reason probe_timeout. Neutral outcomes release the
probe without counting success or failure. Probes are at least one second
apart. There is no synthetic event generation or automatic replay of terminal
failures.

After enough successful probes, draining starts at one permit per second and
doubles every ten seconds up to the configured cap. A recovery failure reopens
immediately. After the configured recovery interval and a complete window,
normal routing resumes. Permits are reservations before routing and can be
unused when filtering or capacity blocks admission. Existing in-flight work
may finish. Recovery rate controls routing, not invocation execution concurrency.

## Transactions and overrides

Configuration, permit reservations, admission fences, and outcome transitions
share the existing consumer advisory lock. Scheduler gates precede routing
preparation so lookup failures also participate. Admission rechecks the circuit
under the consumer lock. History and outcome transitions commit atomically;
root progress lets a later worker reconcile probe outcomes after restarts.
Breaker waits restore the claim's attempt count and preserve the retry duration
ledger. Sibling consumers have independent state, and deterministic invocation
identity and ordering remain in force.

Manual pause and pacing are independent controls. Breaker recovery cannot
resume a manual pause. Reset closes the enabled breaker and clears its samples;
disable removes automatic gating. Neither action removes a manual pause or
pacing. Controls remain addressable for retained removed subscriptions.
Automatic open is actionable for consumer health alerts; manual pause retains
its existing suppression policy.

GET, PUT, DELETE, and POST reset use the existing app ownership, read/deploy
scopes, MFA, bounded request size, timeout, and audit boundaries. Configuration
and live state are excluded from environment cloning. This first implementation
uses runtime API/CLI configuration, independent of deployment manifests.

Apply the append-only migration and upgrade all routing workers before enabling
breakers. Workflow and object notification recipients, and legacy receipts
without captured application recipients, keep their existing routing contracts.
