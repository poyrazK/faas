# ADR-20261017112923: Durable internal payment-recovery confirmations

## Context

Payment-failure warnings have durable intent and provider receipts. The
restored-account confirmation still runs separately after billing recovery and
can be lost to a crash or mail outage. An old confirmation can also wait through
another failure and recovery, at which point an active-status check alone would
make the stale message appear eligible again.

## Decision

Add the exact internal default-off
`FAAS_INTERNAL_MAIL_ACCOUNT_RESTORED_OUTBOX_ENABLED=0` capability. It is
independent of payment-failure issuance. Existing signed Stripe, Paddle and Polar
success ingress selects this path before the legacy expiring replay claim.
Selection is passed explicitly to recovery so a late gate withdrawal, missing
configuration or failed local write cannot fall back to inline mail.

Apid locks namespace admission, account/update and the exact provider customer
binding. It inserts a permanent `(provider,event_id)` recovery receipt. A receipt
must belong to the same account; replay is silent even after a later failure.
Distinct success events on an active account receive receipts without backfill
or another confirmation. Operator suspensions and voluntary deletion remain
unchanged. Receipts retain only provider, event ID, account ID and processing
time. They have no TTL and cascade on account erasure.

A real recovery takes the shared recovery fence and commits account restoration,
the personal-organization mirror, pending failure-warning cancellation and the
encrypted `account_restored` intent together. Failed persistence, a held recovery,
queue saturation or late gate withdrawal rolls the paired changes back. Pending
failure warnings free their slots before confirmation capacity is checked.
The existing credit, subscription, plan, quota and lifecycle operations retain
their ordering outside this transaction. A later quota follow-up failure can
return a retryable response after the paired commit; its saved receipt prevents
another confirmation. Polar invoice enrichment remains asynchronous on this
path as on the legacy path.

Add `accounts.billing_mail_revision`, a positive monotonic lifecycle binding.
A database trigger advances it when status, overdue timestamp, deletion stamp
or email changes, including changes through legacy billing and meterd. Other
security changes and no-op lifecycle writes retain it. Explicit revision writes
cannot roll it back. This metadata invalidation remains enforced with issuance
off, so rolling the feature flag back cannot revive previously committed
confirmations after a status or address cycle. The trigger creates no mail,
intent, receipt or network activity. Existing accounts start at revision one.

The intent binds to the post-restoration revision. Its sealed payload contains
exactly database recovery time, `restored_at`, and uses dedicated apid
current/previous credential readers under their historical verification names.
AAD binds account, recipient, namespace, producer, sender, intent,
template/version, lifecycle revision, original 24-hour deadline, recovery
generation and reader key. No origin is needed. No invoice, payment method,
amount, raw webhook or credential enters the payload.

Retries and restart preserve the frozen V1 body, original recovery time and
deadline, and `account-restored:<intent UUID>` admission key. Apid checks current
address, active status, absence of overdue/deletion stamps and exact lifecycle
revision under account/share before handoff. Maild checks the same metadata
before durable SMTP submission start. Its account lock precedes recovery and
submission locks and is held through submission-start commit. A status/address
change or erasure that wins this ordering cancels stale work, including work
already admitted to maild. After submission start commits, custody may follow
and cannot be recalled or automatically replayed.

The own flag independently gates issuer, worker, marked transport, maild
admission/delivery and private reconciliation/audit replay. Startup checks schema,
the lifecycle trigger and pending reader references. Disabled configuration
reads only its exact gate. Shared suppressions, producer budgets, leases, expiry
and recovery quarantine apply. No recovery advisory lock crosses HTTP. Terminal
outbox settlement erases recipient/ciphertext. Account erasure cascades intent
and receipts; opaque reconciliation audit survives. Down refuses any used
confirmation, recovery receipt or reconciliation history.

## Consequences

Apply the additive migration before opting in. Units, examples and the
environment contract keep the flag at zero. Flag rollback pauses pending
confirmation handoff and maild delivery; base maintenance may still expire and
erase payloads. Future webhook processing returns to the legacy recovery path
and its replay window. Keep permanent receipts and evaluate provider redelivery
before that switch. They do not order distinct provider events or backfill
historical legacy deliveries; invoice projections remain separately upserted.

This adds no public mail API, listener, customer credential, SDK or allowance.
Later meterd notices remain outside this increment. Admission is separate from
SMTP custody and destination delivery. Deployment and live qualification remain
separate work.
