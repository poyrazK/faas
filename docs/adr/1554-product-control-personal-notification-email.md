# ADR-1554 · Product Control personal notification email

- **Status:** accepted for internal implementation
- **Date:** 2026-10-17
- **Decision:** Add personal opt-ins for work outcomes and help replies above
  the existing workspace inbox, with a bounded durable outbox and an independently
  disabled email transport. Develop against a private local mail sink.

## Consent and recipient authority

Inbox read state remains shared. Email preferences belong to one person and are
bound to the owning account, product app, tenant, Auth app, organization and Auth
user. The browser supplies only category booleans and an expected preference
version. It cannot supply an email address, user, tenant, recipient grant or
delivery reference. The live customer middleware and an authenticated Auth
self lookup establish that binding. Mutations require the portal's HTTPS Origin.

The Auth runtime adds a default-off `AUTH_PRODUCT_CONTROL_EMAIL_ENABLED=1`
protocol, migration `0007_product_email_recipients.sql` and two narrowly scoped
operations. An authenticated, active, non-anonymous user with a verified address
can consent for their own current organization. Auth issues an opaque random
recipient capability bound to that organization, user, product and exact address
at consent, expiring after 30 days. Only its SHA-256 hash stays in Auth. A worker
can resolve that capability through the distinct `Product-Recipient` authorization
scheme to obtain the current role and still-verified address. It cannot log in,
enumerate users, act as an administrator or change any membership. Changing the
address, banning/deleting the user, expiration or membership removal stops
resolution. Membership deletion cascades the grant; rejoining does not restore it.
There are at most 100 active product recipient grants per Auth user.

Product Control stores the capability in an age envelope bound to the complete
subscription identifier, using existing host identity rotation support. It stores
no Auth access token, refresh token, administrator key or plaintext recipient
address. The address exists transiently during an authorized send. The private
local sink contains fixture addresses in message files and is for testing only.
Concurrent consent rotation or a lost response can leave an older grant invalid;
delivery then fails closed and the customer can save their current choices again.

## Queue and current access

Preferences use compare-and-swap versions. Each category starts at opt-in time;
enabling or renewing expired consent excludes older updates. The outbox uses the
inbox's retained source projection, not a new event journal: several changes to
the same source between scans can collapse to its latest state. Inbox read/unread
choices do not affect email. Scans retain per-subscriber cursors, and the worker
advances its subscriber cursor even when one Auth realm is unavailable.

Migration `20261017110000000_product_control_email.sql` owns the preference and
delivery tables. SQLC generates the small `pkg/productemail/sqlc` adapter; both
Postgres and memory repositories implement the same contracts. Each tenant has
at most 500 personal preference records and each subscription retains at most
1,000 delivery records in a rolling 30-day window. The worker scans at most 25
subscribers and processes at most 25 deliveries per cycle, with one delivery lease
at a time. A source revision and subscription determine a stable delivery UUID
and provider idempotency key. Queue retries cannot create a second row.

Immediately before each external attempt, the worker resolves Auth authority,
checks the current active account, owned realm, tenant mapping, current role and
product permissions, then rechecks source retention/revision and durable
preferences/lease. It cancels obsolete or denied updates. Auth/transport outages
retry with exponential backoff and provider Retry-After, up to six attempts and
12 hours from enqueue. Expired leases can be reclaimed; older attempt results
cannot overwrite a newer attempt. Unsubscribing cancels queued deliveries and
prevents the next send check. A request already in flight can still complete;
there is no transaction spanning Auth, platform Postgres and an email provider.

## Delivery truth and release switches

The default transport is `disabled`. `local` writes private (0600) deterministic
JSON message files in an explicit private (0700) directory and records **captured**.
It sends no email. The initial live transport reuses the existing Resend adapter
directly; it requires both `FAAS_PRODUCT_CONTROL_EMAIL_MODE=resend` and
`FAAS_PRODUCT_CONTROL_EMAIL_SEND_ENABLED=1`. Its stable idempotency key and bounded
retry lifetime stay within the adapter's 24-hour provider window. Log/noop and
Postmark cannot be selected: their success or retry semantics cannot satisfy this
outbox contract. Nested generic retry/silent-suppression wrappers are excluded.

States are queued (including an active lease in customer views), captured,
provider_accepted, failed and cancelled. Provider acceptance is not delivery to
an inbox. No webhook receipt, bounce handling or delivered status is introduced.
The ledger records safe reason codes and attempts; worker logs contain no
recipient, grant, provider error body or private source content. Message text
contains a fixed update label, product and reference, and a link to the trusted
API origin's customer portal. Reading the portal remains subject to normal Auth.

All portal assets and private preference/history routes remain under
`FAAS_PRODUCT_CONTROL_ENABLED`, default off. Disabling the feature hides every
method before authentication and stops the worker. No credential, deployment,
flag change or real provider call is required for local implementation tests.
Deployed Auth migration/flag setup and real acceptance are later release work.

See the [email runbook](../ops/product-control.md#personal-notification-email)
and [ADR-1551](1551-product-control-customer-notifications.md).
