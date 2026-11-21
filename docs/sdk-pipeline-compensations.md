# Reviewed pipeline compensation (SDK preview)

Node and Python can prepare the same immutable recovery plan from a failed typed
pipeline, request human approval, and run ordered compensating SQL Jobs in a
separate native workflow. The original run and its history remain available.
Existing pipeline callers keep their APIs.

This first version supports one registered typed compensation Job per recovery
definition. Each command applies application-owned PostgreSQL changes. Provider
refunds, remote API calls and uncertain effects from a failed writing Job need
application-specific reconciliation outside this helper.

## Install and register

Apply [pipeline-compensations.sql](../sdk/request-contracts/pipeline-compensations.sql)
using the application's migration role, alongside the existing typed Job,
pipeline, human gate and pipeline tool schemas. The SDK exports the same DDL as
`pipelineCompensationSchema` / `PIPELINE_COMPENSATION_SCHEMA`. Construction never
runs migrations. All receipt stores and business effects must use the same
primary database. Store query adapters require acknowledged autocommit statements;
the worker pool must supply a fresh, exclusively owned connection per transaction.
Configure driver statement and lock timeouts as well as SDK deadlines.

Generate the business `JobContract<UndoInput, UndoOutput>` with the existing
job-types generator. `compensationContracts(business, name)` /
`compensation_contracts(business, name)` derives three portable contracts:

- `worker`: native task input contains only plan, command and recovery run IDs;
  output contains a typed business result and compensation state.
- `root`: workflow input contains the plan ID and exact binding hash; output
  contains the ordered command outcomes and recovery state.
- `review`: human review contains the source run, revision, command descriptions
  and hashes of the exact business inputs.

Register a native Job using the derived worker contract. Configure
`definePipelineCompensation` / `PipelineCompensation` with that Job, its result
store, the receipt stores, backend credentials and the original pipeline contract.
Publish the returned `definition` as the native `recover-<name>` code workflow;
its application route must retain the existing workflow gateway checks. Run each
task through `recovery.handler(...)` using the derived worker contract. Keep this
definition available while its saved plans and runs can still be retried.

Declare **every source Job stage** in `sourceStages` / `source_stages`, marking
each with an explicit `readOnly` / `read_only` boolean. A read-only stage must
perform no business writes. The SDK cannot discover omitted stages or verify
that declaration. A root must be `failed` or `dead`; all submitted source Jobs
must be terminal. Missing Job acceptance checkpoints produce `source_unresolved`.
A writing stage that did not succeed produces `source_effects_unknown`, even if
it might have written only some rows. Resolve that case in application-specific
code before attempting recovery.

The planner receives verified source checkpoints, receipts and typed results
from succeeded stages, including read-only validation stages. It may read current
business records, but must not mutate them or call external effectful services.
Return an ordered list of `CompensationCommand<UndoInput>` values:

```ts
{id: 'remove_row_2', effect_id: stableEffectId,
 description: 'Remove the unchanged second imported row',
 input: {input_id: savedInputId, expected_sha256: originalRowHash}}
```

Derive effect IDs from the original recorded effects, not from retry attempts.
Capture expected versions or fingerprints from verified original evidence.
The [Node](../examples/sdk-csv-jobs/compensation.mjs) and
[Python](../examples/sdk-csv-jobs/compensation.py) CSV definitions show the complete
configuration, planner and SQL callbacks.

## Plan, review and recover

Plan creation rechecks current application access and saves one immutable plan
per scope, definition name and original run. Concurrent Node/Python callers
agree on its content. Changed commands, owner, contracts, policy or revision
under the same identity conflict. Inspecting a plan or its effect receipts also
requires current access.

```ts
const plan = await recovery.plan(failedRunId, session, {timeoutMs: 30_000});
const input = {plan_id: plan.identity, binding_sha256: plan.binding_sha256};
const receipt = await recovery.tool.start(input, savedAttemptUUID, session,
  {timeoutMs: 30_000});
const mcp = createCompensationMCP(recovery);
```

```python
plan = await recovery.plan(failed_run_id, session, timeout=30)
receipt = await recovery.tool.start(plan.input, saved_attempt_uuid, session, timeout=30)
mcp = CompensationMCP(recovery)
```

Persist the attempt UUID before calling start. An attempt uses the existing
[pipeline tool receipt protocol](sdk-pipeline-tools.md), including concurrent
starts, `start_unknown`, exact-run reconciliation, status, result and cancel.
Authorized callers share a definition-level request namespace, so the same UUID
cannot start a second recovery under another caller. Never reuse it for another
plan. An uncertain start must keep that UUID and reconcile the exact accepted
native run; it does not permit automatic resubmission.

