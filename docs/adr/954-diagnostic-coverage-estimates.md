# ADR-954: Advisory diagnostic coverage estimates

## Status
Accepted.

## Context
Coverage watch can take many bounded passes across a large fan-out. Operators need a sampling rate, an initial coverage forecast and an indication of pressure from the age policy, without changing what constitutes coverage or delivery settlement.

## Decision
Watch monitoring with diagnostics includes `diagnostic_estimate`. It reports relevant targets, remaining initial samples, unread recipients deferred by unavailable receipt evidence, diagnostic passes with attempted reads, successful sample count, actual samples per second and average samples per diagnostic pass. Count valid newly read responses, including unavailable/replaced answers and refreshed recipients, rather than delivery successes. Exclude retained samples and passes with no diagnostic read attempts. Count failed read attempts as diagnostic passes, so they reduce the observed average. Measure time using the current watch's monotonic clock; never trust saved estimates or timing aggregates on resume.

Expose states `insufficient_data`, `awaiting_receipts`, `estimated` and `covered`. Covered means initial sampling is complete and has zero remaining-pass/time estimates, even when samples are stale. When unread receipt-dependent targets remain, omit time/pass forecasts. With no successful samples or elapsed timing, omit forecasts. Actual sampling rate remains visible when measurable. The estimate's deferred count includes only unread targets, unlike diagnostic progress's deferred count, which can include valid retained samples.

Let S be successful samples, P diagnostic read passes, E elapsed watch milliseconds, D the next polling/retry delay and R total diagnostic read milliseconds. Observed throughput is 1000*S/E; samples/pass is S/P. Model a future pass cycle as (E+D)/P, accounting for measured request, callback, receipt and past retry time plus the next planned delay. Estimate remaining passes as ceil(remaining/(S/P)) and remaining milliseconds as ceil(remaining passes * modeled cycle). Estimate a full refresh span as ceil((ceil(targets/(S/P))-1)*modeled cycle + R/P), with zero for an empty target set. The refresh span includes gaps between passes and average diagnostic read duration; it does not add an unnecessary inter-pass delay to a single-batch refresh.

`refresh_exceeds_max_age` is advisory when that refresh span exceeds an enabled diagnostic age limit. CLI prints estimates alongside coverage changes and emits the age warning when it first becomes active, rather than on every polling tick. SDKs expose the same result. Checkpoints save the current estimate, while resume resets performance measurements and preserves coverage/cursors.

## Consequences
Forecasts assume future sampling resembles the current invocation and may be optimistic under correlated recipient failures, changing load or receipt availability. They are approximate milliseconds and pass counts, not commitments or an ETA to fresh coverage. An age warning does not prove that coverage cannot complete; timestamps and actual coverage/freshness predicates remain authoritative. No watch goal, timeout, retry budget, read cap, selection, delivery outcome or recovery action changes. No backend endpoint, SQL, migration, new quota or compliance registry changes are needed.
