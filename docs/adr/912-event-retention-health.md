# ADR-912: Event retention health and expiry warnings

Date: 2026-10-09
Status: Accepted

## Context

Individual receipts expose their nominal retention boundary. Operators cannot see an account's approaching pruning deadlines, receipts pinned by backfill, or storage pressure together. Bulk recovery jobs have their own expiry but do not pin their selected receipts.

## Decision

Add read-only GET `/v1/events/retention`, requiring the existing account read scopes and MFA. Default expiry lookahead is 24 hours; permit whole-second durations from 1 second through 30 days. Optional exact source and owned app filters affect receipt counts and samples. App attribution uses captured or backfilled recipients; legacy receipts without that attribution remain visible account-wide. Aggregate every match and return at most 100 samples ordered by nominal retention boundary, source and id, with explicit truncation. Do not return event payloads or work keys. PostgreSQL uses a repeatable-read read-only transaction with a 15-second caller budget. Memory storage keeps only the earliest limit-plus-one samples while aggregating under its mutex; it has no backfill job store or backfill pins.

The nominal boundary remains routing settlement plus the existing 30-day receipt retention. Unsettled receipts have no pruning deadline. Count settled receipts missing settlement timestamps as unknown; expiry alerts degrade instead of treating unknown deadlines as healthy zero. Hold counts refer to settled receipts. Distinguish currently eligible receipts (boundary strictly before observation and no hold), upcoming unheld expiry (boundary from observation through the lookahead, inclusive), held receipts, and overdue held receipts. A receipt can have multiple backfill pins; report one primary reason, preferring a running backfill over retained retryable failures, so hold counts partition held receipts.

Extract the existing pruning hold predicate into the stable SQL function `event_receipt_retention_hold` and use it in both pruning and observations. Running backfills pin their account acceptance ranges, including receipts not yet scanned. Retained `completed_with_failures` jobs pin their retryable failed items until the existing job retention boundary. Preserve all current time boundaries, range semantics and pruning locks. No new pins, deletion policy or automatic recovery are introduced. Eligibility is an observation, not an immediate deletion promise; pruning batches and locks can defer deletion.

Always include unfiltered account customer-storage usage and plan limits. Report separate count and byte utilization and their maximum, without clamping values above 100 after a plan downgrade. Filtered receipt bytes can differ from account storage; platform receipts with zero customer charge remain inspectable.

Add webhook-only `event_retention_expiring_receipts` and `event_storage_utilization_pct` alert metrics. Existing alert resources require an owned app and no subscription selector. The first metric counts currently unheld app receipts already eligible or expiring within the rule's 5m/15m/1h/6h/24h lookahead. The storage metric observes the entire account and ignores the window for aggregation. Both are current observations, not historical totals. Preserve cooldown and recovery notifications. Read failures degrade observations rather than returning healthy zero, and these metrics cannot trigger deployment actions.

Extend recovery preflight with pending-item receipt warning and current hold counts, the earliest currently unheld nominal boundary, and whether the rate-only optimistic drain reaches or crosses it. Warn about unheld pending items due at or before the later of observation plus 24 hours and optimistic drain. Sampled items include their nominal boundary and current hold when known. Missing receipts keep their existing `receipt_expired` classification. Holds can change; a false crossing flag is not a retention guarantee, and no read extends retention, changes recovery state or reserves work.

Expose Go, Node and Python SDKs, `events retention`, and retention warnings in `events recovery-preflight`. Apply the append-only migration before upgrading binaries; remove new alert rules and downgrade binaries before rolling back the migration.

## Consequences

Operators can distinguish recovery holds from receipts ready for pruning and decide whether to advance recovery, wait for pruning or upgrade storage capacity. Existing delivery, ordering, deduplication and retention policies remain authoritative. Neither alerts nor reads archive events or recover data already pruned.
