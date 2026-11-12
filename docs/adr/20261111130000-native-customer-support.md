# ADR-20261111130000: Native customer support with reviewed operations

Date: 2026-11-11
Status: Accepted

## Context

Gregale customers need scoped assistance and staff need ownership, target clocks,
reopening, sensitive-content removal and a route to engineering. The earlier
uncommitted support implementation is unavailable in this checkout. This change
reconstructs a native backend foundation and adds these five workflows; it does
not claim to restore every feature of the missing implementation.

## Decision

The customer API is a `customer-support` preview, released through the existing
`customer_support` platform flag for selected accounts. Staff endpoints retain
their independent private access controls.

Apid owns support intent in `pkg/apid/support`, using PostgreSQL transactions and
sqlc. Personal cases belong to an account; organization cases require current
owner/admin membership. Verified, active accounts may use admin-scoped account
or organization keys or sessions. App, hosting, platform-tenant and project-limited
credentials cannot access the service. Private staff routes require an enrolled,
MFA-completed session and an explicit agent/manager roster entry; an allowlisted
operator without an entry bootstraps as manager. An explicit revoked entry takes
precedence. Private writes require a fresh five-minute step-up and trusted origin.

Each mutation carries an actor/resource/request-bound UUID receipt, checked after
current authorization and before changing revision or relative-time validation.
Receipts contain identifiers and revisions, never conversation content. Their
`request_hash` is a non-reversible SHA-256 digest rather than a sealed secret; its
bytea column is explicitly recorded in the seal-domain checker baseline for review. A bounded
transaction advisory lock serializes support writes, including staff configuration,
capacity admission and reviewed applications. This is a deliberate low-volume
tradeoff; partitioned admission needs equivalent atomic capacity and replay fences.

Ordered policies match plan, intake priority and category. Tickets retain the exact
policy revision and calendar. Target edits affect future cases; ordinary triage does
not reset saved clocks. Business calendars use IANA timezones and real elapsed time
inside local working windows, including DST and holidays. Only an explicit wait
for the customer pauses resolution; initial response and follow-up targets remain
separate. Genuine reopening starts a new episode with the saved policy budgets;
previous episode metrics and satisfaction remain historical.

Enabled teams route to eligible staff by skills, on-duty availability, spare capacity
and least utilization, then a bounded fallback chain. Managers administer role,
capacity and skills; agents may change only their own availability. Handoffs retain
previous/new ownership and reason. Existing ownership is not silently reassigned
when someone goes off duty.

Managers review expiring, actor/revision-bound previews before whole-message or
engineering-reproduction redaction. Identical conversation copies in the same case
are included. Text is stored only in current message or engineering rows; search
reads those current rows. Redaction preserves identity, timestamps and audit
metadata, removes all case previews, and never stores the removed body in a receipt.
It does not erase database backups or external copies.

Engineering escalation is a private record with owner, severity, reproduction,
status and an optional HTTPS issue link. Links are not fetched or posted externally.
A manager previews and explicitly publishes literal customer update text, bound to
both case and engineering revisions. Engineering status changes never close the
customer case or disclose reproduction automatically.

An optional signed HTTPS webhook outbox publishes metadata-only event envelopes,
with stable event IDs, lease fencing and bounded retries. No customer body, subject,
private note, issue link or reproduction text is sent. Receivers deduplicate event
IDs and verify HMAC over the timestamp and exact body.

## Consequences

This is a backend service with a public customer API and private staff API. It does
not add a dashboard, mail ingestion, attachments, knowledge base, incident merging,
provider issue creation or automatic historical data recovery. Those integrations
need their own implementation and acceptance evidence. Support rows are operational
and excluded from environment cloning. Deploy the additive migration before apid.
See `docs/customer-support.md` for the actual contract, limits and operations.
