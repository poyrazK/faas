# Sales workspace and referrals

Gregale's own sales portal lives at `https://sales.gregale.dev` (or
`sales.<platform-domain>`). The edge forwards its root to apid, which redirects
to `/dashboard/sales`. The same page can be opened on the API host. Customers
use `https://api.gregale.dev/dashboard/referrals`.

This feature requires PostgreSQL. It ships disabled. Enabling it exposes
referral registration and authenticated pages; it never enables payouts.

## Configure and roll out

1. Apply `20261103180000000_sales_referrals.sql` and
   `20261103190000000_short_referral_codes.sql` and
   `20261103191000000_validate_referral_codes.sql` before the new apid release.
   This is additive; an older release ignores the new tables and view.
2. Deploy apid, gatewayd-public and the `faas-web` signup form, including the shared host classifiers.
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

Create a personal referral code in the portal and share the six characters,
for example `POJU43`. Codes use uppercase letters and digits, are randomly
generated, globally unique and stable per account and referral type. Database
uniqueness and bounded retries handle collisions. Signup accepts lowercase input
and normalizes it. Codes are identifiers, not credentials.

A new customer enters the optional **Referral code** field when creating their
account. Password and magic-link signup accept `referral_code`; Google/GitHub
signup saves the explicit selection in a signed, secure, host-only cookie for
15 minutes before redirecting to the provider. An empty selection clears it.
There are no referral URLs or automatic click attribution. Invalid or paused
codes are rejected before account creation. Existing-account sign-in and signup
retries cannot attach a new referral to an old account.

The signup transaction captures attribution with the account and personal
organization. Failure rolls back all three. No click counts are presented as
unique visitors; the portal reports registered accounts and recorded payments.

Sales staff can search the current page, review current plan and app count,
record a lead stage (`new`, `contacted`, `qualified`, `lost`), schedule a follow-up,
save a note, pause/resume their code, and export the visible page to CSV. CSV
cells neutralize spreadsheet formula prefixes. The sales kit links to the
product site, docs and public status. A manager sees all sources; a rep sees
only referrals from codes they own. Manual stages never mark an invoice paid.

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

The authenticated API routes below require a live session, reject API keys, and return
`Cache-Control: no-store`. Staff routes also enforce explicit sales membership,
verified email and enrolled/completed MFA. Session mutations use the existing
origin policy. Codes are generated by explicit POST; GETs never create codes.

| Route | Use |
| --- | --- |
| `GET /v1/sales/me?after=CURSOR` | Scoped staff rows, totals, current terms, own sales code |
| `POST /v1/sales/codes` | Create or recover the account's stable sales code |
| `PATCH /v1/sales/codes/{code}` | Pause/resume a scoped code with `{"active":false}` |
| `PATCH /v1/sales/customers/{account}` | Replace stage, note and optional `follow_up` (`YYYY-MM-DD` or empty) |
| `GET /v1/referrals/me?after=CURSOR` | Customer's referral statuses and estimates; no referred contact details |
| `POST /v1/referrals/codes` | Create or recover the customer's stable personal code |
| `PATCH /v1/referrals/codes/{code}` | Pause/resume an owned code |

`POST /v1/referrals/selection` is a public, origin-checked and rate-limited
signup preparation endpoint. Its body is `{"referral_code":"POJU43"}` (or an
empty string to clear selection); it reveals no owner or customer data.

The customer-referral follow-up fields above use last-write-wins semantics;
refresh before editing shared notes. The lead pipeline below uses versioned
edits. Audit events record the actor, action and scoped customer identifier,
excluding note contents, referral cookies and credentials. Deleting an account
erases its outgoing codes and dependent attributions, or its own incoming
attribution. Account suspension blocks reward eligibility and new code uses.
Acquisition attribution and notes are not cloned into application environments.

## Verify and disable

Use separate rep, manager and customer accounts in a test deployment. Verify a
new signup with each code type, the same-email retry, a first paid invoice,
a duplicate invoice notification, a pending refund, and a revoked sales member.
Check that a rep cannot read or update another rep's customer and a customer
response contains no referred email or notes. Verify the sales hostname's root,
login, MFA and authenticated pages through the public edge.

