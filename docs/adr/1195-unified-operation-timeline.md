# ADR-1195 · Unified operation timeline

- **Status:** accepted for implementation
- **Date:** 2026-10-14
- **Owner:** apid and Operations maintainers
- **Related:** [ADR-521](521-customer-operations.md),
  [ADR-611](611-invocation-backed-event-attempt-history.md), [ADR-213](213-durable-unified-log-events.md)

## Context

One Operation's history is spread over five surfaces. Lifecycle and progress
are operation events. Each generation is an execution summary. Each HTTP
attempt is a row in the invocation attempt history, which no operation route
exposes. Workflow step attempts sit behind the workflow run API, and completion
callback attempts behind `delivery-attempts`. Runtime logs are kept per
instance and know nothing about operations.

Answering "why did this order take forty minutes" means stitching these by hand,
and the last link is missing: the instance that ran an attempt is not retained.
The invocation row keeps only the latest instance and instance rows are reaped
after parking, so a failed attempt from an hour ago cannot be tied to its logs.

## Decision

`GET /v1/apps/{slug}/operations/{id}/timeline` returns one chronological history
of an Operation. It is a read model over durable records, not a new ledger.
It uses the read scope and MFA like the other account operation reads.

**Entries.** Four sources merge into typed entries:

| kind | source | `at` |
|---|---|---|
| `event` | operation events (lifecycle, progress, artifacts, recovery) | `created_at` |
| `attempt` | invocation attempt history of HTTP executions | `started_at` |
| `step_attempt` | workflow step attempts of workflow executions | `started_at` |
| `delivery_attempt` | completion callback delivery attempts | `started_at` |

Attempt entries share one shape: execution, generation (HTTP only), step
(workflow only), attempt number, outcome (`running`, `succeeded`, `retry`, `failed`,
`dead_letter`, `cancelled` or `unknown`), HTTP status when known, error detail,
start, finish and next attempt.

**Paging.** Each source is read in its own durable order after its own
position: events by sequence, HTTP attempts by (generation, attempt), step
attempts by (start, step, attempt), deliveries by (replay generation, attempt
number). Step attempts need no generation in their key or their entry:
recovery resumes the same workflow run, whose attempt numbers continue across
generations, and an operation workflow is one linear chain, so its run starts
one step attempt at a time. A page merges the sources by `at`, breaking ties
by the kind order above. The opaque cursor carries every source's position, so
pages never skip or repeat a record, even while the Operation runs. A single
time cursor was rejected because a row that commits after the read, with an
earlier timestamp, would be skipped.

The timeline is not a follow stream. An attempt entry is a snapshot at read
time: a running attempt reads as `running` and is not re-emitted when it ends.
Live followers keep using the gapless events route; the timeline is for
investigation.

**Placements.** A new `customer_operation_attempt_instances` row records the
instance that ran each HTTP attempt. It is written by the existing fenced
per-attempt instance stamp, in the same statement, so a placement exists only
for an attempt that held a live lease. The first placement of an attempt wins.
Rows cascade with their execution.

**Logs.** An attempt with a placement carries a log window: instance, start,
finish (absent while running) and the UTC archive days the window touches, at
most seven. The correlation is named `instance_window`: these are the lines
that instance wrote during the attempt. An instance that serves concurrent
requests interleaves other requests' lines; request-exact correlation needs the
runtime to tag lines with operation identity, which this ADR does not do. The
CLI reads the windows through the existing archived-log route, so log access
control and plan retention stay in one place.

Workflow step attempts have no placement. Steps reach the app through the
gateway's synthetic dispatch, which chooses the instance; schedd never learns
it. Their entries carry timing and outcome without a log window.

**Retention.** An expired Operation returns 410, as other reads do. Events can
expire before the Operation: the response sets `events_expired` and still
returns attempts and deliveries. Attempt history keeps at most 30 days, so old
attempts can leave the timeline before the Operation does.

**Audience.** The route is account-only. Attempts, error detail, instances and
logs are owner diagnostics; platform tenants keep the events route.

**CLI.** `gregale customer-operations timeline OP --app SLUG` prints the merged
history. `--all` follows cursors, `--json` prints pages, and `--logs` reads each
attempt's archive days and prints the lines inside its window.

## Consequences

- One route answers what happened, in order, across retries, recoveries and
  callback delivery, and points each HTTP attempt at its logs.
- One new table, written on a path that already writes per attempt. No event
  types change, so tenant event streams are unchanged.
- Log windows are approximate on instances with concurrency above one, and the
  response says so.
- Page size reuses `OperationHistoryPageDefault`/`OperationHistoryPageMax`;
  `OperationTimelineLogDatesMax` bounds archive days per attempt.

## Rejected alternatives

- **Writing attempts and deliveries as operation events.** It would copy rows
  that already have durable owners, make event streams far larger, and expose
  owner diagnostics to tenants who read events.
- **Embedding log lines in the response.** Archives are object-stored, lag the
  live ring and are retained per plan. Pointers keep the route cheap and leave
  log authorization with the log routes.
- **Recording the instance on `invocation_attempt_history`.** That table serves
  every async invocation. The placement belongs to the Operation and should
  cascade with its execution.
