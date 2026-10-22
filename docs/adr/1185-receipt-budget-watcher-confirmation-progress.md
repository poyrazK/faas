# ADR-1185: Receipt-budget watcher confirmation progress

Status: Accepted
Date: 2026-10-13
Related: ADR-1182, ADR-1183, ADR-1184

## Context

Receipt-budget warnings can be waiting for opening confirmation, held in a recovery band, or waiting for recovery confirmation. Operators need to distinguish these states and understand lost confirmation without treating elapsed downtime as observed evidence.

## Decision

Add optional confirmations to the local delivery-health watch result for selected receipt-budget used-slot and full-duration metrics. Expose metric, phase, reason, fresh sample count, required sample count, elapsed confirmation seconds, required seconds, remaining confirmation seconds, latest accepted observation time, and the most recent confirmation loss reason/time. Phases include unknown, clear, warning_pending, firing, recovery_pending, resume_pending and notification_blocked.

Derive progress from the existing local opening/recovery timers and strictly accepted observations. Elapsed time ends at the last accepted observation, never at serialization time; output between polls does not count as a new observation. Remaining duration is only max(required-elapsed, 0), not an ETA or guarantee. A nonzero required sample count identifies an active confirmation window; zero indicates no active timer. A firing hysteresis incident with no recovery window explains that recovery has not been confirmed. A resumed incident starts with no fresh samples and no fresh observation timestamp.

Record local diagnostic reasons for unavailable evidence, missing/stale/future observations, invalid values, repeated or out-of-order observations, and sample gaps. Preserve existing alert reason codes and transitions. Store diagnostics only in watch results, never checkpoint conditions or alert histories. Resume ignores diagnostic timers and past diagnostic counts. Checkpoint versions, selected-policy matching, outbox reservations, acknowledgment rules and persistence ordering remain unchanged.

Go callbacks and final JSON results receive confirmation details. Human CLI final output prints them; optional --confirmation-progress prints them on text stdout between observations, after any required checkpoint persistence. Lifecycle alerts remain on stderr, including --alerts-json output. The live text option requires --watch and cannot combine with --json, which continues to produce one final JSON result. The diagnostics add no alerts, sequences, mutations or notification integration.

## Consequences

Operators can distinguish waiting for fresh evidence from pending warning/recovery durations and understand why confirmation was lost. The output describes the last observation; evidence and remaining duration can change on the next poll. Older servers still produce unknown when budget metadata is missing. Checkpoint formats remain versions 1/2 according to the existing recovery policy. No endpoint, state schema, migration or compliance registry entry is added.

CLI and Go SDK builds, generated CLI reference and static diagnostic derivation, acceptance equivalence, persistence separation and source-mirror checks qualify implementation. No local tests are added or run under the user's constraint. Runtime qualification remains for CI; earlier registry approvals remain pending.
