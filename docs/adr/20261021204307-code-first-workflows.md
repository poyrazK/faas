# ADR-20261021204307: Code-first workflows

Date: 2026-10-21
Status: Accepted
Related: [ADR-081](081-durable-execution-workflows.md) (workflow engine), [ADR-1001](1001-durable-workflow-run-recovery.md) (run recovery), [ADR-1180](1180-long-async-invocations.md) (async durations)

## Context

ADR-081 workflows are declared in `gregale.yaml` as a step graph: each `run:`
step invokes one app handler, and durable waits (`wait_for_duration`,
`wait_for_event`) park the run between handlers without keeping a VM alive.
Control flow lives in YAML (`depends_on`, `when`, `on_failure`, `for_each`),
so a branch that depends on a step result, a loop with an unknown bound, or a
retry that needs local state has to be expressed as configuration.

Most durable-execution products now let developers write the flow as ordinary
async code (`step`, `sleep`, `waitForEvent`) and replay it deterministically
after every durable boundary. Gregale's engine already has what that model
needs underneath: per-run step rows, attempt history, timer and event parking,
retry scheduling, run leases and generation fences, retention, and the CLI and
API views over all of them. On Gregale a parked run also costs nothing: the
app's VM is parked to a snapshot while the run waits, and the next durable
boundary wakes it.

## Decision

### A code workflow declares one entry handler

```yaml
workflows:
  - name: order_followup
    code:
      run: order_followup        # POST /order_followup (or path: /workflows/order)
      timeout: 60s               # per invocation of the entry handler
      retry:                     # default step policy; also bounds entry failures
        max_attempts: 3
        backoff: exponential
```

- `code` and `steps` are mutually exclusive. A code workflow has no `steps`,
  `for_each`, `outbound`, `managed_operation` or Operation link; those remain
  declarative features. Triggers (manual, schedule, event), `max_concurrent_runs`
  and the plan's workflow quotas apply unchanged.
- `run` follows the existing handler-name rules; `path` follows the existing
  path rules. Exactly one is required. `timeout` is bounded by the plan's
  `WorkflowStepMaxTimeout` and defaults to `WorkflowStepDefaultTimeout`.

### The engine drives the code through a replay protocol

Each durable boundary is a call to the entry handler. The request body is a
`gregale.workflow.code/v1` envelope with the run id, workflow name, run input
and the run's **journal**: every operation recorded so far, in call order,
with its sequence number, name, kind, status and result.

The SDK runs the workflow function against the journal. Recorded operations
return their stored result immediately. The first operations the journal does
not contain are new: a `step` runs its function now, in this invocation;
`sleep` and `waitForEvent` are only reported. Once the function can make no
more progress in this invocation it is suspended, and the SDK answers with
the new operations and the run status (`running`, `succeeded` with an output,
or `failed` with an error).

- Several operations may be reported at once. `Promise.all` /
  `asyncio.gather` over steps runs them concurrently in one invocation.
- Operation names come from the code. A repeated name gets a deterministic
  `#n` suffix, so `step("charge")` in a loop records `charge`, `charge#2`, …
- **Determinism is checked on both sides.** The SDK refuses to replay when the
  code asks for a different name or kind at a recorded sequence number. The
  engine refuses a response whose operations skip a sequence number, rename a
  recorded operation or re-report a settled one. Both mark the run `dead`
  with a `nondeterministic workflow` error; retrying cannot fix the code a run
  is pinned to.

### Operations are ordinary step rows

Every operation is a `workflow_steps` row of its run, so `gregale workflows
steps`, `attempts`, retention, cancellation and recovery see code runs with no
new tables. A row's `input` holds its operation descriptor
(`{"gregale_code_op": {"seq", "kind", …}}`).

