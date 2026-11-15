# ADR-20261115120000: Operator console routes for signal actions and the customer's view

Status: Accepted

Extends ADR-20261113180000 and ADR-20261114190002.

## Context

The operations console (poyrazK/faas-frontend, operations.gregale.dev) reads
apid only through `/v1/admin/*`. Contacting a stuck customer, dismissing a
signal with an outcome and the per-kind outcome summary existed only on
apid's server-rendered staff page `/dashboard/admin/support/attention`, so the
console could list signals but not act on them. Operators also could not see
what a customer sees on their own account health page.

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

Writes take the admin mutation policy every console write uses (operator
allowlist, MFA, five-minute step-up, same origin, `Idempotency-Key`) and also
require a signed-in session, like the staff page. The contact route passes
the `Idempotency-Key` to `support.ContactCustomer`, so a retry replays the
ticket and links it again rather than opening a second one. Writes answer
with the signal as it now stands (`ObsAttentionActionResponse`), plus the
ticket id after a contact.

The tenant health route returns `api.AccountHealth` for the account exactly
as `GET /v1/account/health` returns it to the customer.

The staff page stays: support staff on the roster who are not operators keep
using it.

## Consequences

- Operators act on signals from the console; outcome data stays one dataset.
- The summary exposes the same numbers as the staff page's "Last 30 days"
  table, as JSON.
- No migration. Older consoles ignore the new routes; a console calling them
  against an older apid gets 404 and keeps its read-only view.
