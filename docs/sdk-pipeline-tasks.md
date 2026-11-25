# Observe typed pipelines as MCP Tasks

A Node or Python application toolkit can expose selected pipeline starts as MCP
Tasks. A client that opts into Tasks receives a handle and can poll `tasks/get`
from a new connection. The Task reads the current pipeline receipt and returns
the pipeline's validated result when it completes.

## Enable Task views

Apply the [pipeline Task binding schema](../sdk/request-contracts/pipeline-tasks.sql)
in the application database. Configure every serving replica with the same
database, namespace, and stable secret owner key of at least 32 bytes. Keep
those settings stable for the lifetime of issued handles. The store retains
the Task ID, original request ID, and receipt identity. A handle expires after
24 hours by default. Use the expiry index to remove expired bindings during
database maintenance.

Publish the pipeline's `_start`, `_status`, and `_result` operations through
the [application toolkit](sdk-application-toolkits.md). Opt in by the pipeline
tool's base name. Use the existing authenticated human review service and
publish `_reconcile` when operators should recover uncertain starts.

```js
import { createPipelineTasks, postgresPipelineTaskStore } from "@gregale/sdk-node/preview";

const pipelineTasks = createPipelineTasks(toolkit, {
  namespace: "my-app",
  ownerKey: process.env.MY_PIPELINE_TASK_KEY,
  tools: ["recover_csv_cleanup"],
  store: postgresPipelineTaskStore(databaseQuery),
});
createApp(config, { toolkit, pipelineTasks });
```

```python
import os

from faas_sdk.preview import PipelineTasks, PostgresPipelineTaskStore

pipeline_tasks = PipelineTasks(
    toolkit,
    namespace="my-app",
    owner_key=os.environ["MY_PIPELINE_TASK_KEY"],
    tools=["recover_csv_cleanup"],
    store=PostgresPipelineTaskStore(database_query),
)
main(toolkit=toolkit, pipeline_tasks=pipeline_tasks)
```

The PostgreSQL callbacks take parameterized SQL. Use acknowledged writes to the
primary database and apply database statement deadlines. In Python, open async
database clients in the active request loop or use a pool compatible with your
host's request model. Pipeline Task views can run alongside the starter's
queued Tasks. A pipeline worker continues to run under the existing native
workflow. Expiring a Task handle leaves the pipeline receipt available.

## Start and resume

Send the pipeline start call with a stable `request_id` and the Tasks
extension. Save the returned `taskId`. Repeating that start with the same
input and request ID reuses the active handle for the same authenticated
OAuth subject, client, and resource. A client that calls the tool directly
receives the pipeline receipt as usual.

Ask `tasks/get` for current state. Use the poll interval on the handle.
Pipeline Task progress is available in `_meta["io.gregale/pipeline"]` and
includes the current state, request and run references, and step progress.
A pending approval includes the current authenticated review link. An
uncertain start can include its exact reconciliation call when the
authenticated caller has access to the inbox follow-up resolver and the
destination operation. Invoke the reconciliation call separately.

A completed Task includes the original typed tool result. Reads require
current permission for the start and status operations. The result operation
also checks its scopes and business authorization before returning output.
Use the published pipeline operations to cancel orchestration or resolve
human review. The Task timestamp records when the host last sampled the
pipeline.

