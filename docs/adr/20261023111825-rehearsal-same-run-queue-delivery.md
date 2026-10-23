# ADR-20261023111825 · Same-run delivery of captured queue sends

- **Status:** accepted
- **Date:** 2026-10-23
- **Amends:** [ADR-20261021090000](20261021090000-rehearsal-queue-capture.md), [ADR-20261022082721](20261022082721-rehearsal-captured-reads-inbox-async-invoke.md)
- **Decision:** In a contained run that opts in with `queue_delivery: same_run`, a captured queue send addressed to a member of the same run is also enqueued as a real invocation of that member's preview app. The receiving handler is then rehearsed end to end. Every other capture stays captured only.
- **Why:**
  - Captured work never runs (ADR-20261021090000). A run whose primary sends work to a sibling worker could compare only the send, never what the worker did with it.
  - The recipe could drive a consumer only by repeating the message by hand.

## Context

The run already contains the receiving app: a member's preview app, enrolled, with its own deployment. Delivering to it changes nothing outside the run. Delivering anywhere else would run production work, so only same-run targets (`workload:<name>`) are candidates.

gatewayd-internal already writes real `invocations` rows for run members: same-run asynchronous routes go through the managed async admitter (ADR-20261017201011). schedd stays the only component that dispatches them. The seal already waits while member invocations are pending.

## Decision

### Opt-in

`RegisterScenarioTestRequest.queue_delivery` takes `""` (default) or `same_run`. apid accepts `same_run` only when all of these hold:

- the operator flag `FAAS_REHEARSAL_QUEUE_DELIVERY=1` is set;
- the run is contained;
- the run has no managed-work plan (which includes PostgreSQL faults) and no suspend/resume plan.

Those plans fence or cancel member invocations, and delivery would fight them. The response acknowledges `queue_delivery_version: 1`. The policy row stores the mode, and a CHECK enforces the same combination.

### What is delivered

A capture is a delivery candidate when it is kind `queue` (not inbox, delayed task or async invoke) and addressed to `workload:<member>`. A candidate gets one of these results, stored on the immutable capture row:

| Result | When |
|---|---|
| `delivered` | The send names the default (unnamed) queue, has no `work` or `environment`, and the member has no queue bindings or queue triggers. The generic drain then dispatches it to the member's default handler, as it would in production. |
| `unsupported` | Anything else: a named queue, a work policy, an environment, or a member whose bindings would choose the consumer. These stay captured only. Production queue selection lives in apid and is not reproduced here. |
| `overflow` | The run already delivered 64 messages (`MaxScenarioDeliveredQueueMessages`). This bounds ping-pong between members, and it makes the queue evidence incomplete, never silently shorter. |

Delivery happens inside the capture transaction, under the run lock the seal also takes. A capture exists if and only if its delivery does.

The invocation reuses the capture's ID and is an ordinary queue invocation of the member app:

- the captured business payload;
- the captured retry policy, or the app's own, capped at the plan's attempts;
- due now.

Its insert wakes schedd through the existing `invocation_due` notification. No trigger is added on `invocations`.

### Evidence

- A delivered message's snapshot entry carries `delivery: delivered` and `delivery_state`, the invocation's state when the run sealed. `unsupported` is recorded too.
- The snapshot carries `queue_delivery_version: 1`.
- The seal already reports `pending` while member invocations are pending or dispatching, so a delivered message is sealed after it settles.
- Messages the delivered handler sends are captured under the receiving workload, as any other capture.
- Comparison adds the delivery result and terminal state to each message's compared value. A delivery-version mismatch between runs is inconclusive.

### Reads

`GET /v1/invocations/{id}` of a delivered capture answers with the real invocation's state instead of `pending`.

## Consequences

- Recipes that opt in should wait for queue idle (`wait_for.queue_idle`) before checks. Retries that outlast the seal's ten-second wait leave the comparison inconclusive.
- Ordinal names (`#n` in workflow, Run and realtime targets) can swap when delivered handlers run concurrently, as already documented.
- Delivered work bills real VM time, as every rehearsal does.
- Out of this slice:
  - Delayed tasks: their due time can outlive the run.
  - Inbox messages: the envelope's platform ID and time are not captured.
  - Async invokes and workflows: their timers and callbacks have no fence.
  - Named or bound queues.

  Each needs its own increment.
- Release N−1 gatewayd captures without delivering. Its rows carry no result, so a run that opted in reports them `unsupported` rather than claiming delivery.