- **step**: the SDK reports the result of one attempt. Success stores the
  output. A failure is retried under the step's policy (per-step override,
  else `code.retry`, else 3 attempts with exponential back-off, the declarative
  `EvaluateWorkflowRetry` delays). The engine schedules the retry and calls the
  entry handler when it is due; the SDK runs the step again. When attempts are
  exhausted the row is `failed` and the next invocation throws the step error
  into the workflow function, which may catch it.
- **sleep**: a duration from 1 s to the plan's `WorkflowMaxWaitDays`. The row
  parks with `ParkWorkflowTimer`; its first `started_at` fixes the deadline.
- **waitForEvent**: an event name and timeout (1 s to the plan maximum,
  default the plan maximum). The k-th wait for an event name in journal order
  is satisfied by the k-th event of that name the run received, so a wait in a
  loop consumes one event per iteration and an event that arrives before its
  wait is not lost. Matching, parking and timing out happen under the run
  lock, the same rule declarative waits follow. A timed-out wait resolves to
  `null` in the SDK.
- The **entry invocation** itself is recorded as the reserved row `$driver`:
  one attempt per call, with its HTTP status. Transport errors, non-2xx
  answers and malformed envelopes retry with back-off (5 s doubling to 5
  minutes) for at least 5 consecutive failures, or `code.retry.max_attempts`
  when larger, then the run is `dead`. The row closes with the run.

Applying one response is one transaction: the operation rows, their attempts,
the `$driver` attempt and, for a finished function, the run's result are
written together, so a crash cannot leave a journal with a gap. Parking and
the run wake follow in the same pass, through the existing fenced setters
that never overwrite an event's earlier wake.

### Limits (pkg/api/limits.go)

| Limit | Value |
|---|---|
| Operations per run | 1,000 |
| Journal sent to the handler (outputs, payloads and errors) | 1.5 MiB |
| Whole entry request, with the run input | 2.75 MiB (base64 fits the gateway's 4 MiB dispatch body) |
| One call's answer (new results) | 960 KiB (fits the scheduler's 1 MiB invocation response) |
| Operation name | 1–128 bytes of `[A-Za-z0-9_.:#/-]`, starting with a letter or digit; `$` prefix reserved |
| Error text stored per attempt | 4 KiB (truncated) |

A response that would exceed a limit marks the run `dead` with the limit and
observed value in its error.

### SDKs

- Node: `@gregale/sdk-node/workflows` exports `defineWorkflow`,
  `serveWorkflows` (a Fetch `Request → Response` handler), `toNodeListener`
  and `runWorkflowInvocation` (the protocol core, for any framework).
- Python: `faas_sdk.workflows` exports `workflow`, `serve_workflows` (an ASGI
  app) and `run_workflow_invocation`.
- Steps receive `{ attempt, idempotencyKey }`; the key is
  `workflow/<run-id>/<operation-name>`, the declarative step key format.

## Consequences

- Code workflows reuse the engine's durability, leases, fences, retention and
  views. Delivery stays at least once per step, exactly as for declarative
  steps; side effects should use the idempotency key.
- Code outside steps must be deterministic and must not await I/O: the SDK
  suspends the function once no recorded or newly started operation can make
  progress. This is the same contract every replay-based engine has.
- The handler receives the whole journal on every boundary. The 1.5 MiB bound
  keeps that linear cost small; large results belong in object storage. The
  SDKs fail a step whose result would push one call's answer past 960 KiB
  rather than re-running it.
- A code run cannot be simulated by the automation simulator, linked to an
  Operation, or resumed into a different definition; those paths refuse code
  definitions explicitly.

## Alternatives considered

- **Compile code to the declarative graph.** Static extraction cannot express
  branches on step results or loops of unknown length, which is the reason to
  write code at all.
- **A separate journal table.** It would duplicate attempts, retention,
  cancellation, recovery and every view that already reads `workflow_steps`.
- **One step per invocation.** Simpler, but it serialises `Promise.all` and
  doubles the number of wakes for fan-out; reporting a batch costs one loop.
