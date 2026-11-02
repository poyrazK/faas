# ADR-20261102071817: Operator actions reach audit_log

- **Status:** accepted
- **Date:** 2026-11-02
- **Amends:** ADR-091 (operator observability backend), ADR-035 (audit log)

## Context

ADR-091 §3.4 put the operator audit search (`/v1/admin/obs/audit-log/search`)
on `audit_log`, and its filters assume operator rows are there:
`operator_only` is `kind LIKE 'operator.action.%'`, `target_account_id`
matches the `target_account_id` the operator emit helpers set, and
`actor_email` matches `audit_log.account_email`. The overview's
`audit_events_24h`, the operator trace-coverage ratio and the anchors of
ADR-20261026163015 read `audit_log` too.

No operator action is written there. apid's view helpers and actions, schedd's
`.outcome` rows and the audit outbox all append to `events` through
`state.Store.AppendEvent` (ADR-035). `audit_log` gets rows only from account
deletion and alert triage. On production the operator audit search returns
nothing newer than 2026-10-16 while `events` holds 64 `operator.action.view`
rows from 2026-10-30 on, and the console's "Recent operator actions", a
tenant's operator history and the audit trail's operator filter are empty.

## Decision

1. **An `AFTER INSERT` trigger on `events` copies every `operator.action.*`
   row into `audit_log`** in the same transaction
   (`events_operator_action_audit_log`). One place covers every writer:
   apid, schedd and outbox delivery, old and new releases alike.
2. **The row is filed under the operator who acted**: `account_id` is
   `data.actor` when it is a UUID (every operator emit site sets it), else
   `events.actor_account_id`; `account_email` is that account's email;
   `actor` is `data.actor`, else `events.actor`. Alert triage already files
   its rows this way. A customer's own audit log (`GET /v1/audit-log`) is
   filtered to their account, so operator rows about them do not appear there.
3. **The payload is kept verbatim**, with `events.trace_id` added as
   `data.trace_id` when the payload has none, as `InsertAuditLog` stores it.
4. **Views are included.** "Every view is audited" is a promise about who
   looked at a tenant's data; `audit_log` is the record that is kept, sealed
   and searchable. Each audited read of an operator list writes one row.
5. **No backfill.** Older `events` rows are not copied: their `received_at`
   falls in windows the anchors have sealed, and adding to them would break
   verification. The operator audit starts at this migration.

MemStore mirrors the trigger in `copyOperatorActionToAuditLogLocked`, and the
shared store conformance suite pins the behaviour for both stores.

## Consequences

- The operator audit search, the overview's operator panel and 24-hour count,
  and a tenant's operator history show operator actions and views from now on.
- `audit_log` grows by one row per operator action or audited view. The
  console polls the audited deployment, job-run and incident lists (two to
  four reads a minute per open tab); polling them less is a console change.
- A failed `audit_log` insert fails the `events` insert with it. Audit writes
  stay best-effort for the action (ADR-035); outbox delivery retries.
