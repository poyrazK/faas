# ADR-20261017115753: Durable internal overdue-account suspension notices

## Context

Meterd advances overdue accounts to suspended, performs its scheduler/notifier
follow-up and then sends email inline. A crash or exhausted mail retry can lose
the notice after the state transition. Its old account/template key also spans
multiple overdue periods. Apid owns the notification outbox and its dedicated
decryption readers; meterd must not receive those readers or write maild state.

## Decision

Add the independent exact default-off
`FAAS_INTERNAL_MAIL_ACCOUNT_SUSPENDED_OUTBOX_ENABLED=0` capability. Meterd
delegates only the suspension transaction to apid through narrowly scoped routes
on apid's existing loopback operator listener. There is no new listener, public
mail endpoint, customer API-key permission or customer SDK method. A dedicated
service credential authorizes readiness, suspension and exact follow-up
completion. It must differ from producer and Safe Deploy credentials. The file
path `FAAS_INTERNAL_MAIL_SUSPENSION_TOKEN_PATH` is read only with the own flag
on. The daemon client accepts only literal loopback HTTP origins with an
explicit port, disables proxies/redirects, bounds bodies and applies a five-second
timeout. Neither credentials nor database errors enter responses or logs.

The command contains account ID, expected original overdue timestamp and a
request UUID. It accepts no recipient, message, transition time or deadline.
Apid locks namespace admission then the current account. Database time must
have passed the existing seven-day threshold; current status, overdue anchor
and absence of a deletion request must still match. An active account, changed
period, operator suspension or pre-existing legacy suspension creates no mail.

For a real transition apid takes the shared recovery fence and atomically
commits suspended status, the existing billing-mail revision advance, pending
payment-failure cancellation, encrypted `account_suspended` intent and a
per-account/per-overdue-period command receipt. Failed persistence, recovery
hold, capacity exhaustion or gate withdrawal rolls these changes back. No
selected command falls back to a legacy transition or inline mail. The receipt
prevents duplicate ticks and old commands from suspending a restored account
within the same period. A new overdue period permits a new notice.

The sealed payload contains only original database suspension time and original
overdue time. AAD binds account, recipient, sender, namespace, producer,
template/version, post-transition billing revision, overdue anchor, recovery
generation, intent, reader key and original expiry. The frozen V1 body retains
the original suspension time and 21-day deletion deadline. Delivery expires at
the earlier of 24 hours after suspension or that deletion deadline. A delayed
tick beyond that deadline can still enforce suspension, while its notice
expires without delivery. Retries cannot extend either deadline.

Apid checks current suspended status, unchanged address and overdue period,
absence of deletion and exact billing revision before handoff. Maild repeats
those metadata checks under account/share before durable SMTP submission start.
Recovery, deletion, address cycles or status cycles that commit first cancel
the stale notice, including one already admitted to maild. The existing revision
trigger protects saved bindings with issuance off. After submission start
commits, custody cannot be recalled or automatically replayed.

The receipt also leases meterd's existing scheduler/notifier follow-up for five
minutes. Only one request UUID owns a live lease. The same request can retrieve
its lease after a lost reply; a new tick can resume an expired lease while the
same dunning suspension remains current. Meterd rechecks its gate, performs the
existing lifecycle notification, unchanged instance-parking fanout/reason and
billing notification, then acknowledges the exact intent/lease. Completion is
idempotent for that lease and retains its opaque proof. A lost response or
restart cannot enqueue a second email. Follow-up is at least once across crashes;
the existing scheduler operation is idempotent. Individual parking/notification
failures retain their existing best-effort behavior. The later 21-day deletion
transition and its mail remain separate.

The own flag independently gates command issuance, worker, marked transport,
maild admission/delivery and private reconciliation/audit replay. Meterd checks
the parent mail gates and probes the private authenticated route at enabled
startup. Apid checks schema, lifecycle trigger and pending reader overlap; maild
checks metadata schema and trigger. Constraint deparsing is materialized after
its exact schema/relation filter, so unrelated schema churn cannot break family
readiness. Only apid reads dedicated current/previous
credential identities. No origin is required for this template. Shared
suppression, budgets, retry leases, expiry and recovery quarantine apply.

## Consequences

Apply the additive migration before opting in. All units and examples keep the
flag at zero. Enabled capacity or credential failure leaves a new suspension
retryable rather than committing it without its notice. Flag rollback pauses
pending handoff, delivery and private command/follow-up access; base maintenance
can still expire and erase payloads. New timer work returns to its legacy path.
Review already-committed pending follow-up before rollback; re-enabling can
resume it after its lease expires. Changed recovery generations require operator
review. No historical backfill or automatic uncertain SMTP resend is added.

Terminal outbox settlement erases address/ciphertext. Account erasure cascades
intent and command/follow-up receipt; opaque reconciliation audit survives. Down
refuses used notice, receipt or reconciliation history. No deployment, flag
activation, credential enrollment or real mail is part of this increment. Live
delivery qualification remains separate work.
