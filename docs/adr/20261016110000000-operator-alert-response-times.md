# Operator alert response timing

Status: Accepted, 2026-10-16. Extends retained episodes, triage history and [ADR-1747 workload summary](1747-operator-alert-workload-summary.md).

## Decision

GET `/v1/admin/obs/incidents/platform_alert/response-times` reports retained
platform alert episodes activated within the inclusive `[since, as_of]` cohort.
The default window is 24 hours; maximum is 30 days. Since must precede as_of.
Older active episodes are outside this activation cohort; use the [workload
summary](1747-operator-alert-workload-summary.md) for current attention. Recurrences
remain distinct immutable episode IDs.
Collection and retention health accompany every response, with explicit retained
history limitations. They do not establish complete first-ever incident history.
Totals are null before a successful collection. A successful quiet cohort has
known zero counts and null percentiles, never zero-duration placeholder samples.

First acknowledgment is the lowest-revision valid acknowledge action, including
a first acknowledgment recorded after recovery. Repeated acknowledge actions,
unacknowledgment and later workflow changes cannot replace that first receipt.
To prove it is first, every revision 1 through the saved episode revision must
have exactly one valid closed receipt. Missing revisions make acknowledgment
unknown even when a later plausible acknowledgment survives. Duplicates,
out-of-range revisions or malformed receipt fields make it unknown with invalid
receipt coverage. Missing-revisions counts refer to absent valid numeric revision
metadata and do not establish the reason for absence. Invalid receipt coverage
takes precedence over gaps; the disjoint episode categories partition the cohort.

Complete receipts must have nondecreasing timestamps in revision order, and each
timestamp must fall between activation and as_of. A backwards clock, a receipt
before activation or a future receipt excludes that episode from acknowledgment
samples and increments invalid_clock. No duration is clamped into a fast sample.
A complete history with no acknowledge action increments not_observed, including
resolved episodes that were never acknowledged. Snooze does not stop the clock.

Observed resolution is independent: a retained resolved_at between activation
and as_of supplies the sample even when audit receipts have gaps. No resolved_at
increments not_observed; out-of-range resolution timestamps increment
invalid_clock. Unresolved counts include both firing and unknown observations;
unknown_observation reports those that cannot currently be confirmed. Persisted
resolution is confirmed rule recovery, not actual repair time or an SLA.

Each metric's samples, not_observed, unknown and invalid_clock partition the
cohort. Percentiles are discrete nearest-rank p50/p95 over whole-second valid
samples. Zero seconds can be a real sample. No samples gives null percentiles.
Global totals and nonempty severity/component groups have their own denominators
and sample counts, ordered lexicographically by severity then component.

## Bounded reads and privacy

Read collection metadata, retention health, cohort and receipt projections in
one read-only repeatable-read PostgreSQL transaction, or one memory-store mutex.
The request has a five-second budget, at most 1000 episodes and 10000 matching
receipts. Fetch one extra row to detect overflow and reject the entire read with
400 plus limit metadata and advice to narrow since. Never sample silently or
return partial statistics. At most 1000 groups and bounded duration arrays are
held by the domain aggregator; PostgreSQL never returns fleet account/app rows.
An activation-time index supports the cohort range and ordering. The existing
partial audit episode/revision index supports exact episode receipt matching.

Decode only UUID, action, previous revision, workflow booleans, snooze and as-of
clocks inside the store. Owner IDs, actors, emails, trace payloads and raw audit
data never appear in timing DTOs. The API retains operator scope, allowlist and
MFA checks and accepts only one optional since parameter. Database failures are
503 with generic messages. Reads create no new journal or lifecycle writer.

`gregalectl obs response-times --since RFC3339 --json` reads this contract.
Without since the server chooses its default using one clock. Human output keeps
unknown totals and absent samples explicit. HTTP bodies are bounded and upstream
error bodies are redacted using the existing typed operator read helper.
Canonical OpenAPI, both private-route exclusion maps and the generated Node
admin client stay synchronized. Frontend and rollout are separate work; no PR
or deployment is implied by this local implementation.

## Validation

Exercise memory and PostgreSQL first-revision selection, zero durations, clock
regressions, cohort boundaries, duplicate receipts, gaps and malformed receipts,
recurrence isolation, cancellation, read limits and concurrent triage snapshots.
Reopen PostgreSQL and remove an earlier receipt to prevent optimistic first-ack
claims. Verify independent recovery timing, percentile ranks and denominators,
unknown versus quiet cohorts, identity exclusion, API authorization and query
validation, and CLI parameter/coverage/error behavior. Check migration apply/down
and replay, SQLC drift, OpenAPI/Node parity, policy gates and configured lint.
