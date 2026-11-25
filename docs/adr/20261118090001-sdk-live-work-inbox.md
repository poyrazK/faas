# ADR: Refresh authorized work inbox snapshots over private realtime channels

Status: Accepted (SDK preview)

## Context

A caller with an import ID can already read its retained work inbox. Dashboards
and agents need to follow the same record while a reviewer or provider changes
its state, including after connection loss.

## Decision

Bind each watch to one application-defined business record and an authenticated
principal. Use short-lived private-channel tickets and verify current record
access on each ticket and snapshot request. Reuse the inbox's receipt and
authorization checks to render snapshots.

Realtime frames contain only a version, kind, opaque resource ID, and change ID.
They mark state dirty. Subscribe before reading, refresh after reconnects and
retention gaps, and fall back to a bounded periodic read while connected.
Discard in-flight reads when the connection generation changes. Suppress
duplicates and enforce watch deadlines and read budgets in both SDKs.

## Evidence

The Node and Python tests share snapshot cases and exercise current read
authorization, reconnects, duplicate hints, and independent targets. The CSV
example uses local PostgreSQL, the private realtime protocol fixture, and a
browser for human review. It follows a pending cleanup and an uncertain refund
through the same persisted inbox.
