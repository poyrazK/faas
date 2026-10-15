# ADR 1217: Native PostgreSQL read-interruption qualification

Date: 2026-10-12
Status: Accepted

## Context

Portable protocol tests verify one-shot DataRow cuts and concurrent isolation.
Native bandwidth qualification proves partial-read deadline rejection, but does
not verify deployed recovery or isolation after a deliberate connection cut.

## Decision

Add a nineteenth native case using the existing read caller, fixed 64 KiB SQL
result, and verified loopback TLS proxy in an opt-in read-cut provider mode.
Namespace-bound bearer controls arm a fixed 2048-byte DataRow cut, arm a concurrent
pair, clear, or stop. Preserve disposable opt-in and private ephemeral identities.

Require a failed partial read, complete hash-matched fresh recovery, then exactly
one failed and one complete read under a second one-shot cut. Permit two pool
clients only in this fixture mode and synchronize them after separate acquisitions
with a two-second rendezvous. A missing participant fails closed, releases its
client and joins the timer. Require a completed pair receipt and a final fresh
successful read; requests serialized through one client do not qualify isolation.

Capture two positive cut records, exact attempts/TLS counts, stable identity,
joined connections/buffers/timers, and independent backend retirement in a separate
run-bound proof. Prefix evidence records a local flush, not exact peer receipt;
portable receiving protocol tests continue to verify exact prefix delivery.
No row payload, SQL, credentials, schema changes or migration role are introduced.

Raise the gate to nineteen cases and verdict version 10. Local real PostgreSQL
tests exercise the deployed sources, missing-participant cleanup and native proof
validator, while designated native x86_64 KVM qualification remains required.

## Consequences

The native gate tests truncated-read correctness, fresh-connection recovery and
one-shot isolation across independent clients. The dedicated external database and
PID-baseline backend-retirement prerequisites remain unchanged.
