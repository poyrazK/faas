# ADR: Node preparation lifecycle telemetry

- Status: Accepted
- Date: 2026-10-17

## Context

Node coordinated preparation manages local and platform leases and exposes
read-only retained-state inspection. Applications still need bounded phase,
duration and failure observations to explain live processing and feed their
existing logs and metrics without handling protected job or error objects.

## Decision

Add optional `onEvent` to prepared-operation options. Capture and validate it
before database/platform access. Admit telemetry only after the existing worker
guard succeeds. Emit started, claim acquisition, preparation start/settlement,
first renewal failure per lease, confirmed receipt/intent commit, typed unknown
commit and final joined shutdown. Successful heartbeat renewals emit nothing.

Use frozen discriminated metadata with bounded whole milliseconds from a
monotonic clock. Include callback-settlement duration without prepared data.
Translate allowlisted typed SDK errors to fixed reason literals; all other errors
map to generic failure or parent cancellation. Never expose identities, inputs,
tokens, results, arbitrary error codes or diagnostics. Event state retains only
timing, phase and fixed outcome metadata.

Invoke observers synchronously without a receiver, isolate throws and ignore
accidental asynchronous rejection without awaiting notification. Abort work
before reporting renewal failure. Preserve the original operation result/error
and commit uncertainty; confirmed local receipt/intent commit takes precedence
over late platform renewal failure or cancellation in shutdown telemetry.
Match the returned parent abort reason when classifying cancellation, so a later
abort during cleanup does not relabel an earlier failure.
Keep the existing fences, cleanup and explicit recovery policies authoritative.

## Consequences

Applications provide passive nonblocking adapters, bounded diagnostic queues and
their own exporter lifecycle. A blocking observer can still delay timers and
consume budgets. Promises returned by observers are not joined. Events may be
lost on crash and provide no durable sequence, delivery retry, restart replay or
acknowledgment proof. Retained receipt/intent evidence remains the recovery source.

The existing protected-job wrapper forwards `onEvent` for coordinated version 4
jobs without changing records, modes or job ownership. Ordinary worker methods
retain their existing API. No platform route, schema, DDL, migration or registry
update is needed. See the [event contract](../event-acknowledgment-node-telemetry.md).

## Validation

Use Node build/generation, declaration/package inclusion and static documentation
checks. No tests, assertion changes or local test runs are included. Hosted
acceptance is required before release for observer throws/rejections, event
ordering, secret exclusion, cancellation, renewal failure and commit uncertainty.
