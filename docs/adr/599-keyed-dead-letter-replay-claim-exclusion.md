# ADR-599 · Keyed dead-letter replay respects running claims

- **Status:** accepted; local qualification passed
- **Date:** 2026-10-05
- **Decision:** Give a running invocation or broker receipt precedence over
  pending sequence order in the shared keyed claim gate. Serialize in-place
  dead-letter replay with claims through the existing work-lane lock.
- **Why:** In-place replay restores the original sequence. If newer work for
  that key is already running, selecting only the lowest sequence can admit
  the replay concurrently and violate ADR-374's one-running-claim contract.
- **Consequences:** A replay keeps its identity, sequence, policy snapshot,
  payload, expiry, receipt and failure audit. It waits for an existing owner
  to complete or recover its lease. Pending work resumes in sequence order
  after ownership is resolved. API routes and replay budgets stay unchanged.
- **Rejected alternatives:** A fairness cap cannot replace per-key exclusion:
  it is optional and may permit several distinct work keys. Reassigning a
  sequence would change the existing in-place replay ordering contract.

## Claim and recovery behavior

The PostgreSQL gate locks `(app_id, policy_name, key_digest)`, expires pending
work whose original lifetime elapsed, then selects a running owner before any
pending row. It considers both `invocations.dispatching` and
`trigger_records.claimed`, regardless of their sequence relative to a replay.
Without a running owner, the lowest nonterminal sequence wins; a pending retry
still blocks later pending work even while its due time is in the future.

An expired invocation lease remains owned until the existing reaper returns it
to pending. An expired broker owner can reclaim its own receipt and advance
its generation before an older replay proceeds. Stale broker completions and
queue delivery envelopes retain their existing generation fences. This fence
governs Gregale's ledger; application side effects still need idempotency or
an external fencing mechanism.

Generic invocation due scans and named-queue candidate scans omit replay rows
while another same-key claim is running, including higher-sequence owners.
Final admission still checks the lane under its lock. Other keys remain
eligible, subject to their captured fairness and dispatcher limits.

## Replay locking

Direct queue replay, queue-receipt replay, broker-receipt replay, and app/account
dead-letter replay acquire the source's work-lane lock before resetting the
source execution. Queue receipt replay derives the lane from its invocation;
the receipt itself need not carry a work policy.

App/account replay first selects a bounded set of candidate IDs without ledger
locks. It acquires the affected lanes in `(app_id, policy_name, key_digest)`
order, then the keyed invocation rows and broker/queue receipt rows, each in ID
order. Only then does it lock and recheck the selected failure-ledger rows.
Failure capture takes execution-row locks before upserting that ledger; the
same order prevents a stale projection from creating a replay/failure deadlock.

Bulk replay retains `SKIP LOCKED` on the final ledger page and resets only
still-eligible candidates. Concurrent actions can remove candidates, so the
result may contain fewer than the requested limit; the caller can run another
bounded replay for remaining events. Separate ledger pages can share several
lanes; consistent ordering prevents opposite lane acquisition. Jobs, workflows,
webhooks and unkeyed work do not introduce work-lane locks or keyed source-row
locks.

The [ADR-598](598-safe-keyed-invocation-replay.md) child-based recovery route
continues to assign a new tail sequence to failed unbound keyed work. This
decision covers existing in-place dead-letter recovery, which retains its
original sequence. Queue binding ownership, scoped replay checks, retry
budget reset, replay generations and retained failure history stay intact.

## Qualification

A PostgreSQL reproducer failed for all four combinations of replayed and
running invocation/broker work: the older replay was admitted beside a newer
running same-key execution. The fixed gate passes those cases without an
optional fairness cap masking the per-key check.

Local qualification passed with PostgreSQL 16.15 and Go 1.25.13. All 45 replay
cases cover direct/app/account/bulk recovery, named queue polling, queue receipt
lane derivation, disjoint bulk pages sharing two lanes in opposite event order,
source-before-ledger locking against a failure writer, expiry without renewal,
lease recovery, stale generation rejection, concurrent scheduler claims,
pending sequence order and unaffected other keys.

The full portable state suite, existing keyed/fairness/queue replay PostgreSQL
regressions and conformance cases, focused API recovery tests, and scheduler
recovery regressions passed. Generated SQL matches sqlc 1.31.1 output; scoped
golangci-lint 2.4.0, the ADR number gate and diff checks passed. Full Linux CI
and staging delivery qualification remain release gates; this change does not
qualify native guest execution or enable recipient claims in production.
