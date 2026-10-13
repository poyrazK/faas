# ADR-1022: Observed recovery automation effectiveness report

Status: Accepted

## Context

ADR-1021 preserves policy, retry and health transitions. A timeline alone makes it
hard to see retry results and recurring blockers. Retention and prospective
capture prevent treating that evidence as a complete historical audit or a causal
measure of policy effectiveness.

## Decision

Add read-only `GET /v1/apps/{slug}/recovery-policy/report`, with read-scope and MFA
checks, account/app scoping, no-store responses, and the existing request timeout.
The API and `gregale automations report --app APP --since 7d` accept workflow,
policy version and inclusive time-window filters (seven days by default, at most
30 days). The CLI also accepts RFC3339 boundaries and global JSON export.

Use the existing SQLC history query in one read-only repeatable-read transaction
(or one MemStore lock), selecting at most 10001 entries to detect truncation at
10000. Aggregate after releasing the snapshot. This avoids observing different
versions of retained records while paginating. No new tables or background work.

Deduplicate applications and recorded results by action ID, preferring durable
proof over legacy retained records, then latest time and stable ID. Count attempts
and results independently by event time: results may belong to applications before
the window. Without-observed-result counts are window coverage facts, not proof
of a currently running child. Count failures, successes, cancellations and other
results separately. Group by workflow and policy version; app policy pauses use
an empty workflow. Workflow filtering therefore excludes app-wide pause records.

Report mean retry-application-to-result-recording seconds only for matching child
IDs and ordered timestamps, with sample count. This includes inspection delay and
is not failure-to-recovery time or guest execution duration. Never infer a success
percentage, savings, causation or hypothetical policy performance.

Blockers count distinct actions per observed worker reason, not polls or elapsed
time. Limit evidence samples to five records per blocker and five result/attempt
records overall. CLI evidence links target the scoped history endpoint; JSON
exports preserve source identifiers. Sort groups and blockers deterministically.

Always include retention, no-backfill, window and measurement caveats. Mark scan
truncation explicitly and identify small result samples. A nontruncated report
still cannot establish full historical coverage: retained sources can expire,
ledger limits may evict older events, and capture began prospectively.

## Consequences

Operators can inspect actual recovery behavior without causing retries, changing
policy, capturing notifications or mutating history. Reading the larger bounded
snapshot has a higher cost than a history page; the existing timeout bounds it.
Historical policy backtesting remains a separate future design.
