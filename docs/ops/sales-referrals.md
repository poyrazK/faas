# Sales workspace and referrals

Gregale's own sales portal lives at `https://sales.gregale.dev` (or
`sales.<platform-domain>`). The edge forwards its root to apid, which redirects
to `/dashboard/sales`. The same page can be opened on the API host. Customers
use `https://api.gregale.dev/dashboard/referrals`.

This feature requires PostgreSQL. It ships disabled. Enabling it exposes
referral registration and authenticated pages; it never enables payouts.

## Configure and roll out

1. Apply `20261103180000000_sales_referrals.sql` before the new apid release.
   This is additive; an older release ignores the new tables and view.
2. Deploy apid and gatewayd-public, including the shared host classifiers.
   `sales` is now a reserved platform label. Confirm an existing app is not
   relying on that hostname before routing the subdomain.
3. Add a proxied DNS record for `sales.gregale.dev` to the same public edge as
   `api.gregale.dev`. The existing platform wildcard Origin CA certificate
   and Caddy platform site cover it; no separate listener is introduced.
4. Configure the sales team and enable the feature, then restart apid.
   Use existing Gregale account email addresses. Reps and managers must verify
   their email and enroll an authenticator. Removing an email and restarting
   removes access on the next request, including already-issued sessions.
5. Sign in at `/login?next=/dashboard/sales` on the sales host. Staff sessions
   use that host's existing secure cookie; sessions are not shared through a
   parent-domain cookie. Customers continue using the API-host dashboard.

The control-plane Ansible role accepts `faas_sales_enabled`,
`faas_sales_emails` and `faas_sales_manager_emails`. Its reward variables mirror
the environment settings below. The defaults leave the feature off and both
allowlists empty. No production DNS or account access is changed by the code.

| Environment setting | Default | Meaning |
| --- | --- | --- |
| `FAAS_SALES_ENABLED` | off | Only `1` enables sales and referral surfaces |
| `FAAS_SALES_EMAILS` | empty | Comma-separated rep email addresses, exact matches |
| `FAAS_SALES_MANAGER_EMAILS` | empty | Comma-separated managers; includes rep privileges and team-wide reads |
| `FAAS_REFERRAL_CURRENCY` | `usd` | `usd` or `eur`; amounts are never mixed or converted |
| `FAAS_REFERRAL_REWARD_CENTS` | `0` | Fixed potential reward for a customer referrer |
| `FAAS_REFERRAL_NEW_CUSTOMER_REWARD_CENTS` | `0` | Fixed potential reward for the referred customer |
| `FAAS_SALES_COMMISSION_BPS` | `0` | One-time share of first pre-tax paid invoice, 0–10000 basis points |
| `FAAS_REFERRAL_HOLD_DAYS` | `30` | Days after first observed payment before eligibility, 0–365 |
| `FAAS_REFERRAL_CONVERSION_DAYS` | `90` | Signup-to-payment observation window, 1–365 days |

One currency unit is 100 cents. Reward fields accept whole integer cents up to
100,000,000; commission arithmetic rounds down. Invalid enabled configuration
refuses startup. Updating settings changes new signup terms only. Existing
attributions retain the settings captured at their creation.

There is no payout-enable setting or payout endpoint. Portal amounts are
estimates, not a payable balance or an applied account credit. Enabling money
movement requires a separately reviewed integration. This also means an estimate
for the referred customer is not silently applied to their invoices.

## Workflow

Create a personal link in the portal and share it. Visiting
`https://api.gregale.dev/r/CODE` validates the link and sets a signed,
host-only thirty-day first-touch cookie before opening signup. Later visits do
not extend or replace that touch. The cookie has no email, IP address, or user
agent. OAuth returns to the same signup authority. Existing-account sign-in and
signup retries cannot attach a new referral to an old account.

The signup transaction captures attribution with the account and personal
organization. Failure rolls back all three. No click counts are presented as
unique visitors; the portal reports registered accounts and recorded payments.

Sales staff can search the current page, review current plan and app count,
record a lead stage (`new`, `contacted`, `qualified`, `lost`), schedule a follow-up,
save a note, pause/resume their link, and export the visible page to CSV. CSV
cells neutralize spreadsheet formula prefixes. The sales kit links to the
product site, docs and public status. A manager sees all sources; a rep sees
only referrals from links they own. Manual stages never mark an invoice paid.

The data is paginated in stable account-ID order, up to 25 records per page.
Continuation cursors are encrypted and bound to the current actor and visibility;
they do not reveal a referred account identifier.
Metrics cover the full permitted scope and group money by currency and program
kind. Each refresh is an observation, not a locked financial statement.

Reward statuses are `registered`, `holding`, `eligible`, `reversed`,
`currency_mismatch`, `review` or `unconfigured`. The first observed positive
paid invoice must fall inside the conversion window. A paid invoice's recorded
payment starts the hold, rather than its earlier draft creation. Subsequent
invoice syncs, refunds and pending refunds are reflected by the read-only view.
An unrelated newer invoice cannot replace the original conversion.

## Private routes

All API routes below require a live session, reject API keys, and return
`Cache-Control: no-store`. Staff routes also enforce explicit sales membership,
verified email and enrolled/completed MFA. Session mutations use the existing
origin policy. Links are generated by explicit POST; GETs never create links.

| Route | Use |
| --- | --- |
| `GET /v1/sales/me?after=CURSOR` | Scoped staff rows, totals, current terms, own sales link |
| `POST /v1/sales/links` | Create or recover the account's stable sales link |
| `PATCH /v1/sales/links/{code}` | Pause/resume a scoped link with `{"active":false}` |
| `PATCH /v1/sales/customers/{account}` | Replace stage, note and optional `follow_up` (`YYYY-MM-DD` or empty) |
| `GET /v1/referrals/me?after=CURSOR` | Customer's referral statuses and estimates; no referred contact details |
| `POST /v1/referrals/links` | Create or recover the customer's stable personal link |
| `PATCH /v1/referrals/links/{code}` | Pause/resume an owned link |

Follow-up changes use last-write-wins semantics; refresh before editing shared
notes. Audit events record the actor, action and scoped customer identifier,
excluding note contents, referral cookies and credentials. Deleting an account
erases its outgoing links and dependent attributions, or its own incoming
attribution. Account suspension blocks reward eligibility and new link visits.
Acquisition attribution and notes are not cloned into application environments.

## Verify and disable

Use separate rep, manager and customer accounts in a test deployment. Verify a
new signup through each link type, the same-email retry, a first paid invoice,
a duplicate invoice notification, a pending refund, and a revoked sales member.
Check that a rep cannot read or update another rep's customer and a customer
response contains no referred email or notes. Verify the sales hostname's root,
login, MFA and authenticated pages through the public edge.

Set `FAAS_SALES_ENABLED=0` and restart apid to hide all feature pages, APIs,
tracking links and assets. Existing acquisition rows remain; rollback does not
rewrite attribution, grant operator rights or issue payments.
