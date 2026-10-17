# ADR-20261017142138: Durable internal subscription-ended confirmations

## Context

A verified subscription termination currently clears its provider binding,
changes the account to Free, updates compatibility mirrors and sends a
confirmation in separate steps. A failure can leave a partial downgrade or lose
the email. A delayed delivery must not downgrade a newer subscription. The
existing Free-plan transition also clears streaming opt-ins, mirrors the plan
to the personal organization and invalidates the edge's account cache. Dunning
status is preserved, and non-payment revocations receive no voluntary notice.

## Decision

Add exact independent default-off
`FAAS_INTERNAL_MAIL_SUBSCRIPTION_ENDED_OUTBOX_ENABLED=0` across apid, maild and
private reconciliation. Only verified Stripe, Paddle and Polar termination
ingresses select it, after account resolution and invoice projection but before
the legacy expiring replay claim. The shared billing dispatcher uses the same
owner. Selected failures cannot fall back to inline mail. Disabled setup reads
only the exact own flag. There is no new listener, customer mail API, SDK method,
provider credential or meterd command.

The domain locks namespace admission, the current account and the exact
provider-qualified identity. It requires bounded, nonempty event/customer/
subscription handles. Stripe's signed subscription-deleted object supplies its
own ID when the invoice-style subscription field is absent; legacy normalization
and default-off behavior remain unchanged. The current provider customer must
match. A paid account with a missing current binding stays retryable; a different
nonempty subscription is a stale event and causes no downgrade. A Free account
without a subscription receives no backfill.

Each verified delivery has a permanent `(provider,event_id)` receipt bound to
its account and SHA-256 customer/subscription handles. A separate per-account,
provider and subscription-hash business receipt records each actual termination.
Replays cannot reapply the transition after a later subscription or plan cycle,
including distinct delivery IDs for the same already-ended subscription. Raw
customer/subscription handles are not retained in these receipts. Account erasure
cascades both. Reusing an event ID with different facts fails closed.

An applicable termination atomically clears the exact qualified binding, writes
Free and the compatibility customer/subscription cache, clears streaming opt-ins,
mirrors plan/customer/binding to the personal organization, saves both receipts
and, for an active paid account with no overdue/deletion anchor, saves an
encrypted `subscription_ended` intent. Other provider-qualified identities remain
untouched. Account status, overdue and deletion timestamps do not change.
Non-payment, operator-suspended, deletion-pending or already-Free accounts receive
no voluntary confirmation. Existing quota and VM lifecycle enforcement remain
with their existing owners.

The existing `account_plan_changed` app notifications execute through
transactional `pg_notify` with the plan update. A crash after commit cannot lose
this invalidation, and redeliveries cannot repeat it. Billing audit retains its
existing best-effort semantics after the business commit, using the actual saved
previous plan, status and end time. Persistence, mirror/streaming failure,
capacity exhaustion, recovery hold or final flag withdrawal rolls back paired
writes. Recovery holds gate notice-producing transitions; non-mail terminations
retain their billing semantics.

A separate monotonic `accounts.subscription_mail_revision` tracks plan, address,
status, overdue/deletion anchors and legacy provider/subscription cache changes.
A trigger on qualified identity insert/update/delete advances the same account
revision, covering other-provider changes and delete/reinsert cycles. Both guards
remain active with issuance off. Explicit counter changes advance rather than
rewind it. Unrelated password, MFA and quota-warning stamp changes do not cancel
confirmations. All platform identity writers lock the account first, including
`UpsertBillingIdentity`; operator identity edits must follow this order too.

The sealed payload contains the actual original paid plan and the database end
time. AAD binds account, recipient, sender, namespace, producer, template/version,
post-transition subscription revision, intent, recovery generation, reader key
and original expiry. The frozen V1 body retains the existing Free-plan explanation.
Retries retain `subscription-ended:<intent UUID>`, content and end time. Expiry
is 24 hours after the saved transition. Apid holds dedicated current/previous
payload readers under their historical verification identity names. No origin
or new credential is needed.

Apid validates current active/Free state, empty legacy subscription, no overdue
or deletion anchor, recipient and exact subscription revision before handoff.
Maild repeats metadata checks under an account share lock through durable SMTP
submission start. Plan/address/status/binding cycles, re-subscription, another
provider binding, deletion and erasure that commit first invalidate stale mail,
including admitted messages. Maild reads only leaf sqlc metadata and does not
write account, provider or producer-outbox state. Later changes cannot recall
SMTP custody; submitted/uncertain messages are never automatically resent.

## Consequences

Apply the additive migration before opting in. Units and examples retain zero.
Own gates independently cover issuance, claims, marked transport, admission,
delivery, reconciliation and audit replay. Enabled readiness checks schema,
both lifecycle triggers and pending reader coverage. Shared suppression, budgets,
retry leases, expiry and recovery quarantine apply. Terminal settlement erases
recipient and ciphertext while opaque reconciliation audit survives. Down refuses
any used intent, delivery receipt, business receipt or audit history.

Rollback pauses pending durable mail and restores the legacy path for future
events. Previously ended accounts do not create replacement inline confirmations.
Re-enabling resumes only still-current unexpired notices. Keep existing reader
coverage while pending payloads remain. No activation, deployment, credential
enrollment or real email is part of this change. Live qualification remains
separate work.
