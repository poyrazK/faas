# ADR-866: Durable entity developer workflow

Status: Implemented; focused developer-workflow CI passed. Native acceptance pending.

## Decision

Provide a Node starter, pure typed registration and a separately exported in-memory
simulator. Keep the existing platform protocol and storage contracts unchanged.
The starter declares namespaces and schema versions in `gregale.entities.json`,
loads those declarations at startup, and packages an isolated validator from an
explicit file list. Shared decoders keep example handlers and validation consistent.

A declared project must pass bounded artifact checks before source upload. Checks
validate regular in-project paths, compiled module presence, packaged SDK exports,
lockfile integrity and current validator source bytes. They never execute customer
code, assert code purity or replace deployment admission and immutable bindings.

The simulator serializes by entity key, stores replay receipts, supports before/after
commit faults and private restart checkpoints, consumes manually advanced alarms,
and captures outgoing intents. It is not a provider, ownership or relay emulator.
Production endpoints do not expose its local control routes.

## Validation

Author SDK behavioral tests, an out-of-checkout packed starter install/typecheck/HTTP
suite, and Go artifact/CLI tests. A focused Node CI workflow runs the new suites;
existing changed-package CI covers Go. Local test/build execution is deferred per
user instruction. Feature gates and native KVM/GCS acceptance requirements remain.

Focused CI evidence and native acceptance boundaries are recorded in the
[release qualification report](../durable-entity-release-qualification.md).
