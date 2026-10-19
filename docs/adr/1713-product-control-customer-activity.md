# ADR-1713 · Product Control customer activity timeline

- **Status:** accepted for internal implementation
- **Date:** 2026-10-16
- **Decision:** Provide one owner-only, read-only history of retained Product
  Control facts in the console, private API and CLI, behind the existing
  default-off flag. Read the existing stores; add no event journal or worker.

## Sources and meaning

`GET /v1/apps/{slug}/product/customers/activity?tenant=UUID&cursor=CURSOR`, the
console's **View activity timeline**, and `gregale product customers activity`
share the same projection. Each event has a stable `kind:source_uuid` identity,
source UUID, recorded UTC timestamp, reduced source evidence and local review
link. All sources are scoped independently to account, app and tenant.

| Kind | Retained evidence |
| --- | --- |
| `onboarding_completed` | Guided setup receipt and assigned plan/revision |
| `workspace_visit_observed` | First verified customer workspace request recorded for that setup |
| `onboarding_first_success` | Earliest retained verified success admitted after that setup, ordered by resolution time then Operation UUID |
| `customer_assignment_changed` | Reviewed owner assignment receipt, before and after |
| `customer_plan_changed` | Confirmed customer self-service plan receipt, before/after and recorded actor role |
| `work_admitted` | Independent immutable admission terms, including available quote/binding/allowance evidence |
| `work_resolved` | Retained verified success, failure or cancellation and its original admission terms |
| `charge_waived` | Retained waiver time/reason and original admission terms |
| `billing_document_created` | Immutable statement or credit-note metadata without its billing snapshot |
| `invoice_handoff_recorded` | Retained external invoice reference, document identity and admission month |

Several setup receipts can refer to the same workspace visit or successful run.
These remain distinct setup milestones; only `work_admitted` counts a start.
Resolution is retained independently of Operation result expiry. Missing results
do not imply failure or permission to repeat work. A waiver does not establish an
execution outcome; a handoff does not establish payment or a refund. Recorded
success is historical evidence, not a current health or membership check.

No actor identity is invented. Owner change and work receipts do not retain a
person's identity; customer plan receipts retain a role, not a person. No signed
review, Auth token, invitation, operation input/result or billing snapshot is
returned. Operation and onboarding links open their existing scoped source
views; billing links select the original admission month and document anchor.
Assignment projections include the recorded before/after values and source ID.

## Observation and continuation

Each page contains at most fifty events and 3 MiB of JSON, under a shared
twenty-second service deadline. No failed read is returned as a partial success.
Events sort descending by timestamp, ASCII event kind and canonical source UUID.
PostgreSQL uses the C collation, one SQLC query and a read-only repeatable-read
transaction for ownership/scope and source observation. Memory takes its store
lock and retains at most a page plus one lookahead during collection. Both
implementations return detached values.

The versioned opaque cursor binds account/app/tenant and the last emitted event's
timestamp, kind and source UUID. It is an ordering boundary, never authorization.
The next page is strictly older than that complete tuple, including simultaneous
milestones from the same source. Cursor shape, bounds, source kind and scope are
validated on every request. Ownership, read scope and MFA remain independent
checks. Source links are constructed from known local paths and validated IDs.

Each request is a fresh observation, not a shared snapshot carried across pages.
Later writes above an existing boundary are discovered by refreshing the newest
page. Backdated or delayed evidence below a boundary can appear in later pages;
the cursor does not freeze source membership or retention. No full-history count,
last-seen time, inactivity claim or audit completeness claim is made.

## Coverage and authority

`history_scope` is `retained_product_control_records`. Legacy direct assignment
writes have no reviewed-change receipt and cannot be reconstructed. The timeline
does not include every transient Operation state, browsing activity, platform
audit event, Auth membership/invitation change, payment or refund. Auth continues
to own its own role/action history and requires its live customer authority; the
platform owner timeline never fetches or mirrors that journal.

The existing pre-mux owner gate now covers the owner API as well as the console,
so disabled Product Control routes return 404 before method fallback or routing
redirects. Enabled routes keep their normal authentication and read/MFA checks.

The console shows readable summaries, expandable retained details and existing
review links. It performs no repair, setup, observation write or external call.
The CLI makes one GET per page, supports readable/JSON output, and validates
identity, source shape, scope, ordering, continuation and exact local review
links. The private API is absent from public OpenAPI and generated SDK contracts.

Portable tests cover all source kinds, legacy gaps, result expiry, original
prices, waivers/credits/handoffs, equal-time pagination, concurrent newer inserts,
customer/product isolation, no-store, owner/customer boundaries and default-off
behavior. Local checks do not qualify native execution or replace the deployed
Auth/email/browser/export pilot. This change enables no deployment flag.
