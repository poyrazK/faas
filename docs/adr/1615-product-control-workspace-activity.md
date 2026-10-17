# ADR-1615 · Product Control customer workspace activity

- **Status:** accepted for internal implementation
- **Date:** 2026-10-17
- **Decision:** Extend the customer's Updates area with a read-only activity
  timeline across work, confirmed plans, finalized billing and help. Reuse
  retained records and the existing default-off Product Control boundary.

## Customer projection and authority

`GET /v1/product-control/customer/{slug}/activity?filter=all&cursor=CURSOR`
uses the live Application Auth session and selected organization. Caller-supplied
tenant, role, URL or source selectors are rejected. Platform-owner credentials
cannot replace a customer session. The store rechecks account/app ownership,
active tenant, current Auth app identity, organization mapping and effective role
policy before selecting any records. Empty/unknown roles have no access.

The customer DTO is separate from ADR-1603's owner evidence. It contains a stable
`kind/source_uuid` identity, reference UUID, occurrence time and only the fields
needed to describe that event. No owner review URL or raw retained object is
returned. Source-specific visibility applies before pagination:

| Event | Retained source | Current permission | Additional fields |
| --- | --- | --- | --- |
| `work_succeeded`, `work_failed`, `work_cancelled` | Verified receipt settlement | View work | Feature name |
| `work_attention` | Unresolved receipt with a retained, unexpired run requiring reconciliation | View work | Feature name |
| `plan_changed` | Confirmed self-service plan receipt | Manage plan | Before/after plan names |
| `statement_finalized`, `credit_recorded` | Immutable billing document | View billing | Admission month, currency, signed integer millicent amount |
| `help_replied`, `help_resolved` | Latest help update after version one | Existing help context visibility | Workspace, feature or run context kind |

Workspace help requires any product permission; feature help requires Run work
or View billing; run help requires View work. Help checks its recorded Auth app
and organization. Plan receipts require matching before/after organization IDs.
A newly mapped organization cannot inherit those previous organization records.
Work and billing follow the currently mapped tenant's existing visibility; these
records do not retain a person's or historical Auth app identity. None is inferred.

Internal notes, help messages/responses, actor roles, Auth identities, review
hashes, work prices, admission evidence, assignment changes, waivers, invoice
handoffs and billing snapshots are absent. Opening a permitted source uses its
existing independently authenticated reader. Manage plan continues to permit
that reader's existing reviewed commercial terms.

## Retention, ordering and bounds

This projection adds no event journal, migration, worker or read-state table.
Unlike the inbox's rolling 30-day window, activity shows available retained
records. Verified outcomes survive expiry of run details. An expired or missing
unresolved run does not prove failure. Attention disappears when no longer
evidenced; help keeps only its latest update. The result is not a complete audit
history, delivery journal, causal graph or proof of payment/refund.

Filters are `all`, `work`, `plans`, `billing` and `help`. Each page has at most
25 entries, one lookahead, a 64 KiB response and a 20-second service deadline,
with limits in `pkg/api/limits.go`. A timeline read has no unread count or
mutation. Existing notification read state remains separate; memory reuses its
source metadata rules without imposing the notification window.

PostgreSQL uses one SQLC union query inside a read-only repeatable-read
transaction, sharing its snapshot with authorization checks. Memory uses its
mutex and collects at most a page plus lookahead. Both return detached values.
Source scans still depend on retained tenant history; bounds apply to output,
collection memory and request time, not constant-cost history access.

Ordering is descending UTC timestamp, ASCII event kind and canonical UUID, using
PostgreSQL C collation and microsecond precision. A strict opaque cursor binds
the complete tuple, account, product, tenant, current Auth app, organization,
role, effective permissions and filter. Changes to permissions under the same
role invalidate old continuations. Cursors confer no authority. Every page is
a fresh observation; refresh discovers newer records, and updates/expiry can
change membership between pages.

The service validates scope, shape, permissions, filter, order and continuation
before writing a bounded response. The portal repeats these checks and preserves
all six microsecond digits instead of comparing rounded browser milliseconds.

## Portal and release boundary

Updates keeps its All/Unread inbox and adds Workspace activity, type filters,
explicit refresh and older pages. Links use known callbacks and validated
references: the run/result, exact plan confirmation, billing month or help
request. Viewing a plan confirmation neither clears nor confirms a pending
review. An old work outcome can show that run details expired without inventing
a result or repeating work. Statement amounts are nonnegative and credits are
negative, matching their source documents. A credit note does not establish a refund.

Activity loads at workspace connection and otherwise refreshes only on explicit
selection; there is no additional polling loop. Content uses text nodes. Logout,
workspace/role changes and access denials clear the projection. Request
generations fence late responses and previously rendered buttons. The list is
not persisted to browser storage.

The API and static asset share the existing pre-mux default-off flag, same-origin
cookies, CSP, no-store responses and authentication limiter. They remain private
and excluded from public OpenAPI/generated SDK coverage. The existing owner
console/API/CLI activity continues to provide owner diagnostics.

Memory/PostgreSQL contracts, service/router tests and browser checks cover
visibility, remapping, retention, source links, hidden-record pagination, cursor
scope, microsecond ordering and late responses. Implementation requires no
provider credentials and enables no real flags, deployment, mail or workload
execution. Deployed Auth and native workload qualification remain separate.
