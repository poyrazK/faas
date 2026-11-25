# Typed pipeline tools (SDK preview)

Expose an existing typed business pipeline to agents using generated MCP tool
schemas, durable start receipts and current application authorization. Node and
Python share one PostgreSQL protocol, canonical identities and checkpoints.
Existing pipeline callers keep their APIs.

A tool call returns promptly with a request receipt. It does not hold an MCP
connection open for the lifetime of a Job or human approval. Reconnect with the
same request ID to inspect progress or retrieve a validated result. Approval
remains in the existing authenticated human review flow.

## Define a tool

Generate a `JobContract<Input, Output>` with the existing job-types command.
Its input and output become the business schemas advertised by the adapter.

```ts
import {definePipelineTool, postgresPipelineToolStore,
  createPipelineToolMCP} from '@gregale/sdk-node/preview';
import {contract} from './pipeline-generated.js';

const tool = definePipelineTool(contract, {
  name: 'csv_import', description: 'Validate, review and import an uploaded CSV.',
  scope, token: backendToken,
  store: postgresPipelineToolStore(query),
  gateStore, reviewBaseURL: 'https://app.example.test/review',
  authorize: async (request, session, action, signal) => {
    const user = await authenticateCurrentSession(session, signal);
    const record = await loadAuthorizedImport(request.request_id, user, action, signal);
    if (!record) return null;
    // For start, validate request.input against this immutable authorized record.
    // Enforce separate cancel/reconcile permissions when your application needs them.
    return {subject_id: user.stableId, owner_id: record.ownerId};
  },
});
const mcp = createPipelineToolMCP(tool);
```

```python
from faas_sdk.preview import PipelineTool, PipelineToolActor, PipelineToolMCP
from faas_sdk.preview import PostgresPipelineToolStore
from pipeline_generated import Input, Output, contract

async def authorize(request, session, action):
    record = await authorize_current_import(request, session, action)
    if record is None:
        return None
    return PipelineToolActor(subject_id=record.stable_subject, owner_id=record.owner_id)

tool: PipelineTool[Input, Output, Session] = PipelineTool(
    contract, name="csv_import", description="Validate, review and import a CSV.",
    scope=scope, token=backend_token, store=PostgresPipelineToolStore(query),
    gate_store=gate_store, review_base_url="https://app.example.test/review",
    authorize=authorize,
)
mcp = PipelineToolMCP(tool)
```

`authorize` is mandatory for start, duplicate start, inspect, result, cancel and
reconcile. Its actions are `start`, `inspect`, `result`, `cancel`, `reconcile`.
It derives the stable subject and owner from trusted current application data;
MCP arguments never grant ownership. `request.input` is supplied for start and
is absent/None for other operations. Bind a request to an immutable business
record so reads can recheck access without retaining its payload in the receipt.
Use a subject stable across reconnects and credential rotation. Subject and owner
identifiers use up to 128 ASCII letters, digits, `_`, `.`, `:`, or `-`; map other
external subjects to a stable application identifier.

See the working [Node](../examples/sdk-csv-jobs/pipeline-tool.mjs) and
[typed Python](../examples/sdk-csv-jobs/pipeline_tool.py) CSV definitions. They
compare generated inputs by portable logical value, preventing another source,
owner or file from being substituted for the saved request.

## Install receipts and upgrade drivers

Apply [pipeline-tools.sql](../sdk/request-contracts/pipeline-tools.sql) explicitly
with your migration role; `pipelineToolSchema` / `PIPELINE_TOOL_SCHEMA` exports
the same DDL. Construction never runs migrations. SQL adapters require each query
to be one acknowledged **autocommit** statement against the same primary database.
Do not use ambient transactions, asynchronous replicas or driver mutation retries.
Python queries use `$n` placeholders; translate repeated parameters correctly
when adapting to psycopg. Configure statement/lock timeouts in the driver.

The receipt binds account/app/API scope, tool, stable subject, application owner,
request UUID, pipeline contract and exact input-envelope digest. It retains no
input, output, credential, review payload or bearer capability. A tool invocation
adds `{version:1, kind:'tool', identity}` to the existing pipeline envelope.
Upgrade Node/Python workflow drivers before routing these calls to them. Manual,
event and scheduled callers retain their existing envelopes and behavior.

Only an acknowledged new reservation may dispatch a native workflow. Concurrent
SDKs reserve one identity; duplicate calls read its saved checkpoint or report
`start_unknown`. A reservation with a lost acknowledgement never authorizes a
second dispatch. Changed input, owner or contract under that identity conflicts.
Native idempotency keys supplement this receipt and are not its retention boundary.

