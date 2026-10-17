# ADR-1426 · Confirmation diagnostics for rate and deadline alerts

Date: 2026-10-16
Status: Accepted

## Context

Rate and deadline watch conditions already require sustained warning/recovery and
fresh observations, but their progress exposes only condition state and evidence.
Detailed confirmation entries currently cover receipt-budget conditions, leaving
operators unable to see pending sustain, baseline waits or outbox blockage for
rate/deadline alerts.

## Decision

Project all selected rate/deadline conditions into the existing confirmation DTO.
Reuse the local warning/recovery counters and timestamps, preserving receipt-budget
behavior. Show independent warning/recovery progress, fresh sample requirements,
resume waits and completed proof blocked by notification capacity. Add current
condition-specific evidence reasons and growth baseline waits without changing
condition state, timers, incidents, alerts or checkpoint contents.

Prioritize condition timing/order/value failures over otherwise eligible read
metadata. Do not present checkpoint timestamps, baseline reads or absent loss times
as fresh metric proof or durable loss history. Treat remaining time as a snapshot
at the last observation, not an autonomous countdown or promise of notification.

## Consequences

Go clients can explain why each total or reason-specific alert has not opened or
resolved using the existing progress callback/result. No new RPC, endpoint,
migration or external notification destination is required. Diagnostics remain
local, deep-copied and non-authoritative for business completion. Existing alert
and resume semantics are unchanged; Node/Python API contracts remain unchanged.

See [phase, freshness and queue semantics](../event-delivery-confirmation-progress.md).
