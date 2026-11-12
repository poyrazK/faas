# ADR-20261112210000: Customer account health and support ticket snapshots

- **Status:** accepted (2026-11-13)
- **Date:** 2026-11-13
- **Related:** ADR-20261112170000 (operator support visibility),
  ADR-20261111130000 (native customer support)

## Context

ADR-20261112170000 gave operators a diagnosis of why an account is not
working. Customers could not see the same facts, so a past-due payment, an
unverified email or a failing build often became a support ticket. When a
ticket did arrive, staff saw the account as it is now, not as it was when
the customer wrote in, and a request id the customer quoted was just text.

## Decision

### 1. Account health for customers

`GET /v1/account/health` (read scope, public API and SDKs) and
`/dashboard/account/health` show the calling account's view of the operator
diagnosis, mapped by `customerAccountHealth`:

- **restrictions** in customer wording with the next step and a dashboard
  link: suspended, past due, deletion pending, on hold, unverified email,
  overage cap reached, platform release pause. One entry per kind; account
  and organization holds read the same. A kind without customer wording is
  not shown, so a new internal restriction stays internal by default.
- **failed deployments** (7 days) with error code, message and fix hint,
  linked to the deployment page;
- **custom-domain problems**, linked to the domain doctor;
- **refused API requests** (24 hours) by problem code, with route pattern,
  request id and trace id.

Not shown: platform flags, the operator-only `dropped` counter, operator
reason text on holds, and other accounts' data. An abuse hold reads "Your
account is on hold, contact support"; the hold's reason code stays where it
already is, on `GET /v1/account`. The page and API stay reachable for
suspended, deleted-pending and verification-overdue accounts, who need them
most. An abuse-held account remains locked out of the API by the existing
`Account.Active` gate; this ADR does not change that policy. Project-limited
keys cannot read it: it covers the whole account.

The overview shows "N things need your attention" when account
restrictions apply; that count reads only the account row and the release
pause. Failed deployments and refused requests offer "Contact support about
this", which opens the support form with the app, deployment or request id
filled in.

Version one covers the personal account plus organization holds. An
organization-level page for owners and admins is left for later.

### 2. Snapshot at ticket creation

When a customer creates a ticket (API or dashboard form), apid saves a
`supportportal.TicketSnapshot` in `support_ticket_snapshots`: plan, status,
the customer account-health view, and the refused request whose request or
trace id equals the id the customer attached. The ticket row decides whose
account is captured (personal owner, or the member who opened an
organization case), and the request lookup is scoped to that account, so a
customer cannot attach another account's request.

Capture runs after the ticket commits, under a 5-second detached deadline,
and is best-effort: failures are logged and never change the response. The
first snapshot wins, so an idempotent replay keeps the original. Snapshots
are at most 32 KiB; recent requests, then deployment error text, are dropped
to fit. The table is operational data, deleted with the ticket, excluded
from clones, and holds no conversation text.

The staff account panel shows the snapshot under **At ticket creation**,
then the live diagnosis under **Now**, and lists restriction kinds lifted or
added since the ticket was opened.

## Consequences

- One additive migration; deploy it before apid. Without the table, capture
  fails quietly and the panel shows no snapshot.
- Ticket creation does a few extra indexed reads after the response is
  written.
- The public API grows by one read endpoint and seven schemas; the Node and
  Python SDKs are regenerated.
