# ADR-1662: Data API database timing telemetry

Status: Accepted

## Context

Gateway `upstream` latency combines request upload, transport, pool acquisition,
SQL and response preparation. Connection counts do not separate those durations.
The pinned PostgREST 14.3 engine supports `server-timing-enabled`, but its
`transaction` timer wraps `AppState.usePool`, including acquisition and execution.
SQL failures can leave the response path before timing headers are constructed.
See the pinned [response pipeline](https://github.com/PostgREST/postgrest/blob/v14.3/src/PostgREST/App.hs#L156),
[pool execution](https://github.com/PostgREST/postgrest/blob/v14.3/src/PostgREST/AppState.hs#L215),
[millisecond clock](https://github.com/PostgREST/postgrest/blob/v14.3/src/PostgREST/TimeIt.hs),
and [header rendering](https://github.com/PostgREST/postgrest/blob/v14.3/src/PostgREST/Response/Performance.hs).

## Decision

Enable the engine timing header in generated runtime configuration. Read it only
from the private loopback engine response; caller timing headers are not forwarded.
Record a version 1 `database_timings` object in the existing request log, correlated
by its gateway UUID and completion timestamp. Its source is
`postgrest_server_timing`. Accept exactly five unique phases, `jwt`, `parse`,
`plan`, `transaction` and `response`, with finite, nonnegative millisecond values
in the pinned one-decimal format. Reuse the existing 8 KiB diagnostic bound for
parsing. Duplicated, missing, additional or malformed metrics invalidate the
whole observation. Raw headers and descriptions never enter logs or public replies.
PostgREST `plan` means generation of the SQL query from the request and schema
cache, not PostgreSQL planner execution.

An accepted phase has `duration_ms` and `complete: true`: it finished before the
engine sent response headers. Downstream cancellation or a response-size limit
can interrupt gateway streaming without invalidating that completed engine sample.
Requests dispatched without engine response headers get `state: unknown` and
reason `no_response`; missing or invalid headers use `missing_header` or
`invalid_header`. Unknown observations contain no durations. Late callbacks cannot
mutate an emitted record. Requests never dispatched to the engine omit the object.
There is no cross-request cache and no reuse across errors or engine restarts.
An observed duration does not independently prove SQL commit or client receipt.

Every observation explicitly reports `sql_execution: unknown` and `pool_wait:
unknown`. The transaction duration is labelled as combined pool acquisition and
SQL, including connection/session setup, hooks, execution and result transfer.
Do not subtract gateway or engine phases to invent isolated SQL or pool timings.
No query plans, SQL literals, claims, subjects, credentials or request bodies are
needed. No extra SQL queries, connection pools, metric scrapes or background
sampling are added.

CLI diagnostics aggregate observed, unknown and absent coverage separately,
including fixed unknown reasons and per-phase completed-only p50/p95/p99.
Missing samples produce null percentiles. The same object is available for request
lookup, routes, operations and query shapes. Health reports use the retained
REST/RPC sample; timing availability does not change current readiness or exit
semantics. Legacy logs remain readable. Unknown input fields are dropped and
unrecognised timing identities, states or attribution claims are rejected.

## Consequences and validation

We gain an engine-side transaction interval narrower than gateway upstream time,
without claiming separate pool wait or SQL execution. Pinned-engine fixtures
check a slow RPC, pool contention from two occupied connections while reading an
unlocked relation, missing headers on SQL timeout, client cancellation, the
HTTP deadline, engine exit/restart and RLS-preserving recovery. Unit/CLI tests
cover header spoofing, bounded malformed/duplicate metrics, zero duration,
downstream cancellation after headers, unknown/legacy coverage, request lookup,
percentiles, health semantics and redaction.

The engine header adds per-request timing overhead. Live hosted/native load
qualification and independently instrumented SQL execution/pool acquisition
remain separate work. Existing deployments gain the feature when rebuilt from
the updated template; this change does not deploy them.
