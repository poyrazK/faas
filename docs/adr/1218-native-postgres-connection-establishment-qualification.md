# ADR 1218: Native PostgreSQL connection-establishment qualification

Date: 2026-10-12
Status: Accepted

## Context

Native authentication qualification stalls after verified TLS. Portable SSLRequest
fixtures establish bounded pre-TLS connection behavior, but do not prove deployed
queueing, retry bounds or recovery before a database connection has been negotiated.

## Decision

Add a twentieth native case using the existing read caller and an opted-in
SSLRequest mode of the loopback verified TLS provider fixture. Hold the validated
client-leg initial SSL response for five seconds. Preserve existing two-second
pool connection/acquisition deadlines, fixed namespace-bound bearer controls,
disposable opt-in and private ephemeral TLS inputs. No plaintext fallback exists.

Require three sequential explicit retries and three concurrent requests sharing
one pool slot. Record queue depth two and exact provider acquisition counts. Some
queued requests can expire before starting a socket, so require bounded connection
attempts and a positive canceled SSLRequest hold for every failed connection.
TLS/login counts must remain at baseline until the fault is removed.

Clear the hold, require two fresh verified TLS queries returning 42, join all pool
waiters, negotiations, sockets and timers, and independently verify backend
retirement after phases and cleanup. Keep SQL, credentials and authentication
payloads out of the separate run-bound proof. Dedicated database/PID-baseline
prerequisites remain unchanged; no schema or ledger mutation is introduced.

Raise the gate to twenty cases and verdict version 11. Local real PostgreSQL tests
exercise the exact sources and native proof verifier; designated native x86_64 KVM
qualification is still required.

## Consequences

The gate covers dependency connection failures before TLS and login, including
bounded queued requests rather than just active connections. Acquisitions and
socket attempts are distinct evidence because waiting requests can time out
without creating a connection.
