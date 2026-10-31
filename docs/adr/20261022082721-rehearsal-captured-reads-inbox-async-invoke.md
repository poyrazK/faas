# ADR-20261022082721 · Rehearsal reads of captured work, inbox and async invoke

- **Status:** accepted
- **Date:** 2026-10-22
- **Amends:** [ADR-20261021090000](20261021090000-rehearsal-queue-capture.md), [ADR-20261021120000](20261021120000-rehearsal-public-api-event-capture.md), [ADR-20261021150000](20261021150000-rehearsal-workflow-capture.md)
- **Decision:** The run-local platform API (`api.svc.gregale`) reads back the work a run captured, and captures three more write routes:
  - `POST /v1/apps/{slug}/inbox`
  - `POST /v1/apps/{slug}/invoke/async`
  - the organization-scoped `POST /v1/orgs/{org}/apps/{slug}/queues/send`
- **Why:**
  - **Reads diverged.** An app that polls what it just sent — an invocation's status, a delayed task, a workflow run or an event receipt — got a 404 in a contained run where production answers. The candidate's behaviour then differed for a reason unrelated to the change under test.
  - **Writes were blocked.** The inbox facade, asynchronous invokes and organization-scoped sends were blocked by containment.

## Decision

### Reads

These reads are served only for the caller's own run. A capture of another run, or an ID the run never captured, gets the public API's not-found.

| Route | Answer |
|---|---|
| `GET /v1/invocations/{id}` | A captured queue send, inbox message, delayed task or async invoke, in the public `Invocation` shape. The state is `pending` and the payload is the captured business payload. |
| `GET /v1/delayed-tasks/{id}` | A captured delayed task, with the `scheduled_at` its creation reported. |
| `GET /v1/workflows/runs/{id}` | A captured workflow start, in status `pending` with its input. |
| `GET /v1/events/receipt?source=&id=` | A captured event, accepted and never routed: no recipients. |

Captured work never runs, so it never leaves `pending`. A candidate that waits for completion waits until its own timeout, in both runs alike.

### New captures

All three are validated like the public API.

- **Inbox.** An inbox message is a queue send, as in production.
  - The envelope is normalized: the default source is `gregale.send`, and type and data are required.
  - It is captured as kind `queue` with the content `{"inbox": {source, type, datacontenttype, data}, queue_name, environment, flag_context, retry_policy, work}`. The envelope ID and time are platform defaults or wall-clock, so they never compare.
  - The response is `202`, in `SendAppMessageResponse` shape.
- **Async invoke.** An asynchronous invoke is captured as the new queue kind `async_invoke`.
  - The rules: a deadline must be in the future, retention must be non-negative and the retry policy must be valid. The method defaults to `POST` and the path to `/`.
  - A deadline is an instant, so the capture compares only whether one was given (`"deadline": "absolute"`).
  - The response is `202` `{id, status_url}`.
- **Organization-scoped queue send.** It is captured exactly like the account-scoped route. The run bounds what it may reach, not the organization.

Retry policies are now validated on queue sends, inbox messages and delayed tasks. A delayed task with a negative retention is a 400, as in production.

### Storage

`scenario_captured_queue_messages` changes in two ways:

- **Wider kind check.** It now admits `async_invoke`. The check is replaced online in its own migration steps.
- **New `scheduled_at` column.** It is read back only, never compared.

## Consequences

- **Comparison.** Async invokes compare per workload, target and kind like queue sends, by canonical content.
- **External workflow runs.** A read of a workflow run outside the rehearsal gets a 404, although production would answer. It was never captured, and the run-local API reads no production state.
- **Still not served:**
  - realtime `publish-batch`, which is preview-gated in apid;
  - publisher-credential publishes;
  - entities `invoke`, which needs a synchronous result;
  - receipt sub-resources (attempts, replays).

## Rejected alternatives

- **Proxying reads to apid.** That would expose production rows to a contained candidate and mix real and captured state.
- **Reporting captured work as completed.** That would invent results nothing produced.
