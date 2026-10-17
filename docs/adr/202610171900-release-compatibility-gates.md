# Release compatibility gates: N±1, deploy order, skew and migration checks, SLO gate

Date: 2026-10-17
Status: Accepted (owner goal: 30-minute, per-unit, zero-downtime releases)
Related: ADR-1344 §6 (schema discipline, version skew, release safety), ADR-005 (cold boot always works), ADR-124 (migration lock), docs/release-compatibility.md

This record has a date-and-time identifier because date-only identifiers
collide under `make adr-number-uniqueness-check` when two ADRs land on the
same day.

## Context

Independent unit releases mean mixed versions run together all the time: a
release-N daemon talks to release-N-1 daemons, reads rows that N-1 wrote, and
writes `app.json` for guest-init binaries restored from older snapshots. Until
now nothing checked compatibility between versions. The proto drift check
(`make proto-check`) only compares generated code with its `.proto`. The
migration gates only check version IDs, apply and replay. A rollback was a
manual decision. ADR-1344 §6 called for expand/contract migrations with a
linter, `buf breaking` on every proto, and automatic rollback on SLO burn.

## Decision

1. **N±1.** Every unit works with units one release older or newer, in both
   directions. Removal takes two releases: stop using something in N, delete
   it in N+1. No unit may lag more than one release. Guest JSON keys are
   never removed while a live snapshot may hold a guest-init that reads them.
2. **Deploy order:** data (expand migrations) → control plane → edge/node →
   runtime. Rollback goes in reverse. Contract migrations ship as the data
   step of a later release.
3. **Version-skew check** (`scripts/ci/versionskew`, light CI). Compares
   protos (protoc descriptor sets) and the pkg/api guest JSON types
   (AppManifest, BuildManifest, BuildDone, read from Go source) against the
   **previous release tag**, not the pull request base. Removing an RPC,
   field or enum value requires it to have been deprecated in that release,
   and a removed field or value must also be reserved. Findings the base
   already carries do not fail. Exceptions live in an allowlist and each one
   needs a reason.
4. **Migration expand/contract lint**
   (`scripts/ci/check_migration_expand_contract.py`, light CI). Applies to
   new migrations only: no drop or rename of what the previous release still
   references, no type changes, NOT NULL only with a default, no volatile
   defaults, `lock_timeout` (at most 10 s) before any strong lock on an
   existing table, and `CONCURRENTLY` index builds or a waiver that carries a
   reason.
5. **Post-deploy SLO gate** (`scripts/ci/slo_gate.py`, called by the deploy
   workflows). Reads Prometheus's evaluated alert state for the platform SLO
   rules and the unit's own rules. Exits 1 (roll back) when an alert that
   started after the deploy holds for min(`for:`, 2 minutes). Exits 3 (treat
   as a failure) when it cannot see.

## Alternatives considered

- **`buf breaking` against main** (ADR-1344's wording). Not chosen, for
  three reasons. It needs a new pinned binary in CI. Comparing against main
  flags changes that never shipped. It cannot express "removable only if
  deprecated in the previous release": its rule sets either forbid every
  deletion or allow any deletion with a reserved number. The custom
  comparison uses the protoc that CI already installs and checks exactly the
  N±1 contract. This deviates from ADR-1344's tool choice, not its intent.
- **Re-evaluating each rule's PromQL in the SLO gate.** That would mean
  about 50 extra queries every 15 s against the saturated control-plane
  Prometheus, which already evaluates the same rules. Reading `/api/v1/rules`
  costs one request per round and uses Prometheus's own evaluation.
- **A runner-wide `lock_timeout` in `pkg/db.MigrateUp`.** Simpler, but it
  changes how every existing migration runs. The per-file rule keeps the
  timeout visible in the migration that needs it. A runner default can still
  be added later.

## Consequences

- Each new `ALTER TABLE` on an existing table needs one extra line,
  `SET LOCAL lock_timeout = '5s';`. Replaying the last 40 migrations through
  the linter, 15 would have needed it, and one (`20261016151122334`) added a
  column with `DEFAULT clock_timestamp()`, which rewrites the table.
- Deleting a proto field or RPC takes two releases.
- The release pipeline owns the lag floor (every unit at N-1 or newer before
  N rolls out) and the rollback action. These checks cannot see deployed
  versions.
- Not covered yet: guest-init's strict decoding of `job.json`,
  `app-task.json` and `execution.json` (with `DisallowUnknownFields`, so any
  added key breaks an older guest); ADR-1344's previous-binaries-against-new-
  schema CI job; capability negotiation between daemons.
