# Gregale customer support backend

This checkout contains a reconstructed native support backend plus target policies,
team routing, customer reopening, reviewed redaction and engineering handoffs. The
previous uncommitted support code could not be recovered. Attachments, incoming
email, knowledge articles, incident linking, dashboard screens and external issue
creation are not implemented by this change.

Support is cataloged as preview and hidden from customers by default. Use the
existing `customer_support` platform flag to allow selected account IDs during
rollout. Staff routes have their own session and roster checks. The published GA/
beta SDK spec excludes this preview; use the HTTP contract below when enabled.

## Customer API

Use `/v1/support` for personal cases or `/v1/orgs/{org}/support` for an organization's
cases. Organization access requires current owner/admin membership. All access
requires a verified active account. Sessions or unrestricted admin-scoped account
keys are accepted; organization keys must match the route's organization, and
personal keys can access the owner's personal scope. Narrow app, project, platform
customer and hosting credentials are rejected. Personal and organization case
collections remain separate, including when an organization is personal.

| Method | Suffix | Behavior |
| --- | --- | --- |
| POST | `/tickets` | Subject, category, body, optional priority and identifier-only context |
| GET | `/tickets` | Scoped list; `status`, `search`, `cursor` |
| GET | `/tickets/{ticket}` | Public metadata and customer reopening eligibility |
| GET | `/tickets/{ticket}/messages` | Public conversation; numeric `after` cursor |
| POST | `/tickets/{ticket}/messages` | Reply with `expected_revision` and `body` |
| POST | `/tickets/{ticket}/close` | Resolve the current episode with `expected_revision` |
| POST | `/tickets/{ticket}/reopen` | New episode, with `expected_revision` and explanatory `body` |
| GET/POST | `/tickets/{ticket}/satisfaction` | Current eligible episode; submit `episode_id` and rating 1–5 |

Writes return HTTP 202 after committing, with a durable receipt. Use a UUID
`Idempotency-Key`, retained for retries. Reusing a key with a different actor,
operation, resource or decoded request returns 409. Successful retries return the
original receipt even after the reopening window expires; current authorization
is always required. Bodies are UTF-8, maximum 10,000 bytes; subjects 200 bytes;
requests 65,536 bytes. There are at most 1,000 messages per case, 20 new cases per
scope per rolling day and 100 ordinary case mutations per rolling hour. Pages
contain at most 50 items. Limits are in `pkg/api/limits.go`. Context app/deployment
UUIDs must belong to the scope; no secrets, logs or source code are collected.

## Staff API

All private endpoints start with `/v1/admin/support`. API keys are rejected.
Reads require an MFA-completed session and current enrolled/verified/active staff
access. All POSTs also require a fresh five-minute step-up and trusted control-plane
origin. An `FAAS_ADMIN_EMAILS` operator without a roster entry bootstraps as manager;
explicit agent or revoked entries override bootstrap. Managers should provision
another manager before reducing their own role.

| Suffix | Access and request |
| --- | --- |
| GET `/tickets` | Staff queue; `team_id`, `assignee_id`, `status`, `search`, `cursor` |
| GET `/tickets/{ticket}` | Private ownership, skills and saved episode clocks |
| GET `/tickets/{ticket}/messages` | Includes private notes |
| GET `/tickets/{ticket}/events` | Metadata audit with numeric `after` |
| POST `/tickets/{ticket}/messages`, `/notes`, `/close`, `/reopen`, `/pause`, `/resume` | `expected_revision`; `body` for messages, notes and reopening |
| POST `/tickets/{ticket}/triage` | `expected_revision`, `priority`, `team_id`, `assignee_id`, `required_skills`, `reason`; optional `route` or manager-only `override_capacity` |
| GET/POST `/staff` | Staff directory; managers save `account_id`, current `revision` (0 for create), `role`, `profile` |
| POST `/availability` | Self only: profile `expected_revision`, `available`, optional `away_until` |
| GET/POST `/teams` | Read teams; managers save team configuration |
| GET/POST `/policies` | Managers inspect/save target policies |
| GET `/events` | Manager configuration audit with numeric `after` |
| GET `/report?from=…&until=…` | Manager episode compliance report, RFC3339 timestamps |
| GET/POST `/tickets/{ticket}/engineering` | Private engineering record |
| POST `/tickets/{ticket}/engineering/preview`, `/publish` | Manager-reviewed public update |
| POST `/tickets/{ticket}/redactions/preview`, `/apply` | Manager-reviewed content removal |

Managers can mutate any case; agents can mutate their assigned cases, and claim
an unassigned case through triage assigning themselves. Queue membership does not
grant customer access. Freeform handoff reasons are audit metadata and must not
contain secrets or conversation content. Configuration changes are revision checked
and audited. All effective writes require UUID idempotency keys; creating a bounded,
expiring preview does not create a permanent receipt.

## Policies, calendars and reporting

A policy contains `id` (UUID), `revision`, `name`, `enabled`, `order`, optional
`plan`, `category`, `priority`, `team_id`, and target seconds:
`first_response_seconds`, `next_reply_seconds`, `resolution_seconds`,
`reopen_window_seconds`, `customer_reopens_max`, and `calendar`. The first enabled
match in order, then UUID order, wins. Empty conditions match any value. The
fallback is 24-hour initial response, 24-hour follow-up, 72-hour resolution,
seven elapsed days to reopen, and three lifetime customer reopenings.

