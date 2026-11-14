# ADR-20261113120824: Workflow functions

Date: 2026-11-13
Status: Accepted
Builds on: ADR-20261021204307 (code-first workflows), ADR-20261111150000
(typed function-to-function clients), ADR-20261112160000 (results of sent
function calls)

## Context

Code-first workflows give durable `step`, `sleep` and `waitForEvent` on the
ADR-081 engine, but they sit beside functions projects rather than in them. A
workflow is declared in a `workflows:` block with a `code:` entry and served
by hand with `serveWorkflows`. A workflow that calls a sibling function has
two options. It can wrap `functions.x.call()` in a step, which keeps its VM
up for the whole call under the step timeout. Or it can `send()` the call and
wait for an event it has to deliver itself. A function cannot start a
workflow except by publishing an event, and `gregale dev --local` does not
drive runs at all.

## Decision

Owner decisions, 2026-11-13: a function can be a workflow; a workflow calls a
sibling with `wf.call`, which parks the run, and a step-wrapped `call()` stays
allowed; functions start workflows with a typed `start()` that returns a run
handle; `gregale dev --local` drives workflow functions on a local driver.

1. **A function can be a workflow.** A `functions:` entry (or config in code)
   sets `workflow: true`. Planning gives the function's app a code workflow
   named after the function: manual trigger, `code.path: /`, `timeout` and
   `retry` from the entry. The function's handler takes `(input, wf)` and
   returns the output. The Node and Python adapters answer requests carrying
   `X-Faas-Workflow-Mode` by running the handler under the protocol core of
   the SDK in the function's dependencies (`@gregale/sdk-node/workflows`,
   `faas_sdk.workflows`). Without the SDK they answer a problem naming the
   missing package, and the CLI refuses the deploy first. A Go workflow
   function serves `faas.ServeWorkflows` from its `main` as it does today.
   Code-first rules, limits and failure handling are unchanged. Triggers other
   than manual can be added in the entry with the existing `trigger` syntax.

2. **`wf.call(name, input)` parks the run.** It is a new operation kind,
   `call`, carrying the callee's name and input. Applying the response, the
   engine resolves the name against the workflow app's `function_callables`
   (an unknown name fails the operation without retry). In the same
   transaction it enqueues an async invocation of the callee: POST `/`, the
   typed-call marker, the run's deployment scope, sender = the workflow app.
   It records the run and operation on the invocation's `invocation_sends`
   row (`workflow_run_id`, `workflow_op`, expand-only columns), stores the
   invocation id in the operation descriptor, and parks the operation. The
   callee keeps its own retries and dead letters. When the invocation reaches
   a terminal state, the transaction that records it settles the operation:
   success stores the callee's output, anything else fails it with the
   invocation's error. It then wakes the run with the existing fenced setter.
   No VM is held while the call runs. The SDK returns the output, or throws
   `WorkflowStepError` with code `function_call_failed`. The idempotency key
   for the callee is the operation's (`workflow/<run-id>/<op>`), sent as
   `Idempotency-Key`. A step that wraps `functions.x.call()` stays allowed
   for quick calls and is an ordinary step.

3. **Functions start workflows: `functions.x.start(input)`.** The guest
   platform API (`api.svc.gregale:10081`) gains
   `POST /v1/workflow-runs {function, input}`. The caller is resolved from its
   address as for every service call. The name must be one of its callables
   and a workflow function. The run is created through the same admitted
   path as a manual start, with the plan's run quotas and
   `max_concurrent_runs`, on the callee's deployment scope. An
   `Idempotency-Key` header makes retries safe. The starting app is recorded
   in `workflow_run_starts(run_id, starter_app_id)`. `GET
   /v1/workflow-runs/{id}?wait=<s>` (≤ 30 s) returns
   `{id, status, done, output, error}`, and
   `POST /v1/workflow-runs/{id}/events {event, payload}` sends an event, both
   only for runs the caller started (anything else is 404, as with sends).
   The SDKs return a run handle with `status()`, `wait()` and
   `signal(event, payload)`. Generated types give `start()` the workflow's
   input and `wait()` its output.

4. **`gregale dev --local` drives workflow functions.** Each start, whether
   from `functions.x.start()` or `gregale dev trigger --local <fn>`, runs on
   an in-process driver. The driver uses the engine's own journal validation
   (`pkg/workflowcode`). Steps and `wf.call` really run. Sleeps are shortened
   (`--workflow-sleep-scale`, default: sleeps finish at once). Events come
   from `gregale dev trigger --local <fn> --event NAME --payload JSON` or
   `signal()`. Runs are listed in the dev server's output and readable
   through the same local platform route.

## Slices

1. Manifest `workflow: true` (yaml + config in code), plan into a code
   workflow on the function's app, adapter workflow mode, CLI dependency
   check, docs.
2. The `call` operation kind: protocol, the engine applying and settling it,
   `invocation_sends` columns, Node/Python/Go SDK `wf.call`.
3. Guest `workflow-runs` routes, `workflow_run_starts`, SDK `start()` run
   handles, generated types.
4. The local driver in `gregale dev --local`, journey and e2e coverage.

## Consequences

- A durable function is one file with no routing or engine wiring, and
  fan-out/fan-in across functions costs no workflow VM time while callees
  run.
- `call` is a fourth operation kind; older SDKs never report it, and an
  engine that does not know it rejects the response as a protocol error,
  which the release order (control plane before SDK use) avoids.
- Workflow runs gain a second creator (the gateway's guest API) beside apid
  and the trigger scheduler; both go through the same admitted store path.

## Alternatives considered

- **Keep workflows separate** and only add call steps: leaves the wiring and
  the second declaration in every project.
- **`wf.call` as `send()` + `waitForEvent`** in the SDK: needs a completion
  target that is a workflow event, which is the engine change anyway, and
  puts a reserved event name in customer journals.
- **Events-only starts**: no run handle, so a function cannot wait for or
  signal the run it started.