Retain receipts for as long as callers can retry. There is no automatic pruning,
lease takeover or abandoned-intent resubmission. Changing tool name, subject
namespace or scope creates another identity and requires an explicit migration
and business idempotency strategy. Job effects remain at least once.

## Use and recover

```ts
const started = await tool.start(input, requestId, principal, {timeoutMs: 10_000});
const status = await tool.inspect(requestId, principal, {timeoutMs: 10_000});
const output = await tool.result(requestId, principal, {timeoutMs: 10_000});
```

Python uses `start(input, request_id, principal, timeout=10)`, `inspect`, `result`,
`cancel` and `reconcile`, with asyncio task cancellation. Node accepts AbortSignal
and milliseconds. Every operation has one total deadline; bounded local waiting
and transport cancellation do not automatically cancel remote workflows.

Starts return `started` or `start_unknown`, plus the portable receipt and
`replayed`. Inspection returns null/None when no receipt exists; otherwise its
state is `start_unknown`, `running`, `awaiting_approval`, `awaiting_input`,
`succeeded`, `failed` or `cancelled`, with sanitized step progress.

After an uncertain native start, use the original request ID to find and
verify the accepted run.

```ts
const match = await tool.findAcceptedRun(requestId, principal, { timeoutMs: 10_000 });
if (match.state === "found") {
  await tool.reconcile(requestId, match.run_id, principal, { timeoutMs: 10_000 });
}
```

Python uses `find_accepted_run(request_id, principal, timeout=10)` and
`reconcile(request_id, match["run_id"], principal, timeout=10)`. The lookup
uses the original native start key from the durable receipt and requires
current reconciliation authority. It returns `found` only after checking the
native run's app, owner, contract, and full input-envelope digest, including the
tool's trigger identity. It returns `start_unknown` if no retained binding is
visible, including on a server without the lookup route.

Lookup reads the receipt and native run. Call `reconcile` separately to attach
the verified checkpoint. Reconciliation checks the run and current access
again. Concurrent attempts converge on the same checkpoint. If you already
know the verified run ID, pass it directly to `reconcile`. If the reservation
committed but native dispatch cannot be established, keep the receipt unresolved.
Review the business effects before issuing a deliberately new request.

`result` reauthorizes, verifies native completion and validates the generated
output. Before completion it raises `not_terminal`; unresolved starts raise
`start_unknown`. `cancel` explicitly cancels native orchestration. Already
accepted Jobs can continue and database/file effects are not rolled back.
Native failed code workflows cannot currently be continued by attachment.

## MCP registration

The adapter exposes descriptors through `mcp.tools` and dispatches
`mcp.call(name, arguments, authenticatedPrincipal, options)` (Python:
`await mcp.call(name, arguments, principal, timeout=...)`). Register these with
an existing MCP server's `tools/list` and `tools/call` handlers. The host owns
transport, initialization/version negotiation, authentication, scopes, request
size limits and JSON-RPC errors; this SDK is not a second MCP transport server.

Each definition advertises five tools:

| Suffix | Arguments | Behavior |
| --- | --- | --- |
| `_start` | `request_id`, generated `input` | Commit one start intent and return a durable receipt. |
| `_status` | `request_id` | Read progress and pending human-review information. |
| `_result` | `request_id` | Read validated output when complete, otherwise current status. |
| `_cancel` | `request_id` | Explicit, separately authorized orchestration cancellation. |
| `_reconcile` | `request_id`, `run_id` | Adopt an exact matching native run after an uncertain start. |

Use the same UUID for a logical invocation across retries. The JSON-RPC message
ID identifies a transport request and must not replace this business request ID.
Arguments reject extra fields, including caller-provided owner or approval claims.
Schemas wrap business input in an object, including scalar/array contracts.
Results contain a minimized receipt, state, progress, optional review information
and validated output. Structured results are also returned as JSON text for
clients that display text. Tool failures set `isError` with a sanitized stable
code; unknown tools and malformed arguments raise `PipelineToolMCPError` with
JSON-RPC codes -32601 and -32602 for the host to encode.

