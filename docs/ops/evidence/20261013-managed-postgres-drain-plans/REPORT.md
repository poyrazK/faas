# Managed PostgreSQL durable drain-plan evidence

Date: 2026-10-13. Base: `f6b8f9a70e` (main after PR-4604).
Decision: [ADR-1151](../../../adr/1151-managed-postgres-durable-drain-plans.md).

## Implemented boundary

An explicit internal scheduler request captures the entire app instance catalog
across deployment scopes and VM modes under existing fence/ownership authority.
Plan membership and supported native drain reservations commit atomically.
Existing bounded recovery resumes these original members. Unsupported members
remain visible blockers; an empty or oversized inventory queues no partial work.
Read-only status requires exact receipts and current authority before reporting
`catalog_retired`. This observation does not authorize customer activation.

The catalog guard covers late parked/terminal insertion, identity changes,
unconfirmed retention and TRUNCATE while the epoch is current. Confirmed native
receipts survive ordinary instance retention. Cancellation releases the existing
admission barrier and the corresponding catalog guard.

## Reproduced and corrected

The first regression failed because the worker fixture mutated an existing VM's
mode. Fixtures now use the ordinary account/node worker reservation path.

A PostgreSQL repeatable-read transaction with a pre-plan snapshot could lock an
unchanged app row yet fail to see a subsequently committed plan. The dedicated
regression reproduced this bypass before the fix. Fenced membership guards now
require read-committed isolation. A separate concurrent test holds the app lock,
queues capture followed by a late parked insertion, and proves the insertion
observes the newly committed plan and fails.

Catalog guards reuse the existing typed admission-fence constraint so scheduler
callers receive `ErrManagedPostgresAdmissionFenced`. SQLC verification also copies
the new query file into its disposable generation workspace.

## Local verification

Pinned Go 1.26.9, SQLC 1.31.1, private PostgreSQL 16.15:

- Final planner suite with the race detector: passed (38.419 s). Covers all
  scopes/modes, immutable retry, unsupported members, unconfirmed terminal rows,
  missing catalog/member evidence, atomic rollback, lost reply and unreachable
  member recovery, owner ABA, cancellation, empty/oversized inventories,
  concurrent capture/late insertion and concurrent idempotent requests.
- Existing member-receipt and planner regressions: passed (161.451 s), before
  the final typed-error assertion and two additional race cases.
- State classification and clone schema policies: passed (20.379 s).
- Scheduler plan/capability and receipt-recovery regressions: passed (2.651 s).
- Final migration replay and complete up/down walk: passed (6.670 s).
- SQLC generation/verification, ADR uniqueness, runbook SQL, text encoding,
  shell quoting and diff whitespace checks: passed.
- Full schema matches the applied migration set after trailing-whitespace
  normalization. Fresh regeneration also includes function/trigger changes from
  already merged main migrations that its previous snapshot did not reflect.

An exploratory broader migration corpus exhausted local disk while creating
schemas. Only the task-owned, idle disposable database was recreated; shared Go
caches and other checkouts were preserved. The required replay/up-down checks
were then rerun successfully. This environment failure is not a passing full
migration-corpus result.

## Remaining qualification

Native x86_64 Linux KVM `test-metal` and `leakcheck` remain outstanding. The local
receipt observations are fabricated storage/scheduler fixtures and cannot prove
physical guest retirement. This change does not establish external SQL-session
isolation, orphan-host inventory completeness, route convergence, atomic target
credential publication, restart health, restore data-loss policy or rollback.
Production provisioning and customer cutover activation remain disabled.
