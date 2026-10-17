# ADR-20261017105351: Durable internal payment-failure notifications

## Context

The payment-failure webhook advances an account into dunning before a separate,
best-effort email. A crash or mail outage can lose that warning. Subscription
past-due and payment-failed events race for the same status transition; the
former currently wins without sending a notice. Ingress replay claims expire
after five minutes and cannot prevent a repeated failure from re-entering
dunning after payment recovery. A queued warning can also outlive recovery.

## Decision

Add the exact internal default-off
`FAAS_INTERNAL_MAIL_PAYMENT_FAILED_OUTBOX_ENABLED=0` capability. After existing
signature verification, provider-qualified customer lookup and invoice upsert,
Stripe, Paddle and Polar select the new failure path before the legacy replay
claim. Both normalized payment-failed and subscription-past-due events require
a nonempty, bounded provider event ID. Selected calls fail retryably when the
owner configuration, schema, keys or local transaction is unavailable; they
cannot fall back to inline mail or acknowledge a partial transition.

Apid locks namespace admission, account/update and the exact billing identity,
then inserts a permanent `(provider,event_id)` application receipt. An existing
receipt must match account and normalized event type. A first active-to-past-due
transition preserves the existing `coalesce(past_due_at, clock_timestamp())`
dunning anchor, clears the suspension reason and commits an encrypted
`payment_failed` intent with that receipt. Account status serializes overlapping
failure kinds; no-op events receive their own receipts without creating a new
warning or resetting grace. Receipt/state/intent failures roll back together.
The existing shared recovery fence and pending limit apply to new issuance.

Receipts retain only provider, event ID, account ID, type and processing time.
They deliberately have no expiry: pruning them would permit re-entry after
recovery. Account erasure cascades them. They contain no raw webhook, amount,
invoice, payment method, credential or message body. They deduplicate deliveries
handled by this capability; they do not establish ordering between distinct
provider events or backfill receipts for previously processed legacy deliveries.

Enabled payment recovery reviews current status under account/update rather
than trusting the earlier webhook snapshot. The transaction preserves the
existing dunning recovery rules, clears dunning/deletion stamps, mirrors an
actual restoration to the personal organization and cancels pending payment
intents with recipient/ciphertext erasure. Operator suspensions remain intact.
The existing best-effort restored-account email and quota/lifecycle follow-up
remain separate from this transaction.

The sealed payload contains exactly the original database `past_due_at`.
Dedicated apid current/previous credential readers use their existing historical
verification environment names. No origin or payment credentials are needed.
AAD binds account, recipient, namespace, producer, sender, intent,
template/version, original overdue timestamp, 24-hour delivery deadline,
recovery generation and reader key. A checked metadata timestamp also identifies
the overdue period without decrypting mail in maild. A partial unique index
allows one payment-failure intent per account and overdue timestamp.

The frozen V1 body includes the original overdue time and absolute suspension
and deletion-scheduling deadlines. Retries, restart and lost acknowledgement
reuse `payment-failed:<intent UUID>`, the same body and the original expiry.
They do not reset the seven-day or twenty-one-day dunning clock.

Apid checks current address, past-due status, deletion stamp and exact overdue
period under account/share before handoff. Maild independently checks those
conditions for admitted warnings before durable SMTP submission start, using
only the leaf sqlc metadata projection. It acquires account/share before its
own recovery and submission locks and retains it through submission-start
commit. Recovery or erasure that wins this ordering cancels unsent work. A
later overdue period cannot revive an older warning. Missing source metadata
fails closed. The own gate is checked after lock waits and before commit.
Once submission start commits, SMTP custody may follow and cannot be recalled;
submitted/uncertain work retains the existing no-automatic-resend policy.

Independent gates cover issuer/recovery, worker, marked transport, maild
admission and delivery, and private acknowledgement/audit replay. Startup checks
schema and pending reader references. Disabled configuration reads only the
exact gate. Shared suppression, producer budgets, leases, expiry and recovery
quarantine continue to apply. No recovery advisory lock crosses the HTTP
handoff. Terminal outbox settlement erases address and ciphertext; opaque
reconciliation audit can survive account erasure. Down refuses notification,
receipt or reconciliation history.

## Consequences

Apply the additive migration before opting in. Generated units, examples and
the environment contract keep the gate at zero. Rolling back the flag pauses
pending handoff and maild delivery, while base maintenance can expire pending
payloads. It restores legacy processing for future billing webhooks, including
the legacy five-minute replay window; permanent failure deduplication requires
this capability to remain selected. Keep its receipts when rolling back and
evaluate provider redelivery before returning to the legacy path.

This adds no public mail endpoint, listener, customer credential, SDK or email
allowance. Meterd's later dunning notices and the restored-account email are not
made durable here. Admission remains separate from SMTP custody and destination
delivery evidence. Live qualification and deployment are separate work.
