# Versioned Run tasks

Run tasks turn repeated code submissions into named, immutable tools. Publish
`report@v1` once, then invoke it with JSON and file inputs. Each invocation gets
a fresh disposable VM and the existing Run receipt, schema validation, retained
artifacts and usage. This preview does not introduce a persistent sandbox.

## Author and check a draft

`validate` checks the manifest with the same plan limits and schema validator
as publication. It can also check a JSON fixture. It runs offline and defaults
to the Pro plan. Pass `--plan hobby` or `--plan scale` to match your target.
Use the returned definition digest to identify the draft checked for that plan.

```sh
gregale run tasks validate --manifest report.json --input @input.json --plan pro --json
gregale run tasks generate --manifest report.json --language typescript > report-task.ts
gregale run tasks generate --manifest report.json --language python > report_task.py
gregale run tasks generate --manifest report.json --language typescript --check report-task.ts
```

Generation is deterministic. `--check` exits nonzero if the file is missing or
differs, so you can keep generated files checked in and detect drift in CI.
The bindings expose input and result types and a `bindTask` or `bind_task`
factory that pins the version in the preview SDK. Supply a result decoder
when binding the task. Gregale enforces the complete published schema on the
server. Numeric and string bounds, patterns, enums and constants remain
runtime constraints. The generator supports primitives, explicit type unions,
arrays of one item schema, fixed object fields, and dictionaries. Schema
references and composition keywords are accepted by validation where supported
by the server, and reported as unsupported by the generator.

```sh
gregale run tasks test --manifest report.json --input @input.json \
  --input-file input.pdf=sample.pdf --output-dir ./smoke-output \
  --idempotency-key "$REPORT_TEST_KEY" --json
```

The smoke test submits an ordinary Source Run under your current credentials.
It uses the manifest's declared runtime, ready environment, resource limits,
file outputs and JSON contracts. If the manifest declares integration grants,
use an account and fixture intended for test traffic. The command waits for a
successful receipt, checks the result contract and the presence of every
declared output, then saves artifacts when `--output-dir` is set. JSON mode
returns the terminal receipt, and a failed execution or contract exits nonzero.
Publishing the version and testing Agent delegation are separate operations.

Use a distinct key for each CI fixture and attempt, and reuse its original key
for an uncertain submission. A client wait timeout leaves the Run active.
Use a fresh output directory when downloading artifacts again.

## Publish and invoke

Create `report.json`:

```json
{
  "name": "report",
  "version": 1,
  "definition": {
    "runtime": "node24",
    "source": "export default async input => ({ rows: input.rows.length })",
    "input_schema": {"type":"object","required":["rows"],"properties":{"rows":{"type":"array"}}},
    "result_schema": {"type":"object","required":["rows"],"properties":{"rows":{"type":"integer"}}}
  }
}
```

```sh
gregale run tasks publish --manifest report.json
gregale sandbox task call report@v1 --input '{"rows":[1,2,3]}' --idempotency-key report-123 --wait --json
gregale run tasks list
gregale run tasks get report@v1
gregale run tasks retire report@v1
```

Use `--input @input.json`, repeatable `--input-file-id path=FILE_UUID`, and
`--output-dir ./results` for retained file inputs and exported artifacts.
Prepare/upload files through the existing Run file API before invoking. Local
file upload convenience is available through the existing SDK file helpers.

Publishing and retirement require account-wide deploy/write authority. Runs
keys may discover catalog metadata and invoke tasks; the invocation's receipt
and input files retain their normal key-family boundary. A definition's fixed
ready environment delegates that exact environment; callers cannot replace the
runtime, code, dependencies, schemas, resource limits or integration permissions.
Integration policy is rechecked at admission and on outbound calls.