The [CSV example](../examples/sdk-csv-jobs/README.md#resume-csv-cleanup-from-an-mcp-task)
demonstrates a Node-issued handle read from Python, including a lost start
response, review, and retrieval of the final result.

## Ask for typed clarification

When a pipeline calls `waitForInput` in Node or `wait_for_input` in Python,
register its gate contract and gate service under the pipeline tool's base
name. The reply contract uses a flat JSON object with string, number, integer,
or boolean fields. An enum works well when the pipeline has a fixed set of
choices.

```js
import { createPipelineTaskInputForm } from "@gregale/sdk-node/preview";

// Pass this alongside the existing createPipelineTasks options.
const inputForms = {
  clarify_csv_columns: createPipelineTaskInputForm({
    contract: emailColumnGateContract,
    service: emailColumnGateService,
    message: "Which CSV column contains the email address?",
    scopes: ["imports:clarify"],
  }),
};
```

```python
from faas_sdk.preview import PipelineTaskInputForm

# Pass this alongside the existing PipelineTasks options.
input_forms = {
    "clarify_csv_columns": PipelineTaskInputForm(
        contract=email_column_gate_contract,
        service=email_column_gate_service,
        message="Which CSV column contains the email address?",
        scopes=["imports:clarify"],
    )
}
```

A client advertising form elicitation in the `tasks/get` capability envelope
receives `status: "input_required"` and a keyed `elicitation/create` request
for the pending gate it can answer. Retain the `taskId`. After reconnecting,
read the Task again and use the current request key to submit a response.

```js
const [requestKey, form] = Object.entries(task.inputRequests)[0];
// Render form.params.requestedSchema, then call tasks/update with:
const params = {
  taskId: task.taskId,
  inputResponses: {
    [requestKey]: { action: "accept", content: { email_column: "contact" } },
  },
};
```

The host checks current Task ownership, tool access, the reply scopes, and
the gate's role and business authorization. The key binds the Task and run
to the exact gate and its recorded question. Identical retries by the same
authorized caller reuse the recorded answer. Expired, mismatched, and
conflicting answers are rejected. Keep polling until the Task reaches a
terminal state. Clients without form support can use the authenticated
review link in progress metadata. Use the authenticated review flow for
approval gates.

## Subscribe to a pipeline Task

The `mcp-node-tasks` and `mcp-python-tasks` starters accept pipeline Task IDs in
`subscriptions/listen`. Send a modern MCP request with
`notifications: { taskIds: [taskId] }` and declare the Tasks extension
in the request capability envelope. Include `elicitation: { form: {} }`
in that envelope if the client can answer a clarification form.

The SSE acknowledgement lists the Task IDs that the caller can read. The
stream then sends a current snapshot for each accepted ID and subsequent
`notifications/tasks` snapshots when progress, clarification, or the typed
result changes. Both starters refresh the authorized pipeline view on a
two-second polling interval. A change to the time of the last read alone
does not trigger an update. A Task that expires or becomes inaccessible is
removed from the watch. A stream with no remaining filters finishes.

Reconnect to either host with the same Task ID to receive the current
snapshot. Render the latest form or review link from that snapshot and use
`tasks/update` to answer a form. Polling clients can continue using
`tasks/get`. See the [CSV walkthrough](../examples/sdk-csv-jobs/README.md#clarify-a-csv-column-from-an-mcp-task).

## Consume Tasks with the SDK

Use the preview Task client when an agent, CLI, or dashboard consumes a
pipeline from a published MCP endpoint. Pass the same pipeline contract
used to publish the tool and, optionally, the typed clarification contract.
The HTTP adapter uses your authenticated `fetch` or `httpx.AsyncClient`.

```ts
import {
  createPipelineTaskClient,
  createPipelineTaskHttpTransport,
} from "@gregale/sdk-node/preview";

const client = createPipelineTaskClient({
  tool: "clarify_csv_columns",
  contract: csvPipelineContract,
  replyContract: csvReplyContract,
  transport: createPipelineTaskHttpTransport({ endpoint, fetch: authenticatedFetch }),
});

const checkpoint = await client.start(csvInput, requestId, { timeoutMs: 10_000 });
await saveCheckpoint(checkpoint);

const task = client.resume(await loadCheckpoint());
for await (const snapshot of task.watch({ timeoutMs: 60_000, signal })) {
  if (snapshot.status === "input_required") {
    const [key] = Object.keys(snapshot.forms);
    const answer = await askOperator(snapshot.forms[key]);
    await task.answer(snapshot, key, answer, { timeoutMs: 10_000 });
  }
  if (snapshot.status === "completed") {
    const report = await task.readArtifact(snapshot, "mapping_report", { timeoutMs: 10_000 });
    renderReport(report.text);
  }
}
```

```python
from contextlib import aclosing

from faas_sdk.preview import (
    PipelineTaskClarification, PipelineTaskClient,
    PipelineTaskCompletion, PipelineTaskHttpTransport,
)

client = PipelineTaskClient(
    csv_pipeline_contract,
    tool="clarify_csv_columns",
    reply_contract=csv_reply_contract,
    transport=PipelineTaskHttpTransport(endpoint, authenticated_httpx),
)
checkpoint = await client.start(csv_input, request_id, timeout=10)
await save_checkpoint(checkpoint)

task = client.resume(await load_checkpoint())
async with aclosing(task.watch(timeout=60)) as snapshots:
    async for snapshot in snapshots:
        if isinstance(snapshot, PipelineTaskClarification):
            key = next(iter(snapshot.forms))
            answer = await ask_operator(snapshot.forms[key])
            await task.answer(snapshot, key, answer, timeout=10)
        if isinstance(snapshot, PipelineTaskCompletion):
            report = await task.read_artifact(snapshot, "mapping_report", timeout=10)
            render_report(report["text"])
```

A checkpoint is JSON containing the Task ID, original request ID, tool name,
and contract digest. A different process or language can resume it with the
same tool, contract, and OAuth subject, client, and resource. Use the saved
request ID for a deliberate retry of a start whose response was lost.

A watch emits typed snapshots until the Task reaches a terminal state.
It reconnects once after a dropped stream and polls `tasks/get` when
notifications are unavailable. Identical snapshots are suppressed.
Authentication failures and invalid snapshots surface as errors. The
deadline covers the full watch, including time between pulls. Pass an
`AbortSignal` in Node or cancel the consuming task in Python. Close the
iterator when you stop watching. The watch only observes the Task.
The caller invokes starts, answers, and artifact reads explicitly.

For an existing MCP connection, supply a transport whose `request` returns
the JSON-RPC result for the requested method. An optional `subscribe`
yields current authorized snapshots for the specified Task ID. The supplied
HTTP transports handle the MCP metadata, frame bounds, and server errors.

## Discover contracts and generate bindings

Set `publishManifest: true` in `createPipelineTasks` or
`publish_manifest=True` in Python's `PipelineTasks`. The MCP starter then
serves `gregale-pipeline://manifest/v1` as a JSON resource. The manifest
includes Task-enabled pipelines with published start, status, and result
operations that the caller's OAuth scopes allow. Clarification schemas and
named artifacts follow their own scope rules. Each entry includes the
validated pipeline contract and its SHA-256 digest.

Read through the same authenticated transport used by the Task client and
save the result for generation.

```ts
import { writeFile } from "node:fs/promises";
import { readPipelineTaskManifest } from "@gregale/sdk-node/preview";

const manifest = await readPipelineTaskManifest(transport, { timeoutMs: 10_000 });
await writeFile("pipeline-manifest.json", JSON.stringify(manifest, null, 2));
```

```python
import json
from pathlib import Path

from faas_sdk.preview import read_pipeline_task_manifest

manifest = await read_pipeline_task_manifest(transport, timeout=10)
Path("pipeline-manifest.json").write_text(json.dumps(manifest, indent=2), encoding="utf-8")
```

Generate one module per pipeline. It exports input, output, and available
reply types, plus a client factory and the visible artifact names.

```sh
npx --no-install gregale-pipeline-task-types pipeline-manifest.json clarify_csv_columns typescript csv-task.ts
python -m faas_sdk.preview.pipeline_task_types pipeline-manifest.json clarify_csv_columns python csv_task.py
```

Call `createClient(transport)` or `create_client(transport)` from the
generated module. An agent can also read one manifest entry at runtime and
use its `contract` to construct a `JobContract`. The host continues to
authorize each start, Task and artifact read, and answer under current
application policy.

For CI, refresh the manifest using the OAuth scopes intended for the
binding, then repeat the generation command with `--check`. A stale
binding produces a nonzero exit code. A saved Task checkpoint uses the
binding whose contract digest matches the checkpoint.

## Publish a new contract revision

Keep each revision on its own pipeline tool name and native workflow name.
Keep the existing names for the first revision if Tasks are already using
them. Mount the old and new adapters with the same family and publish both
sets of Task operations. Associate each tool name with its own clarification
form and artifact templates. Keep their contracts and definitions available
for as long as clients need their saved Tasks and receipts.

```ts
const previous = createPipelineToolMCP(csvToolV1, {
  revision: { family: "csv_column_mapping", starts: "retained" },
});
const current = createPipelineToolMCP(csvToolV2, {
  revision: { family: "csv_column_mapping", starts: "default" },
});
```

```python
previous = PipelineToolMCP(
    csv_tool_v1, revision={"family": "csv_column_mapping", "starts": "retained"},
)
current = PipelineToolMCP(
    csv_tool_v2, revision={"family": "csv_column_mapping", "starts": "default"},
)
```

Mount at most one default per family. Deploy the same publication settings
and workflows across replicas. Use a `family:` selector to generate a
binding for the default visible to the authenticated caller. Generate an
older binding by its exact tool name when you need to resume its Tasks.

```sh
npx --no-install gregale-pipeline-task-types pipeline-manifest.json family:csv_column_mapping typescript csv-task.ts
python -m faas_sdk.preview.pipeline_task_types pipeline-manifest.json family:csv_column_mapping python csv_task.py
```

A generated revision binding sends its expected contract SHA-256 on start.
An available adapter rejects a mismatched digest with
`PipelineTaskClientError.code == "revision_unavailable"` before reserving a
receipt. A retained revision returns the same error for a fresh request ID.
To retry an earlier start, send the same input and request ID to the original
revision with current authorization. The first accepted run and active Task
are reused from the durable receipt. Request IDs are scoped to the concrete
tool name. Persist the original tool name or generated binding alongside any
request ID whose start response has yet to arrive. Saving the returned Task
checkpoint preserves that binding for later reads, replies, and artifacts.

## Preview a pipeline start

A toolkit can require an expiring preview for fresh starts of one pipeline.
Configure a typed summary contract and a read callback on its MCP adapter.
The callback reads the current source data the user will review and returns a
SHA-256 fingerprint of the source data or version. The summary can include the
expected effects and any clarification or approval that the run expects.

```ts
const adapter = createPipelineToolMCP(tool, {
  preview: {
    contract: summaryContract,
    signingKey: process.env.PIPELINE_PREVIEW_KEY,
    async read(input, principal, options) {
      return readImportPreview(input, principal, options);
      // { sourceVersion, summary }
    },
  },
});
```

```python
from faas_sdk.preview import PipelineStartPreviewPolicy, PipelineToolMCP

adapter = PipelineToolMCP(
    tool,
    preview=PipelineStartPreviewPolicy(
        contract=summary_contract,
        signing_key=os.environ["PIPELINE_PREVIEW_KEY"],
        read=read_import_preview,  # Returns PipelineStartPreviewSource.
    ),
)
```

Use a summary contract with a null input schema and a typed output schema.
Give the read callback read-only access to the source. Mount `<tool>_preview`
with explicit OAuth scopes. The tool authorizer checks current start
permission on both calls. Configure the same secret of at least 32 bytes
across serving replicas. The preview token expires after five minutes.

The scoped manifest includes the summary schema for callers who can invoke
the preview. Generated modules export `Preview` and `previewContract` for
Node, or `Preview` and `preview_contract` for Python. Retain the request ID
and exact input across preview, confirmation, and retry.

```ts
const preview = await client.preview(input, requestId, previewContract, options);
await renderReview(preview.summary);
// After confirmation:
const checkpoint = await client.start(input, requestId, {
  ...options,
  previewToken: preview.preview_token,
});
```

```python
preview = await client.preview(data, request_id, preview_contract, timeout=10)
await render_review(preview["summary"])
# After confirmation:
checkpoint = await client.start(
    data, request_id, preview_token=preview["preview_token"], timeout=10,
)
```

A fresh start requires a token bound to its request ID, authenticated actor,
input, pipeline revision, and current summary and source fingerprint. If the
source changes before confirmation or the token expires, fetch a new preview
and ask the caller to review the current summary. An already reserved request
returns its existing receipt on an authorized retry with the original input
and request ID. Ordinary starts on pipelines without a preview policy keep
their existing API.

## Carry a reviewed file version into execution

A preview of a managed file can return a `PinnedPipelineSource` as
`pinnedSource` in Node or `pinned_source` in Python. The reference contains the
file scope, owner, key, immutable `version_id`, size, and SHA-256. Capture it
from a versioned upload and use verified bytes from that version to compute
the preview summary. The application reads its current source reference again
when confirming the start.

The SDK includes the reference in the signed preview and the native run's
input envelope. The receipt binds the complete native input. Its original
request binding lets a retry recover the same run after the source pointer
changes. The workflow context exposes the reference as `pinnedSource` or
`pinned_source`, including after a pause for clarification or approval.

Use `readPinnedPipelineSource` / `read_pinned_pipeline_source` with the
workflow's owner and an application authorization callback. The helper checks
current access before and after reading, and verifies the version's size and
checksum. A missing or changed version raises `source_unavailable`.
An access denial raises `source_forbidden`. An authorized client can read
these codes as `progress.source_failure` on a failed Task. Apply the verified
read before the dependent effect and keep the referenced version available
for the lifetime of the run.

The [CSV example](../examples/sdk-csv-jobs/README.md#keep-the-reviewed-csv-version-through-clarification)
shows a report that uses the file version from the confirmed preview.

## Review a replacement for a failed Task

Applications can opt into linked recovery for Tasks that fail with
`source_unavailable`. Apply the [recovery schema](../sdk/request-contracts/pipeline-task-recovery.sql)
after the Task binding schema. The PostgreSQL Task store supports links between
the original Task and its replacement. Configure the same recovery policy and
Task owner key on serving replicas.

Set `recovery` on the Task runtime. The policy supplies explicit OAuth scopes
and a `prepare` callback. The callback receives the currently authorized
failed Task's tool, request ID, run ID, and receipt identity. It checks
application evidence that the failed run stopped before the relevant effect,
and returns the typed input and `previous_source_sha256`. Return `null` in
Node or `None` in Python to decline recovery. The original tool revision
must accept starts and expose a preview to the caller. These rules let an
application use the same input, preview contract, and access checks for the
replacement.

```ts
const tasks = createPipelineTasks(toolkit, {
  ...taskSettings,
  recovery: {
    scopes: ["imports:replace"],
    prepare: prepareCsvReplacement,
  },
});
```

```python
tasks = PipelineTasks(
    toolkit,
    **task_settings,
    recovery=PipelineTaskRecoveryPolicy(
        scopes=["imports:replace"], prepare=prepare_csv_replacement,
    ),
)
```

The MCP starter publishes a preview tool and a separate confirmation tool.
Use the original Task reader with a caller-generated recovery request ID.
Retain that ID and the preview token across retries.

```ts
const failed = client.resume(failedCheckpoint);
const proposed = await failed.previewRecovery(recoveryId, previewContract, options);
await renderReview(proposed.previous_source_sha256, proposed.summary);
// After confirmation:
const next = await failed.confirmRecovery(recoveryId, proposed.preview_token, options);
const linked = client.resume(next);
```

```python
failed = client.resume(failed_checkpoint)
proposed = await failed.preview_recovery(recovery_id, preview_contract, timeout=10)
await render_review(proposed["previous_source_sha256"], proposed["summary"])
# After confirmation:
next_checkpoint = await failed.confirm_recovery(recovery_id, proposed["preview_token"], timeout=10)
linked = client.resume(next_checkpoint)
```

A fresh confirmation rechecks the recovery policy and validates the preview
of the replacement's current source. A stale preview requires a fresh review.
An authorized retry with the same recovery request ID returns the already
accepted replacement. The current Task view includes `progress.replaces_task_id`
for tracing it back to the original Task. Keep the original Task available
for the period during which callers may confirm or retry a recovery.

## Assign a clarification to a reviewer

Applications can opt into handoffs for one pending clarification. Install the
[handoff table](../sdk/request-contracts/pipeline-task-handoffs.sql) on the same
PostgreSQL primary as the pipeline gate store. Configure the Task runtime's
`handoffs` option with a handoff store and explicit reviewer OAuth scopes.

Use the Task owner's verified caller identity when issuing or revoking a
grant from application code. The application chooses the reviewer by OAuth
subject. The handoff expires at the earliest of its requested TTL, the Task
expiry, and the pending question's expiry. Keep the Task owner key and
namespace consistent across hosts.

```ts
const tasks = createPipelineTasks(toolkit, {
  ...taskSettings,
  handoffs: {
    store: postgresPipelineTaskHandoffStore(databaseQuery),
    scopes: ["imports:handoff"],
  },
});
const grant = await tasks.grantHandoff(
  taskId, { reviewerSubject, ttlMs: 30_000 }, ownerCaller, options,
);
```

```python
tasks = PipelineTasks(
    toolkit,
    **task_settings,
    handoffs=PipelineTaskHandoffSettings(
        store=PostgresPipelineTaskHandoffStore(database_query),
        scopes=["imports:handoff"],
    ),
)
grant = await tasks.grant_handoff(
    task_id, owner_caller, reviewer_subject=reviewer_subject, ttl_ms=30_000, timeout=10,
)
```

Send the handoff ID through the application's reviewer interface. The reviewer
signs in under their own account. The MCP tools expose the form, reply key, and
expiry to the assigned subject. Each call requires the handoff and form scopes
and uses the app's existing gate authorizer. Resolve the reviewer's current
membership against the gate owner and return the reviewer's actor ID for the
decision audit.

```ts
const reviewer = createPipelineTaskHandoffClient({
  handoffId, replyContract, transport: reviewerTransport,
});
const question = await reviewer.get(options);
const answer = await promptReviewer(question.form);
await reviewer.answer(question, answer, options);
```

```python
reviewer = PipelineTaskHandoffClient(
    handoff_id, reply_contract=reply_contract, transport=reviewer_transport,
)
question = await reviewer.get(timeout=10)
answer = await prompt_reviewer(question["form"])
await reviewer.answer(question, answer, timeout=10)
```

Keep the question and answer for an identical retry if the response is lost.
The gate store checks expiry and revocation while recording the decision.
Use `revokeHandoff` or `revoke_handoff` with the issuer's identity to close
the grant. The Task owner can keep watching the original Task through
completion and read its result with their own scopes.

## List and watch assigned clarifications

The reviewer inbox lists pending handoffs across imports or other business
records for the authenticated OAuth subject and resource. Apply the current
[handoff schema](../sdk/request-contracts/pipeline-task-handoffs.sql) to install
its reviewer index. The PostgreSQL store enables the read-only
`pipeline_task_handoff_list` MCP tool. A custom store can enable the list by
implementing the optional ordered `scan` alongside its existing exact lookup.

A page contains only the handoff ID, expiry, and question. The runtime checks the
grant, form scopes, current reviewer role, and gate on each read. A cursor is
bound to the authenticated reviewer and can be carried to another authorized
client for the same reviewer. Follow `next_cursor` until it is null, including
when a page is empty. Pending work can change between pages.

```ts
import { createPipelineTaskReviewerInbox } from "@gregale/sdk-node/preview";

const inbox = createPipelineTaskReviewerInbox(reviewerTransport);
const first = await inbox.page({ timeoutMs: 5_000, pageSize: 10 });
if (first.next_cursor) {
  const next = await inbox.page({ timeoutMs: 5_000, cursor: first.next_cursor });
  renderPage(next.items);
}

for await (const { items, truncated } of inbox.watch({
  timeoutMs: 120_000,
  signal: controller.signal,
})) {
  renderInbox(items, { truncated });
}
```

```python
from contextlib import aclosing
from faas_sdk.preview import PipelineTaskReviewerInbox

inbox = PipelineTaskReviewerInbox(reviewer_transport)
first = await inbox.page(timeout=5, page_size=10)
if first["next_cursor"]:
    next_page = await inbox.page(timeout=5, cursor=first["next_cursor"])
    render_page(next_page["items"])

async with aclosing(inbox.watch(timeout=120)) as snapshots:
    async for snapshot in snapshots:
        render_inbox(snapshot["items"], truncated=snapshot["truncated"])
```

Watch snapshots replace the previous view. Watches refresh over the authenticated
MCP list every 15 seconds by default. The next refresh retries transient
transport failures within the watch deadline. Watches read at most 10 pages per
snapshot by default and mark partial views with `truncated`. Apps can supply their existing
[live inbox notification source](sdk-live-work-inbox.md) using
`notifications: { resourceId, source }` in Node or
`notifications=(resource_id, source)` in Python. Use a private channel bound to
the reviewer and app. After a committed change, publish
`workInboxInvalidation(resourceId, changeId)` or its Python equivalent. Send the
same change ID on retry. The watch refreshes on reconnect and coalesces
duplicate notifications. Use a fresh authenticated transport when switching
accounts and clear the displayed inbox if the watch loses access.

Open an item with the handoff client and submit the typed answer explicitly.
On the next refresh, answered, expired, revoked, and no-longer-authorized grants
leave the inbox. The original Task continues under its owner's credentials.

## Assign an approval to a reviewer

An application can assign a Task waiting at an approval gate using the same
`grantHandoff` or `grant_handoff` operation. Register an approval adapter for
the pipeline tool and give it the gate service, the review and decision
contract, a title, and the OAuth scopes required to decide.

```ts
const approvalHandoffs = {
  csv_import: createPipelineTaskApproval({
    contract: csvReviewContract,
    service: csvGateService,
    title: "Import validated rows",
    scopes: ["imports:approve"],
  }),
};
const tasks = createPipelineTasks(toolkit, { ...taskSettings, handoffs, approvalHandoffs });
```

```python
approval_handoffs = {
    "csv_import": PipelineTaskApproval(
        contract=csv_review_contract,
        service=csv_gate_service,
        title="Import validated rows",
        scopes=["imports:approve"],
    ),
}
tasks = PipelineTasks(toolkit, **task_settings, handoffs=handoffs, approval_handoffs=approval_handoffs)
```

Enable `includeApprovals: true` or `include_approvals=True` when paging or
watching the reviewer inbox. Approval summaries have `kind: "approval"`.
Keep this option consistent when reusing a page cursor. Existing inbox calls
continue to return clarification summaries.

Use `createPipelineTaskApprovalClient({ handoffId, contract, transport })` or
`PipelineTaskApprovalClient(handoff_id, contract=..., transport=...)`. The
client's `get` reads the typed review, including its digest and a decision key.
Use `decide(view, "approved", payload, options)` or
`await decide(view, "approved", payload, timeout=...)` after an explicit
reviewer decision. Use `"rejected"` to reject the same review. MCP clients can
call `pipeline_task_handoff_approval` and `pipeline_task_handoff_decide` directly.

Include the immutable input or artifact version in the review contract. The
decision key binds the exact review to the grant and gate. The gate ledger
records the reviewer identity, decision, payload, and time. Within the active
grant, an identical retry by the authorized reviewer returns the original
receipt. Expiry, revocation, and conflicting decisions prevent another
decision. The owner can continue watching the same Task.
