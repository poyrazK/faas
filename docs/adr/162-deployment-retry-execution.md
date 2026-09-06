# ADR-162: Execute deployment retries through the build queue

Status: Accepted

## Context

ADR-117's retry endpoint inserted a pending deployment and seeded its displayed
stage, but did not enqueue a build or notify imaged. All six accepted stage
choices could therefore remain pending indefinitely. The pipeline does not
persist reusable per-stage checkpoints: node-local build output is replaced by
the rendered root filesystem, which contains the original deployment identity.
Copying it to a new deployment would not safely resume the requested stage.

## Decision

Source retries use the normal source publication and durable build queue path.
Republish the original build's retained source object under the new build ID
before creating work; use the original spool archive when no retained object
exists. Queue creation and the building status transition remain atomic with
cancellation. Notification failure is tolerated because builderd polls the queue.
Publication or queue failure must not produce a successful API response.

Image retries notify imaged through the normal image deployment path. A failed
notification returns an error and marks an unqueued retry failed. Image deployment
notification crash recovery remains a separate limitation of that existing path.

The failed attempt remains intact. The retry preserves its source inputs,
overrides, sidecars, workflows and full-rootfs settings. Parent-app activity is
checked under a row lock at insertion; current source-size plan limits apply.

The API continues to accept the closed stage vocabulary. Until reusable stage
checkpoints exist, retries rebuild prerequisites from the original source or
image and rerun all security checks. `stage_state.current` reports the actual
starting stage, `source_download`; additive `retry_requested_stage` and
`retry_restart_reason` fields explain the earlier restart. CLI and dashboard show
the actual starting point and explanation. This amends ADR-117's claim that a
stage hint alone resumes execution at that stage.

## Consequences

Accepted source retries now represent durable work, including when a local spool
archive has been removed but its source object is retained. Rebuilding costs more
than resuming a verified checkpoint; this change does not claim that optimization.
If all copies of the source are gone, the user must upload it again. Future stage
resumption needs immutable, identity-aware artifacts and explicit prerequisite
validation before it can skip any work or security gate.
