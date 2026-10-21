# Code-first workflows

Write a durable workflow as an ordinary async function. Each `step` runs at
least once and its result is recorded; `sleep` and `waitForEvent` park the run
for seconds or months without keeping a VM alive. When the run resumes, your
function is replayed: recorded steps return their stored result instantly and
execution continues where it stopped. The design is
[ADR-20261021204307](adr/20261021204307-code-first-workflows.md).

Code-first workflows use the same engine, plans and quotas as
[declarative workflows](event-driven.md#durable-workflow-waits): runs, steps
and attempts appear in `gregale workflows list`, `steps` and `attempts`, and
triggers (manual, schedule, event) work unchanged. Workflow execution remains a
preview feature on Hobby and higher plans.

## Declare the entry handler

```yaml
workflows:
  - name: order_followup
    trigger:
      type: manual
    code:
      run: order_followup        # Gregale calls POST /order_followup
      timeout: 60s               # optional, per call of the entry handler
      retry:                     # optional default for steps
        max_attempts: 3
        backoff: exponential
```

Use `path: /workflows/order` instead of `run` to choose the exact path. A code
workflow has no `steps` list. `timeout` is bounded by the plan's step timeout;
the default is 30 s.

## Node

```js
import http from 'node:http';
import { defineWorkflow, serveWorkflows, toNodeListener } from '@gregale/sdk-node/workflows';

const orderFollowup = defineWorkflow('order_followup', async (order, wf) => {
  const charge = await wf.step('charge', ({ idempotencyKey }) =>
    payments.charge(order.id, { idempotencyKey }));
  await wf.sleep('delivery_delay', '3d');
  const delivery = await wf.step('check_delivery', () => shipping.status(order.id));
  if (delivery.state !== 'delivered') {
    const approval = await wf.waitForEvent('refund_approval', { event: 'approved', timeout: '7d' });
    if (!approval) return { outcome: 'expired' };
    await wf.step('refund', () => payments.refund(charge.id));
    return { outcome: 'refunded' };
  }
  return { outcome: 'delivered' };
});

const workflows = serveWorkflows([orderFollowup]);
http.createServer((req, res) => {
  if (req.url === '/order_followup') return toNodeListener(workflows)(req, res);
  // ... the rest of your API
}).listen(Number(process.env.PORT ?? 8080));
```

`serveWorkflows` returns a Fetch handler (`Request → Response`), so it mounts
directly in Hono, Next.js route handlers or Bun. One handler can serve several
workflows; it dispatches on the workflow name in the request.

## Python

```python
from fastapi import FastAPI
from faas_sdk.workflows import serve_workflows, workflow

@workflow("order_followup")
async def order_followup(order, wf):
    charge = await wf.step("charge", lambda ctx: payments.charge(order["id"], ctx.idempotency_key))
    await wf.sleep("delivery_delay", "3d")
    approval = await wf.wait_for_event("refund_approval", event="approved", timeout="7d")
    return {"charge": charge["id"], "approved": approval is not None}

app = FastAPI()
app.mount("/order_followup", serve_workflows([order_followup]))
```

`serve_workflows` returns an ASGI app; `run_workflow_invocation(definitions,
body)` is the same logic for any other framework. Step functions may be sync or
async. Python durations are seconds, `timedelta` or strings such as `"3d"`.

## Go

```go
import faas "github.com/poyrazK/faas/sdk/go"

var orderFollowup = faas.DefineWorkflow("order_followup", func(ctx context.Context, wf *faas.Workflow, input json.RawMessage) (any, error) {
	order, err := faas.WorkflowDecode[Order](input)
	if err != nil {
		return nil, err
	}
	charge, err := faas.WorkflowStep(wf, "charge", func(ctx context.Context, sc faas.WorkflowStepContext) (Charge, error) {
		return payments.Charge(ctx, order.ID, sc.IdempotencyKey)
	}, faas.WithStepRetry(3, "exponential"))
	if err != nil {
		return nil, err
	}
	wf.Sleep("delivery_delay", 72*time.Hour)
	approval, ok, err := faas.WorkflowWaitForEvent[Approval](wf, "refund_approval", faas.WorkflowWaitOptions{Event: "approved", Timeout: 7 * 24 * time.Hour})
	if err != nil || !ok {
		return map[string]string{"outcome": "expired"}, err
	}
	return map[string]any{"charge": charge.ID, "approved": approval.OK}, nil
})

func main() {
	http.Handle("/order_followup", faas.ServeWorkflows(orderFollowup))
	log.Fatal(http.ListenAndServe(":"+os.Getenv("PORT"), nil))
}
```

Go has no suspendable functions, so the SDK unwinds the workflow function at
its first new operation (deferred calls run) and calls it again on the next
boundary. Call `wf` only from the goroutine the workflow runs on, and use
`faas.WorkflowParallel` to run several steps concurrently; decode their results
with `faas.WorkflowDecode`. Return `faas.NonRetryable(err)` to fail a step at
once. `faas.RunWorkflowLocally` is the local test runner.

## Operations

| Call | Behavior |
|---|---|
| `step(name, fn, { retry })` | Runs `fn` and records its JSON result. A thrown error is retried under the step's policy (`retry` option, else `code.retry`, else 3 attempts with exponential back-off). After the last attempt the next call throws `WorkflowStepError` into your function, where you may catch it. Throw `NonRetryableError` to fail at once. |
| `sleep(name, duration)` | Parks the run for 1 s up to the plan's maximum wait (30 days on Hobby, 90 on Pro, 365 on Scale). |
| `waitForEvent(name, { event, timeout })` | Waits for an event named `event` (default: `name`) sent with `gregale workflows events RUN_ID approved --payload '{...}'` or `POST /v1/workflows/runs/{id}/events`. Resolves with the payload, or `null` after `timeout` (default: the plan's maximum wait). |

- **Parallel steps.** `Promise.all([wf.step(...), wf.step(...)])` (or
  `asyncio.gather`) runs the steps concurrently in one call.
- **Loops.** Repeating a name records `poll`, `poll#2`, `poll#3`, …, so a step
  or wait can sit in a loop. Each wait for an event name consumes one event of
  that name, in arrival order, and an event sent before its wait starts is kept
  for it.
- **Idempotency.** Steps receive `idempotencyKey` (`idempotency_key` in Python),
  `workflow/<run-id>/<step-name>`, unchanged across attempts. Delivery is at
  least once, as for declarative steps; pass the key to external APIs.

## Rules for workflow code

Your function is replayed from the start on every resume, so code **outside**
steps must be deterministic:

- Read the clock, random numbers, environment and network only inside steps.
- Do not `await` anything other than `step`, `sleep` and `waitForEvent` (and
  combinations of them) outside a step. The SDK suspends the function as soon
  as nothing else can make progress.
- Do not rename, reorder or remove operations while runs are in flight. Runs
  stay pinned to the deployment that started them; if code at a recorded
  position asks for a different operation, the run stops as `dead` with a
  `nondeterministic workflow` error instead of replaying wrong results.

## Limits

| Limit | Value |
|---|---|
| Operations per run | 1,000 |
| Recorded results, payloads and errors per run | 1.5 MiB |
| Results reported by one call (parallel steps share it) | 960 KiB of JSON |
| Workflow output | 960 KiB of JSON |
| Operation name | 1–128 of `A-Z a-z 0-9 _ . : # / -`, starting with a letter or digit |

A step whose result would push one call's answer past 960 KiB fails without
retry. Large data belongs in [object storage](object-storage.md); return its
key from the step.

## Failures

- An uncaught error in your function fails the run (`failed`) with its message.
- The entry handler itself is retried when it times out, crashes, answers
  non-2xx or returns a malformed body: 5 s back-off doubling to 5 minutes, at
  least 5 consecutive attempts (or `code.retry.max_attempts` when larger),
  then the run is `dead`. Each call is recorded as the `$driver` step:
  `gregale workflows attempts RUN_ID '$driver'`.
- A run that exceeds a limit, or replays non-deterministically, is `dead`.
- Code-first runs cannot be resumed with `gregale workflows resume`, linked to
  Operations or simulated by the automation simulator; start a new run instead.

## Test locally

```js
import { runWorkflowLocally } from '@gregale/sdk-node/workflows';

const result = await runWorkflowLocally(orderFollowup, { id: '42' }, { events: { approved: [{ ok: true }] } });
assert.equal(result.status, 'succeeded');
```

`runWorkflowLocally` (`run_workflow_locally` in Python) drives the same
protocol in-process: sleeps finish at once, waits take the given events in
order or time out, and failed steps retry under their policy. The entry
handler refuses calls without Gregale's internal workflow headers; pass
`allowUnverifiedCallers: true` (`allow_unverified_callers=True`) only to call
it from a local test client.
