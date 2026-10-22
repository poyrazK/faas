# ADR-1268: Pending retry parent evidence repair

## Status

Accepted, 2026-10-13.

## Context

Applied retries have a verified child-evidence repair path. A pending retry can
also lose the saved parent result that justified its immutable plan. Operators
need to repair that proof without admitting a child or following another item.

## Decision

Add read-only parent diagnosis and explicit repair beneath the existing retry
action evidence path: GET `/evidence/parent` and POST `/evidence/parent/repair`.
The CLI selects this path with `recovery-retry-evidence --parent`. Existing child
commands and endpoints retain their behavior. Diagnosis requires app read access
and MFA; repair requires deploy write and MFA.

Derive the parent job, item position, workflow/run and generation exclusively
from the retained automatic retry plan. Only pending actions without a child are
repairable. Match the parent's native queued item to the frozen target, verify
its selection and account/app ownership, and require retained admission history
for the exact parent generation. Support the exact item position rather than
assuming the first item; a missing item must not follow the next sibling.

Share the existing verified terminal observation classification. Live generation
count must equal the required parent generation; update and non-null finish times
must lie between its admission and the current observation. Missing history,
newer generations, changed lineage, nonterminal runs and existing conflicting
results remain blocked. Save the actual verified outcome, not an operator-supplied
failure. A changed terminal outcome may cause the worker to refuse the old retry.

Acquire the account recovery lock before policy/action authority and share-lock
the live run while selecting proof for PostgreSQL insertion. First-result-wins
ledger semantics protect normal capture racing with repair. Memory performs the
same operation under the store lock. Diagnosis is a consistent read-only snapshot.

Capture a separate immutable `parent_evidence_repairs` receipt per action on the
review, with request UUID, operator reason, actor, time and actual outcome. This
is independent of child `evidence_repairs`, so repairing a parent does not consume
the future child's repair receipt. Identical request UUID/reason retries return
the receipt, including after child submission; changed requests conflict. Ledger
insertion and receipt capture are atomic and preserve concurrent JSON changes.

Wake the normal worker for fresh eligibility, policy-version, budget, cooldown,
quota and unresolved-work checks. Repair creates no child, charges no execution
budget, releases no slot and does not resume or enable a policy. A changed policy
version does not gain authority from restored proof. Suppressed or already-applied
actions cannot perform a new parent repair.

## Consequences

Operators can repair verified evidence both before and after retry submission.
Unknown proof remains blocked; pruned history cannot be reconstructed or forced.
Diagnosis explicitly identifies parent evidence and item position without exposing
workflow input. No schema columns or migrations are needed. Two server route
registrations add two baseline lines; implementation remains in appevents/state.
