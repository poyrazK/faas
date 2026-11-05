# ADR-20261105111902: Managed PostgreSQL restore planning and bounded recovery

- **Status:** accepted and implemented; native production qualification remains separate
- **Date:** 2026-11-05
- **Owner:** apid managed PostgreSQL, schedd and pgproxyd
- **Amends:** [native PostgreSQL](1027-native-managed-postgres-on-firecracker-database-nodes.md) and [live-login recovery](20261105102924-managed-postgres-live-login-dataset-recovery.md)

## Problem

A native restore copied the source's current major even when its point preceded
an upgrade. The scheduler correctly required the historical major, leaving an
unusable target reservation. Proxy backend authentication could also block past
its setup deadline and retain pooled capacity. Live-login recovery repeatedly
selected the same first twenty failures, preventing later databases from being
repaired.

## Decision

Before reserving an ordinary restore, an optional provider observation resolves
the major valid at the requested point. Only that specification field may
change. The target catalog row and subsequent restore requests retain the
resolved major, including when it is a retiring major. Existing reservations
replay their stored specification without consulting a new source history.
Explicitly captured source definitions must already agree with the historical
major; recovery does not rewrite a caller's frozen definition.

The native scheduler exposes its persisted major spans and an explicit
completeness marker as additive internal protobuf fields. Native planning uses
the same exclusive upper boundaries as `VM.MajorAt`. An older scheduler that
reports no upgrade can establish an unchanged major. An upgraded source without
complete history is deferred before reserving quota; the control plane never
guesses its historical major. This observation does not create a VM, choose a
new provider resource or change the scheduler's restore protocol.

Proxy setup binds blocking backend reads and writes to cancellation until
`ReadyForQuery`. Cancellation closes the stream and releases reserved pooled
capacity. Successful setup detaches the cancellation hook before returning the
stream, so session lifetime stays independent of setup lifetime. The direct
client handshake also bounds credential lookup and backend setup. Stream
creation and its initial routing frame obey their setup context too.

Live-login discovery orders untried databases first, then the oldest durable
attempt. Each bounded worker entry commits its attempt before provider I/O,
separately from login publication. A provider error or publication rollback
therefore cannot pin the first page. The existing locked eligibility checks,
sealed credentials, authorization receipts, live keys and replication slots
retain their ownership rules. Live-provider calls use the configured provider
timeout.

## Rollout and validation

Apply expand migration `20261105111902000` before the apid upgrade. Older apid
instances ignore the new attempt table. Deploy the updated scheduler before
relying on historical restores of upgraded native databases; protobuf readers
that do not know the new fields continue reading their existing runtime fields.

Regressions cover native service restores across an actual scheduler upgrade,
frozen specification replay, rejected plans before reservation, scheduler history
boundaries, stalled direct and pooled backend setup, stream lifetime after
successful setup, and durable login-recovery fairness across failures and store
restarts. These local tests do not replace native hardware qualification.
