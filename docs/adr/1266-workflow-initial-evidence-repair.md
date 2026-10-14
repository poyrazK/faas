# ADR-1266: Verified initial recovery evidence repair

## Status

Accepted, 2026-10-13.

## Context

Initial automatic recovery deliberately holds unknown outcomes. A retained live
terminal generation can remain blocked when its item-owned result is missing.
Operators need diagnosis and a safe way to restore that proof.

## Decision

Provide read-only `GET .../initial-recovery-reviews/{reviewID}/evidence` and
`POST .../initial-recovery-reviews/{reviewID}/evidence/repair`. Diagnosis requires
app read scope and MFA; repair requires deploy write and MFA. Both use existing
account/app scoping and retained-review rules. Only submitted reviews with frozen
`automatic_initial` authority are repairable, including unresolved older versions.
The diagnosis contains lineage IDs and execution evidence without workflow input.

Repair is bookkeeping, not admission. It requires the retained queued native item,
matching workflow/account/app/run, expected generation zero, live generation one,
retained generation-one admission after job creation, terminal status and a
non-null finish time. Run update and finish times must fall between admission and
the current observation. Missing, future, running, superseded or mismatched proof
cannot be repaired. An existing result is never overwritten, including conflicting
or unusable results.

PostgreSQL repairs acquire the account recovery lock before policy authority and
share-lock the live run while selecting proof for insertion. The result ledger's
existing unique key preserves first-result-wins behavior if normal capture races.
Memory performs equivalent checks and capture under the existing store lock.
Diagnosis uses a consistent read-only snapshot and never captures results.

Require a canonical nonzero request UUID and an operator reason. Persist a receipt
with request identity, reason, actor, outcome and time on the existing review JSON,
plus a durable `evidence_repaired` audit transition. Identical request ID/reason
replays return the retained receipt; changed identity/reason conflicts. The
receipt and ledger insertion are atomic. A pre-existing result without this
repair receipt is diagnosed as already recorded and is not rewritten.

Advance the existing worker's next eligible observation without changing policy
pause, enablement, budgets or failure counters. The repair action does not write
`automatic_result` or release a slot. The normal worker consumes the saved proof,
records its outcome, applies current-policy failure accounting and clears a
previously observed block through the existing notification lifecycle. Disabled
or paused policies require normal operator enable/resume before worker progress.

## Consequences

Verified terminal evidence can recover an interrupted result ledger without
synthetic success, new execution or budget refunds. Unknown evidence remains
blocked. Repair cannot reconstruct pruned jobs or admission history, infer an old
generation from a newer run, override existing results or force-clear a slot.
Native completion notifications retain their existing meaning. Two new route
registrations add two lines to the server size baseline; all implementation is
in the appevents domain and state layer.