Default wire compatibility is MCP `2025-11-25`. After host negotiation,
`protocolVersion:'2026-07-28'` / `protocol_version='2026-07-28'` adds the required
`resultType:'complete'`. The host supplies that version, not model arguments.
The application toolkit can opt selected starts into
[durable MCP Task views](sdk-pipeline-tasks.md), which use the original receipt
for status and results. See the
[MCP tools specification](https://modelcontextprotocol.io/specification/2026-07-28/server/tools).
SDK tool arguments and complete response frames are bounded to 1 MiB; gate
inspection is bounded to 100 records. Native plan limits remain authoritative.

## Human approval

Configure both the existing gate store and a fixed review base URL to enable
approval views. For a nonterminal native run the adapter validates each gate's
run, owner, scope, root contract and exact input digest. It resolves pending
gates through the existing database-time expiry mechanism before reporting them.
A valid pending gate returns an `awaiting_approval` / `awaiting_input` state with
gate ID, name, expiry and review link. Review payload and decision authority stay
in the authenticated review service. Multiple simultaneous unresolved gates
fail closed; the current pipeline gate API expects sequential waits.

The review base URL must use HTTPS, or loopback HTTP for local development,
without query, fragment or embedded credentials. Links contain only run and gate
IDs. The human review service must authenticate and reauthorize access; possession
of a URL or receipt is not permission. There is deliberately no MCP approval or
reply-decision tool. Resolved rejection/expiry remains a business pipeline outcome
and does not itself grant permission to perform later actions.

## Evidence

Shared Node/Python fixtures cover exact schemas, descriptors, identities,
checkpoints and trigger envelopes. Tests exercise concurrent starts, restart,
lost reservation/start/completion replies, changed inputs/contracts, revoked
access, cross-request reconciliation, gate binding/expiry, typed results,
explicit cancellation and deadlines. Installed npm/wheel consumers check the
public generic types and preview-only exports.

[The CSV tool example](../examples/sdk-csv-jobs/README.md#agent-tools-for-reviewed-imports)
uses actual PostgreSQL, concurrent Node/Python adapters, an authenticated MCP
JSON-RPC fixture, lost-start reconciliation, mixed workers and Chrome human
review before retrieving the verified report. Native APIs and MCP transport are
fixtures; this does not establish deployed broker/VM or hosted MCP publication
acceptance. No model provider or agent loop is required by the SDK.

## Publish pipeline output files

Register named output resources when a successful pipeline returns a
`PipelineArtifact`, such as a CSV report. Each registration selects an
artifact from the validated output and declares its media type and byte limit.
This adapter serves UTF-8 files.

```ts
const adapter = createPipelineToolMCP(importTool, {
  outputResources: [{
    name: "validation_report",
    description: "Validation report for the import.",
    mimeType: "text/csv",
    files: reportBucket,
    maxBytes: 65_536,
    select: output => output.report,
  }],
});

const [report] = adapter.resourceTemplates;
const mount = {
  adapter,
  toolScopes: importToolScopes,
  resourceScopes: { [report.uriTemplate]: ["imports:read", "reports:read"] },
};
```

```python
from faas_sdk.preview import PipelineOutputResource, PipelineToolMCP, ToolkitMount

adapter = PipelineToolMCP(
    import_tool,
    output_resources=[
        PipelineOutputResource(
            name="validation_report",
            description="Validation report for the import.",
            mime_type="text/csv",
            files=report_bucket,
            max_bytes=65_536,
            select=lambda output: output.get("report"),
        ),
    ],
)
mount = ToolkitMount(
    adapter,
    tool_scopes=import_tool_scopes,
    resource_scopes={
        adapter.resource_templates[0]["uriTemplate"]: ["imports:read", "reports:read"],
    },
)
```

Mount the adapter in the application toolkit. The toolkit publishes the
resource templates with the declared OAuth scopes and filters resource links
using the same policy. Use a file bucket whose client can serve the host's
request model. The Python starter dispatches requests in separate event loops.
The CSV fixture uses an HTTP/1.1 file client with connection reuse disabled.

Successful `_result` calls and completed Tasks include the same
`resource_link` blocks alongside their structured output. An absent artifact
produces no link. A client can retain the URI and pass it to `resources/read`.
The reader resolves the authorized receipt, checks that the URI still matches
the run and artifact, and verifies the byte count and SHA-256. It checks
authorization again before returning the content. Repeat reads use the current
access policy and the original artifact binding.

The default resource byte limit is 256 KiB. Configure up to 1 MiB per resource
within the 1 MiB serialized response budget. Revoked application access and
mismatched links return resource-not-found. Changed files and over-budget content return a read
error with no file bytes. See the [CSV mapping report walkthrough](../examples/sdk-csv-jobs/README.md#read-the-csv-mapping-report-over-mcp).
