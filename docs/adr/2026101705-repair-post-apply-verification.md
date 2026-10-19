# ADR-2026101705: Read-only verification of committed repairs

- Status: accepted for internal operator qualification
- Date: 2026-10-17
- Extends: [selective repair](20261016-selective-data-repair.md), [linked replacement](2026101702-repair-replacement.md), [resolution](2026101703-repair-apply-resolution.md) and [retention](2026101704-repair-superseded-retention.md)
- Scope: PostgreSQL source observation, standalone/hosted APIs, CLI and both review screens. No control-plane schema, Operation state or VM lifecycle change.

## Decision

Add an explicit `verify` action for an exact plan ID/digest with a committed
receipt. Pre-apply validation deliberately skips applied plans; verification
instead observes the recorded correction after commit. Keep the historical
restored/excluded counts and apply timestamp separate from current unchanged,
edited and missing counts. Later valid work does not rewrite a successful commit
into a failed repair. An observation is not a semantic correctness guarantee.

The source verifies the full or retained plan digest, then binds the committed
repair UUID to a finalized journal operation in the same scope, with matching
source, enrollment and schema fingerprint. A missing receipt is a conflict,
never proof of rollback. Before reading business rows, require the retained
journal row count to match the committed candidate count and the existing row
bound. Use the repair journal, independent of the original plan items, so plan
compaction does not disable verification while this journal is retained.

Use one read-only repeatable-read transaction with the existing timeout. Read
only the original repaired row identities and the enrolled fields recorded by
that repair. A matching version and repaired values count as unchanged. A later
version, including unrelated-field edits and edits changed back, counts as
edited. A same-version value mismatch also counts as edited with its distinct
reason. A missing immutable row UUID counts as missing, even if another row now
uses its primary key. Original exclusions remain historical counts; do not read
them or synthesize a new candidate set.

## Availability and authority

Journal purge yields a committed report with `available=false`, issue
`journal_purged`, and empty observation rows/counters. A schema, enrollment or
RLS change similarly yields `schema_changed` without row inspection. Neither
turns unavailable evidence into healthy zeroes or discards the durable receipt.
Transport/permission errors remain errors. A corrupt receipt, missing journal
link or truncated unpurged journal fails closed. Changed plan expiry or current
check policy cannot alter the historical result. No business checks execute.

The report contains source/plan/repair IDs, original digest and commit timestamp,
restored/excluded counts, observation timestamp and availability, conserved
current counters and edited/missing row details. Rows contain only immutable
identity, exact captured key JSON, expected post-repair/observed versions and a
fixed reason. Reuse the existing 1,000-row, 16 KiB row-key, 256 KiB aggregate-key
and response bounds from `pkg/api/limits.go`. Omit oversized keys explicitly.
Current business values, SQL, credentials and database diagnostics stay out of
reports. Scope, receipt identity, counts and row/reason consistency are checked
at source, remote and hosted boundaries.

Standalone and hosted POST endpoints accept only the digest. Hosted access uses
PostgreSQL read/admin permission, MFA, same-origin checks and exact binding
identity. Add a separate executor capability interface so older executors return
unavailable without changing their existing contract. Verification does not
advance an Operation, settle an unknown outcome, emit events, reset approval or
expiry, create evidence, or authorize another apply. Resolution and
reconciliation keep their existing positive-proof protocol.

The CLI prints available observations with exit zero even when later edits or
missing rows exist; unavailable journal/schema reports remain JSON with exit
one. Both review screens show historical receipt and timed observations
separately, including after plan compaction. They clear observations on reload
or identity changes and render exact keys as text. No browser storage or
background verification is introduced.

Fresh schema and the repeatable explicit owner upgrade install the same stable
read-only function with a fixed catalog search path. This extends the existing
application-owner SQL exception: arbitrary enrolled customer tables cannot be
represented as control-plane SQLC tables. No constructor or request installs
DDL, and PUBLIC execution stays revoked. Qualified reader roles need the
function and existing underlying read/fingerprint permissions; no new mutation
authority or maintenance grants are needed.

## Verification

Portable PostgreSQL tests cover the 460-candidate failed apply, closure,
459/41 replacement, committed receipt and subsequent 458/1 observation; version
changes, deleted/reinserted primary keys and same-version value mismatches;
snapshot isolation, in-flight commit/rollback visibility, scope/digest binding,
key budgets and numeric precision; plan then journal compaction, schema/RLS
changes and repeatable owner upgrades. State snapshots assert no business,
journal, approval or receipt mutations and a sequence probe verifies no check
execution. Authenticated API/CLI tests reject malformed and mismatched reports.
Direct and scheduled hosted journeys preserve terminal and unknown-outcome
history across verification; the real CLI journey observes later edits and
unavailable journals while recovering the original receipt. Browser checks
cover separate receipt/observation messages, exact text keys and unavailable or
mismatched reports. Existing portable repair acceptance remains required.