Calendars use `timezone` (IANA), `weekdays` (Sunday=0), `start_minute`, `end_minute`
and `holidays` (`YYYY-MM-DD`). An empty weekday list means continuous service.
A working calendar has one local daily window; overnight shifts require a future
extension. Targets range from one minute to thirty working days' worth of seconds;
reopening is elapsed time up to thirty days and at most ten reopenings. Calendar
search is bounded to 3,660 dates and 128 holidays. Policy edits and priority triage
never silently reset an existing clock. Reopened episodes use the original policy
revision and fresh budgets, even if the global policy has changed or been disabled.

The first public staff reply satisfies initial response. Later customer messages
start follow-up clocks; repeated messages retain the oldest unanswered deadline.
Private notes do not satisfy either. Staff can explicitly pause resolution after
replying and while no customer question awaits a response. Customer replies resume
resolution automatically; staff may also resume explicitly. Pauses count working
time and never erase a previously breached target. Repeated resolution is rejected
and cannot extend reopening eligibility. Satisfaction is bound to a genuine episode.

Reports group episodes started in `[from,until)` by saved policy/revision, plan,
priority and category. They show current outcomes as of the report time, completed
met/breached and pending separately, follow-up samples, and paused business seconds.
A resolved episode without any staff response counts as an initial-response miss.
Windows are at most 90 days and 50,000 episodes; shorter windows are required above
that bound. The report is a current cohort view, not a historical as-of reconstruction.

## Teams and capacity

A team has UUID `id`, current `revision`, `name`, `enabled`, `members` (staff UUIDs)
and optional `fallback_id`. Fallback chains are acyclic and at most five teams.
A profile has `available`, `capacity` (1–1,000), `skills` (up to eight lowercase
slugs), optional `away_until` and `calendar`. The roster supports at most 100 staff,
50 teams and 50 policies. Granting roles requires active, verified, MFA-enrolled
accounts. Routing skips stale/revoked, off-duty, unavailable, underskilled or full
staff and uses least utilization with a stable UUID tie break. No candidate leaves
the case owned by its original team and unassigned. Availability changes do not
silently move existing ownership.

Triage can set `route:true` to choose an assignee through the selected team's
fallback chain, or name an eligible assignee explicitly. A manager capacity
override is recorded and never bypasses current staff role, MFA, skill, on-duty
or membership checks. Handoff audit records retain old and new ownership.

## Reviewed redaction and engineering

Redaction preview: `{expected_revision,message_id,reason}` or
`{expected_revision,engineering:true,reason}`. Reasons are `secret`, `personal_data`
or `other`. A preview returns exact affected message IDs, case revision, hash and
expiry. Whole messages/notes with identical text in the same case are included.
Apply uses `{preview_id,expected_revision}` with a new idempotency key. Approvals
are actor bound, expire after 15 minutes and fail if the case changed. Original
text is replaced with `[redacted]`, removed from current search, and all case
previews are deleted. Identities, timestamps, redaction flags and metadata audit
remain. A reproduction redaction removes that private field. Independently copied
or edited paraphrases need separate review; backups, external exports and browser
copies need their own retention/removal procedures.

Engineering saves `expected_revision` (case), `revision` (engineering, 0 to create),
`owner_id` (current eligible staff), `severity` (`low/normal/high/critical`), `status`
(`investigating/accepted/in_progress/fix_ready/resolved/cancelled`), `reproduction`
and optional plain HTTPS `issue_url` without credentials/query/fragment. The URL
is never fetched and no issue is created. Changing engineering status never
closes the customer case.

Preview a customer update with `{expected_revision,engineering_revision,body}`.
Publish with `{preview_id,expected_revision}` and a new key. A manager reviews the
literal body and publishes exactly that text; case and engineering revisions must
still match. Private reproduction, owner and issue URL never appear in customer
DTOs or webhooks. There are at most ten live previews per actor, cleaned in bounded
batches once a minute.

## Optional provider-neutral webhook

Configure both `FAAS_SUPPORT_WEBHOOK_URL` (HTTPS) and
`FAAS_SUPPORT_WEBHOOK_SECRET` (at least 32 bytes); omit both to keep delivery off.
Bad partial configuration fails startup. The backend works without a helpdesk
provider. The adapter sends only:

```json
{"id":"event-uuid","ticket_id":"ticket-uuid","kind":"reply","version":1}
```

Event kinds are creation, public reply, resolution, reopening and reviewed
engineering publication. Headers: `X-Gregale-Event-ID`, `X-Gregale-Timestamp`
(Unix seconds), `X-Gregale-Signature: v1=<hex HMAC-SHA256>`. Verify HMAC of the
ASCII timestamp, `.`, and the exact raw JSON body; validate timestamp freshness
and deduplicate by event ID. Receivers use authorized APIs to obtain content;
webhooks never carry it. Redirects are rejected, requests time out after ten
seconds, leases are fenced and deliveries retry up to ten claims. Delivery is at
least once; inspect `support_outbox` for `failed` rows. There is no replay UI yet.
Webhook configuration is process-local and starts delivery only for events queued
while enabled; it does not backfill old events. This change was not tested against
a live external helpdesk.

## Deployment and recovery

Apply `20261111130000001_support_service.sql` before deploying apid. It adds twelve
support tables without changing workload state. Support writes serialize under one
bounded advisory transaction lock; reassess this strategy before high-volume
operation. Back up support rows with the main database; restoring a database also
restores its retained content, so redaction tombstones/removal procedures must be
reapplied as required. Support rows are operational and never copied by environment
cloning. Permanent request receipts should not be pruned independently of cases.
No migration imports the missing previous implementation or reconstructs customer
history. The source branch and commit are the recovery point for this rebuild.
