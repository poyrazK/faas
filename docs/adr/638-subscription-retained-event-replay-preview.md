# ADR-638: Subscription-scoped retained-event replay preview

- **Status:** accepted
- **Date:** 2026-10-06
- **Decision:** Expose a read-only, bounded historical preview for one current
  ordinary application subscription using retained fanout envelopes.
- **Why:** Failure repair restores originally captured recipients. A newly
  introduced consumer needs to discover older matching events without changing
  original receipts or accidentally invoking handlers during exploration.

## Contract

`GET /v1/apps/{slug}/event-subscriptions/{subscriptionID}/replay-preview` uses
the existing account authentication, MFA and read scopes. Both current app and
subscription ownership are checked inside the read transaction, independently
of the cursor. Foreign, deleted or transferred targets return 404. Disabled
subscriptions and subscriptions with work bindings return explicit 409 codes.
Workflow starts and object notification declarations are separate target
surfaces and are not accepted here.

Required `from` and `until` select a half-open range of **platform acceptance
time**, not producer CloudEvents `time`. `cutoff_at` is the lesser of `until`
and the first page's observation time. Ascending `(created_at, outbox_id)`
pagination retains that cutoff even when the upper bound was in the future.
Page size defaults to 50 and caps at 100 examined envelopes, including pattern
and content-filter mismatches. The query reads at most one extra envelope to
detect continuation. A page with no matches can still have `next_after`.
Counts are page-local; no account-wide payload scan or unbounded matching total
is implied. Iterate all pages to inspect the currently surviving candidates.

Versioned opaque cursors bind the account, app, subscription, current declaration
revision, requested range, cutoff and last examined immutable tuple. Limits can
change. The revision includes source/type, canonical filter, identity and
creation/update timestamps. Changing a declaration, including disable/re-enable,
requires restarting the preview. Cursor fields are untrusted, validated inputs,
not signed audit evidence. SQL predicates enforce tenant ownership separately.

The authoritative routing matcher evaluates the current target against each
retained envelope. Legacy spelling/default normalization uses the original
acceptance time for missing envelope time, rather than the current wall clock.
Stored envelopes and original recipient snapshots are never rewritten. Current
schema registration is not rerun over historically accepted envelopes. Invalid
stored envelopes or filters fail the read rather than silently becoming an empty
result. Metadata responses contain identity, schema version, acceptance time,
receipt link and original membership (`captured`, `not_captured`, or `unknown`
for a legacy null snapshot). Captured membership is neither evidence of handler
completion nor an execution eligibility decision. Original siblings, receipts,
attempts, invocations and deduplication identities are untouched.

## Retention and consistency

The existing fanout outbox is the retained source; the shorter-lived customer
event listing and ledger are not a second archive. Settled receipt retention is
30 days **after routing settlement**, with unresolved work protected by existing
rules. `earliest_retained_at` is account-wide and independent of range/target;
it does not establish gap-free coverage. `history_complete=false` states that
this view cannot prove a complete historical archive. No archive retention
promise, execution plan, temporary retention pin or replay job is introduced.

One read-only repeatable-read transaction covers target, earliest surviving row
and bounded candidates for each PostgreSQL response. MemStore mirrors the read
under its mutex with bounded candidate storage. A five-second context deadline
applies in the API and both stores. Deadline exhaustion returns 503
`event_replay_preview_read_timeout`, never an empty successful preview. Responses
use `Cache-Control: no-store` and never expose event payloads.

This is a live retained view across requests. Pruning can remove an unread row
or the last cursor row; continuation still uses the tuple. Delayed commits of
older acceptances can change visible membership, including behind a consumed
cursor. Restart to see them. A fixed acceptance cutoff is not an MVCC snapshot
spanning requests. Oldest-first inspection does not establish delivery FIFO.

## Storage and rollout

An additive `(account_id, created_at, id)` outbox index supports range reads,
tuple continuation and the account's earliest retained row. All production SQL
is generated through sqlc. Apply the migration before exposing the API; building
the index on a large retained outbox may need a maintenance window. Old writers
continue operating unchanged. No routing flag, adoption, retry, lease, retention,
admission or VM lifecycle behavior changes. The Go client method is
`pkg/api.Client.PreviewEventReplay`; CLI entry is `gregale events replay-preview`.

Qualification covers envelopes predating target creation, acceptance versus
producer time, matching parity, original captured membership, empty pages with
continuation, cutoff stability during publication, pruned cursor rows, legacy
unknown membership, account/app isolation, declaration changes, disabled and
work-bound targets, read cancellation, malformed inputs, metadata-only output
and unchanged delivery state. Durable replay execution remains a later decision:
it requires bounded resumable jobs, admission/backpressure, duplicate policy and
replay lineage rather than mutation of historical acceptance snapshots.
