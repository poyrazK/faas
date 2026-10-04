# ADR-491: Atomic regional runtime quota write fences

Date: 2026-10-04
Status: Implementation in progress; allowance activation and native qualification pending.

## Context

Scheduler and vmmd authority reads (ADR-489/490) reject execution after quota
journal acceptance, but a grant can arrive between a read and a durable launch
write. Capacity reservations need a common serialization boundary with grant
acceptance and retirement before any regional allowance can activate.

## Decision

Instance creation, resurrection, runtime identity changes, serving publication,
warm promotion and migration publication serialize with the regional quota
journal. MemStore uses the journal mutex. PostgreSQL row triggers take the same
app advisory transaction lock as journal mutations and refresh the head lookup
after waiting. Admission writes require read-committed isolation; fixed MVCC
snapshots are refused because they can miss a head committed during a lock wait.
Identity reparenting locks both app roots in canonical order. The row triggers
precede existing resource and identity row triggers; explicit SQL transactions
with incompatible lock ordering may abort on deadlock and must retry.

Every existing quota head remains frozen, including retirement-first tombstones.
This change activates no allowance. Rows created before journal acceptance remain
visible. Same-identity telemetry, pure running-to-warm pause, quiesce, drain,
cancellation and terminal cleanup remain available. A terminal row or deletion is
not evidence that a VM, paused resident or in-flight boot has actually stopped.

App-task claiming excludes frozen apps so their queued intent cannot starve
independent apps. The claim update and transition to running also take the journal
lock through a database trigger, closing acceptance races after selection. The
SQLC claim response retains all immutable deployment, schedule, exclusive owner,
failure-policy and lease pins without rereading the row. Expired queued tasks
still cancel; existing leases can renew while cancellation and teardown run.

Durable serving seats and physical host pins, signed delivery, compute-owner
transport, explicit runtime quota consent, stable operator keys, global billing
and all-selected-region ingress remain required. Scheduler and vmmd reads remain
necessary: the database fence alone cannot stop an already issued runtime request.

## Validation

Run shared MemStore/PostgreSQL contracts for resident creation, warm resume,
publication, migration retention, task selection, cancellation and teardown.
Exercise direct SQL bypass attempts, real advisory-lock waits with grant commit
and rollback, fixed-snapshot rejection, and empty/populated migration downgrades.
Run existing quota, app-task, state conformance and scheduler regression tests with
the race detector. Native Linux x86_64 KVM lifecycle and leak acceptance remain
required before runtime activation or drain proofs.
