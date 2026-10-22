# ADR-956: Persistent sampled routing blocker detection

## Status
Accepted.

## Context
Selected consumer summaries expose blockers but do not distinguish a persistent observed blocker from changing or insufficient routing evidence.

## Decision
Add an opt-in watch stall threshold, bounded by centralized diagnostic duration bounds, requiring diagnostics and a positive diagnostic age limit. Track one compact observation and monotonic timing span per selected recipient within the current invocation. Ignore saved tracker aggregates. Only newly sampled, validated, fresh available/not-waiting evidence can start or extend an episode. Retained evidence cannot extend timing or sample count. Require strictly increasing observation timestamps, at least two samples and an unchanged compact routing observation spanning the threshold to label an eligible blocker stalled.

Eligible reasons are subscription pause/rate limits, circuit controls, ordering, capacity and retry backoff. Ready, workflow/receipt processing and routing in progress are not blocker signals. Compare routing state, waiting reason, capacity scope, target availability and exact ordering identity/state, normalizing subscription case and equivalent timestamp encodings. A change restarts the episode; stale/future, unavailable, replaced, invalid, non-monotonic evidence and gaps exceeding the age policy break confirmation. Freshness is rechecked when emitting monitoring summaries. Record first/last server observation timestamps alongside the local monotonic sample span; do not accrue time from polling retained samples.

Emit per-subscription `consumer_stalls` with counts of insufficient evidence, not blocked, observing and stalled recipients. Additional overlapping indicators report changed routing and forward transitions (pending to processing; available to enqueued) in the latest confirmed pair. Include exact guarded blocker identities and sampling spans. CLI alerts once per stalled episode and re-arms after loss/change of confirmation. SDKs expose equivalent watch options/results. No automatic recovery, stop condition or delivery mutation follows an alert.

## Consequences
This is evidence of persistent sampled routing conditions, not proof of uninterrupted blockage, original blocker duration, account-wide consumer health or handler completion. A diagnostic coverage watch can terminate before any stall is observable. Memory remains proportional to the already bounded selection; there are no additional reads, backend scans, SQL, migrations or compliance registry changes.
