# ADR-20261019120051: Durable manifest cron staging and recovery

Date: 2026-10-12
Status: Accepted

## Context

ADR-20261019120050 recovers profiling and tracing after a pre-submission CLI crash. Manifest
HTTP cron reconciliation still records inverse operations only in memory. A
process kill or lost mutation reply can leave a partial scheduling set, and
read-then-write compensation can overwrite another operator's changes.

## Decision

The single-app CLI saves a closed HTTP scheduling journal before staging. The
journal contains app/deployment identity, cron revision, stable row IDs and
before/desired schedule, path, enabled, timezone and skip-if-running values.
It excludes command payloads, source, manifests, environment and credentials.
An omitted trigger declaration and `--no-triggers` leave crons unmanaged; an
explicit empty trigger set requests removal of HTTP crons.

The app-scoped deployment-cron-staging API snapshots HTTP intent and atomically
stages or restores the complete set. Protocol types live in `pkg/api`, guards
and persistence in `pkg/state`, handlers in `pkg/apid/deploystaging`; `cmd/apid`
keeps authenticated routes and notification/audit adapters. The endpoint uses
existing deployment read/write scopes and MFA, rejects unknown fields, validates
schedules/timezones, and never creates builds or deployments.

Each app stores one HTTP cron revision, owner, canonical intent digest and the
server-held original rows. Original rows retain IDs and recurrence bookkeeping;
this is bounded by the app's existing cron quotas, with no operation ledger or
reaper. The request does not supply original operational fields. Normal HTTP
intent writes invalidate ownership, including explicit same-value updates and
edits away and back. A database trigger covers other writers. Command cron edits
and scheduler firing/suspension bookkeeping leave HTTP ownership intact.

Account then app locks serialize set replacement, deployment creation, ordinary
cron CRUD and quota checks. Commands remain untouched but count toward both
caps. Replacement checks final counts before any mutations, so replacing at the
exact quota succeeds and rejected quota checks change nothing. Direct database
writers also invalidate ownership; PostgreSQL may abort a conflicting transaction
rather than allow a lock-order race to overwrite intent.

Stage binds the exact app, unchanged latest deployment, captured revision and
before set. It increments the revision once and claims ownership even for a
same-value set. Exact duplicates are read-only. Cleanup restores only the exact
owned desired set. Cleanup before staging seals the revision and fences a delayed
stage. A lost cleanup reply is recognized from the original set and superseded
revision without writing. Conflicts remain pending with no unchecked fallback.
Retained rows preserve later firing progress and suspension changes; recreated
original rows retain their IDs and use a later schedule revision. This restores
scheduling configuration, not already dispatched work or deleted history and
related resources. Command schedules remain independently managed.

Cron cleanup shares ADR-20261019120050's durable submission boundary. Accepted or uncertain
submission preserves staging. Never-submitted or definitively rejected attempts
can resume cleanup from another process without source or manifest and create
zero deployments. Cron and policy cleanup completion are saved separately, so a
lost reply or conflict in one group does not replay completed cleanup in another.
Old saved sessions retain their existing behavior. Older servers fail before
staging; the CLI does not fall back to unguarded cron CRUD.

Project apply and server-side source-ref manifest reconciliation remain outside
this CLI slice. Existing reconciliation helpers remain for those callers.

## Validation

Acceptance covers process death with a lost staging reply, cleanup before a
delayed stage, lost cleanup replies, concurrent edits and ABA, same-value owners,
command preservation, scheduler progress, exact-cap replacement, quota rejection,
app/account/deployment identity guards, and real PostgreSQL concurrency. Cleanup
must create zero deployments and work after deleting the source directory.
