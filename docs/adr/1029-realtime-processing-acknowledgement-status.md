# ADR 1029: Backend-visible channel processing acknowledgements

Status: accepted for preview implementation; migration and runtime validation pending.

## Decision

Reuse the existing principal + named durable subscription + channel identity.
Record the latest advancing protocol ACK separately from the resume checkpoint.
Initial baselines, load refreshes, duplicate/stale ACKs and explicit resets do not
produce processing events. Reset clears the latest acknowledgement. Existing
checkpoints are not backfilled because their origins cannot be reconstructed.

Expose a read-only, account/app-scoped status endpoint under the existing history
preview, MFA and read scopes. Status queries neither create nor refresh consumers.
Sequences are decimal strings. Missing or inactive-for-30-days checkpoints return
404, even before physical cleanup.

An AFTER UPDATE PostgreSQL trigger atomically captures an app-scoped webhook event
and recipient snapshot with each advancing ACK. Only enabled app webhooks explicitly
selecting `realtime.channel.acknowledged` receive events; catch-all subscriptions do
not opt in. No channel binding is required: these receipts contain identity and
sequence metadata only, never message payloads. Existing signed delivery, retries
and attempt history apply. The memory store mirrors this behavior.

## Meaning and limits

An ACK is an authenticated consumer assertion. The SDK sends it after its message
handler succeeds. A custom client can acknowledge without completing work. This
cannot provide exactly-once processing, transactional side-effect guarantees or
human read receipts. Filtered subscriptions and initialization/reset baselines make
it unsafe to infer processing of every earlier message from a watermark.

Status retains only the latest advancing ACK, not a per-message receipt ledger.
Webhooks let backends maintain their own sequence/event receipt history; delivery
is at least once and may be out of order. Consumers sharing an identity also share
one checkpoint; use distinct subscription names for independently tracked devices.
Ticket-only subscriptions and SSE have no named durable ACK path and are excluded.

Apply the migration before deploying the changed cursor writer. Runtime tests and
migration execution are deferred at the user's request.
