# ADR: Webhook backlogs are capped, secrets rotate with overlap, and one account cannot fill the delivery pool

Date: 2026-11-01
Status: Accepted
Amends: [ADR-076](076-webhook-delivery.md) (outbound delivery), the inbound webhook design (ADR-1193 bindings unchanged)
Related: event-driven GA blocker hunt #6 (2026-11-01)

## Context

The 2026-11-01 review of inbound and outbound webhooks found:

- **The application outbox was uncapped.** `POST /v1/apps/{slug}/outbox`
  queued a delivery per call with no bound on an account's undelivered
  deliveries, and the dispatcher claimed deliveries of suspended, deleting
  and abuse-held accounts and of deleted apps.
- **Inbound webhooks skipped the queue caps and any rate limit.** A verified
  delivery became a queue invocation without the plan's queue depth or
  backlog bytes, and the route carries no API credential, so the per-account
  API budget never applied: a valid signature admitted any rate.
- **Rotating a secret failed deliveries.** The new secret replaced the old
  one at once. Outbound receivers still holding the old secret answered 401
  until they were updated, and the deliveries went to the dead letter;
  inbound providers still signing with the old secret were refused.
- **One account could fill the delivery pool.** Each scheduler runs 64
  delivery slots and each subscription may hold 4, so an account with many
  subscriptions to slow receivers held most of every pool, and a receiver
  that hung held its slots for the full 10-second attempt on every retry.

## Decision

1. **Outbox caps** (`pkg/api/limits.go`): an account's undelivered webhook
   deliveries (pending or in flight) are capped by count and payload bytes,
   Free 1,000 / 16 MiB, Hobby 5,000 / 64 MiB, Pro 25,000 / 256 MiB,
   Scale 50,000 / 512 MiB. An explicit outbox delivery at a cap gets
   `429 webhook_outbox_full` with the limit, the observed value and
   `Retry-After: 60`. The count runs under a per-account advisory lock and
   stops at the cap. Platform events fan out without the check and count
   toward the backlog.
2. **Deferred accounts and apps.** The claim skips deliveries of an account
   that is not active or past due, or is abuse-held, and of an app-scoped
   subscription whose app is deleted. They keep their schedule and resume
   when the account or app returns, or go with it when it is purged. Fleet
   queue health leaves them out.
3. **Inbound caps.** An inbound delivery is admitted under the app's queue
   depth and the account's queue backlog bytes, as a queue send is, and is
   refused with `429 inbound_webhook_backlog_full` and `Retry-After: 60` so
   the provider retries the same event. Inbound bodies do not add to the
   backlog-bytes counter (it carries queue payload bytes); the pending cap
   bounds them.
4. **Inbound rate.** Each apid process gives an account a token bucket of
   `InboundWebhookAcceptsPerMinute` verified deliveries a minute (Free 60,
   Hobby 1,200, Pro 3,000, Scale 6,000), refusing past it with
   `429 inbound_webhook_rate_limited` and `Retry-After`.
5. **Rotation overlap** (`WebhookSecretRotationOverlap`, 24 hours). A
   rotation keeps the replaced secret, sealed to the same seal domain, in
   `previous_secret_sealed` / `previous_signing_secret_sealed` with an
   expiry. Outbound deliveries carry a second signature made with it in
   `X-Faas-Webhook-Signature-Previous`; the Go, Node and Python verifiers
   accept a match in either header. Inbound endpoints accept a signature
   made with either secret. A rotation inside the window replaces the
   previous secret with the one it retires. Datadog deliveries, which carry
   an API key rather than a signature, and the alert wire are unchanged.
6. **Per-account in-flight cap** (`AppWebhookMaxInFlightPerAccount`, 16):
   the claim gives one account at most 16 live claims. Two schedulers
   claiming for one account at the same instant can pass it by one claim
   batch; it is a fairness bound, not a quota.
7. **Timeout cooldown** (`DefaultTimeoutCooldown`, 1 minute): an attempt
   that timed out pauses its subscription like a 429/503 with
   `Retry-After`. One recovery probe then decides whether it reopens.

## Consequences

- An application producing outbox deliveries faster than its receivers
  accept them is refused at the cap instead of growing the table.
- A provider sending faster than the plan rate, or into a full queue, is
  answered 429 and retries; no verified event is dropped by Gregale.
- A receiver can install a rotated secret up to a day later without failed
  deliveries. Receivers verifying by hand must check the new header to get
  this; ones that do not see no change.
- A slow or hanging receiver holds at most one probe slot a minute, and one
  account at most a quarter of a scheduler's pool.
- New columns on `app_webhooks` and `inbound_webhook_endpoints` (expand,
  constant defaults). An older apid or schedd ignores them and keeps the
  single-secret behaviour.