Set `FAAS_SALES_ENABLED=0` and restart apid to hide all feature pages, APIs,
code selection and assets. Existing acquisition rows remain; rollback does not
rewrite attribution, grant operator rights or issue payments.

## Lead pipeline and follow-up tasks

Apply `20261103193823000_sales_pipeline.sql` before the matching apid release.
The existing `FAAS_SALES_ENABLED` flag covers the lead pipeline and task inbox.
No `faas-web` change is needed for these additions; they are served inside the
sales portal. Existing sales membership, verified email and MFA rules apply.

Use **Add lead** before a prospect signs up. Record their name and email, company,
stage and notes. Reps own their new leads; managers can choose an active sales
team member. Managers see all leads, including those assigned to a revoked rep,
and can reassign them. The lead and its tasks move together. Duplicate emails
for the same owner are rejected. Search covers name, email and company across
the full permitted scope; stage counts follow search/owner filters, across pages.

Use **Add task** from a lead row to schedule a follow-up. The inbox shows open,
completed, overdue, due-today and upcoming tasks, with completion, reopening and
rescheduling. Its summary counts all open tasks for the selected owner scope.
The browser sends its local calendar date; date labels and reminders follow
that calendar. Tasks refresh every minute while the page is visible. These are
in-app reminders; no messages or notifications are sent externally.

A prospect shows a matching signup or payment only when the same email has an
actual sales referral credited to that lead's current owner. Customer referral
codes and another rep's code do not count as that match. Manual won/lost stages
do not mark invoices paid. Reassignment or CRM deletion never changes referral
credit. Deleting a lead deletes its follow-up tasks, retaining acquisition rows.

Edits and deletes require the current `version`. A 409 `sales_record_conflict`
means the row changed or is no longer available in the caller's scope. Close
the editor, refresh, and reopen the record before retrying. A duplicate email
returns 409 `sales_lead_exists`. All mutations are audited without contact names,
email addresses, notes or task titles in the event payload.

| Private route | Use |
| --- | --- |
| `GET /v1/sales/team` | Eligible salespeople, current actor/role and stage options |
| `GET /v1/sales/leads?q=&stage=&owner=&after=` | Scoped prospect list and stage counts |
| `POST /v1/sales/leads` | Create a lead; initial version is omitted or zero |
| `PUT /v1/sales/leads/{lead}` | Replace name, email, company, stage, note and owner with the current version |
| `DELETE /v1/sales/leads/{lead}?version=VERSION` | Delete an accessible lead and its tasks |
| `GET /v1/sales/tasks?status=open&due=all&on=YYYY-MM-DD&owner=&after=` | Due-date ordered tasks and open-task totals |
| `POST /v1/sales/tasks` | Create a task with `lead_id`, `title` and `due_on` |
| `PUT /v1/sales/tasks/{task}` | Replace `title`, `due_on`, `done` and `version`; lead cannot be changed |

Lead names accept 120 bytes, company/task titles 200, email 254, and notes 2000.
Search accepts 100 bytes; pages contain up to 25 records. Task due dates range
from 2000 through 2100. Text limits are UTF-8 bytes, not character counts.

Verify with a rep and manager: create a lead and task, reassign the lead, check
that the old rep loses both and the new owner gains both, complete and reopen a
task, attempt a stale update, and create a matching sales referral signup. Check
that another rep's code cannot expose signup status, and that customer referral
pages do not include the lead pipeline, task scripts or staff contact data.

## Manager reports

The sales portal's **Manager reports** section is available only to configured
sales managers with verified email and completed MFA. Ordinary reps and customer
pages do not load the report script, and the API rejects non-manager sessions
and API keys. No new migration or customer-console release is needed.

`GET /v1/sales/reports?from=YYYY-MM-DD&to=YYYY-MM-DD&tz=Europe/Istanbul&owner=UUID`
returns a creation-cohort report. `from` and `to` are inclusive calendar dates,
within 2000–2100 and span at most 366 calendar days, including both endpoints. Without
explicit dates, the API uses the current month through today in the selected
time zone; the API default zone is UTC. The portal uses the browser's zone by
default and offers UTC. `owner` is optional. The portal includes month, seven-day
and thirty-day presets, custom dates, per-rep filtering, sorting and CSV exports.

