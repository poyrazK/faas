# ADR-946: Freshness-aware recovery monitoring

Date: 2026-10-11
Status: Accepted; production qualification pending
Related: ADR-939, ADR-940, ADR-945

## Context

Resume preserves earlier observations while completing bounded coverage.
A long-running or restarted watch can therefore combine old terminal evidence
with newer rows. Some automation needs a maximum age for all current evidence.

## Decision

Add optional CLI --max-observation-age, Go CommitRecoveryWatchOptions with
WatchCommitRecoveryStatusWithOptions, Node maxObservationAgeMs and Python
max_observation_age. Zero/default disables enforcement. Positive ages range
from 100 milliseconds to 24 hours; the CLI option requires watch. Existing Go
watch and retry methods delegate with freshness disabled. Node/Python status
read helpers also accept the age option for bounded selective rereads.

Classify current rows at the client's wall clock: fresh, stale,
missing_timestamp, future_timestamp or unobserved. Not_observed and read_error
are unobserved even if a partial read recorded a timestamp. A successful row
without a timestamp is missing; a future timestamp is never fresh. Otherwise
age greater than the configured bound is stale; equality is fresh. Malformed
saved timestamps retain existing validation errors rather than being trusted.

After validating saved selection and source ownership, read unread items,
then failed reads, then successful observations requiring freshness repair.
Prioritize missing/future timestamps, followed by stale observations oldest
first, keeping output in its original order. Every reread consumes the existing
per-pass read budget. Clear old current evidence before reading; preserve the
original attempt, selection, expected generation and existing comparison
baseline. Do not mutate caller inputs or inherit a stale success after failure.

Only settle when the existing acknowledged-terminal predicate and freshness
of every current row both hold. If coverage is complete but some evidence is
stale, resume selective rereads rather than restarting at the first row.
Once coverage and freshness are satisfied, ordinary nonterminal monitoring
refreshes again. Reevaluate age after callbacks so callback time cannot make
old evidence satisfy a subsequent settlement decision.

When enabled, monitoring includes max_observation_age_ms and freshness counts
for all five categories, including zeros. Acknowledged_executions_terminal
also requires freshness. Complete, remaining and status counts retain their
coverage/delivery meanings; fresh evidence can still be unresolved or failed.
Plain status helpers return observations without a monitoring summary. Saved
freshness counts never govern current scheduling or settlement. Restarted
watches use their explicitly supplied policy, not the previous checkpoint's
recorded age bound.

## Consequences

Automation can enforce evidence age while retaining bounded reads, retries,
checkpoint ownership and unknown-outcome semantics. Large selections or slow
reads may not fit the requested age window; they continue until the overall
timeout without claiming fresh completion. Wall-clock skew is visible through
future timestamps; age and summary counts describe observation-time samples,
not an atomic snapshot or permanent freshness guarantee. Monotonic timing
still controls elapsed time and the watch timeout. No replay is submitted.
Production skew, retention and multi-pass qualification remains pending.
No local tests are added or run; compliance registries are unchanged.
