# ADR-20261115183907: Operator console routes for signal actions, the customer's view and the support inbox

Renumbered from ADR-20261115120000 to resolve a concurrent identifier collision. Historical citations in issued migrations retain the original identifier.

Status: Accepted

Extends ADR-20261111130000, ADR-20261112170000, ADR-20261113180000 and ADR-20261114190002.

## Context

The operations console (poyrazK/faas-frontend, operations.gregale.dev) reads
apid only through `/v1/admin/*`. Contacting a stuck customer, dismissing a
signal with an outcome and the per-kind outcome summary existed only on
apid's server-rendered staff page `/dashboard/admin/support/attention`, so the
console could list signals but not act on them. Operators also could not see
what a customer sees on their own account health page. The support inbox
already had JSON staff routes (`/v1/admin/support/*`), but the account panel
next to a ticket (owner, live diagnosis, the snapshot taken when the ticket
was opened, restrictions lifted or added since) was built only for the
server-rendered `/dashboard/admin/support/{ticket}`.

## Decision

Add the JSON twins of the staff page, sharing its code
(`server.dismissAttention`, `server.contactAttention`), so both front ends
record the same outcomes, metrics, ticket links and audit:

| Route | Policy |
| --- | --- |
| `GET /v1/admin/obs/attention/summary` | operator session read |
| `GET /v1/admin/obs/attention/{id}/contact-draft` | operator session read |
| `POST /v1/admin/obs/attention/{id}/dismiss` `{outcome, note}` | admin mutation + session principal |
| `POST /v1/admin/obs/attention/{id}/contact` `{subject, category, body}` | admin mutation + session principal |
| `GET /v1/admin/obs/tenants/{id}/health` | operator session read |
| `GET /v1/admin/support/tickets/{ticket}/customer` | session principal + support roster |

Writes take the admin mutation policy every console write uses (operator
allowlist, MFA, five-minute step-up, same origin, `Idempotency-Key`) and also
require a signed-in session, like the staff page. The contact route passes
the `Idempotency-Key` to `support.ContactCustomer`, so a retry replays the
ticket and links it again rather than opening a second one. Writes answer
with the signal as it now stands (`ObsAttentionActionResponse`), plus the
ticket id after a contact.

The tenant health route returns `api.AccountHealth` for the account exactly
as `GET /v1/account/health` returns it to the customer.

The ticket customer route returns `ObsSupportTicketCustomer`, the same
panel the staff ticket page renders (`server.supportCustomerContext`). Like
the other `/v1/admin/support/*` reads it is gated by the support roster, not
the operator allowlist: it requires a session principal and checks
`support.Service.StaffRole` before loading the ticket's owner. An unknown
ticket is 404; an account that cannot be loaded answers `unavailable: true`.

The staff support routes are now registered one literal pattern each (same
middleware as the loop they replace), so the read-policy test and the
console's contract check can see them; the read-policy test lists them as
roster-gated reads.

The staff pages stay: support staff on the roster who are not operators keep
using them.

## Consequences

- Operators act on signals and work the support inbox from the console;
  outcome data and ticket history stay one dataset.
- The summary exposes the same numbers as the staff page's "Last 30 days"
  table, as JSON.
- No migration. Older consoles ignore the new routes; a console calling them
  against an older apid gets 404 and keeps its read-only view.
