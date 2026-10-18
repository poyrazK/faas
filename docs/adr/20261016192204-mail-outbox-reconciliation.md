# Reconcile an existing internal producer-mail admission

- Status: accepted for an internal experiment; activation remains disabled.
- Date: 2026-10-16
- Builds on: [producer outbox](2026101803-mail-producer-outbox.md), ADR-982 apid ownership.

## Context

An apid notification handoff can lose its acknowledgement after maild commits
admission. Automatic idempotent retries usually recover that response, but
exhaustion or expiry can leave a retained failure beside the original maild
message. Operators need to record the observed admission without creating mail
or changing recipient custody. A database restore can remove evidence, so a
missing record cannot prove nonacceptance.

## Decision

Add the exact default-off `FAAS_INTERNAL_MAIL_OUTBOX_RECONCILIATION_ENABLED` gate,
in addition to master, operations and producer-outbox gates. A metadata-only
`mailctl outbox admission --id UUID` lookup uses the existing protected maild
operator socket and credential. It derives the saved key and namespace/producer
scope, verifies sender, deadline, recipient count and admission time, and exposes
safe receipt IDs and blockers. It reads no message body or replay fingerprint.

The apid executable gains a local `mail-outbox reconcile` operator mode. It
requires an explicit owner-database connection and namespace, opens no listener,
loads no mail credentials and runs no migrations. Domain implementation stays in
`pkg/apid/mailoutbox`. maild remains a read-only observer of producer intents;
mailctl retains no database access. Local database authority authenticates the
write, while actor/reason/evidence remain operator assertions.

An additive migration adds positive, monotonically incremented intent revisions
and a separate apid reconciliation audit. Applying takes a request-ID fence,
locks account then intent, then takes the shared maild recovery fence. Acquiring
recovery after the rows avoids waiting on a dispatch that is calling maild while
holding that fence. The owner re-reads the receipt in its own PostgreSQL
transaction, checks the expected revision, message UUID and current/saved
recovery generations, and atomically acknowledges the intent with an audit.
Active claims, global recovery holds, quarantine and cancelled/suppressed or
already acknowledged intents refuse a new operation. An expired claim may settle
under the row lock; a stale worker cannot send afterward. A confirmed earlier
admission can settle a failed/expired intent without changing its deadline.

Acknowledgement cannot admit or resend mail, refund attempts, extend expiry,
change producer/recovery identity, release suppressions or alter maild recipient
state. Existing delivery and custody controls retain authority. Missing or
mismatched admission evidence leaves the intent untouched for external review.

Audits persist a hash of the complete request, opaque intent/message IDs,
revisions, previous state, recovery ID and operator assertions. Identical saved
request-ID replay returns the original result, including after intent/account
erasure; changed arguments conflict. No recipient, content, replay key or
credential is retained. Rollback refuses any used audit. Disabling the gate
preserves revisions and history, and performs no reconciliation activity.

## Validation

Disposable PostgreSQL and the real private maild handler exercise a committed
admission followed by response loss, producer exhaustion, receipt lookup and
owner acknowledgement, leaving one SMTP handoff per original message. Other
tests exercise private authentication, default-off behavior before configuration
reads, concurrency, stale revisions/leases, recovery changes during lock waits,
audit rollback, saved-request replay and erasure. Live delivery qualification and
activation remain separate.
