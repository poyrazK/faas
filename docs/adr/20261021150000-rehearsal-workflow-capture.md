# ADR-20261021150000 · Rehearsal capture of workflow starts and events

- **Status:** accepted
- **Date:** 2026-10-21
- **Amends:** [ADR-20261021090000](20261021090000-rehearsal-queue-capture.md)
- **Decision:** The run-local platform API serves `POST /v1/apps/{slug}/workflows/{name}/runs` and `POST /v1/workflows/runs/{id}/events` for contained callers. It records each request in a new evidence table, `scenario_captured_platform_calls`, and never performs it. Sealed snapshots carry these calls, and comparisons compare them.
- **Why:**
  - **Blocked calls.** Workflow starts and events go to the public API, which containment blocks. A candidate's workflow calls therefore failed in a contained run.
  - **Uncompared calls.** A candidate that started a workflow twice, or signalled the wrong run, still passed.

## Decision

### Table

`scenario_captured_platform_calls` is one table for workflow and realtime calls, distinguished by `kind`. Its rows hold:

| Column | Content |
|---|---|
| `kind` | `workflow_run` or `workflow_event`. `realtime_publish` and `realtime_signal` are reserved for the next slice. |
| `target` | What the call addressed. |
| `name` | The workflow or event name. |
| `idempotency_key` | The request's key, if it had one. |
| `message` | The compared content. |

Rows cascade with the policy and take the run's advisory lock, like the other captures. The bounds are 256 calls per run and 64 KiB per message. A larger body is kept only as an incomplete marker.

### Targets

Platform IDs are never compared. Targets are normalized as follows:

- **Workflow start.** The `{slug}` resolves as for queue sends:
  - `workload:<name>` for a member of the same run;
  - `app:<slug>` for another app of the caller's account;
  - a 404 for anything else, as in production.
- **Workflow event to a captured run.** The run is named by who started it and in what order: `run:<starter workload>/<start target>/<workflow>#<n>`, where `n` is that start's ordinal among the same starter's starts of the same workflow and target. So "the second invoice run" compares across sibling runs.
- **Workflow event to any other run.** A run outside the rehearsal must belong to the caller's account. It is named `run:<id>`; an unknown run is a 404.

### Validation and replay

Validation follows apid:

- **Starts.**
  - The body must be JSON or empty; empty means `{}`.
  - The limit is 1 MiB, with a 413 above it.
  - `Idempotency-Key` must hold 1–255 non-control bytes after trimming.
- **Events.**
  - The body is decoded strictly.
  - `event_name` is required and must not use the reserved `workflow.callback.` prefix.

Replays mirror production:

- **Start replay.** A start with the same key, workflow and input returns the first capture with `Idempotent-Replayed: true`. Another input under the same key is a 409.
- **Event replay.** An event with the same key and the same event name succeeds without a second event, whatever its payload, as production's `ON CONFLICT` does.
  - Another event name under the same key is a 409.
  - Production answers that case with a storage error instead; the clearer status is deliberate.

### Responses

- **Start.** 201, in the `WorkflowRunResponse` shape: the capture ID, the workflow, status `pending` and the input.
- **Event.** 200 `{"status":"received"}`.
- **Header.** Every captured response carries `X-Gregale-Rehearsal: captured`.

### Snapshot and comparison

- **Snapshot fields.** These are additive: `platform_capture_version` (1), `platform_calls` and `incomplete_platform_calls`.
- **Comparison.** Calls compare as a multiset per workload, kind, target and name, by canonical message.
  - Starts carry `{"input":…}`; events carry `{"payload":…}`.
  - A comparison across different platform capture versions is inconclusive.

## Consequences

- **Not executed.** Captured workflows never run, and no step, wait or callback happens. A scenario that depends on a workflow's effects must drive them from the recipe.
- **What is not checked.** The capture does not check the workflow definition, the plan gate or the concurrency quota. A start of an undeployed workflow is captured as a start. An event to an external run is not checked against that run's state.
- **Reads are not served.** Reads such as `GET /v1/workflows/runs/{id}` reach the service proxy and return 404.
- **Concurrent starts.** Two starts of the same workflow that race within one workload can swap ordinals between sibling runs. A scenario that signals one of them should start them in sequence.

## Rejected alternatives

- **One table per primitive.** Workflows and realtime need the same columns and the same seal, bound and comparison logic.
- **Executing workflows in a run-scoped runtime.** The workflow engine's claims, timers and callbacks have no rehearsal fence. That needs its own decision, like same-run queue delivery.
