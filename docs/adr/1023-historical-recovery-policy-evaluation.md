# ADR-1023: Evidence-limited historical recovery policy evaluation

Status: Accepted

## Context

Change preview (ADR-1017) examines current retained targets. Outcome reporting
(ADR-1022) summarizes actual recorded behavior. Operators also need to evaluate
proposed settings against past candidate evidence without executing retries.
Historical reviews preserve decisions, policy configuration and generation, but
not individual run status, all unseen failures or complete execution context.

## Decision

Add read-only `POST /v1/apps/{slug}/recovery-policy/evaluate`, with read-scope/MFA
checks, account/app isolation, no-store responses and the existing request/body
limits. Require a valid proposed policy and the current `expected_version`.
Default to seven days, accept a maximum 30-day inclusive window, reject future
bounds, and optionally restrict the historical review sample by workflow.

The CLI is `gregale automations recovery-policy-evaluate --app APP --file
POLICY.json --since 7d`. The policy file has the existing `expected_version` and
`policy` shape. Global `--json` exports the comparison and its coverage notes.
Explicit RFC3339 start/end flags are supported; `--since` cannot accompany a start.

Read the current configuration, up to 101 oldest matching retained reviews and
up to 10001 oldest recorded retry applications in one repeatable-read, read-only
PostgreSQL transaction (or one MemStore lock). Use SQLC queries. Keep at most
100 reviews, 500 decisions and 10000 application records, with separate truncation
flags. Application evidence is app-wide even when candidates are workflow-filtered.
Its warmup reaches back the longer current/proposed cooldown or one hour, subject
to the ledger's 30-day / 10000-event retention. Aggregate a detached snapshot.

Compare current configuration settings and proposed settings against the same
captured decisions. Enabled, workflow, trigger and generation constraints can
establish exclusions. For saved-failure retries, the historical policy's status
filter is a possible status set. A proposed superset proves that filter matches;
a disjoint filter proves exclusion; a narrower overlapping filter is unknown.
An unrestricted historical filter does not establish any individual status.
Never substitute current live run status for historical proof. Decisions that
previously passed preflight (prepared, already_prepared, generation_limit,
cooldown or hourly_budget) support the remaining eligibility checks. Other
preflight outcomes and non-saved-failure triggers remain unknown where necessary.

Replay preparation chronologically by review time and ID, then decision position.
Both configurations start with empty budget, cooldown and deduplication state.
Unknown candidates are not charged. Apply app-wide rolling hourly preparation
budget, cooldown between review batches, and deduplication by canonical run ID,
generation and action. Preparation is conditional under these assumptions, not a
prediction of the actual scheduler's ordering, historical admission or outcomes.
The current configuration's operational pause/failure state is not replayed.

Separately count unique recorded applications strictly before each review time,
excluding the hourly lower boundary. Their count can prove lower-bound execution
budget pressure; the latest known application can prove cooldown pressure. Equal
review/application timestamps do not establish ordering. Without observed pressure,
return insufficient_history, never execution eligibility. Bound evidence identifiers
to five per candidate, including older last-application cooldown proof if needed.
Do not replay hypothetical executions or failure pauses, infer quota availability,
predict success or capture notifications.

## Consequences

The comparison is useful for adjusting generation, preparation budgets, cooldowns
and filters, and for identifying observed execution pressure. It is explicitly
limited to historical candidates that the old policy actually reviewed. Pruning,
prospective capture, sample truncation and unobserved initial state prevent claiming
full policy backtesting or counterfactual effectiveness. No new persistence schema
or background worker is needed.
