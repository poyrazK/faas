# ADR: Typed live views of Jobs and file pipelines

Status: Accepted (SDK preview)

## Context

Typed Jobs and file pipelines have durable run receipts and authoritative Jobs
status/results, but application dashboards poll independently. The SDK already
has private subscription tickets, retained realtime delivery and reconnect/gap
recovery. Worker-emitted completion cannot establish native task success.

## Decision

Add an authored, versioned snapshot contract shared by Node, browser and Python.
Realtime messages invalidate snapshots. They carry only resource identity and
a typed change kind; they never advance counts, terminal state or report access.
Subscribe before reading, resnapshot on reconnect and gaps, bind to one accepted
run, serialize reads, coalesce pending invalidations, and fence cancelled work.
React observes the same lazy Node/browser controller. Python exposes an owned
async iterator using the existing recovery coordinator and WebSocket decoder.

The application authorizes both snapshot reads and short-lived exact-channel
tickets. Auth identity changes create a new controller and clear the old view.
Application revocation must also call the native ticket/session revocation API.
Terminal state comes from native Jobs; report readiness additionally requires
verified results and a materialized report. No signed file URLs or platform
credentials enter snapshots or local browser persistence.

An optional bounded server bridge observes native Jobs while a viewer lease is
active and publishes retained invalidations. The bridge shares work within one
process, introduces no platform state machine, and recovers through existing
durable application receipts. Multiple replicas can emit duplicate hints. This
does not promise delivery of every task transition, exactly-once notification,
or browser-independent business side effects.

## Consequences and evidence

Existing SDK callers and platform component ownership remain unchanged. Browser
polling moves to bounded shared backend observation until native Jobs offers a
push source. Shared contract fixtures cover both SDKs; focused tests cover
authorization, reconnects/gaps, duplicate/reordered hints, completion/report
verification, cancellation, deadlines and limits. The CSV local journey uses
real PostgreSQL, Node/Python workers and Chrome with protocol fixtures for
storage, scheduling and realtime; deployed preview acceptance remains separate.
