# ADR-1181: Capacity-command receipt budget diagnostics

Status: Accepted
Date: 2026-10-13
Related: ADR-1178, ADR-1179, ADR-1180

## Context

Each subscription can retain 128 successful capacity-command receipts. Operators can inspect receipts and history, but otherwise discover a full budget only after a new command is rejected. They need advisory usage and expiry information before writing.

## Decision

Add optional capacity_command_budget to the existing read-only delivery-control status response and delivery-health capacity-pressure snapshot. Count all owned subscription receipts with retain_until after one observation time, including successful no-ops and without an outcome filter. Return app/subscription scope, observed_at, complete, retained_receipts, the existing limit, remaining_slots, full, earliest_expiry_at when nonempty, and advisory guidance. Logical expiry frees slots even before physical cleanup.

Use an aggregate SQLC query for count and minimum expiry, in the same read-only repeatable-read transaction as status or pressure. The status ownership lookup uses the existing nonlocking read target query; writes retain their existing locking behavior. MemStore aggregates under the existing status/pressure mutex and copies the earliest deadline. Reads never delete receipts, reserve slots or extend retention. Mutation responses continue to omit the optional diagnostics. No route, table, quota or migration is added.

Status prints retained usage, remaining slots and earliest expiry. Capacity preview validates complete scope, arithmetic, freshness and the shared pressure observation time. Its JSON includes an optional new_command_receipt_slot_available boolean and a receipt_budget_warning when full or unavailable. Older servers without metadata preserve existing capacity preview behavior, explicitly leave receipt-slot availability unknown, and receive no inferred zero/full values. Present malformed metadata fails validation. Existing proposal validity, projection and exit-code semantics remain unchanged.

## Consequences

The diagnostic is advisory. Concurrent new commands can consume slots, and parent deletion or expiry can remove evidence. A full budget affects new command UUIDs; known unchanged-input repeats remain available. Commands without UUIDs do not record idempotent evidence. Receipt availability does not prove write permission, MFA readiness, stable reviewed guards, live delivery capacity or execution order. Earliest expiry is an opportunity to free a slot, not a reservation or guaranteed retry time.

Builds, SDK generation/source parsing, SQLC parity, prepared SDK coverage mappings and static checks qualify implementation. No local tests are added or run under the user's constraint, and no migrations or business operations are executed. Existing pending clone-table and GET SDK mapping registry patches remain separate.
