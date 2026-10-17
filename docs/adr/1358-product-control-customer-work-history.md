# ADR-1358 · Product Control customer work history

- **Status:** accepted for internal implementation
- **Date:** 2026-10-17
- **Decision:** Search retained commercial receipts in the customer API and
  portal, then open the existing customer Run details view.

## Authority and projection

`GET /v1/product-control/customer/{slug}/work-history` requires a customer session,
live Application Auth membership in the selected organization and View work.
Both stores recheck account/app ownership, active tenant, current Auth app,
organization mapping and effective role policy. PostgreSQL shares a read-only
repeatable-read snapshot between authorization and source reads. Memory holds
its mutex and returns detached values. Billing-only and plan-manager roles gain
no work access. Pausing new runs preserves authorized history reads.

Source selection starts at independent commercial receipts, without joining the
current catalog's feature or plan lists. Removed features and old plans remain
discoverable while their receipts are retained. Entries contain only the run
reference, original feature/plan/start month/start time and recorded or retained
outcome, including result/detail availability. Prices, charges, allowance
breakdowns, payloads, execution diagnostics, owner reasons and billing documents
are absent even for billing members. Run details and the existing result,
billing and help readers retain their own authorization boundaries.

The existing current-tenant reader model applies. Receipts do not establish
historical person or Auth-app provenance. Missing and foreign exact references
both produce an empty history page in the authorized workspace.

## Search and continuation

Filters combine with AND: exact canonical `operation_id`, original `feature`,
`state`, inclusive UTC admission dates `from` and `through`. Omitted/empty state
means `all`; other values are `accepted`, `running`, `succeeded`, `failed`,
`cancelled`, `requires_reconciliation` and `unavailable`. Unavailable does not
assert failure. Dates must be real `YYYY-MM-DD` dates with a positive four-digit
year; a date range cannot be reversed. Unknown selectors and duplicate query
keys are rejected.

Order is immutable admission time descending, then canonical operation UUID
descending, at PostgreSQL microsecond precision. A page contains at most 25
entries; stores select/collect one extra row to establish continuation. The
cursor scope binds every filter, current account/app/tenant/Auth app/organization,
role and effective permission set. A cursor is an ordering boundary, not
authority; authorization runs on every request. Reuse under changed filters or
authority fails. Scope, shape and ordering are checked again at the service
boundary. Cursor bytes are limited to 2048, JSON to 32 KiB, and service reads to
20 seconds, all defined in `pkg/api/limits.go`.

The shared Run details outcome projection preserves succeeded/failed/cancelled
settlement after result expiry. Active Operations remain readable beyond the
result deadline; stopped Operations expire under existing rules. Missing/expired
unresolved work is unavailable, never an invented failure.

There is no new history window, audit journal or cross-page snapshot. Outcomes,
result availability and authority are observed anew on each page. An outcome
change can move a run into or out of a filtered search; restart from the first
page for current matches. Admission ordering prevents newer starts from shifting
a continuation boundary. Page memory/output are bounded; source scan cost still
depends on retained history. No total count or constant-cost query is claimed.
No new index or schema is required.

## Portal and rollout

History loads on explicit search, with next/first page and clear-filter actions.
Free-text feature filtering supports names absent from today's catalog. Rows
distinguish result availability from recorded outcome and open Run details,
whose permitted actions lead to result/files, billing month and contextual help.
Browsing does not start/retry work, acknowledge updates or change preferences.

Filter edits invalidate pending requests and old row actions and clear selected
details/results. Request and workspace generations fence late pages and errors.
Workspace switching, sign-out, permission changes and access denial clear history.
The portal uses text nodes, persists no searches and adds no polling.

The API and `portal-history.js` asset remain private and excluded from public
OpenAPI/SDK coverage. Both use the existing default-off Product Control gate and
no-store responses. No migration, worker, CLI/SDK change, email transport,
provider credential or deployed workload is introduced.