`createCompensationMCP` / `CompensationMCP` exposes those five lifecycle tools and
one `_plan` tool. Planning returns the plan reference and command summaries;
it does not return business command payloads. Register descriptors and `call`
with the application's existing authenticated MCP host. Human approval stays in
the existing authenticated gate service. No agent approval tool is added.

A recovery pauses at the `review` approval gate before submitting commands.
Review binds the exact plan hash, revision, source run, command order and inputs.
The application must show that review data to the human and enforce current
reviewer roles through the existing gate service. A recovery worker checks the
approved stored decision, exact native recovery envelope, attempt/run binding,
owner, contracts, current backend execution permission and source quiescence.
Possession of a plan, run ID or review URL grants no authority.

`authorize` runs for planning and all lifecycle operations, including duplicates.
It derives `CompensationActor {actor_id, owner_id}` from trusted current data.
`authorizeExecution` / `authorize_execution` rechecks backend policy before new
work. The worker's required transaction authorization callback checks current
business ownership and permission again inside the SQL transaction.

## Atomic SQL effects and outcomes

`handler({pool, authorize, apply})` / `handler(pool=..., authorize=..., apply=...)`
owns BEGIN/COMMIT, the advisory effect lock and the receipt insert. Its transaction
argument permits business queries; callbacks must not begin/commit transactions,
change connection settings, perform remote effects or retain the handle after
returning. In Python, `tx.execute` returns the driver's cursor. In Node,
`tx.query` returns `{rows}`. Respect cancellation and configure database timeouts.

The apply callback locks the business record, verifies its current owner/version
or fingerprint, and returns one of these typed outcomes:

| State | Meaning |
| --- | --- |
| `compensated` | The intended SQL change was applied. |
| `already_compensated` | The desired business state already exists. |
| `needs_review` | A changed record or other business condition prevents automatic undo. |

Each outcome includes `result: UndoOutput`. Business writes and the outcome
receipt commit together. `needs_review` rolls back callback writes to a savepoint
before recording its outcome; the workflow stops there. Commands run sequentially
and the worker verifies earlier effect receipts before applying the next command.
A retry of the same effect returns its retained original outcome without calling
apply again. `recovery.effects(...)` provides a currently authorized audit view.

A lost COMMIT acknowledgement, including cancellation while COMMIT is unresolved,
returns `commit_unknown`. Retrying the same task can read the committed receipt
or safely apply after the original transaction rolls back. Do not infer rollback
from a timeout. Explicit cancellation stops orchestration; already committed or
inflight business changes may remain.

The recovery root returns `completed`, `needs_review`, `rejected`, `expired` or
`cancelled` with ordered outcomes. Native workflow/Job failures remain inspectable
through the normal lifecycle. Failed code workflows cannot continue execution.
After a terminal recovery, an explicitly chosen **new attempt UUID** starts a
separate workflow with fresh human approval while reusing completed effect
receipts. It does not rewrite the plan, override `needs_review`, or silently retry
an uncertain earlier start. Manual resolution remains an application operation.

## Revisions, retention and bounds

`revision` is required. Bump it when changing compensation semantics; a code
change cannot be detected from a schema hash. Keep old workers and policies
available for their plans, or drain those plans before upgrading. An existing
plan conflicts with a different revision/configuration. A new definition name
creates a separate namespace and requires an explicit effect migration strategy
to avoid compensating the same original effect twice.

Retain original Job/pipeline evidence, plans, attempt/run bindings and effect
receipts for as long as recovery can be requested or retried. There is no implicit
pruning, lease takeover or automatic replanning. Plans retain business command
inputs and source evidence digests in the application's database; keep those
records private and credential-free. Tool receipts and MCP summaries retain
only their existing bounded metadata.

This preview bounds a plan to 20 commands, 10 declared source stages and 50 inputs
per source Job; source evidence is at most 256 KiB, a plan 64 KiB, and review data
16 KiB. Required reviewer roles are capped at 16 and approval timeout at 24 hours.
These are SDK protocol bounds; native plan quotas and existing Job, workflow,
journal and MCP limits still apply. Node durations use milliseconds; Python uses
seconds and asyncio cancellation. Both SDKs fence late callbacks after the total
operation deadline; database cancellation still depends on the configured driver.

Shared fixtures cover canonical plans/contracts, ordered outcomes and MCP
descriptors. Unit tests cover source proof, authorization, approval binding,
concurrent plans/retries, unknown commits, savepoint rollback, deadlines and
cancellation. The CSV acceptance uses real PostgreSQL and Chrome with native
control-plane and MCP fixtures; deployed workflow publication and VM qualification
remain separate acceptance steps.
