# ADR-913: Bounded receipt retention holds for bulk recovery

Date: 2026-10-09
Status: Accepted

## Context

ADR-912 exposes receipts approaching their pruning boundary and warns recovery
operators. Bulk recovery previously did not hold selected receipts, so a paced
or paused job could lose receipts before admission. Existing backfill holds do
not guarantee protection for a different recovery job.

## Decision

Add optional boolean `protect_receipts` to the existing recovery request for
routing and execution modes. Default false preserves existing jobs. Store the
opt-in in immutable selection JSON, with a boolean shape constraint and an
index on pending recovery items by outbox and job, plus an account/deadline
index for settled receipt pruning. Reuse existing job lifetime,
active-job quota and recipient cap; introduce no new retention policy or quota.
Preview is read-only and never holds receipts. Create the job and its selected
items atomically under the existing account range advisory transaction lock.
Creation includes only receipts still retained at selection, not preview results.

A receipt is held when at least one account-owned opted-in job has a pending item
for that outbox, is running or paused, and has `expires_at` strictly after the
observation instant. Admission, skipping or cancellation releases that item's
hold. Terminal state or expiry releases the job's holds, regardless of whether
expiry cleanup has run. Multiple recipients and overlapping jobs do not duplicate
storage charges. Pausing or changing rate never extends the existing 24-hour
expiry. No retrofit or renewal API is added.

Use the shared retention predicate for pruning, reporting and preflight. Keep
running and retryable backfill holds ahead of `recovery_pending` in primary
reason precedence, so report counts partition held settled receipts. Add
`recovery_holds` to retention health. Preflight includes current recovery holds
and exposes this active job's `receipt_protection_until`. Memory storage mirrors
recovery holds under its mutex; it still has no backfill job store.

Pruning must acquire account locks before its deletion snapshot. The former
single statement selected and checked receipts before trying the account lock,
which could use a stale snapshot after concurrent creation committed. Derive an
advisory account list from a bounded batch of oldest unheld receipts, then for
each account open a READ COMMITTED
transaction, try its account range lock in a separate statement, and delete a
bounded batch using the next statement's fresh snapshot. Skip contended account
locks and receipt row locks, recheck all predicates, and release locks at commit.
Creation and backfill retries cannot introduce a hold between that recheck and
commit. Preserve nominal settlement-plus-30-day pruning and backfill boundaries.
Use the pruner's existing cutoff plus identity retention as its observation clock;
reports and preflight pass their explicit observation time. Retain a four-argument
SQL wrapper for existing readers, with current transaction time.

Expose Go, Node and Python SDK parity and CLI `--protect-receipts` on preview and
creation. Preview documentation explicitly says it reserves nothing. Existing
status selection and expiry show the policy. No delivery or recovery operation
is triggered during implementation.

## Consequences

A committed protected selection survives receipt pruning until each pending item
is processed or the bounded job expires. Held customer receipts remain fully
charged against existing account storage limits and may block new publications.
No hold restores already pruned data, extends delivery age, preserves execution
records, guarantees replay eligibility, or promises exactly-once business effects.
Already admitted execution attempts follow their existing retention independently.

Apply the migration and upgrade every scheduler/pruning worker before exposing
the option through upgraded API binaries. Old pruning workers retain the old
snapshot order and must not coexist with opted-in job creation. Complete or
cancel protected active jobs and downgrade binaries before migration rollback;
Down rejects any such active jobs and restores the previous hold predicate.
