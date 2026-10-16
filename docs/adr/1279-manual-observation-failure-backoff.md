# ADR-1279: Bounded backoff for manual observation data failures

Date: 2026-10-13

## Context

Separate manual and automatic passes prevent cross-pass starvation. A deterministic
error in the oldest manual review can still repeatedly end its own pass before
healthy reviews are observed.

## Decision

Run PostgreSQL observation writes inside a savepoint after acquiring the existing
account, policy and review locks. Recognized review data errors roll back all
observation effects, then atomically record `observation_failure` in the retained
review JSON and advance its existing next-observation timestamp. A committed
failure deferral counts as processed work within the existing batch cap.

Recognize invalid recovery UUIDs, JSON syntax/type errors and PostgreSQL check
violations or invalid text/date values (23514, 22P02, 22007, 22008). Connection,
cancellation, deadline, serialization, locking, resource and unknown errors stop
the pass. Failure persistence or transaction commit errors also stop the pass;
they cannot claim successful deferral. No raw database messages are persisted.
Validate claimed review identity before accessing execution evidence.

Memory observation validates the same identities before side effects. Both stores
use centralized exponential backoff from one minute to 30 minutes. Attempts
saturate at 32. Successful observation clears failure metadata even if execution
remains pending. Inspection exposes code, attempts, failure time and retry time;
it never refreshes polling state. Existing evidence repair can wake observation
through its existing due-clock update. No additional execution or repair authority,
job, automatic budget charge or final result is granted by deferral.

## Consequences

Healthy manual reviews can progress after a faulty review is deferred. PostgreSQL
rollback preserves atomic attention, history and outbox behavior. State uses the
existing review JSON and due-clock column; no schema migration is needed. The
metadata describes monitoring failure, independently of evidence blockers.

Automatic policy execution errors retain their existing pass-wide behavior. Its
policy authority and execution-budget transactions need a separate design before
introducing policy-local failure backoff. Malformed review JSON may still prevent
typed review inspection; durable metadata remains in the database for diagnosis.
