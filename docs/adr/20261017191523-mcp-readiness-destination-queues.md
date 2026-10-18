# ADR-20261017191523: Independent MCP readiness destination queues

## Context

ADR-20261017191522 keeps a logical recovery at the rule's FIFO head until every informed
receiver succeeds. A broken primary can therefore block subsequent failures and
recoveries at a healthy fallback. Retries must remain durable without forcing
operators to discard the old primary recovery.

## Decision

Persist independent primary/fallback progress, retry deadlines, delivery IDs and
fenced leases in each event's private `destination_deliveries` snapshot. Claims
and completions serialize app → rule → event, but HTTP dispatch does not hold
those locks. Separate receivers can own separate leases, including on the same
logical recovery. Completion locates its destination by the token and updates
only that destination. Stable event identity and immutable payloads are retained.

Preserve sequence order within each receiver role. An earlier primary event
reserves a potential fallback position until it succeeds or fails over; recovery
cannot overtake a failure that could still reach fallback. Exact pinned URL
matches also preserve order across receiver roles after configuration changes.
Fallback recovery is preferred when both receivers are eligible, but its retry
cooldown or active lease does not stop primary recovery.

When an older failed-over incident still owes primary a recovery, an unattempted
new incident with fallback enabled starts on fallback. Record the explicit
`primary_queue_blocked` reason, pin the configured destinations at first claim,
and retain sticky fallback for that incident. Its primary receives no failure
or recovery. Without a fallback policy the primary queue waits normally. Once
older primary recoveries finish, later new incidents start at primary again.

Reminder creation waits for outstanding notifications of the current incident,
so another incident's primary recovery does not suppress healthy fallback reminders.
Maintenance, fresh-evidence requirements and acknowledgements still apply.

Keep the existing rule summary and event-wide retry/abandon controls. Add safe
per-receiver queue heads and progress to existing responses and optional
`destination` selection to existing actions. A targeted action requires that
receiver's current head and event delivery version; request replay includes the
receiver identity. A targeted action cannot cancel another receiver's live lease
or completed delivery. Receipts and audit events retain the selection. Event-wide
actions retain their oldest-event and no-active-lease restrictions.

## Migration and recovery

Stop readiness evaluators, apply the additive migration, upgrade all evaluators,
then resume dispatch. Mixed old/new dispatch workers are unsupported. Empty
snapshots hydrate lazily from retained incident routing, successful receiver flags,
physical delivery references, retry deadlines and legacy lease tokens. A legacy
active claim keeps its fence; a stale completion cannot own a replacement lease.
Retiring expired primary fired claims through retry, abandonment, maintenance or
reminder suppression preserves uncertain primary receipt for later recovery.
Rollback refuses to discard materialized destination state or targeted decisions.

## Consequences

A logical recovery can remain outstanding while a healthy receiver progresses
through later incidents. The rule summary describes its oldest event; receiver
queues describe actual scheduling. `waiting_primary` is a potential fallback
reservation, not an attempted delivery. `waiting_receiver` means an older event
uses the same pinned URL under another receiver role. Neither can be retried or
abandoned through a receiver action. Independent retries remain at-least-once;
webhook consumers must deduplicate stable logical event IDs.

Queue planning reads outstanding events for one rule under its existing ownership
locks. Native paging integrations and more than one fallback remain separate work.
