# ADR-605: Native qualification for Customer Job Operations

## Status

Harness implemented with admission closed — 2026-10-06. Dedicated native KVM
execution and leak receipts are pending.

## Context

ADR-602 and ADR-603 have portable state/API coverage. They require evidence
that a real guest receives the scheduler's proof, prepares private results,
and completes through the native task exit channel. Passing portable tests or
compiling a metal package cannot establish that evidence.

## Decision

Add four native Customer Job Operations scenarios to the existing real Jobs
harness. Use real PostgreSQL and apid, schedd, imaged, vmmd and meterd
subprocesses. Seed customer intent and source objects; never synthesize task
claims, VM instances or task completion. Guest result preparation remains
separate from the host-confirmed business outcome.

The guest calls the production tokenless Job reporting routes through a
per-instance test-only Firecracker vsock HTTP relay to loopback apid. The
relay accepts only that operation's runtime routes, forwards the guest's
scheduler-issued proof unchanged, and cannot mint capabilities or settle
execution. Its socket has the native Firecracker lease owner and mode 0600.
A private fixture route releases a holding guest after fault injection. A
lost file acknowledgement occurs after the real API commits a private copy;
the source then disappears and the guest looks up the retained receipt.

This relay qualifies native proof transport, host task settlement, durable
operation state and private-file preparation once the lane executes. It does
not qualify public DNS/TLS/HTTP routing, guest upload to a production object
provider, or a production provider's own reliability. The source is a seeded
HTTP S3 fixture. Platform result storage uses the acceptance host's configured
backend so cold boot retains access to staged bases and scan sidecars.

Add a blocking `customer-job-operations-only` native lane. Derive its entire
selected test set from the scenario file, refuse missing/skipped/failed
scenarios, and retain the native runner's host preflight, lock, service
restoration and leak checks. Each scenario waits for native Job instance
cleanup, stops its daemon owners and asserts zero native resource leaks.
Fixture result copies are removed using their account-bound storage metadata.
The tests also remain in the full suite's Jobs phase; its existing nonblocking
exception does not apply to this dedicated lane.

## Validation and rollout

Portable fixture contracts, race checks, shell lane/verdict contracts, workflow
budgets and metal compilation can run without KVM. Record their results
separately from hardware acceptance. Production preview policy stays closed
until native receipts and the remaining ingress/provider/fleet qualification
are reviewed. The [qualification guide](../ops/customer-job-operations-native.md)
defines coverage, limits and the evidence to retain.
