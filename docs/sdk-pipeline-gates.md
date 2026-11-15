# Approval gates and external input (SDK preview)

Typed job pipelines can pause for an application user, reviewer, or another
service. They use the existing code-workflow journal and native event wait.
The application owns authentication and the PostgreSQL gate ledger. Existing
pipelines need no migration; opt in with a gate store and new gate calls.

## Define the review and reply

Use a generated `JobContract<Review, Reply>`. Its input describes the exact
action being reviewed; its output validates the decision payload. Include
immutable artifact digests and the planned action inputs, rather than a URL
whose content can change. Generate both languages with the existing
`job-types` command. Use a `null` output schema when no reply data is needed.

```ts
// @gregale/sdk-node/preview
const pipeline = defineJobPipeline(rootContract, {
  scope, store: jobReceipts, jobs, gateStore,
}, async (input, wf) => {
  const checked = await wf.job('validate', [{id: input.id, value: input}]);
  const review = {source: input.source, rows: checked.results[0]!.result.rows};
  const decision = await wf.approval('review', reviewContract, review, {
    timeoutMs: 60_000, requiredRoles: ['reviewer'],
  });
  if (decision.state !== 'approved') return {status: decision.state};
  return importReviewedRows(review.rows, wf);
});
```

```python
# faas_sdk.preview; generated review_contract retains Review/Reply types
decision = await wf.approval(
    "review", review_contract, review,
    options=PipelineGateOptions(timeout=60, required_roles=("reviewer",)),
)
if decision.state != "approved":
    return {"status": decision.state}
```

Approval outcomes are `approved`, `rejected`, `expired`, or `cancelled`.
`wf.waitForInput` / `wf.wait_for_input` instead accepts a typed `submitted`
reply, or expires/is cancelled. Successful/rejected replies carry `actor_id`,
`payload`, and `decided_at`; expiry has a null actor and payload. Call gates
sequentially and await each result. A pending or denied gate blocks future
`wf.job` submissions in that invocation. A handler can return a typed business
rejection without failing the native workflow.

Gate names are unique within a run, including Job stage names. Review and reply
data are capped at 16 KiB each; required roles are explicit (all must match),
with at most 16 roles. The gate timeout is required, between 1 ms and 24 hours;
the native workflow plan's own limits still apply. The existing pipeline
operation/journal budgets also apply. At most 16 native wakes are processed
per gate; irrelevant or forged notifications never grant approval.

## Install the ledger and authenticate decisions

Install [pipeline-gates.sql](../sdk/request-contracts/pipeline-gates.sql) using
your application's migration process. Construct `postgresPipelineGateStore`
or `PostgresPipelineGateStore` with an acknowledged autocommit query against
the same application primary for every gate service and workflow worker. Never place gate writes in an
ambient business transaction or read them from an asynchronous replica.
Python callbacks use PostgreSQL `$n` placeholders, just like pipeline receipts;
translate repeated parameters correctly if using psycopg `%s` placeholders.

```ts
const gates = createPipelineGateService({
  scope, pipelineSha256: rootContract.sha256,
  contract: reviewContract, store: gateStore,
  transport: nativePipelineGateTransport({
    scope, token: backendToken, contract: rootContract, timeoutMs: 10_000,
  }),
  authorize: async (record, action, session, signal) => {
    // Read current application membership from your trusted auth system.
    return authenticateMembership(session, record.intent.owner_id, signal);
    // Returns {actor_id, owner_id, roles}. Never derive these from POST JSON.
  },
});
// view.can_decide reflects current roles, decision and native activity.
const view = await gates.inspect(gateId, session, {timeoutMs: 10_000});
await gates.decide(gateId, {
  binding_sha256: view.binding_sha256,
  state: 'approved', payload: {reason: 'Reviewed the exact rows'},
}, session, {timeoutMs: 10_000});
```

Python exposes the equivalent `PipelineGateService`, `NativePipelineGateTransport`,
`PipelineGateActor`, `inspect`, `decide`, and `drain`; pass `timeout` in seconds
and use normal asyncio task cancellation. Node accepts an `AbortSignal` and
milliseconds. Deadlines bound each service operation, including auth, database,
native verification, and delivery. An interrupted decision write may already
have committed: retry the identical decision or inspect it before choosing again.

The native transport verifies the current platform account, app, root workflow
name/contract, owner, and original input digest before accepting requests. The
application must still authenticate the current user and authorize the exact
run before listing gate IDs, issuing private live tickets, or returning reports.
The store/transport interfaces are trusted backend adapters, not browser APIs.

Decisions bind the workflow/run, owner, root contract/input, gate contract,
review, roles, and timeout. A changed review conflicts under the same gate ID.
The database performs an atomic first-decision-wins update and enforces expiry
using its own clock. Identical retries by the same actor preserve the original
decision; a different actor, payload, or opposing decision conflicts. Every
retry checks current membership again. Previously committed approvals remain
auditable decisions; revoking membership does not rewrite them.

## Recover and display progress

Decision persistence and pending delivery share one database row. Deliveries use
a stable native event name, payload, and idempotency key. Run
`gates.drain(100, {timeoutMs: 10_000})` / `await gates.drain(100, timeout=10)`
from a trusted backend recovery loop after startup and regularly afterward.
Batches prioritize committed/expired decisions and rotate active gates, so a
full batch cannot permanently hide a later cancelled parent.
An uncertain publish stays pending. A new service instance can resend it.
Inactive parents close pending gates; cancelling the gate itself uses a
`cancelled` decision through the authorized service. Cancelling orchestration
does not cancel already accepted Jobs.

Native events only wake the workflow. The runtime ignores their content and
reads the authoritative gate decision before continuing. Open/resolve steps
retry transient failures up to three times and replay after process restarts.
A failed native code workflow still cannot continue; inspect/attach-by-ID
does not restart a terminal workflow. Retain the original run ID/checkpoint
and gate ledger for the desired recovery/audit period.

`LiveJobSnapshot.waiting`, when present, contains `{kind, gate_id, name}`.
Applications obtain it from an authorized gate read and combine it with the
existing native pipeline snapshot. The existing live bridge, browser client,
and React hook carry it without an extra browser polling loop. Private tickets
must be renewed through application authentication. Clear displayed review
data when the principal changes or access is revoked.

See the [CSV review example](../examples/sdk-csv-jobs/README.md#reviewed-import-preview).
Shared contract/journal fixtures and the local PostgreSQL/Chrome tests verify
cross-language recovery. These fixtures do not establish deployed acceptance.
