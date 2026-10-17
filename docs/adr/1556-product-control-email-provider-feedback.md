# ADR-1556 · Product Control email delivery feedback

- **Status:** accepted for internal implementation
- **Date:** 2026-10-17
- **Decision:** Correlate signed provider reports with durable Product Control
  email receipts and pause the affected personal subscription after a permanent
  bounce or spam complaint.

## Receipt and report contract

Resend sends now retain the successful response's canonical message UUID.
An ambiguous successful response without that receipt retries with the original
provider idempotency key, deadline and six-attempt budget. The shared platform
mail sender's existing error-only contract stays compatible. Local captures
never acquire a provider receipt or provider-reported status. Previously accepted
records without a receipt cannot be correlated retroactively.

The private `POST /v1/product-control/email/resend-events` endpoint uses a
distinct optional `FAAS_PRODUCT_CONTROL_EMAIL_WEBHOOK_SECRET` and the existing
Svix verifier. It requires one bounded ID, timestamp and signature header, a
valid signature within five minutes and a body of at most 64 KiB. The handler
accepts no query selectors. A missing secret returns 503; invalid signatures or
recognized malformed events return 400. Unsupported event kinds return 200.
The existing default-off Product Control surface gate hides every method before
authentication. Provider authentication uses the signature rather than customer
or owner credentials. The endpoint does not use the platform billing-mail
webhook or its global address suppression.

Recognized events are `email.delivered`, `email.bounced` and `email.complained`.
Only the provider message UUID, kind, event time and permanent-bounce bit are
retained. Raw bodies, addresses, subjects, bounce diagnostics, tracking and
signature IDs are neither persisted nor logged. Report time must be within the
rolling 30-day retention window and no more than five minutes ahead. A report
matches a unique persisted provider receipt, never an address in the payload.

Callbacks can precede the send response. A globally bounded pending table holds
at most 10,000 unmatched provider/kind pairs, expiring 24 hours after their first
receipt; duplicates do not extend that time. It can contain unrelated unmatched
provider IDs but cannot change any subscription until an exact receipt matches.
At capacity the handler returns 503 so the provider can retry. Cleanup is bounded
to 1,000 rows per transaction; matching also filters expiry independently of
cleanup. Acceptance and feedback serialize through a short PostgreSQL advisory
lock, lock the delivery row and merge early reports atomically with the receipt.
Memory uses the same contract under its repository mutex.

Semantic deduplication is by provider UUID and event kind. The earliest time for
each kind is retained; a permanent bounce cannot be weakened by a later temporary
bounce. Current state has monotonic precedence: complaint, bounce, delivered,
provider accepted. Late delivery reports cannot erase a bounce or complaint.
Reports neither create attempts nor reopen a completed delivery. Processing time
and provider report time are separate. A delivered report means the receiving
mail server accepted the message; it proves neither inbox placement nor reading.
Portal, owner console and CLI show those distinctions and the report timestamp.

## Subscription pause

A `Permanent` bounce or complaint pauses the personal subscription for that
product, workspace and Auth identity. Temporary or unclassified bounces are
visible without automatically pausing. A complaint takes precedence over an
earlier hard-bounce pause. Other personal subscriptions remain independent.
Queued messages are cancelled with `recipient_suppressed`; the final worker
readiness check stops leased messages before a subsequent provider request.
An already in-flight request can still complete.

Preference saves, grant renewals, address changes and owner recovery cannot clear
the pause. This increment intentionally has no resume or force-send action; a
future recovery workflow must establish renewed authority and provider policy
before permitting a restart. The UI exposes the pause without suggesting that
saving preferences resumes delivery. Pause metadata persists with the personal
subscription, independently of the rolling message history.

Append-only migration
`20261017150000000_product_control_email_feedback.sql` adds checked pause/report
fields, a unique provider UUID and the bounded pending metadata table. SQL uses
the leaf SQLC adapter. Product Control stays default off, email defaults disabled
and real sending keeps its independent switch. Signed loopback fixtures and
disposable PostgreSQL verify implementation without provider credentials,
external messages, deployment or live provider qualification.

See [ADR-1555](1555-product-control-owner-email-controls.md) and the
[feedback runbook](../ops/product-control.md#provider-delivery-feedback-and-paused-subscriptions).
