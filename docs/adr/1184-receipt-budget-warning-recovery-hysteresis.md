# ADR-1184: Receipt-budget warning recovery hysteresis

Status: Accepted
Date: 2026-10-13
Related: ADR-1182, ADR-1183

## Context

Remaining-slot warnings clear on the first fresh sample at their warning boundary. New commands and expiry can move usage across that boundary repeatedly, creating noisy incident transitions.

## Decision

Add optional ReceiptBudgetRecoveryRemaining and ReceiptBudgetRecoverySustain to the CLI/Go watcher. CLI flags are --receipt-budget-recovery-threshold N and --receipt-budget-recovery-sustain DURATION, both requiring --watch and an enabled remaining-slot warning. A positive recovery threshold must be at least the warning threshold and at most 128. Zero threshold uses the warning boundary when recovery sustain is enabled. Sustain accepts existing 100ms–24h bounds and defaults to 30 seconds when recovery is enabled. Both options zero preserve the prior immediate-clearing behavior; the full-budget duration alert remains independent.

The used-slot metric keeps its warning threshold limit-warning_remaining+1. Recovery uses inclusive maximum used slots limit-recovery_remaining. For warning below 16 and recovery at 24 or more remaining slots, open at usage >=113 after warning confirmation and resolve only at usage <=104 after recovery confirmation. A confirmed incident stays firing in the middle band and while recovery confirmation is pending. Recovery requires at least two strictly advancing fresh observations and its configured local duration; a sample outside the recovery range resets the recovery timer. Opening and recovery timers are independent and never persisted.

Retain existing evidence validation, freshness, sample-gap, confirmation-loss, incident IDs, sequences, queue reservations, persistence ordering and acknowledgments. Invalid or unavailable evidence closes confirmation, not the incident as a successful recovery, and resets both timers. A resumed historical incident remains unknown in the middle band; it regains firing only after fresh warning confirmation, or resolves after fresh recovery confirmation. Downtime never counts toward either duration.

Persist recovery_threshold (maximum used slots) and recovery_sustain_seconds on conditions and alerts. Checkpoint version 2 is required exactly when a condition enables recovery policy. Version 1 remains the format for watches without recovery policy, preserving prior selections. Older readers reject version 2 rather than ignoring a new policy. Validate integer disjoint recovery boundaries, bounded durations, matching alert policy, bounded raw usage and the inclusive recovery criterion for resolutions. Allow a version-2 firing condition below its opening threshold during the middle band or pending recovery. Resume must match both opening and recovery settings; changing policy requires a new checkpoint rather than modifying an existing incident.

## Consequences

Recovery hysteresis reduces boundary flapping but deliberately delays recovery notification. A recovered budget is advisory and slots can be consumed immediately afterward. No controls, quota, retention, route or schema changes are introduced. Node/Python health models remain unchanged; the shared lifecycle watcher is the CLI/Go implementation.

CLI and Go SDK builds, generated CLI reference and static lifecycle/evidence/checkpoint/source-mirror checks qualify implementation. No local tests are added or run under the user's constraint; runtime qualification remains for CI. Prior compliance registry approvals remain pending.
