# ADR-456: Saved canary route health decisions and explanations

- Status: Accepted
- Date: 2026-10-02
- Related: ADR-122, ADR-453, ADR-454, ADR-455

## Context

Critical-route error and latency guards expose live evidence. Their windows move,
configuration changes establish a fresh anchor, and completed stages cannot be
reconstructed from a compact traffic audit. Customers need the exact evidence
that allowed or held a release for later investigation. Existing route finding
history retains configured policy evaluations rather than runtime health decisions.

## Decision

Retain immutable snapshots of actual health evaluations during APID's manual and
worker canary advances. Save the candidate/stable identities and available commit
SHAs, stage and revision, observation anchor, requested traffic, prior traffic,
source, decision, independent error/latency verdicts and full weighted observations.
Capture a versioned evaluation policy with sample minima, window length, ingestion
allowance, percentile quantile, numerical tolerance and all comparison thresholds.
No request payloads, raw URLs, credentials or arbitrary actor text are retained.

Use a dedicated route_health_history table with cascading ownership references.
All SQL is generated through sqlc. The app/deployment traffic lock also serializes
history insertion and pruning. A health hold commits only its evidence, without a
traffic change or traffic audit. Successful advance evidence commits in the same
transaction as the traffic change. A later lease, audit or traffic failure rolls
back the tentative allowed snapshot. History persistence failure leaves traffic
unchanged. The existing configured-policy gate still precedes the health guard;
attempts blocked before a health evaluation create no health snapshot.

Deduplicate exact evidence, source and requested transition, excluding only the
retry's checked-at wall clock and generated IDs. Window bounds, anchor, identities,
revision, counts, percentiles and verdicts stay in the key. Identical retries reuse
the first snapshot and its ID. Changed observations or windows create new entries.
Default configurations without selected routes create no history. Reads are pure
and never synthesize decisions or query current telemetry to fill old evidence.

Retain the newest 100 decisions and at most 4 MiB of encoded evidence per deployment,
with at most 64 KiB per entry. Bound each history page to 10 entries, default 5.
Prune with insertion while holding the parent lock. Bounds live in pkg/api/limits.go
and structural byte ceilings are enforced in the migration. Cursor pagination
requires a retained, account/app/deployment-owned decision. Pruned cursors return
not found. Evidence remains readable after plan downgrade and for completed or
superseded deployments while the app and deployment exist.

Expose app-read/MFA-protected list and single-entry API endpoints with Go, Node
and Python SDK support. Add an optional history_id to canary decision metadata,
advance audit metadata and blocked error hints. The CLI's routes health explain
command renders a saved decision, thresholds, route/window evidence and a bounded
timeline. It also exports JSON. A restored-health label requires a blocked-to-
allowed healthy transition with the same stage, revision, stable identity and
observation anchor. Context changes are labeled separately and never imply recovery.
Historical evidence cannot authorize new traffic changes or serve as current CI
health; live report and atomic enforcement remain authoritative.

## Consequences

Customers can investigate a held or completed release after its observation windows
move and share durable evidence with support. Repeated identical worker holds do
not create duplicate records. Retention is bounded and older snapshots/cursors can
be pruned. The timeline records evaluated advance attempts, not a continuous incident
monitor; it cannot establish an exact incident start or an unobserved recovery.
State and API tests cover immutable evidence, ownership, deduplication, retention,
pagination, error/latency holds, successful recovery and rollback on transition failure.
