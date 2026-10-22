# Managed PostgreSQL durable drain-plan evidence

Date: 2026-10-13. Final implementation base: `e1bfe3368f`.
Initial verification base: `f6b8f9a70e` (main after PR-4604).
Decision: [ADR-1153](../../../adr/1153-managed-postgres-durable-drain-plans.md).

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

A further minimized PostgreSQL probe reproduced a stale repeatable-read TRUNCATE
bypass: a committed plan was invisible and its instance table was emptied. The
plan verdict still failed closed without native receipts. The table-wide guard
now requires read-committed isolation before consulting any app snapshot. Both
repeatable-read and serializable probes reject the operation with SQLSTATE 40001;
the integrated regression also checks that the captured incarnation is preserved.

Catalog guards reuse the existing typed admission-fence constraint so scheduler
callers receive `ErrManagedPostgresAdmissionFenced`. SQLC verification also copies
the new query file into its disposable generation workspace.

Initial CI caught a malformed-receipt decoder error being returned as nil and
two clone inventory omissions. Typed conflict classification now preserves an
inspectable `receipt_invalid` blocker, with a corrupt-timestamp regression.
Blocked plan members independently count as retained deployment borrowers even
when they have no native drain row. Cleanup locks both receipt and member writers
until its commit; borrower, atomic-cleanup and concurrent-insertion regressions
cover this boundary. Captured deployment scope is explicitly retained historical
evidence in the environment-cleanup inventory.

Rebasing discovered an ADR-1151 collision, so this decision is ADR-1153. A separate
ADR-976 collision already present on main was resolved by renumbering the local
capture cohort document to ADR-1154; its decision and production gates are unchanged.

## Local verification

Pinned Go 1.26.9, SQLC 1.31.1, private PostgreSQL 16.15:

- Final rebased planner suite with the race detector: passed (73.199 s). Covers all
  scopes/modes, immutable retry, unsupported members, unconfirmed terminal rows,
  missing catalog/member evidence, atomic rollback, lost reply and unreachable
  member recovery, owner ABA, cancellation, empty/oversized inventories,
  concurrent capture/late insertion, concurrent idempotent requests and malformed
  retained native receipts.
- Existing member-receipt and planner regressions: passed (161.451 s), before
  the final typed-error assertion and two additional race cases.
- Before rebase: state classification and clone schema policies passed (20.379 s).
- Before rebase: scheduler plan/capability and receipt-recovery regressions passed
  (2.651 s).
- Before rebase: migration replay and complete up/down walk passed (6.670 s).
- SQLC generation/verification, ADR uniqueness, runbook SQL, text encoding,
  shell quoting and diff whitespace checks: passed.
- The final schema was regenerated from fresh migrations in a UTC database.
  Fresh regeneration also includes function/trigger changes from already merged main migrations that its previous snapshot did not reflect.

An exploratory broader migration corpus exhausted local disk while creating
schemas. Only the task-owned, idle disposable database was recreated; shared Go
caches and other checkouts were preserved. The required replay/up-down checks
were then rerun successfully. This environment failure is not a passing full
migration-corpus result.

After rebase, local disk exhaustion interrupted two planner cases; the complete
planner race suite then passed on retry. Linux lint retries and the local state
race build did not complete. Subsequent Go builds failed because shared cache
files disappeared during validation (including a diagnostic-only `-vet=off`
attempt). Shared cache contents were not deleted by this task. Remaining lint,
vet, builds, clone regressions and migration replay must be verified by Linux CI
on the final PR head; these interrupted local attempts are not passing evidence.

## Remaining qualification

Native x86_64 Linux KVM `test-metal` and `leakcheck` remain outstanding. The local
receipt observations are fabricated storage/scheduler fixtures and cannot prove
physical guest retirement. This change does not establish external SQL-session
isolation, orphan-host inventory completeness, route convergence, atomic target
credential publication, restart health, restore data-loss policy or rollback.
Production provisioning and customer cutover activation remain disabled.
