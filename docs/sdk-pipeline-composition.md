# Typed pipeline composition (SDK preview)

A pipeline can invoke one registered pipeline or process a bounded batch of
children. Each child is a native workflow run with its own journal, deployment
snapshot, Jobs, approvals and typed output. The parent uses native journal steps
and sleeps to observe them. Existing pipelines keep their current APIs.

## Register and invoke

Install [pipeline-children.sql](../sdk/request-contracts/pipeline-children.sql)
as an explicit application migration. Use acknowledged autocommit queries on
the same application primary for every driver and recovery worker. Python
queries use PostgreSQL `$n` placeholders; adapt repeated parameters correctly
when using psycopg. Gate and Job receipt schemas retain their existing setup.

```ts
import {
  PipelineChildren, defineJobPipeline, postgresPipelineChildrenStore,
} from '@gregale/sdk-node/preview';

const child = {contract: importContract}; // generated JobContract<Input, Output>
const children = new PipelineChildren({
  scope, parentContract, children: [child],
  store: postgresPipelineChildrenStore(query), token: backendToken, gateStore,
});
const pipeline = defineJobPipeline(parentContract, {
  scope, store: jobReceipts, jobs: {}, children,
}, async (input, wf) => {
  const result = await wf.children('imports', child, input.imports, {
    timeoutMs: 180_000, concurrency: 2,
    failurePolicy: 'collect', onParentStopped: 'cancel_children',
    onDeadline: 'cancel_children',
  });
  return summarize(result);
});
```

```python
from faas_sdk.preview import PipelineChild, PipelineChildOptions, PipelineChildren

child = PipelineChild(contract=import_contract)
children = PipelineChildren(
    scope=scope, parent_contract=parent_contract, children=[child],
    store=children_store, token=backend_token, gate_store=gate_store,
)
# Pass children=children to JobPipeline(...).
result = await wf.children(
    "imports", child, inputs,  # Sequence[JobInput[Input]] with stable IDs
    options=PipelineChildOptions(
        timeout=180, concurrency=2, failure_policy="collect",
        on_parent_stopped="cancel_children", on_deadline="cancel_children",
    ),
)
```

`wf.child(name, child, input, options)` is the single-child form (Python uses
`options=`). It returns one typed child view and uses a one-slot batch with the
stable item ID `one`. Register the exact definition object supplied to these
calls. Publish both parent and child workflow definitions in the owning app.
This first slice supports account-owned workflows within the same app/scope.
Their independent native runs remain subject to the platform's plan limits.

Await composition calls sequentially; use a batch for concurrency. Names must
be unique across Jobs, gates and child batches in a parent invocation. Pending
or denied parent gates block child admission. The child inherits its parent's
application owner; callers cannot choose a different child owner or account.

## Outcomes and policies

Results preserve input order and contain the batch identity, parent run ID,
batch name, stop reason, completion flag and child views. Each child has a stable
item ID, phase, optional native run ID/status, cancellation-request flag, and a
generated typed output only after verified native success. Phases are:

| Phase | Meaning |
| --- | --- |
| `unstarted` | Not admitted yet |
| `submission_unresolved` | Reserved; start or receipt acknowledgement is uncertain |
| `running` | Accepted native child, including event/approval waits |
| `succeeded` | Native success and validated output |
| `failed` / `cancelled` | Observed native terminal outcome |
| `skipped` | Never admitted before the batch stopped |

`failurePolicy: 'collect'` admits remaining items after individual failures.
`'fail_fast'` stops admission when a child failure/cancellation is recorded.
`onFailFast`, `onDeadline` and `onParentStopped` independently select
`leave_running` or `cancel_children`; all default to `leave_running`.
Python uses the equivalent snake_case fields. A stopped parent includes native
cancellation, failure, death or completion. A stop can return running or unresolved
children, so inspect `stop_reason`, `complete` and each phase. The parent handler
decides whether these outcomes should fail the business operation.

