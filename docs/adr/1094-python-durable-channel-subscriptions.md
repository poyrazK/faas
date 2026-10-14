# ADR 1094: Python durable channel subscriptions

Status: Accepted; runtime validation deferred.

## Context

The managed v2 server already supports named principal-scoped durable checkpoints
(ADR 369), but native Python sessions only use locally held channel cursors.
Restart recovery therefore requires an application-managed cursor store.

## Decision

Add optional durable_subscription to dedicated WebSocket client configuration and
to each multiplexed channel registration. Validate nonempty trimmed names within
128 UTF-8 bytes without NUL/CR/LF. Preserve client-held defaults and existing inbox
consumer behavior; reject durable channel names combined with dedicated inboxes.
Shared subscriptions explicitly select their own name independently of the
handshake transport's dedicated defaults.

Include the name in subscribe frames, validate it on subscribed, acknowledged,
resync_required and subscription_reset responses, and use the loaded checkpoint
as the stream's received baseline. Extend RealtimeRecoveryTarget with optional
resume_sequence and default-false resync_required, keeping checkpoint information
separate from the authoritative replay high-water target. The recovery coordinator
adopts a newer persisted checkpoint; newer local completed progress suppresses
repeated application effects while re-ACKing server redelivery. Explicit server
resync forces snapshot recovery even if local progress is ahead of its checkpoint.

Durable iterators wait for matching persisted processing ACK confirmations after
application effects complete, using a configurable bounded checkpoint deadline.
ACK timeout or server checkpoint-write failure reconnects within existing retry
budgets. Expose confirmed_sequence independently from local processed progress
on native sessions and multiplexed handles. Initial load and confirmed snapshot
reset also establish the server checkpoint. Replay readiness includes its final
persisted ACK; no ACK is sent before application processing succeeds.

After retention-gap snapshot restoration, reset the durable checkpoint using the
existing reset/subscription_reset protocol, confirm identity/cursor and resubscribe
before replay. Retry fresh recovery if retention moves again during reset. Live
unsubscribe never deletes durable server state. Exclude names from diagnostics.

## Consequences

One stable logical consumer can restart without a local cursor store if its
application state retains the corresponding completed work. A checkpoint cannot
restore lost application state. Endpoint/principal/name/channel define identity;
independent consumers use distinct names and concurrent processors must not share
one logical checkpoint. Effect commit and server ACK are separate transactions:
crash-window redelivery remains possible, requiring idempotency/deduplication.
No routes, grants, schema or migrations change. Static lint, compilation, SDK AST,
metadata and generator preservation/export inspection run; runtime tests remain
deferred at the user's request.
