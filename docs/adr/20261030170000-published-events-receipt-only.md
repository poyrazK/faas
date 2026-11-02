# ADR: A published event's receipt is its only copy

Date: 2026-10-30
Status: Accepted
Related: [ADR 20261023215834](20261023215834-event-plan-caps.md) (receipt windows and storage budgets), ADR-075 (audit-log retention), [ADR 20261029093000](20261029093000-event-ga-gate.md) (event GA gate)

## Context

Every publish inserts the envelope into `events` (`EventStorageAppend`,
the object-storage notification and API-discovery writers). An `AFTER
INSERT` trigger, `enqueue_event_fanout`, then copies it into the receipt in
`event_fanout_outbox`. The receipt is pruned when the plan's window ends
(1, 3, 7 or 14 days) and is charged to the account's storage budget. The
`events` row was never read again, and only the 90-day audit-log
retention removed it (`pkg/eventretention`).

So every customer payload stayed in the shared control-plane table for 90
days, outside the plan's window and its storage budget. At the GA target
of 50 events/s that is about 4.3 million rows, roughly 9 GB, a day. The
SOC 2 audit log is `audit_log`, which stays append-only; `events` is not
it.

## Decision

The owner chose (2026-10-30) that the receipt is a published event's only
copy.

- `enqueue_event_fanout` runs `BEFORE INSERT`. For `event.published` it
  checks the envelope, writes the receipt (or refuses an identity conflict,
  as before) and returns `NULL`, so no `events` row is written. Every other
  kind is inserted as before. The function body is unchanged except its
  last line.
- A data migration removes the published rows earlier releases left in
  `events`, one day at a time.
- The in-memory store keeps no `events` row for a published event, and the
  API-discovery writer no longer tells from the insert's row count whether
  its event fanned out.

## Consequences

- Customer payloads live only as long as the plan's window and its storage
  budget. One row per publish is no longer written, a write saved.
- An older apid's API-discovery write no longer sends its own fanout
  notification. The routing sweep picks the receipt up on its next tick
  (5 s). Every other writer is unaffected, and N and N-1 run side by side.
- The account export used to carry published envelopes in its audit trail,
  as a side effect of the `events` rows. Exporting the account's retained
  receipts is a follow-up.
