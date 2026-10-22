# ADR 1216: Native PostgreSQL bandwidth qualification

Date: 2026-10-12
Status: Accepted

## Context

Portable protocol tests already verify aggregate bandwidth pacing and cancellation.
Native authentication and COMMIT qualification do not establish deployed read
behavior when a slow transfer outlives its query deadline.

## Decision

Add an eighteenth native case with the existing read caller and an explicitly
opted-in bandwidth mode of the verified TLS provider fixture. Keep the proxy on
provider loopback and preserve upstream CA/DNS verification. Namespace-bound bearer
controls select fixed response rates (256 KiB/s or 16 KiB/s), clear, or stop.

Use a fixed 64 KiB SQL result and a 1.5-second query deadline. Return byte count
and SHA-256 only after the complete query; never accept a partial read as success.
Require complete limited reads, a slower transfer rejected with 503, complete
recovery after clear, positive pacing/cancellation evidence, stable identity,
exact attempts/TLS counts and joined buffers, connections and timers.

Use a separate run-bound bandwidth proof and independent backend retirement after
all phases and cleanup. Reuse private ephemeral TLS/control inputs and the existing
PID-baseline dedicated-database contract. No ledger mutations, migration credentials,
public proxy ports, platform changes or schema fallback are introduced.

Raise the native gate to eighteen cases and verdict version 9. Local real PostgreSQL
tests exercise the deployed sources and native proof validator, while designated
native x86_64 KVM qualification remains required.

## Consequences

Gregale now qualifies throughput-related read failures and recovery in deployed
workloads. Rate evidence measures PostgreSQL protocol plaintext writes at the proxy,
not encrypted network throughput or proof of peer delivery. The fixture keeps
partial payloads and credential material out of its redacted proof artifacts.
