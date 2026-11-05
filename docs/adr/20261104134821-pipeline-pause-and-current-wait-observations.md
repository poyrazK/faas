# ADR-20261104134821: Pipeline pause and current wait observations

- **Status:** implemented for local/staging qualification; production disabled
- **Date:** 2026-11-04
- **Amends:** ADR-1591, ADR-20261103220000, ADR-20261104115303

## Context

Reader-policy changes require an exclusive table lock. When a read transaction
held that lock open, an approved reader change failed before reconciliation
disabled the native subscription. A subsequent pause request therefore left
replication running until the reader policy could be updated. With unavailable
source credentials, disable already happened first, but the same policy error
prevented acknowledgement of the committed pause.

CLI resume increments the requested revision while retaining the last observed
state. Its wait loop accepted a previous blocked observation as a current failure
and exited before asking the worker for the resumed revision's status.

## Decision

For an approved reader update on a paused pipeline, disable all owned
subscriptions first, under the existing per-pipeline target lock and
current-intent fence.
After a reader-policy failure, recheck the fence and continue observing the
paused pipeline. Preserve the approved reader request and applied reader list;
the normal worker retries the policy update, including while paused or without
source credentials. Pause acknowledgement confirms suspension independently of
reader-change acknowledgement. Only a committed policy receipt can advance the
reader request to applied. Subscription ownership failures still prevent pause
acknowledgement, and a lost lease or changed intent prevents completion.

For CLI waits, reject an actionable blocked observation only when it is fresh.
An obsolete revision or expired observation requires polling until current
evidence or the command deadline. A fresh actionable failure still ends the
wait; a transient database outage remains retryable. Durable resync-required and
deletion receipts keep their terminal behavior.

## Validation and rollout

Native TLS PostgreSQL regressions hold a read transaction across an approved
reader update and pause. They check stale-worker fencing, subscription disable,
worker exit, a current paused observation, retained unapplied readers and source
writes held back until resume. After the lock clears, the reader update applies
while paused and the original generation catches up on resume. The same coverage
runs without a decryptable source credential.

An HTTP CLI regression returns the preceding authentication failure from the
resume response and then a current live observation. Wait regressions cover
expired observations, current actionable failures, transient database outages,
durable resync receipts and deadlines. Full pipeline acceptance with race
detection, affected-package lint, API build and repository checks remain required.

No catalog migration or VM lifecycle change is introduced. Apply the preceding
ownership migrations before deploying the combined changes. Production and
provider qualification gates remain unchanged.
