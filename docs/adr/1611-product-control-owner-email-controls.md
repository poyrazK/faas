# ADR-1611 · Product Control owner email controls

- **Status:** accepted for internal implementation
- **Date:** 2026-10-17
- **Decision:** Add owner console and CLI delivery diagnostics plus idempotent,
  narrowly eligible recovery above the existing personal email outbox.

## Observation and boundaries

Product owners can inspect a product-wide or customer-scoped rolling 30-day
delivery window. Queue totals and oldest pending age cover every status in the
selected window, independently of the history filter or page. PostgreSQL reads
counts and history in one read-only repeatable-read transaction. Both stores
return at most 25 newest messages plus a continuation; ordering uses creation
time and delivery UUID, including ties. Cursors bind account, app, customer and
status. Every page is a fresh observation; statuses can change between pages.

The safe projection includes notification and delivery references, an opaque
subscription reference, attempts, timestamps, next attempt, original deadline
and fixed reason codes. It excludes addresses, Auth user IDs, grants, message
text, work inputs/results and provider response bodies. Entries describe each
message's latest retained state, not an attempt journal. Counts are not proof
that all interested customers were scanned or that Auth is currently healthy.

The owner API uses the existing account/app ownership checks, MFA, read scope
for observation and deploy-write scope plus verified email for recovery. The
console requires the existing owner browser session, MFA and verified email on
POST. One CSRF cookie binds the owner and product without accumulating cookies
for each delivery. The server scopes the customer/delivery and compares the
observed failed attempt. Customer sessions and tenant-scoped credentials do not gain owner
access. All routes, including unsupported methods, remain hidden before auth
when the existing default-off Product Control flag is disabled.

## Retry contract

Only a `failed` message with `provider_rejected`, one through five attempts,
remaining original delivery time, no active delay and unchanged enabled consent
is eligible. Queue eligibility is advisory: the service checks the current owned
Auth realm, recipient capability, membership/role, account, customer mapping,
product permissions and retained source revision before queuing. The worker
repeats its normal checks immediately before sending. Transient Auth/transport
failures keep their automatic backoff and Retry-After. Exhausted, expired,
cancelled, captured, provider-accepted and leased messages are not reopened.

Recovery requeues the same delivery and preserves its provider key, attempts,
preference/source revision and 12-hour deadline. It cannot reset the six-attempt
budget, extend the provider idempotency window or create replacement work. It
sends no email in the HTTP request. Disabling or losing transport readiness
prevents new recovery actions; existing receipts can still be read by repeating
the same authorized request.

The caller supplies the customer UUID and observed failed attempt. An atomic
queue change and immutable receipt keyed by delivery/failed-attempt make repeated
requests safe, including after later worker attempts finish. PostgreSQL shares
the preference scope lock and locks the delivery row, coordinating consent,
claims and concurrent retry clicks; memory uses its repository mutex. A stale
worker cannot finish a newer lease. A repeated receipt acknowledges the original
requeue and never repeats it. Receipts cascade with their delivery's retention,
and the attempt budget bounds each delivery to five owner retry receipts.

Append-only migration `20261017185613546_product_control_email_owner.sql` adds
the receipt table and owner scope index. Existing issued migrations stay intact.
All SQL uses the leaf outbox SQLC adapter. No Auth protocol change, deployment,
credentials, flag enablement or real email is necessary for local verification.
Provider acceptance remains distinct from delivered mail; webhooks and bounces
are outside this increment.

See [ADR-1610](1610-product-control-personal-notification-email.md) and the
[owner email runbook](../ops/product-control.md#owner-notification-email-controls).
