# ADR-491: In-place retry of a failed workflow step

- Status: Accepted for implementation
- Date: 2026-10-03
- Related: ADR-081 (durable execution workflows), ADR-490 (transactional managed HTTP workflow steps)

## Context

Automatic workflow retries are bounded. Once the retry policy is exhausted, the
run becomes terminal and the scheduler will not advance it. Operators need a
way to recover from a transient dependency outage without creating a new run
and repeating successful earlier steps. This is especially useful for a
managed-operation step whose customer-side receipt has committed while Gregale
lost the response or rejected a later result/effect transition.

## Decision

Add `POST /v1/workflows/runs/{id}/steps/{step}/retry` and
`gregale workflows retry <run_id> <step_name>`. The operation resumes the
existing run and keeps its run ID, definition snapshot, original workflow
input, failed step's resolved input, and append-only attempt history. A later
dispatch appends the next attempt number; it does not erase prior execution
evidence. Reopening the same run and step also preserves the managed operation
ID defined by ADR-490, allowing the customer SDK to replay a committed receipt.

Only a terminal failed or dead HTTP step may be selected. The store locks the
run before its steps, then validates and updates them in one transaction. A
retry is rejected when the run was cancelled, another step is active or
failed, the target is a failure/timeout handler, a target handler has already
succeeded, or a downstream `depends_on` step has succeeded. Skipped dependency
descendants are reset to pending so ordinary DAG evaluation can continue.
Per-app concurrent-run quota admission uses the same advisory lock as new
workflow runs. Wait steps, callbacks, conditions, and timers are outside this
retry surface.

The retry does not promise exactly-once behavior for ordinary HTTP handlers.
They remain at-least-once and need application idempotency. Managed-operation
handlers can use the stable operation receipt to replay committed work.

## Consequences

Operators can recover a failed step without repeating successful ancestors or
changing the run's identity. Completed downstream effects and active or
independent failures are guarded because safely compensating those cases needs
a broader replay model. Attempt numbers remain cumulative across manual retry;
each request grants a new dispatch, while the manifest's automatic retry
budget continues to apply to the cumulative attempt count.

See `docs/event-driven.md` and `docs/cli-reference.md` for the user contract.
