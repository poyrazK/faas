# ADR 1060: Dry-run replay jobs and reviewed recovery

Status: Accepted; runtime, migration and UI validation deferred.

## Context

Operators need to review recovery eligibility before mutating the dead-letter queue, then replay the failures they reviewed rather than a fresh filter that might select different failures.

## Decision

Add `POST .../groups/{group}/replay-jobs/dry-run` with the existing replay filter/rate request. Capture up to 128 immutable failure identities and check them gradually without expiring leases, replaying messages, changing attempts/fences, consuming claim credit or affecting processing metrics/history. Only job/report rows and safe processing diagnostics change. Shared inspection checks existence, dead status, exact dead_since, frozen deadline and current valid same-key leases. An expired lease alone does not block eligibility; the checker does not maintain the queue. Actual replay still expires leases/deadlines before checking and mutating.

A dry run uses checking/check_paused states and a dry_run mode flag. The separate API and native dashboard routes return unavailable on older binaries, while old background selectors ignore checking jobs. A database mode/state constraint prevents older controls from turning a dry run into a running replay. Upgrade all API replicas before exposing the feature: older readers lack the new SDK fields/states and quota accounting. Both modes share four active jobs per group, the configured per-job upper rate limit, suspension behavior for disabled endpoints/inactive owners and bounded terminal history. Creation remains non-idempotent; inspect recent jobs after an uncertain response.

Job responses distinguish eligible, replayed and skipped counts and include per-item observation times, dry_run, reviewed_job_id and started_job_id. Existing retained outcomes may omit checked_at. Eligibility is evaluated at each item's check time, not a global snapshot or a reservation. Worker claim pause/circuit/rate gates are not replay eligibility checks and still apply when workers claim ready messages. Expected skips do not count as processing errors. Dry runs emit no replay lifecycle notifications, avoiding completion notices that imply queue recovery.

The `start` control action requires a completed dry run and at least one eligible outcome. Under the group/report locks it creates one real replay at the report's rate, copying only eligible identities from the frozen selection, and saves a started_job_id on the report in the same transaction. The real job rechecks each identity, deadline and ordering condition before mutation; later failure incarnations are never replayed through an old report. Do not add newly matching failures or previously skipped items. Start retries return the same real job while retained, even if it is now terminal. If the linked replay was pruned, return conflict rather than create another; a pruned report returns not found. Fresh review is required for a new recovery decision.

Promotion preserves its source report during terminal-history pruning and keeps history references as nullable opaque UUIDs without foreign-key retention or deletion cascades. Subsequent history pruning may remove either linked record independently. Live replays keep working after the report disappears. Group deletion removes both through ordinary group ownership. Inspector forms expose dry-run creation, eligibility/skip reasons and starting eligible selections, with the existing CSRF, ownership, MFA and deploy-write checks. Read endpoints and both SDKs expose report fields and start controls.

## Consequences

Migration `20261012102700538_realtime_replay_job_dry_runs.sql` is additive after notifications and remains unapplied. It expands state vocabulary, adds mode/history references, and gates the notification trigger to real jobs. Downgrade refuses active dry runs; cancel them first. It discards terminal dry-run reports, restores the original notification trigger and preserves live replay progress. Runtime tests, applying migrations and rendering the inspector remain deferred at the user's request. Builds and static schema checks do not verify Postgres execution or race behavior.