There is no mutable `latest` alias. Changed code, dependencies, contracts or agent
permissions require a new version. Retiring erases the sealed definition and
keeps the version tombstone. It does not cancel admitted Runs or invalidate their
receipts. The preview counts retired names and versions against catalog quotas:
Hobby 5 names/20 versions, Pro 25/100, Scale 100/250. Free has no publication.
Encrypted retained definition budgets are 16/64/128 MiB respectively; retirement
frees those bytes. Individual definitions are bounded to 2 MiB before sealing. For stored outputs,
a task without an explicit byte budget reserves at most half the scratch space
remaining after source, the maximum JSON input envelope and the standard scratch
reserve. The catalog exposes that frozen cap. Dynamic file inputs must fit
beside it; publish a larger disk or a smaller explicit output cap when needed.
Oversized explicit output budgets are refused at publication.

## API and SDK

| Action | Route |
| --- | --- |
| Publish | `POST /v1/run-tasks` |
| List page | `GET /v1/run-tasks?cursor=...` |
| Read metadata | `GET /v1/run-tasks/{name}/versions/{version}` |
| Retire | `DELETE /v1/run-tasks/{name}/versions/{version}` |
| Invoke | `POST /v1/run-tasks/invoke` with `Idempotency-Key` |
| Delegate an uploaded input | `POST /v1/execution-files/{id}/agent-grants` |

Publication returns metadata and a content digest, never source or invocation
inputs. Identical publication reuses the same version; changed definitions
conflict. Invocation returns the normal Run response plus `task` lineage.
Use an explicit stable key when the first response is lost. Ordinary invocation
replay retains the existing 24-hour window and key-family scope.

Go's preview client exposes `PublishExecutionTask`, `InvokeExecutionTask`,
`GetExecutionTask`, `ListExecutionTasks` and `RetireExecutionTask`. Node's preview
`RunsService` exposes the corresponding camelCase methods. Python's preview
`api.runs` modules expose the corresponding snake_case sync/async functions.
Continue using existing Run watches, receipt reads and artifact download helpers.
A succeeded Run with `result_validation.status == "invalid"` failed its result
contract and must not be treated as a successful tool result.

## Stateless Agent continuation

Publish a version with `agent_endpoint_ids` listing the owned realtime inbox
endpoints permitted to invoke it. The customer agent uses the managed RAP tool:

```json
{
  "operation": "gregale_task",
  "id": "tool-call-123",
  "call_id": "provider-call-123",
  "group_id": "thread-123",
  "callback_url": "<signed tool_result callback URL issued for this thread>",
  "arguments": {"name":"report","version":1,"input":{"rows":[1,2,3]}}
}
```

