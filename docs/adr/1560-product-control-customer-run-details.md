# ADR-1560 · Product Control customer run details

- **Status:** accepted for internal implementation
- **Date:** 2026-10-17
- **Decision:** Explain an individual run's original plan, consumed allowance,
  retained outcome and permitted billing evidence in the customer portal.

## Authority and projection

`GET /v1/product-control/customer/{slug}/operations/{operation}/details` requires
live Application Auth membership in the selected organization and View work.
Its store boundary rechecks account/app ownership, active tenant, current Auth
app identity, organization mapping and the current role policy in the same
snapshot as the receipt read. Tenant, role, period and other query selectors
are rejected. Platform owner credentials cannot replace the customer session.
The route uses the independently retained commercial receipt, including when
Operations can no longer return the run. It deliberately does not use the
middleware's `{id}` retained-operation lookup; the scoped receipt store performs
its own account/app/tenant check. A missing or foreign receipt is not found.

A separate typed customer DTO selects the recorded feature, plan, admission
month and timestamp, one consumed start, optional original allowance counts,
outcome and optional charge. It does not expose the owner explanation DTO,
input or result contents, execution history/credentials, definition/deployment
IDs, confirmed quote hashes, owner reasons, billing snapshots or invoice handoffs.
The normal independently authorized result/download readers remain unchanged.

Historical terms come from the immutable receipt, not today's plan or catalog.
Admission evidence supplies starts before/after and the original monthly limit.
Older receipts legitimately lack that evidence; the portal says it was not
retained. Failure, cancellation and waiver do not return the consumed start.
These counts describe admission, not the current workspace balance.

Receipt settlement establishes succeeded/failed/cancelled outcomes. Retained
active work supplies queued/running status and retained stopped work can require
review. Retention matches Operations: active work survives its result deadline;
stopped work expires. Missing or expired unresolved work is `unavailable`, not
failed. Settled outcomes remain available after result expiry. A waived charge
does not establish an execution outcome.

The existing tenant reader model applies: work and billing receipts do not retain
historical person/Auth-app provenance. Current membership and the currently
mapped tenant authorize reading; no historical identity is invented. Pausing new
runs does not revoke View work. Mapping and role changes are rechecked per read.

## Charge semantics

Only members who have both View work and View billing receive the charge object.
A billing-only role still has no run access. Work-only responses omit prices,
currency, amounts, waivers, statement references and credit counts altogether.

| State | Evidence and amounts |
| --- | --- |
| `pending` | Retained queued/running work; amount and net omitted |
| `awaiting_review` | Retained reconciliation state; amount and net omitted |
| `unresolved` | No retained outcome; amount and net omitted |
| `not_billable` | Recorded failed/cancelled outcome; zero success charge |
| `waived` | Recorded waiver; zero success charge, independently of outcome |
| `provisional` | Unwaived successful receipt; agreed unit price, month not finalized |
| `finalized` | Successful receipt and finalized statement for its admission month |
| `partially_credited`, `credited` | Statement plus signed credit-note aggregate for this exact receipt |

The unit price is the price agreed per success. Pending/unknown amounts are
omitted instead of asserting zero. Statement references contain only UUID and
creation time. Credits remain negative; net equals the success charge plus
credits and cannot fall below zero. The aggregate includes only this receipt's
account/app/tenant/month/statement. Document count does not grow the response.
Finalization is not proof of payment; a credit note is not proof of a cash refund.

## Implementation and portal

No schema, migration, worker or event journal is added. PostgreSQL uses SQLC
queries in a read-only repeatable-read transaction shared with authorization.
Memory projects detached values while holding its mutex. Only customer metadata
is selected from Operations; result/input payloads are not returned. Billing
aggregation is skipped without billing permission. The service read budget is
20 seconds and JSON is limited to 16 KiB in `pkg/api/limits.go`; source scan cost still
depends on retained billing history. Service and browser validate scope, shape,
permissions and financial arithmetic before rendering.

Recent work gains Run details. Work activity links open the same panel. It shows
original terms, historical usage, outcome and charge, with explicit result/files,
billing-month and contextual help actions. These reads never restart/cancel
work, confirm a plan, acknowledge inbox updates or change email consent. No
additional polling or browser persistence is introduced. An observation time
and refresh button identify when the evidence was checked.

Text nodes render all content. Selection/request generations fence late results,
errors and old action buttons. Closing details, signing out and switching
workspace clear the panel and prior result. Current-role changes and access
denials clear the customer workspace. Opening a new run clears old result bytes
before reading, and result/billing callbacks check the selected run before
rendering. Standalone result links close an unrelated run explanation; delayed
result payloads and old artifact buttons cannot restore a previous selection.
Existing notification result links remain independently authorized.

The API and `portal-run.js` share the existing pre-mux, default-off Product
Control flag, no-store responses, same-origin cookie boundary, CSP and limiter.
They remain private and excluded from public OpenAPI/SDKs. The owner explanation
API/console/CLI remain available. Implementation and verification use fictional
Auth, local memory/PostgreSQL and state-only work fixtures, without provider
credentials, real workloads, mail, deployment or real flag enablement.
