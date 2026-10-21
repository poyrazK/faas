# ADR-20261017125602: Durable internal non-payment deletion notices

## Context

Meterd moves suspended overdue accounts to `deleted_pending` after 21 days,
then notifies lifecycle subscribers and sends email inline. A crash or mail
failure after the transition can lose the notice. Its account/template key also
spans overdue periods. The real hard-deletion deadline is the persisted
`deletion_requested_at` plus the existing account-deletion grace period, rather
than a deadline recomputed at each retry. Apid owns mail intents and dedicated
decryption readers; meterd must retain its timer/notifier responsibilities.

## Decision

Add exact independent default-off
`FAAS_INTERNAL_MAIL_NONPAYMENT_DELETION_OUTBOX_ENABLED=0`. Meterd delegates the
paired transition to apid through narrow routes on its existing loopback
operator listener. There is no new listener, public mail API, customer API-key
permission or generated customer SDK method. The dedicated file credential at
`FAAS_INTERNAL_MAIL_NONPAYMENT_DELETION_TOKEN_PATH` grants only readiness,
non-payment deletion scheduling and exact follow-up completion. It differs
from producer, Safe Deploy and enabled suspension-command credentials. Maild
receives neither this token nor apid's payload readers.

The private client accepts only plain HTTP literal loopback origins with an
explicit port, disables proxies/redirects, bounds response bodies and uses a
five-second timeout. Enabled meterd startup probes authenticated readiness.
The exact own flag is the only new configuration read when disabled. Late
enablement requires configuration and restart, with no legacy fallback when a
selected command fails. Requests carry account ID, expected original overdue
timestamp and request UUID; no recipient, body, transition time or deadline.

Apid locks namespace admission then the current account. The database clock
must have passed the original overdue timestamp plus 21 days. Current status
must be suspended without a deletion request, or a still-current deletion
created by this command. Active accounts, changed overdue periods, operator
suspensions without an overdue anchor and legacy deletion-pending accounts
receive no backfill.

A new transition atomically saves `deleted_pending`, its database deletion
timestamp, the existing billing-mail revision advance, cancellation of pending
failure/suspension notices, an encrypted `nonpayment_deletion` intent and a
per-account/per-overdue-period command receipt. Persistence failure, recovery
hold, capacity exhaustion or flag withdrawal rolls back all paired writes.
Duplicate ticks reuse the same intent. An old command cannot schedule deletion
again after restoration within the same overdue period; a new period permits
a new notice.

The sealed payload contains only the original overdue timestamp and saved
deletion timestamp. AAD binds account, recipient, sender, namespace, producer,
template/version, post-transition billing revision, overdue/deletion timestamps,
intent, recovery generation, reader key and original expiry. The frozen V1 body
derives the hard-deletion deadline from that saved timestamp and the canonical
account grace duration. Delivery expires at the earlier of 24 hours after the
saved transition or the hard-deletion deadline. A late sweep starts the existing
grace period when deletion is actually scheduled; overdue age never shortens or
extends that persisted grace deadline. Retries keep
`nonpayment-deletion:<intent UUID>`, the original body and original expiry.

Apid checks current deletion-pending status, address, overdue anchor, saved
deletion timestamp and exact billing revision before handoff. Maild repeats
those metadata checks under an account share lock before durable SMTP submission
start. Payment recovery, address/status cycles, deadline changes or erasure
that commit first cancel stale notices, including already-admitted messages.
The existing billing revision trigger remains active with issuance off. Maild
reads only leaf sqlc metadata and never writes billing or producer outbox state.
Changes after submission start cannot recall SMTP custody; submitted/uncertain
mail is never automatically resent.

The receipt leases meterd's existing lifecycle/account-deletion notifications
for five minutes. The same request can retrieve its live lease after a lost
reply; a later tick can resume an expired lease only while the saved deletion,
overdue period and billing revision remain current. Completion requires exact
account, intent and lease; exact completion replay is idempotent and retains
opaque proof. The sweep also visits deletion-pending accounts to recover this
follow-up. It makes no scheduler calls or inline mail sends on the selected
path. Notification follow-up remains at least once across crashes with the
existing best-effort handling of individual failures.

The own flag gates command issuance, worker claims, marked transport, maild
admission/delivery and private reconciliation/audit replay. Apid checks schema,
lifecycle trigger and pending reader overlap; maild checks delivery metadata and
the trigger. Constraint deparsing follows a materialized exact schema/relation
filter. Only apid receives dedicated current/previous readers under their
historical verification identity names. No origin is needed. Shared suppression,
budgets, retry leases, expiry and recovery quarantine apply.

## Consequences

Apply the additive migration before opting in. Units and examples keep the flag
at zero. Enabled outbox or credential failure leaves deletion scheduling
retryable. Rollback pauses pending handoff, delivery and private follow-up;
base maintenance can still expire and erase payloads. Future timer work follows
the legacy path. Review already-committed incomplete follow-up before rollback,
because deletion-pending accounts do not repeat the legacy transition. Re-enable
to resume eligible leases; changed recovery generations or billing revisions
require operator review. Receipts never change the grace reaper's deadline.

Account erasure cascades intent and command receipt. Terminal settlement erases
recipient/ciphertext, while opaque reconciliation audit survives. Down refuses
any used notice, command receipt or reconciliation history. Quota warnings and
subscription-ended confirmations remain separate work. No deployment,
activation, credential enrollment or real mail is part of this change. Live
delivery qualification remains separate work.