Post to `/v1/agent-tools/invoke` with the app's live `gregale:agents` workload
token or an account deploy/write key. A callback URL alone cannot spend compute.
The caller must own the callback endpoint and the task must explicitly allow it.
The tool accepts JSON input and optional
[scoped file grants](#delegate-an-uploaded-file-to-one-tool-call). The stateless Node
starter routes model calls named `gregale_task` through its
`GregaleTaskTools` adapter. Return `{call: {tool: "gregale_task", input:
{name: "report", version: 1, input: {rows: [1,2,3]}}}}` from the model step.
The starter persists the call identity before dispatch and reuses it on retries.
Its `platform.taskReceipt(id)` reads a scoped receipt; `platform.taskArtifact(id,
name)` returns a streaming HTTP response for a declared output.

The Run and endpoint/thread/tool-call link commit atomically. Repeating the call
recovers the same Run, including after an apid restart and after the ordinary
idempotency ledger window. Changed arguments under the same call identity fail
closed. Finished Runs publish a stable RAP tool result to the existing durable
inbox, which wakes the customer's next agent slice. Publication precedes the
completion acknowledgement, so a crash reuses the same logical inbox message.
Delivery and customer processing remain at least once; external effects need
idempotency.

The completion includes task lineage, Run status, contract verdict and a scoped
receipt path. Large results are omitted with `result_omitted: true`; fetch the
receipt to read them. Failed, cancelled, timed-out and OOM Runs remain explicit
statuses. Invalid result schemas produce `outcome: result_schema_invalid`.

App workloads read only linked invocations through
`GET /v1/agent-tools/runs/{id}` and download only their declared outputs through
`GET /v1/agent-tools/runs/{id}/artifacts/{name}`. The app's current endpoint
ownership is checked on each read. These routes do not grant general Run or
file access. Thread revocation or endpoint deletion stops continuation delivery.

## Delegate an uploaded file to one tool call

Authenticate the customer and upload the input through the Run file API from
your trusted backend. Use account-wide deploy authority and access to the
endpoint's app to issue a staging grant for a ready upload. Go clients can call
`CreateAgentExecutionFileGrant`.

For the Report Agent's sales task, upload an orders CSV first. Send this body to
`POST /v1/execution-files/{id}/agent-grants`:

```json
{
  "endpoint_id": "11111111-1111-4111-8111-111111111111",
  "thread_id": "report-thread",
  "tool_call_id": "assistant:upload-1",
  "task_name": "gr-agent-sales",
  "version": 1,
  "path": "orders.csv"
}
```

In the `gregale_task` request, use `"id": "assistant:upload-1"`,
`"group_id": "report-thread"`, and a tool-result callback for that endpoint and
thread. Attach the returned `grant_token` privately at dispatch:

```json
{
  "name": "gr-agent-sales",
  "version": 1,
  "input": {"csv_file": "orders.csv"},
  "file_grants": ["<grant_token>"]
}
```

The staging path resolves under the guest's input directory. A call can carry
up to four grants within its 8 KiB RAP envelope. Run file count, bytes and disk
limits still apply. The Agent still needs its live app credential. Keep the
grants with the pending dispatch in private app storage so retries send the
same token. Exclude them from model context, customer-visible history and logs.

An unused grant expires within 15 minutes or at the upload's expiry, whichever
comes first. File revocation prevents new admissions. Once admitted, replay
looks up the original Run before checking file or grant expiry, and the admitted
Run retains access to its input. Each new logical call needs a fresh grant.

## Rollout

Apply the additive task tables and upgrade apid readers/workers before enabling
`FAAS_EXECUTION_TASKS_ENABLED=1`, with the existing Run API/V2 gates. The Agents
capability is additionally required for agent invocation. Writers default off.
Disabling task writers preserves catalog reads, retirement, invocation replay
and already admitted completion obligations. Temporarily disabling Agents
preserves pending task continuations until re-enabled.

Functional KVM evidence and native production runtime eligibility are separate
release gates. This feature does not change VM lifecycle ownership or qualify
a production runtime image.

Customer and Agent attribution, per-customer/task allowances and invocation
history are described in [Run controls](run-controls.md).

## Typed task handles

The Node preview client's `task<Input, Output>(name, version, decodeResult)` and
Python preview client's `task(name, version, decode_result)` return a handle for
one immutable task version. The decoder validates the application result type;
the published input/result schemas remain enforced by Gregale.

Node handles use the SDK's process-wide API configuration. Keep one active
base URL and credential configuration per process. Python handles retain their
client's transport.

Use `task.run(input, options)` for file preparation, invocation, waiting and
optional durable artifact delivery. Every call requires an explicit stable
idempotency key. `task.start` returns a Run handle early; its `result`, `cancel`
and `deliver` methods separate observation, execution control and storage.
Python also provides `astart`, `arun`, `aresult`, `acancel` and `adeliver`.

An `onPrepared`/`on_prepared` callback receives the exact serializable wire
intent before admission. Persist it in private application storage. After an
uncertain acknowledgement or process restart, `task.submit(intent)` (Python:
`submit`/`asubmit`) recovers the original admission without reopening local files.
If the original attempt had not uploaded missing inputs, resume `start` with the
same input files and key. Failed execution and contract validation raise an
error retaining the receipt; a client wait ending leaves execution active.

The [PDF-to-report example](../examples/gregale-run/pdf-report/README.md) uses
both SDKs with a system package environment, typed JSON and a durable HTML output.