The required timeout starts at the first database registration and replay never
extends it. Database-clock expiry fences admission atomically with the receipt
revision. A child may finish after the deadline when policy leaves it running.
Cancellation requests are recorded before calling native cancellation, and
`cancel_requested` alone does not establish terminal cancellation. Native cancel
is safe to repeat on an already terminal workflow run.

Cancelling a child stops its orchestration. Already accepted Jobs retain their
independent execution and explicit cancellation semantics. Native cancellation,
application auth and the ledger are separate services: an already in-flight
start can race a parent stop. Recovery observes/adopts accepted children and
applies the captured cancellation policy. Unknown starts require reconciliation
before their native run can be cancelled.

## Recovery and provenance

One revisioned application row owns the immutable input manifest, policies,
deadline, child reservations and checkpoints. A CAS enforces concurrency across
competing Node/Python drivers. A reserved child consumes a slot even when its
native start response is lost. Only the driver receiving a newly acknowledged
reservation can send its initial start request. Retries and lost journals reuse
receipts and never automatically repeat an uncertain start.

Child input envelopes carry SDK lineage: parent run ID, batch identity and item
ID. This participates in the input digest. Native reads verify account, app,
workflow/contract, owner and the exact input/lineage digest. A different run with
identical business input and different lineage cannot satisfy the receipt.
The optional envelope field is preserved by updated typed clients; ordinary
starts and existing checkpoints retain their previous shape. Deploy matching
SDK versions for composed parent/child handlers and their approval services.

After identifying a known child run, authorize the parent owner and call:

```ts
await children.reconcile(parentId, 'imports', itemId, childRunId, ownerId,
  {timeoutMs: 10_000, signal});
await children.recover(100, {timeoutMs: 10_000, signal});
```

Python exposes `reconcile(..., timeout=10)` and `recover(100, timeout=10)`;
asyncio task cancellation propagates. Recovery must run regularly on a trusted
backend, including after restarts and while no browser is open. It observes
accepted runs, records stops and retries idempotent cancellation; it never
starts an unstarted or unresolved child. Recovery batches rotate across rows.
An interrupted mutation can already have committed; inspect before choosing a
new action. Retain application receipts alongside native run history for the
desired audit/recovery period. Attaching by ID does not restart a failed native
code workflow, and composition does not add native continuation support.

## Progress and bounds

After authenticating the current application user, call
`children.inspect(parentId, batchName, ownerId, child, options)`. Python has the
same method with `timeout=`. This returns authorized child details without
starting/cancelling runs. Supplying a gate store adds pending approval/input
metadata for children whose native status is `awaiting_event`.
`pipelineChildrenLiveSnapshot` / `pipeline_children_live_snapshot` combines the
view with authoritative parent inspection for the existing live bridge and
React/browser clients. Counts represent child pipelines; the full view supplies
individual run links, outcomes and report descriptors. Private tickets and all
report access still belong to application authorization.

SDK work budgets are 50 items per batch, concurrency 1–10, 64 KiB intent data,
128 KiB receipt state, and a required timeout of 1 ms–24 hours. These are SDK
budgets, not platform quotas. Intermediate polls journal compact progress; child outputs enter the parent
journal at completion/stop. Existing workflow operation, journal, poll and plan
limits still apply. Request deadlines cover auth/provenance reads and each
round's I/O; choose the request budget for the batch size and backend latency.
Transient open/round failures retry under the native step policy (three attempts
here); invalid bindings fail without retry.

The [batch CSV example](../examples/sdk-csv-jobs/README.md#batch-imports-preview)
includes generated contracts, both SDKs, live parent/child views and protected
review/report links. Its PostgreSQL/Chrome fixture exercises competing drivers,
lost replies/journals, reconciliation and two-user access. Native control-plane
and realtime behavior in that fixture is simulated; it is not deployed acceptance.
