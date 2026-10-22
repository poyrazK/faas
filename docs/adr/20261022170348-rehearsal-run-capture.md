# ADR-20261022170348 · Rehearsal capture of standalone Runs

- **Status:** accepted
- **Date:** 2026-10-22
- **Amends:** [ADR-20261021150000](20261021150000-rehearsal-workflow-capture.md), [ADR-20261022082721](20261022082721-rehearsal-captured-reads-inbox-async-invoke.md)
- **Decision:** The run-local platform API (`api.svc.gregale`) captures standalone Run starts and cancellations, and reads back the run's own captured starts:
  - `POST /v1/executions`
  - `DELETE /v1/executions/{id}`
  - `GET /v1/executions/{id}` and `GET /v1/executions`
  - `GET /v1/executions/capabilities`, for the run account's plan
- **Why:** An app that starts a disposable Run (ADR-171) failed inside a contained rehearsal: the route was blocked. The candidate's behaviour then differed for a reason unrelated to the change under test. Performing the Run is not an option either, because it would bill the account and could reach managed integrations.

## Decision

### Run starts

A start is checked like `createExecution`, against the run account's plan:

- **Entitlement:** a plan without Runs gets `403 executions_not_allowed`.
- **Request:** the same strict JSON body, the 3 MiB cap, the workflow metadata rules, `Resolve` against the plan, and the integration ID bound.

It answers `202` with a Run in status `queued`, in the public `ExecutionResponse` shape, and the Run never starts.

The capture is a platform call of the new kind `execution`. Its target is always `execution:runs` and its name is the runtime. The compared message holds:

- the runtime, profile, entrypoint, output files, resolved limits and network;
- the workflow ID, step label and normalized integration IDs;
- `source_sha256`: the source text's digest, or for a files bundle the digest of its path-ordered file digests, with `file_count`;
- `input_sha256`: the digest of the input as canonical JSON.

Source and input are digests, never values. Production seals them before they cross apid, and run evidence must not hold them in plaintext. A digest still detects a changed program or input. The workflow ID is caller-authored metadata, not a platform ID, so it compares exactly.

### Cancellations

A cancellation of the run's own captured start is captured as kind `execution_cancel`, named `cancel`, with an empty message. Its target names the start the way a workflow event names a captured run: `execution:<starter workload>/<runtime>#<start ordinal>`. So "cancel the second python312 Run" compares across runs.

It answers `202` with the start in status `cancelled`, as a queued Run cancels at once in production. Any other ID, a production Run among them, is the public not-found. A contained run cannot cancel production work.

### Reads

`GET /v1/executions/{id}` serves the run's own captured start: `queued`, or `cancelled` with `finished_at` and zero usage once the run cancelled it. As for every captured read (ADR-20261022082721), anything else is not found.

`GET /v1/executions` lists only the run's own captured starts, newest first. It takes the public `limit`, `offset`, `status` and `workflow_id` with the same validation, and `next_offset` is `-1` at the end. Production Runs of the account never appear.

`GET /v1/executions/capabilities` answers for the run account's plan from the same builder apid uses. It reports the control plane as enabled, because a contained start is captured rather than admitted, so admission is available exactly when the plan is entitled.

### Idempotency

apid scopes an execution's `Idempotency-Key` to the method and path, and replays the first response under it whatever the new body. The capture does the same:

- A start replays the first captured start under its key.
- A cancellation replays per addressed start.

A key longer than the capture column's 255 bytes is kept as its SHA-256 digest.

### Not served

| Request | Why |
|---|---|
| `artifact_inputs` | Refused with `422`. Staging one reads a production Run's artifacts or redeems a one-time grant; a captured Run has no artifacts. |
| The event stream, artifact grants | Not served. |

## Consequences

- Migration `20261022170348390` widens the platform call kind CHECK and the target CHECK (`execution:` prefix) online: the new `_v2` constraints are added NOT VALID and validated, then the old ones are dropped. Its down step refuses while captured Runs exist.
- The rehearsal network policy now carries the account plan, read by joining `accounts`.
- `platform_capture_version` stays 1. A compare from an older CLI treats the new kinds as invalid evidence and reports the comparison inconclusive. It never matches them silently.
- The execution API host flag (`executionAPIEnabled`) and integration existence are not checked; a capture never reaches either.
