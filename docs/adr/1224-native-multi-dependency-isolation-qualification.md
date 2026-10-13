# 1016: Qualify multi-dependency application isolation

Date: 2026-10-13

Status: Accepted

## Context

Single-provider recovery does not establish that one dependency outage leaves
unrelated application functionality usable. Shared permits, retry budgets or
circuit state can spread failure to an otherwise healthy provider.

## Decision

Add a sealed opt-in application fixture with two routes in one process and one
HTTP listener. Each route reuses the combined resilience implementation with
independent policy state. Deploy two independently controlled verified-TLS
PostgreSQL providers. Stall one, exercise its bounded six-caller overload, and
require six reads against the other to complete while the first remains active.
Swap the fault and repeat without resetting policies.

Require healthy reads and health probes below 750 ms, exact cumulative attempts
and policy counters, permit grant/release equality, bounded retries, fail-fast
open circuits, single-probe recovery and independent backend retirement. Bind
proofs to distinct stable application, policy and provider identities. Inspect
both hot-provider receipts and retire all three developer sessions and VM IDs.

Expand native acceptance to 26 cases and verdict version 17 on the designated
x86_64 KVM fleet. Real PostgreSQL and portable HTTP tests validate the driver,
verifier, cancellation and composition without claiming native qualification.

## Consequences

This adds two providers to one acceptance case and increases bounded gate time.
The fixed fixture introduces no production policy defaults or generic reset
controls. The database remains shared, while connection pools, proxy fault state
and caller policies are separate. This tests per-provider client isolation, not
independent database servers or cross-instance policy coordination.