The dates select when CRM leads and sales referral accounts were created, not
when their outcome happened. Lead counts/stages use the current CRM owner.
Signup and first-payment counts use the original sales referrer. Customer-program
referrals are excluded. Current won/lost stages define win rate; an empty
won/lost or signup denominator displays no percentage. The elapsed-payment
average uses recorded signup and first-payment timestamps. First payments remain
recorded conversions after a refund, with the refund shown in collections.

Collections include only the referred account's captured first invoice. Recorded
payments, refunds, pending refunds and net collections are grouped by actual
invoice currency. Net collections subtract recorded and pending refunds per
invoice, with a minimum of zero; amounts include tax. Missing or wrong-account
invoice snapshots are counted and excluded from monetary totals. New team members
with no activity remain visible; past/unavailable source owners retain their
surviving history. Reports reflect the current retained records at `observed_at`.

The response contains `window`, `reps`, `rows`, `collections` and `observed_at`.
Collection `*_cents` fields are decimal strings for exact browser arithmetic.
The comparison and collection exports are separate CSVs. Every exported row
includes the creation-date range, reporting zone and observation timestamp;
collection exports keep integer cents and currency in separate columns.

Verify with data around a reporting-zone midnight and a daylight-saving change,
an empty period, multiple currencies, a refund, a pending refund, and a salesperson
with no activity. Reassign a CRM lead and confirm that lead counts move while
referral credit does not. Check that a rep receives 403 from the report endpoint,
and that comparison/collection CSVs preserve the selected period and scope.

## Website contact/demo intake

Apply `20261103211600000_sales_intake.sql` after the CRM migration, then deploy
apid and the website. The website `/contact` route, footer **Contact sales**
link and pricing **Request a demo** action are enabled by the build-time flag
`VITE_FEATURE_SALES_INTAKE=1`. Both `/v1/*` and the website must share the
existing trusted control-plane origin. No additional DNS is required for the
contact page; the staff inbox stays on `sales.gregale.dev`.

Backend settings (also exposed by the matching `faas_sales_intake_*` Ansible
variables):

| Setting | Default | Meaning |
| --- | --- | --- |
| `FAAS_SALES_INTAKE_ENABLED` | off | `1` opens anonymous intake; also requires sales enabled |
| `FAAS_SALES_INTAKE_FOLLOWUP_DAYS` | `1` | Initial follow-up due after 0–30 calendar days from assignment |
| `FAAS_SALES_INTAKE_TIMEZONE` | `UTC` | IANA time zone used to calculate that calendar date |

A rep must have an active account, verified email, MFA and membership in
`FAAS_SALES_EMAILS` to receive generic inquiries. Managers can receive a request
through their own code or manual assignment. With no eligible reps, requests
enter **Website inquiries → Needs manager review**. A manager should monitor
this queue, choose a salesperson and use **Assign & create follow-up**. Code
conflicts and unavailable codes go here too. Entering a code never reveals the
owner to the visitor or grants commission. The visitor must enter a referral
code again at signup to establish actual referral credit.

Each assignment creates a lead (or reuses an existing lead without changing its
staff fields) and a follow-up task atomically. Repeated open-email submissions
retain the original owner and message; the latest repeat message appears
separately. Only one initial follow-up remains open; later requests rearm a
completed/missing first task. Closing the inquiry retains the lead and tasks.
Deleted leads reappear as manager-review inquiries. All submitted identities
and messages remain unverified.

The public form uses a two-second-minimum, one-hour signed token, a honeypot,
strict body/text bounds and a separate ten-request-per-minute/IP budget shared
by challenge and submit. Retries reuse the token so an uncertain response does
not duplicate the inquiry or task. Expired tokens can be refreshed without
clearing the form. These measures deter basic spam; review suspicious messages
before acting. No external email or calendar event is sent. Original and latest
messages persist until database retention/deletion, subject to the existing
backup and restricted-access controls. The release stores no visitor IP/user
agent. Disable the intake flag to stop capture while retaining staff access.
