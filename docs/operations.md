# Gregale Operations

Operations defines an application contract for customer work: typed input and
output, verified ownership, progress, result references, and completion delivery.
This implementation is **internal and not launched**. HTTP execution, private
result retention, SDK helpers and bounded preview admission are implemented
locally. Production submission and definition registration remain disabled. See
[ADR-521](adr/521-customer-operations.md) and the
[implementation checkpoint](operations-http-preview-implementation.md).

An operation identifies the customer's logical request. Execution attempts
retain their own identities and recovery semantics. Business outcome and
notification outcome are separate fields: a successful export remains successful
while its completion webhook is awaiting retry.

The customer operation context reserves `X-Gregale-Customer-Operation-Id`.
The existing `X-Gregale-Operation-Id` identifies an exclusive operation and
retains its separate meaning. Public ingress strips both reserved header
namespaces. The execution adapter attaches customer context only after checking
the persisted invocation claim.

## Workflow execution

An operation can name `workflow: export-chain` from its immutable deployment.
Its POST path becomes the submission route. The first adapter accepts a linear
chain of HTTP steps in the default production scope, with progress stages equal
to the ordered step names. It excludes branches, waits, outbound actions,
iteration, compensation and automatic retries. When retry is omitted, the
adapter captures an explicit one-attempt policy per generation.

Admission commits the scoped operation, workflow run, seeded steps and receipt
atomically and enforces both Operations and native workflow quotas. Successful
steps retain their outputs. Progress is projected from the workflow ledger; the
final workflow output must match the operation's output schema. Dispatch stays
on the operation's pinned deployment/release even after the default changes.

A failed or interrupted dispatched HTTP action requires reconciliation. An
account-authorized `safe_to_retry` recovery with verification evidence resumes
the same run, keeps successful outputs and original inputs, and advances the
operation generation together with the native resume receipt. Direct workflow
resume or step retry cannot bypass this boundary. Stable action keys must remain
valid across resumes; operator approval alone does not deduplicate an external
provider. Cancellation stops the scheduler call and fences stale output, while
an uncertain external effect still needs reconciliation.
Native workflow active-run and resume limits also apply; rejected recovery
leaves the operation generation and recovery receipt unchanged.

Typed JSON results and independent completion webhooks use the existing
customer APIs and browser session. The HTTP runtime claim, progress report and
private artifact helpers continue to require their own invocation authority;
the workflow adapter uses a separate final-step artifact proof.
`GregaleWorkflowOperations.uploadArtifact({report_id, name, data, maxBytes})`
uploads verified bytes directly to private operation storage. It needs no bucket,
source URI or provider credential. The stable report identity binds the operation,
workflow run and final step, independently of the current generation/attempt.
Keep its name and bytes unchanged across approved resumes. The SDK checks for a
durable receipt before every transfer retry, including after a lost response.
Retaining bytes never completes the action; only a confirmed final-step success
publishes the reference.
An explicit operator `succeeded` resolution can publish a verified pending copy.
A failed/interrupted attempt keeps its copy private for reconciliation. Approved
resume can rebind that copy to its new attempt without rereading or writing the
source. Lookup requires fresh native authority and can rebind the existing receipt
to the resumed attempt without consuming another report or emitting another event.
Current ownership, cancellation, deadline and lease checks fence transfers and
receipt binding. Generic authorization errors never mean “upload missing.”
Existing artifact/account quotas, retained downloads and owner-deletion cleanup
apply. See [ADR-670](adr/670-workflow-operation-direct-artifact-uploads.md).
The [Customer Workflow Operations native lane](ops/customer-workflow-operations-native.md)
adds real-guest scenarios for these contracts. Its KVM execution and leak receipts
remain pending; workflow admission stays closed.

The browser-safe Node SDK exposes `CustomerOperationFeature<TInput, TOutput>` as
the shared customer-facing lifecycle controller. It composes host login
credentials with session creation, history loading, receipt resume,
identity-change fencing and teardown; the UI still owns rendering and explicit
operation actions. Its input generic types submissions and its output generic
types operation results; server-side JSON Schema validation remains authoritative.
The lower-level `CustomerOperationAuth` adapter remains available for custom
lifecycle integrations. The HTTP, Job and workflow starters use the controller
and the same host adapter contract. See [ADR-673](adr/673-shared-customer-operations-auth.md)
and [ADR-674](adr/674-customer-operation-feature-controller.md).

Run `gregale customer-operations types --dir . --app <slug> --plan <plan>` to
generate a source-local TypeScript declaration file from the validated input
and output schemas. The HTTP, Job and Workflow starter browser apps reference
these generated types through JSDoc, so editors can type `session.start()` and
the returned operation result. The generator projects schema shapes; server
validation remains authoritative. Commit the declaration and run the same
command with `--check` in CI to fail when it is missing or stale; check mode is
read-only. The starter `npm run typecheck` command also checks browser feature
code against the generated declarations and SDK types. See [ADR-675](adr/675-customer-operation-typescript-generation.md).

The existing `prepareArtifact` helper remains available for managed `obj://`
sources. It requires a stable source key and checks for a verified copy before
calling the application's writer. An uncertain external write without a verified
copy still needs provider evidence before a fresh request may write again. See
[ADR-658](adr/658-workflow-operation-artifacts.md).
Every linear HTTP workflow action can opt into
`GregaleWorkflowOperations.runCancellableRequest(req.headers, async scope => ...)`.
The scope reads a current native control proof before entering business code,
polls cancellation/authority, and checks again before returning success. Pass
`scope.signal` to I/O, call `await scope.checkpoint()` between work units, and
check/yield during long CPU loops. Direct uploads observe the scope's abort signal;
managed-source artifact writers receive the same optional `signal`.
The fixed deadline comes from the current attempt's start and captured timeout
(30 seconds when omitted), with the native lease supplying an earlier stop bound.
Reads never renew either bound. Cancellation can appear as lost native authority
because it atomically interrupts the attempt. Expired attempts cannot prepare
files or commit successful outputs, even with a live lease. Stopping an upload
does not undo an external write or discard an already verified private copy.
See [ADR-659](adr/659-workflow-operation-cooperative-control.md).

Initialize the complete customer feature with
`gregale init --template customer-operation-workflow-export --path workflow-export`.
It includes history, reload recovery, step progress, cancellation, private downloads
and a receipt-backed operator recovery guide. Install the internal packed SDK as
described in the [workflow export starter](../examples/customer-operation-workflow-export/README.md).
See [ADR-672](adr/672-customer-workflow-operation-starter.md) and
[ADR-676](adr/676-workflow-customer-operations.md). Admission remains closed.

## Waits and approvals

A workflow operation can pause without holding an app instance. Besides HTTP
actions, its linear chain may contain a `wait_for_duration` timer or a
`wait_for_callback` step that the definition declares as an approval:

```yaml
workflows:
  - name: refund-chain
    steps:
      - {name: validate, path: /refunds/validate}
      - {name: manager-approval, wait_for_callback: true, timeout: 72h, depends_on: [validate]}
      - {name: cooling-off, wait_for_duration: 24h, depends_on: [manager-approval]}
      - {name: refund, path: /refunds/execute, depends_on: [cooling-off]}
operations:
  - name: refund
    workflow: refund-chain
    progress_stages: [validate, manager-approval, cooling-off, refund]
    approvals:
      - {step: manager-approval, approver: account}
```

While parked, the operation is `waiting` with `waiting: {kind, step, since,
until, approver}`; history records `waiting` and `resumed` events. The declared
approver decides with `POST /v1/apps/{slug}/operations/{id}/approvals/{step}`
(account) or `POST /v1/platform-tenant-self/customer-operations/{id}/approvals/{step}`
(customer), sending `expected_generation`, `decision` (`approved` or `rejected`)
and an optional comment, or run `gregale customer-operations approve OP_ID --app
SLUG --expected-generation N --step STEP --decision approved` (`--self` with a
customer token). The decision and the deciding key or token are recorded as an
`approval_decided` event. Repeating a decision is idempotent; a different
decision conflicts. Rejection settles the operation `failed` with
`approval_rejected`; a passed approval timeout settles `approval_expired`. Waits
never require reconciliation, and cancelling a waiting operation settles it
`cancelled`. Generic workflow callback completion is refused for operation runs,
so only the approval endpoints can resume an approval. Condition checks and
branches remain unavailable to operations. See
[ADR-1194](adr/1194-operation-waits-and-approvals.md).

### Event waits and signals

A step can also wait for something outside the app, such as a payment provider
confirming a charge:

```yaml
      - {name: payment, wait_for_event: payment.succeeded, timeout: 24h, depends_on: [validate]}
      - {name: ship, path: /orders/ship, depends_on: [payment], input: {charge: '{{steps.payment.output.charge}}'}}
```

An event wait needs a `timeout`. While parked, the operation is `waiting` with
`waiting.kind: event` and `waiting.event`. Deliver the event with a signal:

```sh
gregale customer-operations signal OP_ID --app shop --event payment.succeeded \
  --signal-id evt_3Nx --payload '{"charge":"ch_1"}'
# Or reach every active operation about one business subject:
gregale customer-operations signal --app shop --scope production --operation fulfil \
  --subject order:ord_42 --event payment.succeeded --signal-id evt_3Nx
```

The API routes are `POST /v1/apps/{slug}/operations/{id}/signals` and
`POST /v1/apps/{slug}/operation-signals`. Both take deploy-write scope and MFA.
The payload becomes the wait step's output. `signal_id` makes delivery
idempotent: repeating a signal returns its original receipt, and reusing the ID
with a different event or payload is `409 operation_signal_conflict`.

- An event the workflow never waits for is `422 operation_signal_unexpected`.
- A settled operation takes no new signals (`409 operation_not_active`).
- A signal that arrives before its wait is held and used when the wait starts.
- A subject signal reaches at most 16 active operations of that definition,
  matched by the [subject](#business-references) fixed at acceptance.
- History records each delivery as a `signal_received` event, with its
  `source`: `account`, `customer` or `webhook`.
- A wait that times out settles `failed` with `event_timeout`.
- Native workflow event injection is refused for operation runs, so every event
  appears in the operation's history.

See [ADR-1197](adr/1197-operation-event-waits-and-signals.md).

A customer resumes its own operations with its tenant credentials and the
`platform_tenant:operations:manage` scope, without the account's token:

```sh
gregale customer-operations signal OP_ID --self --event payment.succeeded --signal-id evt_3Nx
gregale customer-operations signal --self --definition DEF_ID --subject order:ord_42 \
  --event payment.succeeded --signal-id evt_3Nx
```

The routes are `POST /v1/platform-tenant-self/customer-operations/{id}/signals`
and `POST /v1/platform-tenant-self/customer-operation-signals`. The subject
route names the definition, which fixes the app, environment and operation, and
reaches only the customer's own operations; another customer's operation about
the same subject ID is never signalled. A `signal_id` is per operation whichever
route delivers it. See
[ADR-20261017100853](adr/20261017100853-customer-self-service-operation-signals.md).

## Deadlines and budgets

A definition can bound each Operation it accepts:

```yaml
operations:
  - name: refund
    # ...contract fields...
    limits:
      deadline_seconds: 3600       # wall clock from acceptance, waits included
      max_attempts: 5              # automatic attempts of one HTTP execution
      max_execution_seconds: 300   # attempt runtime summed across generations
```

Each field is optional and bounded by the plan (see [Limits](#limits)). Limits
are fixed when an Operation is accepted; a later definition does not change
accepted work.

- **Deadline.** The Operation settles `failed` with `failure_code:
  deadline_exceeded` when its deadline passes, whether it is queued, retrying,
  running or waiting for a timer or approval. Its completion callback fires.
  An HTTP attempt still running at the deadline is cut. Unless the definition
  declares `recovery: safe_retry`, that attempt needs reconciliation,
  because Gregale cannot know whether it took effect. Workflow Operations
  settle within one minute of the deadline.
- **Attempts.** `max_attempts` becomes the HTTP execution's retry policy.
- **Execution budget.** HTTP attempts are cut when the summed runtime reaches
  `max_execution_seconds`; a retry that would start with no budget left settles
  the Operation `failed` with `budget_exhausted`.
- **Recovery** with `safe_to_retry` is refused (`409 operation_limit_exceeded`)
  once a deadline has passed or the budget is spent. Recording an outcome is
  still allowed.

Workflow definitions accept `deadline_seconds` and `max_execution_seconds`
([ADR-20261016110803](adr/20261016110803-workflow-operation-execution-budget.md)). The budget sums
the runtime of the HTTP step attempts; timer, approval and event waits do not
count. Each attempt is cut at the earlier of its step timeout and the budget
still unspent. A cut step, like any failed workflow step, needs reconciliation,
and `safe_to_retry` recovery is refused once the budget is spent. Workflow steps
never retry automatically, so `max_attempts` is HTTP-only.

Job definitions accept no limits. Reads return `limits`, `deadline_at` and
`usage {execution_ms, attempts}`, and `gregale customer-operations get` prints
the deadline and budget. See
[ADR-1196](adr/1196-operation-deadlines-and-budgets.md).

## Contract

`OperationDefinitionSpec` captures an HTTP target or named workflow, platform-tenant ownership,
JSON input/output schemas, progress stages, a completion webhook, and recovery
policy. Schema validation accepts bundled local references and rejects external
resources. Canonical JSON supplies stable input identity, rejects duplicate
members, and treats equivalent number spellings consistently.

Result artifacts bind a filename, exact byte count and SHA-256. Direct uploads
derive an opaque operation reference and verify the supplied bytes. Managed-source
attachments also declare an object reference and verify its ownership and scope.
Both retain a private copy. Downloads verify that copy again and never fall back
to a mutable customer object. Cleanup uses durable
receipts, including abandoned copies from interrupted uploads.

Uncertain effects require reconciliation by default. `safe_retry` is an explicit
declaration that repeating the handler is safe; it does not establish exactly-once
external effects. Confirmed results and delivery receipts have independent
lifecycles.

Ordinary HTTP definitions can opt into `transaction_receipt: postgres_v1` with
`reconcile_on_unknown`. The Node, Go and sync/async Python transaction adapters
commit supplied-transaction business writes and the saved typed result together
in the customer's PostgreSQL database. After an evidenced approved recovery,
the handler replays a committed receipt before invoking business code. The receipt
is bound to the captured scope, contract and deployment/release, plus exact input.
A committed receipt does not certify platform completion or external effects;
completion delivery still follows the existing independent path. Install and
retain the receipt schema explicitly. See [transaction adapter usage and
acceptance](operation-transactions.md#customer-operations-http-adapter) and
[ADR-661](adr/661-customer-operation-transaction-receipts.md).
## Business references

Declare a public reference to the application entity this work belongs to:

```yaml
operations:
  - name: fulfill-order
    # Other contract fields and schemas remain required.
    subject:
      type: order
      id_from: /order_id
```

Gregale captures `subject: {type: order, id: "ord-123"}` once from validated
input. The JSON Pointer must select a nonempty string of at most 256 UTF-8
bytes. References survive progress, completion, redeploy replay, and recovery.
Existing declarations and records may omit them.

Customer history supports paired `subject_type=order&subject_id=ord-123`
filters with the existing explicit `app_id` and `scope`. Ownership still comes
from the customer's credentials. Identical IDs used by different customers
remain isolated. The reference is public correlation metadata; application
handlers must still authorize the underlying order before business writes or
receipt replay. Choose stable public IDs suitable for customer-visible history.

The browser client accepts `{appID, scope, subjectType: 'order', subjectID:
'ord-123'}` in `list`. Go exposes `SubjectType`/`SubjectID` in
`OperationListOptions`; generated Python list calls accept `subject_type` and
`subject_id`. Both selectors are required together and pagination preserves
them. An operator can use:

```sh
gregale customer-operations list --app orders --scope default \
  --subject-type order --subject-id ord-123
```

The dashboard offers reference filters and links to related operations within
the selected environment and customer. See
[ADR-714](adr/714-customer-operation-business-references.md).

## PostgreSQL HTTP handler transactions

The internal Node adapter connects an HTTP Operation to application business
writes. Set `http_transaction_version: 1` in its immutable definition and
explicitly install the SDK's `customerOperationReceiptSchema` in the application's
PostgreSQL database as its owner. Version 1 saves the full JSON business result;
the output schema describes that result directly. The same setting is accepted
in YAML/TOML source declarations and appears in definition listings, SDK models,
and CLI validation. Source bundles and build retry preserve the immutable pin.
Ordinary definitions do not advertise transaction support.

Go handlers can use `NewCustomerOperationRuntime` and
`CustomerOperationRuntime.Transaction` for the same receipt, milestone, and
workflow-state transaction path. The [Go SDK guide](../sdk/go/README.md)
includes a `net/http` example, replay behavior, and handling for committed
publication errors.

Python async handlers can use `GregaleOperations.transaction` with an idle
psycopg `AsyncConnection`. The [Python SDK guide](../sdk/python/README.md)
shows exact raw request capture, business-row locking, and replay handling.

The runnable [order-fulfillment example](../examples/customer-operation-orders/README.md)
includes business authorization, explicit database setup, deployment packaging,
committed progress, and receipt-backed recovery after a lost HTTP response.

Capture the original request body before JSON middleware changes it. Perform
business authorization before calling `transaction`, because a receipt replay
skips the callback. This Express-style example assumes `rawBody` is a Buffer
and `authorizeOrder` checks the verified customer against the application's data:

```ts
import { GregaleOperations } from '@gregale/sdk-node/preview';

const operations = new GregaleOperations({ apiURL: 'https://api.example.com' });

app.post('/orders/fulfill', async (req, res) => {
  const input = JSON.parse(req.rawBody.toString('utf8'));
  const customerId = req.get('X-Faas-Platform-Tenant-Id');
  await authorizeOrder(customerId, input.orderId);
  const receipt = await operations.transaction({
    headers: req.headers, method: req.method,
    path: req.originalUrl, body: req.rawBody,
  }, pool, async tx => {
    const { rows } = await tx.query(
      "UPDATE orders SET status='fulfilled' WHERE id=$1 AND customer_id=$2 RETURNING id,status",
      [input.orderId, customerId],
    );
    if (rows.length !== 1) throw new Error('Order unavailable');
    return { orderId: rows[0].id, status: rows[0].status };
  });
  res.type('application/json').send(receipt.body);
});
```

Use this only on Gregale's trusted guest listener. Ingress strips the reserved
headers and synthetic delivery reconstructs negotiation from the pinned
definition after validating the invocation, attempt, and capability. The helper
requires version 1, verified owner UUIDs, and the pinned result byte limit.
`runRequest`, `progress`, and `artifact` remain available; `transaction` establishes
the same private runtime context for its callback.

Business writes and `public.gregale_customer_operation_inbox` commit in one
READ COMMITTED transaction. Concurrent duplicates serialize on the Operation
UUID. Receipt replay verifies account, app, customer, and exact method/target/body
before returning the saved JSON bytes. Recovery changes the execution identity
while preserving the logical Operation and committed receipt. Invalid JSON,
oversized results, or callback failure roll back both writes and receipt.

The callback must use the supplied transaction, leave commit/rollback to the
helper, and avoid external side effects. `OperationCommitUnknownError` means
the acknowledgement was lost; retain the same Operation identity and use the
existing authorized recovery path. The helper never automatically reruns the
callback. The default reconciliation policy remains in force. A saved receipt
does not authorize stale completion or renew a lease, and PostgreSQL commit and
Gregale completion remain separate transactions.

Keep receipts while the original Operation can replay. Installation and cleanup
are explicit. This result-only protocol uses a separate receipt table and lock
namespace from managed operations; named effects are outside this slice. See
[ADR-713](adr/713-customer-operation-http-transactions.md). Production admission
remains disabled.

## Limits

The proposed qualification policy lives in `pkg/api/limits.go`; it is independent
of existing VM and asynchronous invocation quotas. Free has no Operations
allowance. The following are contract bounds, not a rollout announcement.

| Bound | Hobby | Pro | Scale |
| --- | ---: | ---: | ---: |
| Definitions per app | 10 | 50 | 200 |
| Pending operations per account | 1,000 | 10,000 | 100,000 |
| Reports per operation | 1,024 | 4,096 | 16,384 |
| Recoveries per operation | 32 | 128 | 256 |
| Progress stages | 16 | 32 | 64 |
| Result retention | 7 days | 30 days | 90 days |
| Event retention | 1 day | 7 days | 30 days |
| Idempotency retention | 30 days | 90 days | 180 days |
| Artifacts per operation | 8 | 16 | 32 |
| Bytes per artifact | 8 MiB | 32 MiB | 64 MiB |
| Total artifact bytes per operation | 32 MiB | 128 MiB | 256 MiB |
| Maximum `deadline_seconds` | 1 day | 7 days | 30 days |
| Maximum `max_attempts` | 3 | 10 | 25 |
| Maximum `max_execution_seconds` | 1 hour | 1 day | 7 days |

Schemas are capped at 64 KiB, submission JSON at 1 MiB, and idempotency keys at
128 bytes. Retained idempotency receipts outlive results. A replay during that
window must not silently create another operation after its result expires.
Quota problems expose numeric `limit` and `observed` values and link here.

API verification spooling is bounded to 256 MiB per node, with at most four
concurrent transfers per account and eight per node. Transfers have a 30-second
deadline. The cleanup worker processes at most 20 receipts per one-minute tick;
failed deletion is retried after five minutes with a fenced cleanup lease.

## Staged HTTP API

The built-in dashboard exposes retained customer work at
`/dashboard/apps/{slug}/customer-operations`. The list filters by environment,
customer ID, operation name and business state, with the API's bounded,
filter-bound history cursors. Open an operation to inspect its reported progress,
declared stages, business timeline, execution generations and pinned deployment.
Invocation links open the existing account-scoped invocation details. Business
outcome and completion notification status remain separate; reading the page
does not recover work or retry delivery.

The detail view includes retained result availability and authorized artifact
download links. Input/result bodies, artifact storage locations, runtime
capabilities and raw event payloads are excluded. Missing definition or history
reads remain explicit unavailable states while the current business snapshot is
visible. An event cursor outside retained coverage shows a resynchronization
notice rather than reconstructing history. Refresh reads current status; all
times are UTC and pages are not cacheable.

These read-only pages use the existing account session and MFA boundary. They
remain available for retained work after admission closes and do not enable
production submissions or promote Operations beyond its internal status. The
separate `faas-web` console is not changed by this built-in dashboard slice.

Account credentials with the existing MFA policy can read and cancel work through
`/v1/apps/{slug}/operations/{id}`. Account-only `POST .../{id}/recover` requires
`deploy:write`, an expected generation, stable recovery ID and reconciliation
evidence. A stale generation cannot repeat work.

Account operators can list retained work by app and environment, optionally
filter by tenant, inspect durable events and execution generations, and retry
only a dead completion delivery. `gregale customer-operations` provides these
actions plus status watching, verified downloads, cancellation and explicit
recovery. See the [backend operator CLI guide](ops/customer-operations-cli.md)
for commands, authority requirements and exit codes. Developers can also inspect
immutable deployment definitions, validate source manifests and sample input
offline, and submit as a tenant through `start --self` with a private reusable
request receipt. Tenant `get`, `events`, `watch`, `download` and `cancel` commands
use server-derived ownership. Receipt replay retains the original definition,
input and idempotency key; known acceptance returns the same operation without
another submission.

Customer bearer tokens use a separate namespace:

- `GET /v1/platform-tenant-self/customer-operations` discovers retained work with
  the read scope. Require an explicit `app_id` and environment `scope`; optional
  `name` and `state` filters narrow the view. Pages default to 20, cap at 100, and
  return a `next_cursor` bound to account, authenticated tenant, app, environment
  and filters. Creation time and ID determine descending order; progress updates
  do not move rows. This is a live view: retention and state changes can change
  membership. Active work stays visible; expired settled work is omitted.
  Summaries omit input, result bytes, artifact locations, delivery errors and
  execution authority. History remains readable when admission closes.
- `POST /v1/platform-tenant-self/customer-operations` requires
  `platform_tenant:operations:manage` and an explicit stable `Idempotency-Key`.
- `GET /v1/platform-tenant-self/customer-operations/{id}` requires
  `platform_tenant:operations:read`. Foreign and missing identities both return 404.
- `GET /v1/platform-tenant-self/customer-operations/{id}/events` returns bounded
  JSON history or SSE when `Accept: text/event-stream` is requested. Reconnect with
  `Last-Event-ID`; an explicit `after` query overrides it. SSE `operation` frames
  carry durable events, `snapshot` carries current status, and `resync` carries a
  fresh snapshot when retained history cannot cover the cursor. Credentials are
  rechecked throughout the stream.
- `POST /v1/platform-tenant-self/customer-operations/{id}/cancel` requires the
  manage scope and `expected_generation`. Cancellation remains an intent when
  dispatch has begun.
- `GET /v1/platform-tenant-self/customer-operations/{id}/artifacts/{artifact}`
  requires the read scope and downloads a verified retained result. Its SHA-256
  is exposed through `X-Gregale-Artifact-SHA256`. Results can expire independently
  of the operation's successful business outcome.

While the owning account suspends a customer, that customer's reads close at
once: listing, status, events, milestones, identity, submission lookup and
artifact downloads return `403`, and an open event stream ends with
`auth_expired` at its next credential recheck. Suspension does not cancel
admitted work, and the account keeps reading every outcome. Resuming the
customer reopens its reads.

Runtime `POST /v1/runtime/operations/{id}/progress` and `.../{id}/artifacts`
require a fresh workload JWT for audience `gregale:operations`, plus the current
invocation ID, attempt and ephemeral capability. Customer and account tokens do
not grant runtime reporting authority. Reports use stable `report_id` values for
retrying the same update. The scheduler renews the live execution claim and
cancels dispatch if renewal fails; uncertain effects require reconciliation.

Runtime `GET /v1/runtime/operations/{id}/control` uses the same workload identity
and attempt proof. It observes cancellation intent, the admitted deadline and
current lease together, without input, result, owner or capability fields.
Expired deadlines and stale claims are denied. This read creates no report,
event or lease renewal and remains available after admission closes. The server
recommends polls every 100–1000 ms, adjusting for short execution leases.

Operator runtime trust uses `operations_workload_jwks_path` and
`operations_workload_issuer` in apid TOML, optionally overridden by
`FAAS_OPERATIONS_WORKLOAD_JWKS_PATH` and `FAAS_OPERATIONS_WORKLOAD_ISSUER`.
Supply the public JWKS matching the workload signer. Missing trust denies
runtime reporting; configuring trust does not open admission.

The Node `GregaleOperations.runRequest(headers, handler)` helper keeps this
authority isolated per HTTP request and fetches fresh workload identity for each
report. Its public `context()` omits the capability. Python provides the same
request isolation through `faas_sdk.preview.operations_runtime.GregaleOperations`.
These HTTP helpers reject native Job and workflow contexts.

Node handlers can opt into `runCancellableRequest(headers, async scope => ...)`.
The scope reads control before entering business code and before accepting its
returned result, refreshes identity on each poll and exposes `signal`,
`deadlineAt`, `checkpoint()` and `throwIfStopped()`. Pass the signal to I/O,
checkpoint before another unit of work, and yield during CPU loops. Send the HTTP
response only after this wrapper resolves. Cancellation, deadline, lease expiry
and unavailable control stop the scope; reports and new file writes are blocked.
Polling and pending control I/O are aborted when the request finishes.

Time budgets use server observation durations, subtract request latency and
check elapsed monotonic time plus forward wall-clock steps. Initial clock skew
and backward steps cannot extend a claim. Control is an advisory observation,
not an atomic fence on external storage or services. Code that ignores the signal
can keep running; cancellation cannot undo an effect. Stopped dispatched work
still follows its pinned recovery policy, including reconciliation for uncertain
effects. The helper never reruns business work or marks it safely cancelled.
Go's `GetOperationExecutionControl` and Python's runtime `control()` expose the
same typed read for applications that manage their own cooperative scope.

The HTTP Node runtime's `uploadArtifact({report_id, name, data, maxBytes})`
uploads bounded private bytes with the current invocation's workload identity.
It snapshots text/bytes, derives their size and SHA-256, and looks up a durable
receipt before each private-transfer retry. Matching concurrent calls coalesce;
changed content under the same report ID conflicts. The default SDK memory bound
is 8 MiB and the API enforces captured file quotas independently. No bucket writer
or provider credential is needed. The receipt does not complete HTTP work and
customers can download the retained copy only after business success. Cancellation
fences new attachments; a finished/replaced invocation cannot reuse its proof.
The [HTTP starter](../examples/customer-operation-export/README.md) returns
`artifact_id` and `rows` and downloads that exact result file. Existing immutable
output contracts stay pinned; the updated starter deploys a new schema revision.
Go exposes `UploadOperationArtifact` and `ReuseOperationUpload`; generated
Node/Python contracts expose the binary and receipt-lookup routes.

The Node runtime's `prepareArtifact({name, uri, data, maxBytes, report_id?})`
snapshots file bytes and computes the exact size and SHA-256. Its
`uploadAndAttach(writer)` invokes the application's existing bucket writer at
most once per prepared file, then reports the managed source reference with one
stable identity. After a lost response, `attach()` replays that report without
rewriting the source. Verification or storage errors remain visible. Prepared
files are confined to their original HTTP request and have no durable client
receipt; they do not authorize recovery or mark business success. The
[SDK example](../sdk/node/README.md#internal-http-operations-preview) and CLI
managed-source examples show application memory bounds and existing storage bindings.

The browser-safe Node `GregaleOperationClient` accepts a credential callback,
refreshes it for requests and stream reconnects, and resumes from durable event
cursors. Go, Node and Python expose typed HTTP contracts. The Go download client
also verifies length and digest and refuses redirects carrying credentials.

Use `GregaleOperationClient.list({appID, scope})` to rebuild the customer's work
list after sign-in, then open an ID through the existing read and subscription
routes. The [export example](../examples/customer-operation-export/README.md)
connects discovery, progress, cancellation and download without an application
operation-state table or browser persistence.

`gregale init --template customer-operation-export --path customer-operation-export`
creates an independently installable HTTP feature starter. Its source includes
schemas, the handler, browser controls and executable tests. The SDK's
`GregaleOperationSession` owns history, subscriptions, submission retries,
selection fencing and result access. An optional
`receiptStore: createBrowserOperationReceiptStore()` saves scoped metadata before
POST and uses Web Locks to coordinate tabs. After reload and fresh sign-in,
`resume()` looks up acceptance and restores current status/progress without
submitting work. Unresolved acceptance requires the same input and an explicit
retry with its frozen definition/key; unconfirmed retries stop after one day.
No credentials or raw input are persisted. The export starter enables this store. The dedicated
`@gregale/sdk-node/operations` entry point is browser-safe;
`@gregale/sdk-node/operations/runtime` supplies trusted handler reporting.
See the [starter README](../cmd/gregale/templates/customer-operation-export/README.md)
for local SDK packaging and preview deployment prerequisites. Initialization
does not change admission, licensing or native qualification requirements.

Source-local declarations bundle input and output JSON Schema files relative to
the selected manifest. An optional `app` selector binds a declaration to that app;
common declarations apply to the selected app. For example:

```yaml
operations:
  - name: customer-export
    method: POST
    path: /exports
    owner: platform_tenant
    input_schema: schemas/export-input.json
    output_schema: schemas/export-output.json
    progress_stages: [generating, storing]
    recovery: reconcile_on_unknown
```

## Inspect and preview recovery

Account operators can read the retained recovery view before recording a decision:

```sh
gregale customer-operations inspect OPERATION_ID --app exports --json
gregale customer-operations recover OPERATION_ID --app exports --preview \
  --expected-generation 1 --resolution safe_to_retry --json
```

The account-only API routes are `GET .../{id}/recovery-inspection` and
`POST .../{id}/recovery-preview` under `/v1/apps/{slug}/operations`. Both require
account read scope and MFA and remain available while admission is closed.
Inspection shows pinned execution/code, ordered confirmed and uncertain workflow
steps, prepared or attached file metadata and retry blockers. File metadata grants
no download access and does not reveal storage locations, raw errors or payloads.

Preview identifies steps to reopen and reuse, retained files available for reuse,
and files a succeeded resolution would publish. It shares the native resume
planner. HTTP retry creates a new invocation and clears previous file references;
workflow retry resumes its retained run. `eligible` means current platform checks
pass. External effects still require operator verification and reconciliation
evidence. Preview starts no work, records no decision, publishes no files and
reserves no quota. Blocked previews exit 4; stale generations return a conflict.
For a succeeded proposal, supply the typed `--result-file`.

Apply uses the existing recovery command with a stable `--recovery-id` and
`--evidence-file`. Optionally pass `--inspection-revision` from the inspection or
preview to reject changed execution or file-binding evidence before a new decision.
The revision does not reserve capacity; apply checks current quota and eligibility
again. An identical accepted recovery request remains replayable with its original
revision. Completion notification recovery stays independent.
See [ADR-660](adr/660-operation-recovery-inspection.md).

## Read one operation's history

`GET /v1/apps/{slug}/operations/{id}/timeline` merges an Operation's events, HTTP
execution attempts, workflow step attempts and completion delivery attempts into
one page ordered by time. It requires account read scope and MFA; platform tenants
read events instead.

```sh
gregale customer-operations timeline OPERATION_ID --app exports --all
gregale customer-operations timeline OPERATION_ID --app exports --all --logs
```

Each source pages after its own position, so following `next_cursor` never skips
or repeats a record while the Operation runs. Attempt entries are read-time
snapshots: a running attempt is not repeated once it ends, so follow live work
with events. Each HTTP attempt names the instance that ran it, its time window
and the UTC archive days to read with `gregale logs --archive`. That is an
instance window, not request-exact correlation. Workflow step attempts carry
timing and outcome only, because the gateway chooses their instance. When event
history has expired, `events_expired` is set and attempts and deliveries remain.
Attempt history is kept for at most 30 days. See
[ADR-1195](adr/1195-unified-operation-timeline.md).

## Bounded preview admission

The local continuation adds the operator TOML setting
`operations_preview_policy_path` to apid and gatewayd-internal. Its default is
empty and admits no new work. An absolute path selects a versioned JSON policy
with an explicit UTC window, account/app/environment cohorts, and individual
platform tenant IDs. No wildcard or all-customer grant is supported. Plan limits
and authentication still apply. Configuring workload trust alone never opens
admission.

Each cohort can select `execution_kinds: ["http", "workflow", "job"]`.
Omitting the field or setting it to null grants HTTP only, including HTTP
transaction-receipt contracts. Native workflow and Job admission requires the
corresponding explicit value and its qualification receipts. Values must be
unique and exact; an empty array or malformed allowlist closes the policy.
The immutable definition determines its execution type. A source deployment
with several types requires all of them before registration or build enqueue.
Update every API and gateway binary before native rollout; older binaries fail
closed on the new field. See [ADR-666](adr/666-operation-execution-preview-admission.md).

The policy is capped at 64 KiB, ten cohorts, ten customers per cohort and a
30-day window (raised from one hour so a pilot can run without hourly file
rotation). Each admission reads the file again. Missing, unreadable,
malformed, expired or group/world-writable files close admission, without a
cached open fallback. Changes use atomic file replacement and require no daemon
restart. The allowed cohort covers definition registration and source deployment;
customer submission additionally checks the authenticated tenant. apid starts
require workload trust and private result storage. Gateway startup retains the
route resolver even with admission closed: a declared operation route returns a
closed-admission error rather than falling through to ordinary execution.

Closing admission also rejects idempotent resubmission; clients can continue
reading their retained status and results. It does not cancel accepted work or
alter execution and recovery semantics. Retained status, events, reports,
downloads, account recovery, delivery and cleanup remain available. A decision
already taken before a policy replacement may still commit; operators verify
closure on every serving node and inspect accepted work separately.

No production cohort is enabled and no replacement PR is open. Native lifecycle
and fleet rollout qualification remain required before a customer preview. The
[preview runbook](ops/operations-http-preview.md) describes preparation,
admission verification and rollback. The proposed
[ADR-20261017102231](adr/20261017102231-operations-preview-exit.md) sets the evidence for the first
cohort, for each native execution kind and for general admission per kind.

Owning accounts can observe submission prerequisites with
`gregale customer-operations doctor --app SLUG --deployment DEPLOYMENT_UUID
--tenant TENANT_UUID [--name NAME] [--json]`. The account read endpoint reports the
responding API node's current preview decision, tenant and plan, pending capacity,
deployment/definition/release pins and configuration presence.
Each selected definition has an `execution_preview` check with its
`execution_kind`; `preview_execution_kind_excluded` blocks submission for that
definition. Use `--name` to inspect a single type in a mixed deployment.
Completion destination warnings stay separate from submission blockers.
Unprobed gateway, runtime, storage I/O, native lifecycle and fleet rollback
checks remain unknown.
Observed eligibility reserves no capacity and grants no admission. Diagnostics
are bounded to 1,024 checks and a ten-second request context using existing state
reads; they add no SQL or migration. See the
[CLI guide](ops/customer-operations-cli.md#diagnose-submission-prerequisites).

Account operators can inspect completion transport with `customer-operations
delivery` and `delivery-attempts`, then use receipt-backed `retry-delivery` for a
specific observed dead notification generation. Exact retry IDs return the
original decision and never regenerate the business result. See
[the backend and CLI guide](ops/customer-operations-cli.md) for lost-response
recovery, identity/retention bounds and the compatible legacy API.

### Durable recovery decision acknowledgements

`recover --receipt-file PATH` saves an immutable private request before applying
an explicit evidenced decision. Resume from the same receipt after a timeout;
`PATH.decided.json` confirms the original decision without reporting stale data
as current business status. Account-only `POST .../{id}/recover-receipt` returns
`OperationRecoveryDecision`; existing `/recover` remains compatible. Request
conflicts fail, expired unconfirmed receipts cannot apply, and preview never
creates receipts. See the [operator examples](ops/customer-operations-cli.md#resume-a-recovery-decision-after-losing-its-response)
and [ADR-662](adr/662-operation-recovery-decision-receipts.md).

## Webhook-submitted Operations

A signed provider event can start an Operation directly. Create an inbound
webhook endpoint (ADR-212), then bind it to an operation and the customer that
owns the resulting work:

```sh
curl -X PUT "$GREGALE_API/v1/apps/billing/inbound-webhooks/$ENDPOINT/operation-binding" \
  -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -d '{"expected_version":0,"operation":"invoice-paid","platform_tenant_id":"'$TENANT'",
       "event_types":["invoice.paid"],"take_over_delivery":true}'
```

The owner is fixed by the binding; Gregale never reads a customer from the
provider payload. The tenant must be active and linked to the app through an
active tenant surface. The operation must exist on the environment's live
deployment when you bind; each event resolves it again and admission pins the
definition exactly as for a customer submission.

Each verified event becomes this input, so the definition's input schema and
`subject.id_from` pointer describe the envelope:

```json
{"webhook": {"endpoint": "stripe-invoices", "provider": "stripe",
             "event_id": "evt_123", "event_type": "invoice.paid"},
 "payload": {"id": "evt_123", "type": "invoice.paid", "data": {"object": {}}}}
```

The same input reaches HTTP, workflow and Job adapters. A provider retry returns
the original operation with `duplicate: true`, even after the binding changes;
the same event ID with a different body returns 409. Event types outside a
non-empty `event_types` list are acknowledged as `ignored` and create no work.
Closed preview admission, a missing definition or capacity limits return 503
with `Retry-After`, so the provider keeps the event and retries. Deleting the
binding with its `expected_version` returns the endpoint to ordinary delivery;
admitted operations and their receipts are retained. An endpoint has one
delivery mode: remove an automation or exclusive operation binding first. See
[ADR-1193](adr/1193-webhook-submitted-customer-operations.md).

### Resume waiting Operations from a webhook

A binding with `"mode":"signal"` starts nothing. It delivers provider events as
signals to Operations already waiting on a `wait_for_event` step:

```sh
curl -X PUT "$GREGALE_API/v1/apps/shop/inbound-webhooks/$ENDPOINT/operation-binding" \
  -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -d '{"expected_version":0,"operation":"fulfil","platform_tenant_id":"'$TENANT'",
       "take_over_delivery":true,"mode":"signal",
       "subject":{"type":"order","id_from":"/data/object/metadata/order_id"},
       "signals":[{"provider_event":"payment_intent.succeeded","event":"payment.succeeded"}]}'
```

`subject.id_from` points into the provider payload, and `subject.type` must
match the type the operation declares. Each `signals` entry maps a provider
event type to the event a wait step names. When you bind, the operation must be
a workflow that waits for every mapped event. Leave `event_types` empty;
`signals` is the filter.

A mapped event reaches the bound customer's active Operations of that
definition about that subject, at most 16. Other customers' Operations with the
same subject ID are never reached. The provider body becomes the wait step's
output, so it must be at most 64 KiB.

| Answer | Meaning |
|---|---|
| `202 signaled` | At least one Operation received the signal. A provider retry is the same signal, so it lands once and returns `duplicate: true`. |
| `202 unmatched` | No active Operation is waiting on that subject. The event is dropped. |
| `202 ignored` | The event type is unmapped, or the payload lacks the subject. |
| non-2xx | Any other refusal. The provider retries the event. |

Operations whose pinned workflow does not wait for the event are skipped. See
[ADR-20261016100929](adr/20261016100929-webhook-delivered-operation-signals.md).

## Batch Job Operations (closed preview)

`gregale init --template customer-operation-job-export --path exports` creates
an independently installable Job feature: a customer-owned image/command,
typed schemas and manifest, SDK progress/control/private results, and a browser
session with durable submission receipts and downloads. The Job uploads directly
using its live task capability; it needs no bucket, app upload broker or storage
credential. See the
[starter README](../cmd/gregale/templates/customer-operation-job-export/README.md)
for image registration, app setup, immutable definition selection and
lost-response recovery. Portable packed-SDK checks do not open admission or
replace native/provider qualification.

A definition can set `job: export-job` for an active, materialized account-owned batch Job. It requires POST ingress and reconciliation recovery and cannot also select a workflow or transaction receipt. Admission creates one task and freezes the canonical input and native execution configuration. The 32 KiB input and 240 customer environment-entry bounds leave room for trusted runtime context.

A Node command can use `runJobOperation({ apiURL }, async (input, scope) => result)` from `@gregale/sdk-node`. `scope.progress(...)`, `scope.checkpoint()` and `scope.signal` use the live task capability. Returning the typed result prepares an immutable receipt; successful native exit confirms business success and creates completion delivery independently. No result receipt or an uncertain exit requires reconciliation. Use `scope.uploadArtifact({ report_id, name, data, maxBytes })` to upload bounded text or bytes directly into private result storage. The SDK snapshots data, coalesces matching calls and checks receipts before each interrupted-transfer retry. It never repeats the handler. Reusing a report ID with different content or metadata conflicts. The SDK defaults to an 8 MiB application memory bound, independently of captured server quotas. Existing managed-source `scope.prepareArtifact(...)` and `uploadAndAttach(writer)` remain supported; `attach()` can retain an existing source. Native output-manifest files remain separate Job artifacts.

Account inspection/preview and recovery work across Job generations. An approved `safe_to_retry` decision creates a new run with the original snapshot. Direct Job retry/replay cannot bypass it. Image cleanup protects retained operation snapshots. This preview retains the production admission gate until dedicated native KVM execution and cleanup qualification passes; see [ADR-664](adr/664-job-customer-operations.md).

Direct Job uploads declare a stable report ID, name, exact byte count and
SHA-256. Gregale derives an opaque `operation://.../artifacts/...` reference and
retains a private file. Each transfer stages a fresh storage object; concurrent
copies converge on one receipt and cleanup removes unused copies. Existing
managed-source declarations use owned private `obj://app/bucket/key` references.
Customer
status, events and scoped downloads expose the file only with confirmed task
success and a typed result, or explicit account success recovery. Completion
webhook retries do not change business success or regenerate files.

Uncertain exit keeps the copy private and visible in account recovery inspection.
A succeeded preview lists the files it would publish. `safe_to_retry` starts a
new Job generation with fresh receipts and file declarations; reconcile any
uncertain business effect before repeating it. Existing artifact quotas, private
copy cleanup and result retention apply. See [ADR-665](adr/665-job-operation-artifacts.md)
and [ADR-668](adr/668-job-operation-direct-artifact-uploads.md).
## Durable business milestones (internal HTTP implementation)

Milestones record declared public facts about committed application work. Add schema files to the source manifest:

```yaml
operations:
  - name: fulfill-order
    # Existing method, path, owner, input/output schemas, subject, and stages apply.
    http_transaction_version: 1
    milestones:
      order-fulfilled: schemas/order-fulfilled.json
```

Install the current `customerOperationReceiptSchema` explicitly in your application's PostgreSQL database. It adds the receipt and milestone outbox tables. Record a fact inside the transaction callback:

```js
const receipt = await operations.transaction(request, pool, async tx => {
  await tx.query('UPDATE orders SET status = $1 WHERE id = $2', ['fulfilled', orderID]);
  tx.milestone('order-fulfilled', {order_id: orderID, status: 'fulfilled'});
  return {order_id: orderID, status: 'fulfilled'};
});
```

Authorize the order before this call and recheck it under the business row lock. The SDK snapshots payloads, validates the batch against the pinned contract before commit, and stores each milestone with its stable UUID and occurrence time alongside the business write and receipt. After commit it publishes and acknowledges pending facts. Schema validation failure rolls back all three. Publication failure throws `OperationMilestonePublicationError` in Node or returns `CustomerOperationPublicationError` in Go and Python with a committed flag; all preserve the outbox. Reconcile and authorize recovery of the same Operation; receipt replay publishes pending facts without repeating the callback. A lost publication acknowledgement deduplicates on the platform.

Read retained facts using `GregaleOperationClient.milestones(operationID)` or `businessMilestones({appID, scope, subjectType, subjectID})`. The Go and Python clients expose the equivalent Operation and business-reference feeds. Go and Python also support transaction-backed milestone and workflow-state validation and publication through their application runtimes. Account operators can inspect them with:

```sh
gregale customer-operations milestones OPERATION_UUID --app orders
gregale customer-operations milestones --app orders --scope default --subject-type order --subject-id ORDER_ID
```

Tenant credentials use `--self`; an Operation UUID needs no app argument, while business-reference reads require an app UUID. Feed pages use `--limit` and `--cursor`. The dashboard shows milestones on Operation detail pages and when an exact business reference is selected. Payloads are public fields defined by the application schema. Customer identity comes from credentials, and business-reference selectors never override ownership.

Facts are ordered by first platform publication time, which can be later than their application occurrence time after recovery. They survive execution generations and remain retained for the Operation result lifetime, independently of shorter event history. Limits for all enabled plans are 16 declarations, 16 KiB aggregate schema bytes, 8 KiB per payload, 64 facts per Operation, and 64 KiB per validation batch. Milestones do not establish the final Operation outcome or authorize new executions. See [ADR-715](adr/715-customer-operation-business-milestones.md) and the [order example](../examples/customer-operation-orders/README.md). Production admission remains gated.

### Read-only business workflows

Map milestones from one or more Operations into a named process in the source manifest:

```yaml
operation_workflows:
  - name: order-lifecycle
    title: Order lifecycle
    version: 1
    steps:
      - {name: placed, label: Order placed, operation: place-order, milestone: order-placed, instance_id_from: /workflow_run_id, position: 1}
      - {name: paid, label: Payment authorized, operation: authorize-payment, milestone: payment-authorized, instance_id_from: /workflow_run_id, position: 2}
      - {name: fulfilled, label: Order fulfilled, operation: fulfill-order, milestone: order-fulfilled, instance_id_from: /workflow_run_id, position: 3}
```

Each referenced Operation must declare the named transaction-backed milestone. Deployment resolves these steps into each immutable Operation definition, so an existing fact keeps the labels pinned to the version that reported it. The milestone feeds and Go, Node, and Python SDKs include `workflow_steps`; the dashboard groups those observed facts across Operations by the selected business reference. The view lists steps only when a corresponding fact exists. An absent step is not assigned a status. See [ADR-716](adr/716-customer-operation-business-workflows.md).

Each step also selects `instance_id_from`, a JSON Pointer to a stable workflow-run ID in that milestone's payload. The ID must be a nonempty UTF-8 string of at most 256 bytes without control characters. The same run ID must be published by each Operation participating in that run. Gregale validates it before accepting the fact, exposes it as `instance_id`, and groups the dashboard by workflow and instance. This keeps two runs for the same order separate; applications should create and propagate one run ID for each business process attempt. The projection remains observational and does not infer outcomes for missing steps. See [ADR-717](adr/717-operation-workflow-instance-identity.md).

Read one run through either business-milestone feed by supplying `workflow` and `workflow_instance_id` together, alongside the existing app, environment, and business-reference selectors. The returned page contains only facts for that exact run; its cursor is bound to the complete filter set. For example, the Node customer client accepts:

```ts
await client.businessMilestones({
  appID, scope: 'production', subjectType: 'order', subjectID: orderID,
  workflow: 'order-lifecycle', workflowInstanceID: workflowRunID,
});
```

Go's `OperationMilestoneListOptions` and the generated Python business-milestone API expose the same filters. Results contain observed facts and their pinned step metadata; absent steps remain unknown. See [ADR-718](adr/718-operation-workflow-instance-read.md).

### Generate application workflow bindings

Generate bindings from the app's validated manifest and bundled schemas before
building application code:

```sh
gregale customer-operations bindings --app orders --plan pro --dir . \
  --language typescript --output workflow-bindings.ts
gregale customer-operations bindings --app orders --plan pro --dir . \
  --language typescript --output workflow-bindings.ts --check
```

This command runs locally without credentials and uses the deployment contract
compiler. It supports `typescript`, `javascript`, `go`, and `python`; Go accepts
`--package` (default `workflowbindings`). The output includes declared workflow,
state, Operation, milestone, version, and revision constants. TypeScript uses
state unions, Python uses `Literal` types, and Go uses named state types.

Each declared edge gets a helper scoped to its producer Operation. For the
order example, import
`transition_order_fulfillment__fulfill_order__pending__fulfilled` and call it
with the transaction, run ID, state read under the business-row lock, and the
required `order-fulfilled` payload. Go exports the same helper with the
`Transition_` prefix. Hyphens become underscores in generated identifiers;
ambiguous identifiers fail generation. Required payload parameters are ordered
by milestone name. The helper checks the expected source before queuing any
facts, queues every required milestone, and records the edge. Authorize before
receipt replay and let helper errors propagate out of the transaction callback.
The SDK and runtime still validate payload schemas, transition evidence, and
the immutable deployment contract before commit.

Workflows with states and no declared transitions also get a typed `report_`
helper (`Report_` in Go). An Operation with no allowed edge in a workflow that
declares transitions gets no transition or snapshot helper. Generated code
uses structural transaction interfaces compatible with the app SDKs; it adds
no runtime dependencies. Payload shapes remain subject to the declared JSON
schemas and are not generated as application types.

Commit the generated module and run `--check` in CI with the deployment's app
and target plan. The comparison includes a digest of every selected normalized
Operation contract, including its schemas, so contract drift fails the check.
`--check` never writes. Regeneration replaces existing generated files atomically
and rejects unrelated files and symlink outputs. The
[order example](../examples/customer-operation-orders/README.md) includes this
module in its deployment bundle and uses its helper inside the business write.

### Report current workflow state

Applications can declare the accepted current states for a workflow, then report the state from the same transaction that updates business data:

```yaml
operation_workflows:
  - name: order-lifecycle
    title: Order lifecycle
    states: [awaiting-payment, fulfillment-in-progress, completed, cancelled]
    terminal_states: [completed, cancelled]
    state_stale_after:
      awaiting-payment: 30m
      fulfillment-in-progress: 2h
    transitions:
      - {from: awaiting-payment, to: fulfillment-in-progress}
      - {from: fulfillment-in-progress, to: completed, operation: fulfill-order, requires_milestones: [order-fulfilled]}
      - {from: awaiting-payment, to: cancelled}
    steps:
      - {name: fulfilled, label: Order fulfilled, operation: fulfill-order, milestone: order-fulfilled, instance_id_from: /workflow_run_id, position: 1}
```

```ts
await runtime.transaction(request, pool, async tx => {
  await tx.query("UPDATE orders SET status = 'fulfilled' WHERE id = $1", [orderID]);
  tx.milestone('order-fulfilled', {order_id: orderID, workflow_run_id: workflowRunID});
  tx.workflowTransition('order-lifecycle', workflowRunID, 'fulfillment-in-progress', 'completed');
  return {order_id: orderID, status: 'fulfilled'};
});
```

`terminal_states` is an optional subset of `states`. Mark each state that ends the business workflow, such as `completed` or `cancelled`; a terminal state cannot have an outgoing transition. A reported state outside this list is active. When the list is omitted, all reported states are active. Gregale pins this declaration with each workflow step and returns a `terminal` boolean on current workflow states, so the dashboard and CLI can label the application-reported state. This classification does not end an Operation, trigger cleanup, or infer completion from milestones or execution status.

Set `version` when defining a workflow contract. Manifests that omit it resolve to version `1`; raise it when the business meaning or transition requirements change. A transition may target one Operation with `operation` and list `requires_milestones`. Every required milestone must be declared by that Operation and reported with the transition in the same application transaction. The Node SDK attaches that transaction's milestone references to each transition; Gregale validates the references before commit and verifies the retained facts again during publication. Current state and history reads include the pinned contract version and evidence references. See [ADR-517](adr/517-versioned-customer-workflow-contracts.md).

`state_stale_after` optionally maps active states to Go duration strings, such as `30m` or `2h`. Each threshold must be between one second and ten years, in whole seconds. Gregale pins the threshold and returns `stale`, `stale_after_seconds`, and the app-reported `occurred_at` with each current state. Age is measured from `occurred_at`, so delayed publication does not extend or shorten the state deadline. Business-reference reads accept `stale_only=true` to return only stale entries in `workflow_states`; milestone facts and state history remain unchanged. The dashboard exposes this filter after a business reference is selected, and the CLI provides `--stale-only`. Gregale marks a run for attention and leaves state changes and recovery to the application. See [ADR-724](adr/724-customer-operation-workflow-stale-state-detection.md).

The destination state must be declared by the pinned workflow, and `workflowTransition` must use a declared edge. The application checks the source state against its business row while holding the row lock. The Node, Go, and Python SDKs also compare `from_state` with the latest state saved on the per-run counter; a mismatch aborts the business transaction. The counter lock serializes this check across Operations, and the saved state survives cleanup of older outbox rows. Installing the current `customerOperationReceiptSchema` adds this state field and backfills it from retained reports. The first reported transition can establish the report history, so the application still checks its business row as the source of truth. Gregale validates the declared edge before the transaction commits. The SDK saves the report in its durable outbox and publishes it after commit under the current execution fence. Recovery republishes pending reports without rerunning the business callback; publication errors identify a committed transaction. Gregale keeps the newest revision, so a delayed publication cannot replace a newer state. Business-reference reads return the latest explicitly reported state alongside milestone facts, and the dashboard displays it with its update time and revision. No state appears until the application reports one, and Gregale does not derive state from milestones or execution status. See [ADR-719](adr/719-customer-operation-workflow-states.md), [ADR-720](adr/720-customer-operation-workflow-transitions.md), [ADR-722](adr/722-customer-operation-workflow-transition-continuity.md), and [ADR-723](adr/723-customer-operation-workflow-terminal-states.md).

Read the retained changes for one run by adding the same `workflow` and `workflow_instance_id` pair to a business-reference read. The response includes `workflow_state_history`, ordered by the app-assigned revision, with the prior and new state, application occurrence time, platform publication time, and the Operation that published the report. `workflow_state_cursor` paginates this history independently of the milestone `cursor`; both cursors remain bound to the business reference and ownership filters. The Node client accepts `workflowStateCursor`, Go uses `WorkflowStateCursor`, generated Python accepts `workflow_state_cursor`, and the CLI accepts `--workflow-state-cursor`. The response also includes `workflow_instance`, a grouped view with the selected contract's steps in order, `allowed_transitions`, current explicit state, the transition-history page, and two fact summaries per step. Each allowed edge includes its source and destination states, target Operation name, and any `required_milestones` that must be committed with it. The edges come from the selected contract version; the application still checks its locked business row and caller authorization before taking one. `observed`, `milestones_in_page`, and `latest_milestone` cover the current milestone page. `observed_in_retention`, `milestones_in_retention`, and `latest_retained_milestone` cover matching facts for the selected contract version that remain under normal Operation retention, independent of the page cursor. A step with no retained fact may have expired evidence; it does not prove that the step never occurred. `has_more` is true while either cursor has more data; continue with `next_milestone_cursor` and `next_transition_cursor`. See [ADR-721](adr/721-customer-operation-workflow-state-history.md).

### Explain the next workflow action

Select a business workflow with both `workflow` and `workflow_instance_id` to read
`workflow_instance.decision`. The explanation uses the selected contract version
and latest application-reported state, independently of milestone and history
pagination. `state_revision` identifies the report used. `next_actions` contains
matching edges, their producer Operation names, and required milestone names.

`reason` is `state_unknown`, `terminal`, `no_declared_transition`,
`transitions_available`, or `state_stale`. Unknown or stale reports set
`needs_attention`. Stale reports keep matching actions visible for inspection;
terminal reports propose none. No declared edge does not imply a business failure.

These are contract options. The application must check authorization and its
locked business row before performing an Operation. Required milestones must be
committed with the new transition; historical evidence does not satisfy them.
The CLI milestone timeline and dashboard selected workflow display the explanation.
All SDKs expose the same optional decision object for compatibility with older servers.

### Report workflow blockers

Applications can report why a target Operation must wait, using up to 16 public
`{code, description, operation}` objects. Codes and Operation names are lowercase
slugs (at most 64 bytes); descriptions are UTF-8 text (at most 512 bytes, without
control characters). A target/code pair must be unique. Keep descriptions suitable
for the customer-visible timeline; exclude secrets and internal diagnostic data.

Report blockers after authorizing the customer and locking the business row:

```javascript
// Inside operations.transaction(...), using the state read from the locked row:
tx.workflowBlockers('order-fulfillment', order.workflow_instance_id, order.status, [
  {code: 'payment-pending', description: 'Payment confirmation is pending.', operation: 'fulfill-order'},
]);
```

Go uses `tx.WorkflowBlockers(workflow, instanceID, state,
[]preview.OperationWorkflowBlocker{...})`; Python uses
`tx.workflow_blockers(workflow, instance_id, state,
[OperationWorkflowBlocker(code=..., description=..., operation=...)])`.
Errors must propagate out of the transaction callback so business changes roll back.
These helpers queue a same-state report with `blockers_only: true` and
`from_state == state`. It receives the usual transactionally assigned revision,
checks the saved state head, and publishes through the durable outbox after commit.
The pinned workflow must declare that state for the reporting Operation. A
blocker-only report cannot change state or carry transition evidence. It does not
require an artificial self-transition in the contract.

Each accepted newest state revision replaces the **entire** blocker list.
Call the helper with an empty list to clear resolved blockers. A later normal
state/transition report without blockers clears them too. Older reports remain
in history and cannot restore blockers over a newer revision. Direct report API
clients may also attach blockers to a normal state transition.

`workflow_instance.decision.blockers` shows the current list independently of
milestone/history pagination. Nonterminal workflows with blockers use
`reason: application_blocked`, unless staleness takes precedence; either sets
`needs_attention`. Terminal workflows retain their terminal classification and
show any reported blockers. Contract `next_actions` remain visible, including
edges targeted by blockers. Target Operation names are application observations;
they do not establish that the Operation exists or is available in this contract.
The application must enforce its business conditions and authorization when
executing an Operation; Gregale does not automatically deny requests from these
reports. The CLI and dashboard distinguish blocker-only history updates from
business transitions.

The report timestamp records when the application evaluated this snapshot.
Refreshing blockers also refreshes the reported snapshot age used for staleness;
applications needing state-entry duration should report that duration separately.

Before upgrading a running installation, apply the platform migration
`20261008222056613_customer_operation_workflow_blockers.sql`. Application database
owners must also reinstall the additive customer Operation schema supplied by
their SDK before using the updated transaction adapter. The new outbox columns
have empty/false defaults, so pending older outbox rows remain publishable.

### Find workflows needing attention

Read `GET /v1/apps/{slug}/workflow-attention?scope=production` to list current
retained workflow snapshots with application-reported blockers or a passed stale
threshold. The account route uses the same read scope and MFA requirements as
business milestone reads. Customer applications use
`GET /v1/platform-tenant-self/workflow-attention?app_id=UUID&scope=production`;
customer identity comes exclusively from credentials and tenant overrides are rejected.

Optional filters are `workflow`, `target_operation`, `reason=blocked|stale`,
`limit` (1–100, default 20), and `cursor`. Account readers may also use
`tenant_id`. Filters apply before pagination. A target Operation matches a reported
blocker, or a declared transition from a stale workflow's reported state under
its selected contract version. A blocked workflow does not match an unrelated
Operation merely because that Operation has a possible transition.

Each item contains the public business reference, reporting Operation ID, explicit
state and revision, blockers, and `reasons` (`blocked`, `stale`, or both). Account
responses include the customer ID; customer responses omit it. Terminal workflows
with blockers remain visible; terminal states are not classified stale. Unreported
states cannot be enumerated, and expired reporting Operations are omitted under
normal retention rather than replaced with an older report.

Items are ordered by latest report publication time descending, with a stable
workflow-identity tie breaker. `next_cursor` is bound to the account/customer role,
app, environment, customer selection, workflow, target Operation, and reason.
Staleness uses the first page's `evaluated_at` across continuations; retention is
checked at each read. This is a live queue: reports can move or disappear while
you browse. Refresh the first page for the latest view. The queue observes facts;
it neither authorizes nor performs business Operations.

```sh
gregale customer-operations attention --app orders --scope production
gregale customer-operations attention --app orders --scope production \
  --workflow order-fulfillment --target-operation fulfill-order --reason blocked
gregale customer-operations attention --self --app-id APP_UUID --scope production
```

Go exposes `ListAccountWorkflowAttention` and
`ListPlatformTenantSelfWorkflowAttention` with `OperationWorkflowAttentionOptions`.
Node customer clients expose `client.workflowAttention({appID, scope, workflow,
targetOperation, reason, limit, cursor})`; the generated `OperationsService`
also exposes both account and customer endpoints. Python provides
`list_account_workflow_attention` and `list_platform_tenant_self_workflow_attention`
modules in `faas_sdk.api.operations`, with synchronous and asynchronous calls.

The dashboard queue is at
`/dashboard/apps/{slug}/customer-operations/attention`. Filter by environment,
customer, workflow, target Operation, or reason; each row links to the exact
customer/business reference and workflow instance's explanation and history.
Apply the additive platform index migration
`20261008222056620_customer_operation_workflow_attention.sql` when deploying.

### Assign blocker ownership and a next action

A reported blocker can include optional `owner` and `next_action` fields.
`owner` is a public application-assigned person or team identifier, limited to
128 UTF-8 bytes. `next_action` is public resolution guidance, limited to 512
UTF-8 bytes. Neither accepts control characters. Empty or omitted ownership
means unassigned. Keep secrets and private contact details out of these fields:
customer-scoped reads and dashboards expose them.

Authorize assignment changes in the application and lock the business row, then
publish the complete replacement blocker list in the same transaction:

```javascript
// Keep every other active blocker in remainingBlockers.
tx.workflowBlockers('order-fulfillment', lockedOrder.workflow_instance_id,
  lockedOrder.status, [...remainingBlockers, {
    code: 'payment-pending', operation: 'fulfill-order',
    description: 'Payment confirmation has not arrived.',
    owner: 'billing-team', next_action: 'Check the payment provider receipt.',
  }]);
```

Use the equivalent `OperationWorkflowBlocker.Owner` / `NextAction` fields in Go
or `OperationWorkflowBlocker(owner=..., next_action=...)` in Python. The existing
`WorkflowBlockers` / `workflow_blockers` helpers report assignment without
changing business state. Reassignment preserves `first_observed_at` for the
same active target/code, so assigning work does not reset its age. Ownership is
part of the revisioned blocker snapshot; a later replacement that omits it
clears the assignment. Upgrade all writers before relying on these fields:
older SDKs may discard them when republishing metadata.

The attention queue, workflow detail, and retained history show ownership and
next actions; the CLI attention output includes populated assignment fields.
These observations identify responsibility, not a verified platform principal
or permission to execute an action. The application remains responsible for
checking current business rows and authorizing the suggested step.

When the blocker is actually resolved, report the existing source-linked
resolution fact and optional `resolved_by` (a public identifier of at most 128
UTF-8 bytes without control characters). The report timestamp provides the
resolution time. Clearing a blocker alone is not proof that it was resolved;
resolution attribution is application-reported, not verified operator identity.
No new platform or application-schema migration is required for these fields.

### Find work by owner

The workflow attention queue and summary accept `owner` (an exact,
case-sensitive public identifier) or `unassigned=true`. These selectors are
mutually exclusive. Empty and absent blocker owners both mean unassigned;
workflows without blockers are not unassigned work. Identifiers retain their
128 UTF-8 byte limit and may not contain control characters.

```sh
gregale customer-operations attention --app orders --scope production --owner billing-team
gregale customer-operations attention --app orders --scope production --unassigned
gregale customer-operations attention-summary --app orders --scope production --group-by owner
```

The account and customer-self attention endpoints accept the same query
parameters. Go uses `OperationWorkflowAttentionOptions{Owner: "billing-team"}`
or `{Unassigned: true}`; the Node customer client uses `owner` / `unassigned`
and `groupBy: 'owner'`. Generated Python attention clients accept `owner`,
`unassigned`, and the owner grouping value.

Owner selection, blocker code, and target Operation must match one blocker,
rather than different blockers in the same workflow. Queue entries retain the
complete workflow snapshot for context. In summaries with owner selection,
blocker counts and ages cover only matching blockers. `group_by=owner` likewise
counts only matching blockers for each owner; the empty group `value` represents
unassigned work. A real owner named `unassigned` remains a distinct group.

Each group reports workflow and blocker counts, overdue workflow counts, and
oldest known blocker age. Overdue status belongs to the workflow deadline,
not a separate owner deadline. A workflow shared by two owners counts in each
owner group, so group workflow counts must not be summed. Totals remain unique
matching workflows; stale or dependency-only workflows without blockers can
appear in unfiltered totals without belonging to an owner group.

Dashboard group links open the scoped owner queue; each row links to its
workflow explanation and resolution history. Queue and summary pagination bind
both ownership selectors and all other filters. Changing a filter requires a
fresh cursor. Assignment is still controlled and authorized by the application.

### Declare blocker escalation policies

Declare `blocker_escalations` on an application workflow, keyed by blocker code:

```yaml
blocker_escalations:
  payment-pending:
    after_seconds: 3600
    owner: billing-escalation-team
```

The resolved Operation workflow steps carry the same map. Policies are part of
the pinned definition and workflow version; keep every participating step's
policy declaration identical and publish a new contract version when changing
its meaning. Each workflow supports at most 32 policies. Thresholds are whole
seconds from 1 to 315360000 (ten years); the public recommended recipient follows
the 128 UTF-8 byte owner bound and cannot be empty or contain control characters.

At the attention cursor's evaluation time, Gregale compares the active blocker's
`first_observed_at` plus `after_seconds` with that time. Reaching the threshold
produces an `escalations` finding with blocker code, target Operation,
recommended recipient, threshold, and `escalated_at` (when the threshold was
reached). An omitted observation time stays unknown; publication time is not
used as a substitute. Terminal workflows do not produce escalation findings.
Reassignment preserves age, so it does not postpone escalation. Clearing the
blocker removes it from the current escalation queue; retained resolution history
remains available through the existing workflow links.

```sh
gregale customer-operations attention --app orders --scope production --reason escalated
gregale customer-operations attention-summary --app orders --scope production --reason escalated --group-by owner
```

Account and customer-self attention endpoints and SDKs accept `reason=escalated`.
When combined with owner, code, or target filters, one escalated blocker must
match those selectors. Owner-grouped summaries expose `escalated_workflow_count`
and `escalated_blocker_count` for that assignee's blockers. The escalation
recipient is a recommendation and does not replace the assignee. Queue entries
retain full context; escalated-only summaries count only matching escalated
blockers. The dashboard displays the recommended recipient and threshold time.

Gregale observes and recommends; the application decides how to contact a team,
authorize changes, or execute a business action. No notifications are sent by
this feature and no new platform or application-schema migration is required.
The evaluation time stays fixed across cursor pages, while retained observations
may change; refresh the queue to include thresholds crossed since the first page.

### Explain blocker resolutions

When clearing a reported blocker, the application can include an explicit
`blocker_resolutions` fact on the new workflow state report. A fact contains:

| Field | Meaning |
| --- | --- |
| `code`, `operation` | The prior blocker code and target Operation name. |
| `description` | Public explanation of why that blocker was cleared. |
| `resolved_by` | Optional public application-reported resolver or team identifier. |
| `blocker_operation_id` | Operation that published the source blocker report. |
| `blocker_report_id` | Exact source report identity. |
| `blocker_revision` | Source state revision, strictly earlier than the new report. |

Current workflow states now expose `operation_id` and `report_id`, in addition to
`revision`, so callers can reference the report they inspected. Historical entries
already expose their `operation_id`, `id`, and `revision`. These new current-state
locator fields are optional for compatibility with older servers; resolution
reporting requires a server that supports them or a known historical report.

Inside the Customer Operation transaction, after authorizing the customer,
locking the business row, and evaluating the actual business condition:

```javascript
// priorState is an authenticated, previously published workflow snapshot.
// lockedOrder is the current application row; remainingBlockers is the complete replacement list.
tx.workflowBlockers('order-fulfillment', lockedOrder.workflow_instance_id, lockedOrder.status,
  remainingBlockers, [{
    code: 'payment-pending',
    operation: 'fulfill-order',
    description: 'Payment confirmation received.',
    resolved_by: 'billing-team',
    blocker_operation_id: priorState.operation_id,
    blocker_report_id: priorState.report_id,
    blocker_revision: priorState.revision,
  }]);
```

Go accepts resolution facts as variadic `preview.OperationWorkflowBlockerResolution`
arguments after the blocker list in `tx.WorkflowBlockers(...)`. Python accepts
`resolutions=[OperationWorkflowBlockerResolution(...)]` as a keyword argument to
`tx.workflow_blockers(...)`. Errors must propagate out of the transaction callback.
The blocker replacement and its resolution facts commit with the business write,
receive the usual report revision, and publish through the durable outbox. When
also performing a business transition, queue the resolution-bearing blocker
update first, then the declared transition and its required milestones; the
resolution remains in history even if the subsequent state snapshot has no facts.
Direct report API clients may attach resolutions to normal transitions as well.

At most 16 resolution facts are permitted per report. Public fields have the same
slug/text bounds as blockers. Each target/code pair must be unique and absent
from that report's replacement blocker list. Source IDs must be canonical,
nonzero UUIDs; source revisions must be positive safe integers. The API validates
that the exact source report is retained, contains that blocker, and belongs to
the same account, application, customer, environment, business reference, workflow
instance, and contract version. Newly queued, unpublished reports cannot serve as
resolution sources. Reference the specific occurrence you evaluated; a resolution
of an old report does not establish what happened to a later reappearance.

Resolution identity, occurrence time, publication time, reporting Operation,
and state revision come from the containing report. Repeating the same report is
idempotent; altering its facts conflicts with its retained receipt. Previously
accepted reports can replay even if their source later expires. For a new report,
source expiry between preflight and publication can leave publication incomplete
after the business commit; follow the existing publication recovery semantics.
Source links do not extend retention. The explanation and reference remain in the
resolution report's retained history even when the source is no longer available.

Clearing a blocker without an explanation remains supported and creates no
inferred resolution. The application supplies the explanation; Gregale verifies
the reference and reported list consistency, not the underlying business event.
Read `workflow_state_history[].blocker_resolutions` (also present on snapshot
`transitions`) using the existing paired workflow selectors and history cursor.
The CLI milestone timeline and dashboard selected workflow display the explanation
and source report/revision. Later snapshots can omit the facts without deleting
retained history.

Before deploying, apply
`20261008222056623_customer_operation_blocker_resolutions.sql` on the platform.
Application database owners must reinstall the SDK's additive customer Operation
schema before upgrading the transaction adapter; the new outbox column defaults
to an empty list so older pending reports remain publishable.

### Workflow attention summaries and blocker ages

Use the attention dashboard to see counts and follow a group into its filtered
queue. Summaries cover **all matching retained workflows**, independently of the
workflow queue page or the bounded group page. Choose `workflow` (default),
`blocker_code`, `target_operation`, or account-only `customer` grouping.

```sh
gregale customer-operations attention-summary --app orders --scope production --group-by blocker_code
gregale customer-operations attention --app orders --scope production --blocker-code payment-pending
```

Account API: `GET /v1/apps/{slug}/workflow-attention/summary`.
Customer API: `GET /v1/platform-tenant-self/workflow-attention/summary` with
`app_id`. Both take explicit `scope`, optional `workflow`, `blocker_code`,
`target_operation`, `reason`, `limit` and `cursor`, plus `group_by`. Account
requests can also select `tenant_id`; self identity comes from credentials.
Group pages use separate cursors from the queue. Cursors retain the evaluation
time for staleness and age; reports and retention may change between pages.
Refresh the first page for the latest view.

`totals` and each group's `stats` contain workflow, blocked workflow, stale
workflow, blocker, and unknown-age counts. When any included blocker has a known
start, they also include `oldest_blocker_at` and `oldest_blocker_age_seconds`.
Blocked and stale counts overlap. Code and target groups count only blockers
for that group; workflow and customer groups count all their blockers. Target
Operation groups also include applicable declared transitions of stale
workflows, even when that target has no reported blocker. Groups can overlap,
so adding group counts does not produce totals. Code/target selectors choose
instances; totals still include all blockers on those selected instances.

Blockers may carry optional `first_observed_at` (RFC3339, at or before the
containing report's `occurred_at`). Upgraded Go, Node and Python transactional
SDKs set it for a new blocker episode and preserve it for the same
`(operation, code)` across reports, description changes and state transitions.
Clearing that target/code ends the episode; reporting it again starts a new
one. This date measures an application observation, separate from report
publication or last snapshot time. Direct API writers maintain their own
observation timestamps; Gregale does not infer them from retained history.

Install the updated application-owned `CustomerOperationReceiptSchema` before
using upgraded transactional writers. The workflow counter now retains the
last blocker list and its revision, independently of outbox cleanup. Existing
blockers without a start retain **unknown age**. If an older SDK writer advances
the counter without this metadata, upgraded writers do not assume blocker
continuity or invent a new start. Upgrade every writer for reliable tracking;
an application that knows the original observation time can supply it.
Timestamps travel through the outbox and publication receipts, so retries and
late publication preserve the observation time.

### Business workflow deadlines

A workflow instance can now carry one optional application-reported `deadline_at`.
Use it for a business obligation such as payment due or a promised shipment time.
It is separate from the age of the last state report and from blocker age.

Record deadlines with the business write inside the existing SDK transaction:

```go
// state comes from the locked business row; dueAt is RFC3339.
err := tx.WorkflowDeadline("order-fulfillment", orderID, state, dueAt)
// Passing "" explicitly clears the deadline.
```

```ts
tx.workflowDeadline('order-fulfillment', orderID, state, dueAt);
// tx.workflowDeadline('order-fulfillment', orderID, state, ''); // clear
```

```python
tx.workflow_deadline("order-fulfillment", order_id, state, due_at)
# tx.workflow_deadline("order-fulfillment", order_id, state, "")  # clear
```

Updated transactional SDKs preserve the latest due time on ordinary state,
transition, and blocker reports. A deadline-only update preserves the latest
blocker list. Both use the application workflow counter under the existing
transaction lock, and publication carries the exact snapshot through the outbox
and verifies its receipt. You can queue a state transition and then its deadline
update in the same transaction. Both reports receive consecutive revisions and
preflight validation; deadline-only updates carry no transition milestone
requirements. The supplied state must be declared for the reporting Operation
and match the application's locked row.

Install the updated application-owned receipt schema and upgrade all writers.
Counters retain due times independently of receipt cleanup and detect writers
that did not update deadline metadata. After an older writer advances a counter,
the SDK does not inherit a possibly obsolete due time. Deadline-only updates
fail if blocker continuity is unavailable; establish a current snapshot with
an upgraded writer first. Existing workflows have no inferred deadline.

Direct API reports are full snapshots: `deadline_at` is an optional finite
RFC3339 timestamp; omitted or empty clears it. `deadline_only: true` denotes a
same-state update (`from_state == state`, no milestone evidence), and cannot be
combined with `blockers_only`. Direct writers must include their current blocker
snapshot themselves. Setting a due time in the past is supported and immediately
makes an active retained workflow overdue. Dates normalize to UTC microseconds.
Reports remain idempotent and older revisions cannot replace newer snapshots.

```sh
gregale customer-operations attention --app orders --scope production --reason overdue
gregale customer-operations attention-summary --app orders --scope production --reason overdue --group-by workflow
```

The existing attention APIs accept `reason=overdue`. They evaluate overdue
status at the cursor's evaluation time: an active workflow is overdue when its
deadline is at or before that time. Terminal workflows are not overdue, even
if they retain a deadline in history. Normal retention and ownership boundaries
still apply. Due-only workflows appear even without blockers or stale reports.
Target Operation filters and groups include applicable declared transitions on
stale **or overdue** workflows.

Snapshots expose `deadline_at`, `overdue`, and whole `overdue_seconds`. Attention
summaries add `overdue_workflow_count`, plus `earliest_overdue_deadline_at` and
`longest_overdue_seconds` when a matching overdue workflow exists. Blocked,
stale, and overdue counts can overlap. The dashboard shows due times and overdue
duration, links grouped summaries to filtered queues, and records deadline-only
updates and clears in workflow history. Deadlines are application observations;
they do not grant execution permission or automatically trigger business work.

### Explicit business workflow outcomes

A terminal workflow snapshot can now carry one explicit `outcome_code` and public
`outcome_description`, for example `fulfilled`, `payment-declined`, or
`refund-completed`. Gregale accepts outcomes only when the reporting Operation's
pinned workflow contract declares the reported state terminal. Terminal state
alone does not imply success, failure, or any other business outcome.

Record the business transition, its required milestones, and outcome in the same
transaction. The supplied states come from the locked business row:

```go
if err := tx.WorkflowTransition("order-fulfillment", orderID, fromState, terminalState); err != nil { return err }
if err := tx.WorkflowOutcome("order-fulfillment", orderID, terminalState, "fulfilled", "Order fulfilled."); err != nil { return err }
```

```ts
tx.workflowTransition('order-fulfillment', orderID, fromState, terminalState);
tx.workflowOutcome('order-fulfillment', orderID, terminalState, 'fulfilled', 'Order fulfilled.');
```

```python
tx.workflow_transition("order-fulfillment", order_id, from_state, terminal_state)
tx.workflow_outcome("order-fulfillment", order_id, terminal_state, "fulfilled", "Order fulfilled.")
```

Outcome methods emit a same-state `outcome_only` report after the transition.
They preserve the counter's current blockers and deadline. Consecutive reports
receive transactional revisions and preflight validation. Outbox publication
verifies the outcome code, description, and update kind in its receipt.
Corrections use a new report revision and remain visible in retained history.

Install the updated application-owned receipt schema and upgrade all writers.
The workflow counter retains the latest outcome and its revision independently
of receipt cleanup. Upgraded SDKs preserve it on subsequent reports of the same
state; changing state removes the inherited outcome. A later terminal completion
can report a new result. Counter revision markers prevent inheriting potentially
obsolete outcomes after an older writer advances the counter. Outcome-only
updates require current blocker metadata, as deadline-only updates do.

Direct API writers can include paired `outcome_code`/`outcome_description` in a
terminal transition report, or send `outcome_only: true` with `from_state == state`
and no milestone evidence. Metadata update kinds are mutually exclusive. Direct
reports replace the full snapshot; omission removes the current outcome.
Codes are lowercase slugs of at most 64 bytes; descriptions are public UTF-8
text of at most 512 bytes without control characters. Descriptions should not
contain private business details. Outcome codes are defined by the application.

```sh
gregale customer-operations outcomes --app orders --scope production --code fulfilled
gregale customer-operations outcome-summary --app orders --scope production --group-by outcome
```

Listings use `GET /v1/apps/{slug}/workflow-outcomes` or credential-scoped
`GET /v1/platform-tenant-self/workflow-outcomes` with `app_id`. Summaries append
`/summary`. Explicit `scope` is required; optional selectors are `workflow`,
`code`, `limit`, `cursor`, and account-only `tenant_id`. Summary `group_by` accepts
`outcome` (default), `workflow`, and account-only `customer`.

These views include **latest retained terminal snapshots with explicit outcomes**.
Each current workflow instance counts once, even when outcomes are repeated or
corrected. Reopening an instance removes it from current completion totals;
earlier outcomes remain in retained history. Terminal instances without a reported
outcome are omitted. Summary counts cover all matching instances independently
of list or group page size. Outcome list, summary, and attention cursors are
separate; current reports and retention can change between pages. Counts describe
currently retained completions, not lifetime totals or inferred success rates.

The dashboard's Business outcomes view links summary groups to matching
instances and their workflow history. Workflow history distinguishes explicit
outcome updates from later snapshots. Outcomes are application observations;
they do not grant execution authority or automatically cause follow-up work.

## Workflow dependencies

Applications can report up to 16 direct prerequisites on a workflow instance.
Each `depends_on` item identifies `subject_type`, `subject_id`, `workflow`, and
`instance_id`, with an optional `required_outcome_code`. All links resolve within
the source application's customer and environment. Duplicate targets and self
links are rejected; targets may be unreported or outside retention.

Report dependencies in the same transaction as the business row change:

```go
err := tx.WorkflowDependencies("fulfillment", orderID, "waiting", []preview.OperationWorkflowDependency{
    {SubjectType: "order", SubjectID: orderID, Workflow: "payment", InstanceID: paymentID, RequiredOutcomeCode: "paid"},
})
```

```typescript
tx.workflowDependencies('fulfillment', orderID, 'waiting', [{
  subject_type: 'order', subject_id: orderID, workflow: 'payment',
  instance_id: paymentID, required_outcome_code: 'paid',
}]);
```

```python
tx.workflow_dependencies("fulfillment", order_id, "waiting", [
    OperationWorkflowDependency(subject_type="order", subject_id=order_id,
        workflow="payment", instance_id=payment_id, required_outcome_code="paid"),
])
```

These methods replace the complete list. Pass an empty list to clear all links;
remove one link by reporting the remaining list. The state must match the locked
business row. This is a `dependencies_only` same-state metadata update, not a
transition, and has no milestone evidence. SDKs preserve the current blockers,
deadline, and same-state outcome. Other reports inherit the dependency snapshot
when their counter metadata is current.

Selecting a workflow instance in the existing business milestones API returns
`workflow_instance.related_workflows`, independent of history pagination:

| Status | Meaning |
| --- | --- |
| `unknown` | No retained target state, or terminal target missing a requested outcome. |
| `waiting` | Target explicitly reports an active state. |
| `terminal` | Target is terminal and no particular outcome was requested. |
| `satisfied` | Target is terminal and reports the requested outcome. |
| `outcome_mismatch` | Target is terminal with a different reported outcome. |

Terminal does not imply business success. An active source with unresolved links
needs attention; existing blocker, stale, and deadline reasons retain precedence.
The explanation retains declared next actions: dependencies do not authorize or
execute business decisions. Dashboard links open the target's scoped history;
CLI text output includes prerequisite statuses and dependency update records.

Resolution follows one hop only and honors the target Operation's retention.
Cycles are not traversed. No cross-customer links or business payloads are exposed.
Dependency attention appears in the selected instance explanation and in the attention queue and summaries.

Before using this feature, apply platform migration
`20261008222056633_customer_operation_workflow_dependencies.sql` and the updated
SDK customer schema. The customer schema adds `depends_on`/`dependencies_only`
to the outbox and dependency snapshots with revision markers to workflow counters.
Upgrade all writers. Revision gaps from older writers prevent inheritance of
obsolete dependencies; metadata updates require current blocker counter data.

## Dependency-aware attention

The existing workflow attention endpoints include active source workflows with
`waiting`, `unknown`, or `outcome_mismatch` direct prerequisites. Their `reasons`
include `dependency`, and `dependency_attention` lists all unresolved references
and statuses. A source is counted once even when several prerequisites need work.
Terminal sources do not gain dependency attention. Terminal prerequisites without
a requested outcome, and prerequisites with a satisfied outcome, do not cause it.

Use `reason=dependency`, `dependency_status=waiting|unknown|outcome_mismatch`, or
`required_outcome_code=paid`. If both dependency filters are set they must match
the same reference. Other attention filters are conjunctive. Target Operation
filtering retains its existing blocker and stale/overdue transition semantics.
Filtered workflows still return all unresolved references for investigation.

Summary totals add `dependency_workflow_count` (distinct affected sources) and
`dependency_count` (unresolved links). Group with `dependency_status` or
`required_outcome_code` to identify recurring bottlenecks; groups can overlap.
References without a requested outcome have no required-outcome group. For these
new groupings, dependency counts cover the matching links in the group. Workflow,
customer, blocker, and target groups count all unresolved links on their members.
Totals cover all matching workflows, independent of group pagination.

The dashboard exposes the filters, counts, and links to prerequisite histories.
The CLI supports `--dependency-status`, `--required-outcome-code`, and
`--reason dependency` on attention and attention-summary. Account and self APIs
and SDKs use the same ownership boundaries; self responses expose no customer ID.
Resolution remains one hop and subject to target retention. Current target reports
and retention may change while paging, just as source reports can; cursors fix the
staleness/deadline evaluation instant and bind both dependency filters.

No additional migration is needed beyond the workflow dependency schema.

## Reverse dependency impact

Select a business subject, workflow, and instance with the existing milestones
API to read `workflow_instance.dependency_impact`. It shows the retained workflow
instances that currently point at this prerequisite. The dashboard adds a
**Dependent workflows** section with links to each source's history, reported
state, required outcome, prerequisite status, and whether it is affected.

- `workflow_count` counts distinct retained dependents, including terminal ones.
- `impacted_workflow_count` counts active dependents whose requirement is waiting,
  unknown, or mismatched against the selected prerequisite's reported outcome.
- `items` contains up to 100 dependents, affected ones first, then latest report
  time, then subject type/ID and workflow/instance in ascending byte order.
- `has_more` indicates that the detail list was capped. Counts still cover all
  retained matches. The current impact view has no separate detail cursor.

Each item includes `subject`, current `state`, optional `required_outcome_code`,
`dependency_status`, and `needs_attention`. Here `needs_attention` describes this
prerequisite only; other blockers, deadlines, or dependencies can also affect the
source. Terminal dependents remain visible but do not increase the affected count.
A terminal prerequisite without a requested outcome counts as terminal, not as
inferred business success. A terminal prerequisite that reports the requested
outcome is satisfied. An absent report or missing requested outcome is unknown.

The reverse lookup uses the exact customer/application/environment of the selected
prerequisite. For an unknown prerequisite, account callers must supply `tenant_id`;
without a selected state or explicit customer, the impact field is omitted. Self
callers use their authenticated customer automatically and receive no customer ID.
The source's current report and reporting Operation must remain retained. Removing
or replacing a dependency removes its reverse link; historical links are not
included. Unknown or expired prerequisites can still reveal retained dependents
when the customer is explicit.

This is a one-hop live view, independent of milestone and transition pagination.
It does not recurse through cycles or infer execution order. Counts and selected
prerequisite state can change between reads. No business payloads or execution
authority are added.

The Go, Node, and Python SDKs expose typed impact data on their existing business
milestones responses. CLI text output adds `workflow-impact` totals and
`workflow-dependent` rows; JSON preserves the complete nested response.

Apply platform migration
`20261008222056636_customer_operation_reverse_dependencies.sql` to add a partial
GIN index on retained-report dependency references for reverse lookup. It follows
the existing dependency migration; no additional customer outbox schema is needed.

## Dependency root-cause tracing

A selected workflow instance now includes `dependency_trace` beside its direct
relationships and reverse impact. It follows unmet reported prerequisites to
explain chains such as fulfillment → payment → risk review, where risk review
reports a manual-approval blocker. The dashboard links every path step to its
workflow history; SDKs expose typed findings, and CLI text includes
`workflow-trace`, `workflow-root-cause`, `trace-step`, and reported state/blockers.

Each finding has a `kind`, `explanation`, and a `path` starting with the selected
subject/workflow/instance. Later path steps retain their incoming required outcome.
A finding includes the current retained `state` when available, so reported
blocker descriptions, outcome codes, revisions, and business deadlines stay
inspectable. No input bodies, private artifacts, or execution authority are added.

| Kind | Observed meaning |
| --- | --- |
| `reported_blockers` | The application explicitly reports blockers on this workflow. |
| `state_unknown` | No current retained report exists for the workflow. |
| `outcome_unknown` | A terminal prerequisite lacks its requested reported outcome. |
| `outcome_mismatch` | A terminal prerequisite reports a different outcome than required. |
| `awaiting_application` | Active state with no observed blocker or unmet prerequisite; application progress is still needed. |
| `state_stale` | The reported state exceeds its declared stale threshold. |
| `deadline_overdue` | The reported business deadline has passed. |
| `cycle` | An unmet reported dependency chain revisits an ancestor. |
| `trace_limit` | Further examination stopped at a traversal bound. |

An active workflow can report blockers or missed deadlines while also depending
on another workflow, so tracing continues through its unmet references. Terminal
selected sources stop tracing. Completed prerequisites without a requested
outcome and terminal prerequisites with the requested outcome stop that branch.
Terminal completion alone is not interpreted as business success.

The trace has fixed limits: eight prerequisite hops (at most nine path steps),
64 distinct workflow references, 256 examined dependency edges, and 128 findings.
`truncated` and `limits_reached` make incomplete coverage explicit. Reaching a
lookup limit does not turn an unexamined workflow into a missing-state claim.
`visited_workflow_count` includes the selected source and cached lookups, including
unknown and satisfied prerequisites. `examined_dependency_count` counts edges
actually considered. Follow linked workflows to inspect beyond the bounds.

Traversal follows canonical business reference order. States are cached by exact
subject/workflow/instance independently of the incoming expected outcome.
Shared workflows are expanded once and produce representative paths, not every
possible route. An ancestor revisit is a cycle; sharing a prerequisite through
two independent branches is not. A cycle is a reported relationship finding,
not proof of a business execution deadlock. Traversal does not authorize actions
or change the declared next actions in the existing workflow decision.

All lookups stay within the selected customer's application/environment and
normal Operation retention. Unknown selected workflows require an explicit
customer in account mode; otherwise the trace is omitted. Self callers use their
authenticated customer and receive no customer IDs. Reports can change during
multi-query PostgreSQL reads, while each read keeps the same retention and
staleness/deadline evaluation instant. History cursors do not paginate the trace.

No additional platform or customer schema migration is required for tracing;
it reads the existing workflow dependency reports.

## Workflow transition readiness

Use `POST /v1/apps/{slug}/workflow-readiness` for an account reader with MFA, or
`POST /v1/platform-tenant-self/workflow-readiness` for an authenticated customer
with operations read scope. The request names an exact subject, workflow/run,
Operation, proposed `from_state` / `to_state`, and environment. Account requests
require `tenant_id` and omit `app_id`; self requests require `app_id` and omit
`tenant_id`. Ownership and retention match the existing workflow detail APIs.

```json
{
  "app_id": "11111111-1111-4111-8111-111111111111",
  "scope": "production",
  "subject": {"type": "order", "id": "order-42"},
  "workflow": "fulfillment",
  "instance_id": "fulfillment-42",
  "operation": "ship-order",
  "from_state": "waiting",
  "to_state": "shipping",
  "milestones": ["shipment-created"],
  "state_revision": 7,
  "contract_version": 1
}
```

The response contains `readiness.ready`, whether the edge is `declared`, observed
revision/version, denial `reasons`, target-specific `blockers`, direct
`unmet_dependencies`, required milestones on `transition.required_milestones`,
and `missing_milestones`. A negative check returns HTTP 200; malformed requests
and ownership/authentication failures use the normal API errors.

A check is ready when the exact Operation/source/destination edge is declared,
the retained source is known and active in the proposed source state, optional
revision/version expectations match, target-specific reported blockers are absent,
all direct workflow prerequisites are terminal or satisfied under their requested
outcomes, and every required milestone name is included in the proposed plan.
All reported workflow prerequisites apply to the check; blockers for other
Operations do not deny this edge. Unknown and mismatched prerequisites remain
unmet. Terminal without a requested outcome means completion, not inferred success.

Denial reasons are `transition_undeclared`, `state_unknown`, `terminal`,
`from_state_mismatch`, `revision_mismatch`, `contract_version_mismatch`,
`application_blocked`, `dependency_unmet`, and `milestone_required`.
`state_stale` and `deadline_overdue` are advisories; they are not implicit
transition guards in the workflow contract.

Readiness is a read-only assessment of retained reports and planned names. It
neither reserves a transition nor writes reports, clears blockers, or executes
Operations. Milestone plans are not committed evidence or payload validation;
older retained milestones do not satisfy a new transition. The existing SDK
transaction helpers and workflow-state validation must still commit and validate
the actual milestones with the business write. The application checks its locked
business row and authorization and, where needed, related business rows. Reports
can change after the check, so a positive result is not an atomic guarantee.
Outbox publication lag can produce unknown state or revision mismatches.

The dashboard and existing exact-instance milestone responses also include
`workflow_instance.readiness`: up to 100 declared edges from the current active
state, evaluated with an empty milestone plan. `transition_count` covers all such
edges; `has_more` signals a capped overview. Use the single-edge endpoint for a
specific plan. CLI milestone text prints `workflow-readiness` rows. No additional
schema migration is needed, and existing report validation behavior is unchanged.

### Transactional readiness guards

The Go, Node, and Python customer transaction SDKs can check readiness before a
business write and queue the proposed transition with its actual milestone
payloads on success. Supply a positive expected retained workflow revision and
contract version from the locked application row/contract, plus the intended
subject, scope, and target operation. The SDK pins the app to the transaction.
The application must bind the other selectors to its authenticated Operation;
the checker must authenticate as the same customer, using a read-scoped token.
Execution/publication proof does not grant readiness read access.

All guard failures poison the transaction, so catching an error cannot commit
business writes. Unmet requirements are available in a structured readiness
error. Transport and malformed-response failures also prevent commit. Receipt
replay still skips the callback. Actual milestone payloads and workflow reports
retain their existing validation before commit. Await guards sequentially before
writing and do not queue other evidence concurrently with a guard.

Readiness reads retained Gregale reports while the application SQL transaction
is open; it does not lock those reports or reserve a transition. Lock and recheck
the relevant business rows, including prerequisite rows where required, and
perform application authorization independently. Publication lag can cause a
revision mismatch. Keep network timeouts bounded to avoid holding row locks
indefinitely. No database schema changes are needed for these helpers.

### Business decision evidence

Record why the application chose a business action with a declared milestone.
The versioned `gregale.business-decision.v1` envelope contains `decision` fields:
`workflow`, `instance_id`, `code`, `description`, `rule_id`, and `rule_version`.
Workflow/code/rule ID are lowercase slugs (63/64/64 bytes); the instance ID,
reason description, and rule version are nonempty UTF-8 text bounded to
256/1024/128 bytes without control characters. Rule versions are opaque strings,
so both semantic versions and policy revision identifiers work.

Copy [the payload schema](schemas/business-decision.json) into your app's schemas
and add the milestone to the Operation's immutable declaration:

```yaml
# Inside the existing Operation definition:
milestones:
  approval-decided: schemas/business-decision.json
# Inside the matching operation_workflows entry's steps:
steps:
  - {name: approval-decision, label: Approval decision, operation: approve-order, milestone: approval-decided, instance_id_from: /decision/instance_id, position: 2}
```

Use a unique step name and position within the workflow. Publish the new contract
through your normal application deployment before using the helper; existing
immutable Operation definitions are unchanged. The server validates the payload
against the declared schema and rejects decision evidence that does not match a
workflow/instance resolved by that milestone's declared step.

```ts
tx.businessDecision('approval-decided', {
  workflow: 'order-approval', instance_id: workflowRunID,
  code: 'manual-review-approved', description: 'An authorized reviewer approved the order.',
  rule_id: 'manual-approval', rule_version: '2026-10',
});
tx.workflowTransition('order-approval', workflowRunID, 'reviewing', 'approved');
// Perform the business write through tx after the usual row and authorization checks.
```

Go provides `tx.BusinessDecision(name, preview.OperationBusinessDecision{...})`;
Python provides `tx.business_decision(name, OperationBusinessDecision(...))`
with the model from `faas_sdk.preview.business_decisions`. Node exports
`businessDecisionPayload(decision)` and Python offers `decision.to_payload()`;
Go exports `OperationBusinessDecisionPayload`. These payloads can also be used as
actual milestone facts in the transactional readiness guard.

Decision reports are ordinary durable facts: saved with the business write,
validated before commit, published after commit, and replayed through the existing
outbox. The milestone API returns their structured envelope in `payload`; CLI
milestone inspection shows that payload. The dashboard displays reason and rule
version alongside the observed workflow history. Existing cursors, customer
ownership, retention, and milestone count/byte limits apply. No migration or new
endpoint is required. Evidence does not clear blockers, change readiness guards,
verify the truth of a rule, or authorize a transition; it records the application's
explanation. Report only the customer-visible explanation intended by the declared
schema, without private review notes or credentials.

### Versioned business policy requirements

A workflow transition can optionally require specific application decision evidence:

```yaml
transitions:
  - from: reviewing
    to: approved
    operation: approve-order
    requires_policies:
      - {milestone: approval-decided, rule_id: manual-approval, rule_version: "3", code: manual-review-approved}
```

Declare `approval-decided` with the business-decision payload schema and a workflow
step using `/decision/instance_id`, as described above. Up to 16 requirements are
allowed, each with a unique milestone. Rule ID and decision code are bounded
lowercase slugs; rule versions are nonempty opaque strings of at most 128 UTF-8
bytes. Matching is exact and includes workflow and instance identity. Use a new
immutable contract version when changing requirements; existing pinned Operations
keep their original requirements. Transitions without requirements retain their
existing behavior. Requirements need an explicit target Operation.

Readiness requests accept optional `decisions: [{milestone, decision}]` together
with the planned milestone names. Missing, wrong-version, wrong-code, or wrong-rule
evidence returns `policy_evidence_required` and structured `missing_policies`.
Readiness overviews use an empty plan and display the required rule and version.
SDK transactional guards derive these decisions from the actual milestone
payloads they queue, replacing caller-supplied decision plans.

Precommit validation requires a matching decision payload referenced by the
transition in the same transaction. Publication checks the referenced retained
milestone payloads again; evidence from another Operation, workflow instance, or
an unreferenced fact cannot satisfy a requirement. Metadata-only updates do not
require decision evidence. History continues to expose the immutable milestone
payload and declared transition requirements through existing APIs. Applications
still evaluate the rule, lock/recheck business rows, and perform authorization;
Gregale validates the application-reported evidence, not the truth of the policy.
No database migration is required.

### Business state reconciliation

Reconciliation compares a locked application business row with the retained
workflow instance, then uses the existing transaction outbox to record discrepancies
and refresh the current state. It does not infer missing transitions or decisions.
Business `source_revision` is an opaque nonempty string (up to 128 UTF-8 bytes).
`expected_report_revision` is a separate SDK report counter: pass the last report
revision you expect Gregale to have; zero skips that comparison. Never use a
business row version as a Gregale report revision unless your application has
explicitly maintained that mapping.

Declare a transaction-backed milestone with
[the reconciliation payload schema](schemas/workflow-reconciliation.json), and
bind it to a workflow step using `/reconciliation/instance_id`:

```yaml
# In the existing Operation definition:
milestones:
  workflow-reconciled: schemas/workflow-reconciliation.json
# In the corresponding operation_workflows entry:
steps:
  - {name: reconciliation, label: State reconciliation, operation: reconcile-order, milestone: workflow-reconciled, instance_id_from: /reconciliation/instance_id, reconciliation: true, position: 3}
```

The reconciliation Operation must declare the states it can report through this
workflow and explicitly enable `reconciliation: true` on its discrepancy step.
Fresh snapshots reference exactly that discrepancy milestone as evidence; the
server validates its state, instance, version, and refresh status before accepting
a snapshot without an edge. Normal transition and policy checks remain enforced. Publish the updated immutable contract through normal deployment before
using it. Choose a unique step name/position. The server verifies the evidence's
workflow identity and contract version against this milestone's declared step.

```ts
const receipt = await operations.transaction(request, pool, async tx => {
  const row = (await tx.query('SELECT * FROM orders WHERE id = $1 FOR UPDATE', [orderID])).rows[0];
  // Check the authenticated customer's ownership and application authorization.
  const result = await tx.reconcileWorkflowState(
    'workflow-reconciled', scope, {type: 'order', id: orderID},
    {workflow: 'order-approval', instance_id: workflowRunID,
     authoritative_state: row.status, source_revision: String(row.version),
     expected_report_revision: expectedReportRevision, contract_version: 3},
    options => customerClient.businessMilestones(options),
  );
  return {status: result.status};
});
```

Use the SDK-supported transaction request from the trusted guest listener and a
reader authenticated as the same customer. The helper pins the app from that
transaction and supplies exact scope, subject, workflow, and instance filters;
the application binds these to its authenticated Operation and locked row.
Execution proof does not grant customer history read access. Await reconciliation
sequentially before returning, and do not queue other reports concurrently.

| Comparison status | Queued action |
| --- | --- |
| `in_sync` | None |
| `report_missing` | Explicit state snapshot and discrepancy milestone |
| `report_behind` | Explicit state snapshot and discrepancy milestone |
| `state_mismatch` | Explicit state snapshot and discrepancy milestone |
| `report_ahead` | Discrepancy milestone only; investigate the revision expectation |
| `contract_version_mismatch` | Discrepancy milestone only; align the deployed contracts |

Version conflicts take precedence over missing state; report revision differences
take precedence over state differences. A refresh receives a new SDK report revision
from the existing serialized counter; it does not set that revision to the source
business revision. Current blockers/dependencies/deadlines follow existing snapshot
metadata inheritance. Reconciliation does not clear blockers, invent an outcome,
or provide policy evidence. The application must use the latest locked row and
serialize competing report writers; the remote read does not reserve or lock the
Gregale snapshot. Use bounded network timeouts while holding business locks.

Go exposes `tx.ReconcileWorkflowState(ctx, milestone, scope, subject, input, read)`.
The reader receives `OperationMilestoneListOptions` and returns
`OperationMilestonesResponse`, using the tenant-self feed. Python exposes
`await tx.reconcile_workflow_state(milestone, scope, subject, input, read)` with
`OperationWorkflowReconciliationInput` from `faas_sdk.preview.workflow_reconciliation`;
its async reader receives keyword options as a dictionary and returns the typed
`OperationMilestonesResponse`. Adapt that dictionary to the generated tenant-self
endpoint's UUID/keyword arguments. Reader/validation failures prevent commit even
if caught. Ahead/version statuses are successful diagnostic results; applications
should inspect them, and they never queue a fresh state report.

Discrepancies are historical application observations stored in the existing
milestone API payload and displayed in workflow history on the dashboard, with
both business and report revisions. They are not a live mismatch registry; use
the current state and a subsequent reconciliation to determine whether a gap is
still present. The normal retention, cursor, payload/count limits, contract
validation, publication-after-commit, and receipt replay behavior apply. On replay,
the SDK skips the callback and republishes pending facts. Prefer replaying known
pending Operations first; snapshots restore current visibility without recreating
lost transition history. For periodic reconciliation, invoke a new authenticated
reconciliation Operation identity through your existing scheduler, so receipt
replay does not skip the new comparison. No migration or new endpoint is required.

### Transition-specific business prerequisites

An optional `requires_dependencies` list selects prerequisite workflow names from
an instance's reported `depends_on` links:

```yaml
transitions:
  - {from: ready, to: shipped, operation: ship-order, requires_dependencies: [payment]}
  - {from: ready, to: cancelled, operation: cancel-order, requires_dependencies: []}
```

Omitting this field preserves the existing behavior: all reported prerequisites
apply. An explicit empty list applies no dependency requirements to that edge.
A nonempty list (up to 16 unique workflow names) requires at least one reported
link for each named workflow. Every matching link must be terminal or satisfy its
reported `required_outcome_code`; unfinished, unknown, or mismatched outcomes
remain unmet. Missing links return `dependency_required` with
`missing_dependency_workflows`; existing unresolved links return `dependency_unmet`
with `unmet_dependencies`. Selectors match the full workflow name exactly. Multiple
instances of a selected workflow are all required; use distinct workflow names
when business requirements differ. The declared list requires an explicit target
Operation and is pinned to its immutable contract.

Continue reporting concrete business references through the existing SDK
`WorkflowDependencies` / `workflowDependencies` / `workflow_dependencies` helpers.
This feature selects those links; it does not create links, infer their subjects,
or replace their required outcomes. Account, customer, app, and environment scope
remain the same as the source instance. Blockers targeting the Operation, policy
evidence, and required milestones still apply independently.

API transition models expose `required_dependency_workflows`. Go preserves the
omitted-versus-empty distinction with a pointer to a string slice; Node uses an
optional array, and Python uses `UNSET` versus `[]`. Readiness responses, SDK
guards, dashboard overviews, and CLI prerequisite rows use the selected edge's
requirements. The dashboard's general dependency attention, impact, and tracing
views continue to show the instance's reported links across all possible actions;
action readiness is the precise check for the proposed transition.

Prerequisite checks are observational and are enforced by the transactional
readiness guard when applications opt into it. Direct publication still validates
the transition and its evidence without reserving linked business rows. The
application must lock/check those rows and authorize the action independently.
No database migration is required. Existing contracts retain their behavior;
deploy a new immutable contract version to change edge requirements.

### Retained milestone prerequisites

Use `requires_retained_milestones` to require previously reported facts from
transaction-backed steps of the same workflow. For an order workflow containing
`receive-payment` / `payment-received` and `fulfill-order` steps:

```yaml
transitions:
  - from: payment-pending
    to: fulfilled
    operation: fulfill-order
    requires_retained_milestones:
      - {operation: receive-payment, milestone: payment-received}
```

Each edge accepts up to 16 unique operation/milestone pairs. Each pair must bind
to a declared workflow step; the edge must name its target operation. Raise the
workflow `version` when adding or changing these rules. The lookup covers at most
32 distinct prerequisite pairs across the selected workflow contract.

The existing readiness endpoint, action preview, and selected-instance overview
return `retained_milestones` results. A `verified` result includes the latest
matching fact identity and timestamps; `awaiting_verification` adds
`retained_milestone_required` and keeps `ready` false. Matching includes account,
app, tenant, environment scope, business subject, workflow instance, pinned
contract version, source operation, and milestone name. Page cursors never limit
this lookup. Planned milestones cannot satisfy a retained prerequisite. Facts
from an older contract version do not carry forward automatically.

Verification means a matching application-reported transaction-backed fact is
currently retained. It does not verify an external provider or infer business
truth from arbitrary payload fields. A missing or expired fact does not prove the
event never occurred. Account views without a selected tenant or a reported
state identifying that tenant leave evidence unverified. The dashboard and CLI
show the source milestone, verification status, and evidence identity. SDK
readiness guards check these requirements before staging the proposed transition;
applications still check authorization and their business row under their own
transaction lock. Reporting APIs remain application-owned and do not enforce
this observational prerequisite as a state-change admission rule.

### Invalidating retained prerequisites

A transition can declare `retained_milestone_invalidations` to make a prerequisite
unusable after a later business fact. Both sources must be declared
transaction-backed steps of the same workflow:

```yaml
transitions:
  - from: payment-pending
    to: fulfilled
    operation: fulfill-order
    requires_retained_milestones:
      - {operation: receive-payment, milestone: payment-received}
    retained_milestone_invalidations:
      - prerequisite: {operation: receive-payment, milestone: payment-received}
        invalidated_by: {operation: refund-payment, milestone: payment-refunded}
```

Raise the workflow contract version when adding or changing these rules. An edge
accepts up to 16 unique prerequisite/source pairs. The prerequisite must already
appear in `requires_retained_milestones`, and it cannot invalidate itself. The
32-source lookup bound includes both prerequisite and invalidating sources.

For prerequisites with invalidation rules, readiness uses the latest retained
fact for each source by application `occurred_at`, at or before the evaluation
clock. Publication time and fact identities break ties within one source. A
matching invalidator with an equal or later occurrence time makes the prerequisite
`invalidated`; readiness reports `retained_milestone_invalidated`, the prerequisite
fact reference, and an `invalidation` containing the source and fact reference.
Equal timestamps invalidate conservatively. A payment fact occurring after the
refund can satisfy the prerequisite again. Late publication of an older payment
cannot restore eligibility. Future occurrences do not count yet. Edges without
invalidation or freshness rules retain their existing publication-ordered evidence selection.

The same account, app, tenant, environment, subject, instance, and contract version
checks apply to both facts. Multiple invalidators are allowed; the latest
applicable fact explains the result. An invalidator without retained eligible
prerequisite evidence leaves the result `awaiting_verification`. All facts follow
normal Operation retention; this projection is not a permanent revocation ledger
and expired invalidators stop contributing. SDK guards reject invalidated results.
Applications continue to check their authoritative business row and authorization
before committing a transition.

### Retained prerequisite freshness

Use a versioned freshness rule when evidence has a business expiry, such as an
inventory reservation that remains usable for 15 minutes:

```yaml
requires_retained_milestones:
  - operation: reserve-inventory
    milestone: inventory-reserved
retained_milestone_freshness:
  - prerequisite:
      operation: reserve-inventory
      milestone: inventory-reserved
    max_age: 15m
```

This block belongs to the consuming transition. Selectors must already be required
retained prerequisites. At most 16 unique rules are allowed per edge; `max_age`
must be a positive whole-second duration, bounded to 10 years (315360000 seconds).
Resolved API definitions use `max_age_seconds`. Rules are canonicalized and pinned
in the immutable workflow contract; changes require a new workflow version.

Freshness selects the latest eligible business occurrence, excluding future facts;
late publication of an older event cannot renew a prerequisite. Evidence expires
inclusively at `occurred_at + max_age_seconds`. Readiness returns `expired` and
`retained_milestone_expired`, with the fact identity, `evaluated_at`, `expires_at`,
whole elapsed `age_seconds`, and rounded-up `remaining_seconds` (zero once expired).
Missing evidence stays `awaiting_verification`, carrying the policy and evaluation
time without derived expiry fields. Invalidation takes precedence over expiry,
while freshness details remain available for diagnostics.

Readiness, action previews, and workflow overviews share one store evaluation
clock per response. Dashboard and CLI guidance explain renewal; Go, Node, and
Python guards reject expired or incomplete freshness evidence. Planned facts
cannot renew retained prerequisites. The application commits and publishes a new
prerequisite fact with its actual business occurrence time, then rechecks readiness.
Existing ownership, subject, instance, version, and retention boundaries apply.
Readiness is a point-in-time observation, not a lease: applications must still
verify their reservation and locked business rows before writing.

### Business action previews

A preview returns declared action candidates from a selected workflow instance's
retained current state, including the current revision/version and structured
readiness results for each candidate. It is read-only: no state reports, business
writes, execution, authorization, reservations, or blocker clearing occur.

```http
POST /v1/platform-tenant-self/workflow-actions/preview
Content-Type: application/json

{"app_id":"APP_UUID","scope":"default","subject":{"type":"order","id":"ORDER_ID"},"workflow":"order-approval","instance_id":"RUN_ID"}
```

Customer authentication determines ownership. Account operators use
`POST /v1/apps/{slug}/workflow-actions/preview` with a required `tenant_id` instead
of `app_id`. These use the same read scopes and account MFA requirement as workflow
readiness. An optional `operation` narrows candidates to that target Operation.
Optional `state_revision` and `contract_version` expectations return mismatch
reasons on each evaluated candidate. Invalid selectors are rejected; unknown
retained state is a normal preview response with `reason: state_unknown`.

The response contains `state`, `state_revision`, `contract_version`, `evaluated_at`,
`reason`, `actions`, `action_count`, and `has_more`, plus the selected identity.
Unknown state omits `state` and `state_revision`; an unknown contract has version 0.
A terminal workflow returns no actions with reason `terminal`. An active state
without a matching declared edge returns `no_declared_transition`; otherwise
`actions_available` means candidates exist, even if every candidate has unmet
requirements. At most 100 matching edges are returned in contract order; counts
cover all matching edges. There is no cursor. Use `operation` to narrow a capped
preview, and use the single-edge readiness API for a specific action.

Each action's `transition` gives its target Operation, from/to states, required
milestones, exact policy rule/version/code requirements, and dependency workflow
selectors. Its readiness result identifies matching blockers, unmet prerequisite
links, missing prerequisite workflow links, missing milestones, missing policies,
and stale/deadline advisories. Candidates use an empty evidence plan, so required
milestones and policy evidence remain unmet until the application supplies actual
facts. Policy milestones are identified in `transition.required_policies` even
when not separately listed in `required_milestones`. Preview reads reuse one
scoped source snapshot and its direct dependency resolution; reports can change
during these reads, so `evaluated_at` is an evaluation time, not a transaction fence.

```ts
const preview = await customerClient.workflowActionPreview({
  app_id: appID, scope, subject: {type: 'order', id: orderID},
  workflow: 'order-approval', instance_id: workflowRunID,
});
// Present preview.actions and their requirements to the user.
```

Go provides `client.PreviewPlatformTenantSelfWorkflowActions(ctx, request)` and
`client.PreviewAccountWorkflowActions(ctx, slug, request)`. Python provides
`preview_platform_tenant_self_workflow_actions` and `preview_account_workflow_actions`
under `faas_sdk.api.operations` with typed action preview request/response models.
The Node generated `OperationsService` exposes both account and self endpoints.

To perform a selected action, enter the business transaction, lock and authorize
its business rows, supply actual milestone payloads, and call the transactional
readiness guard using the locked source state and expected report revision.
Do not execute using a cached preview's `ready` value or skip policy evaluation.
Applications decide what payloads to construct and which actions to offer for
review or automation. Existing pinned contracts are unchanged. No migration is
required.

### Business invariant reports

The application evaluates conditions such as “refund amount does not exceed the
captured payment” or “a shipped order has a shipment reference.” Report its result
as `passed`, `failed`, or `unknown`, with a stable code, opaque version, public
explanation, affected Operations, and the locked workflow state. Gregale validates
reported fields; it does not evaluate business expressions or verify their truth.

Declare a transaction-backed milestone using
[the invariant payload schema](schemas/business-invariant.json), then bind its
workflow step to `/invariant/instance_id`:

```yaml
# In the existing Operation definition:
milestones:
  invariant-checked: schemas/business-invariant.json
# In its corresponding operation_workflows entry:
steps:
  - {name: invariant-check, label: Business invariant check, operation: inspect-order, milestone: invariant-checked, instance_id_from: /invariant/instance_id, position: 4}
```

Use a unique step name and position. Deploy the updated immutable contract before
using the helper. The reporting Operation's workflow mapping must declare the
reported state. This is a fact plus a same-state blocker update; it does not
require a new transition or reconciliation permission.

```ts
// Inside the customer transaction callback, after locking the business row
// and the application's complete current blocker snapshot:
const blockers = tx.reportBusinessInvariant('invariant-checked', {
  workflow: 'order-fulfillment', instance_id: workflowRunID, state: row.status,
  code: 'shipment-reference', version: '1',
  status: row.status !== 'shipped' || row.shipment_reference ? 'passed' : 'failed',
  description: 'A shipped order must have a shipment reference.',
  operations: ['complete-order', 'refund-order'],
}, lockedBlockers);
// Persist blockers with the application's business metadata if it owns that head.
// Supply returned blockers to the next invariant helper call in this callback.
```

Go exposes `tx.ReportBusinessInvariant(milestone, preview.OperationBusinessInvariant,
currentBlockers)` and returns updated blockers plus an error. Python exposes
`tx.report_business_invariant(milestone, OperationBusinessInvariant(...), current)`
with the model from `faas_sdk.preview.business_invariants`. Node also exports
`businessInvariantPayload` and `applyBusinessInvariant`; Go exports the payload
model. Invariant codes are at most 54 lowercase slug bytes; opaque versions are
at most 64 UTF-8 bytes; public descriptions at most 256; workflow instances at
most 256. Strings are nonempty without control characters. One to sixteen unique
target Operation names are required. Existing total workflow blocker and milestone
limits still apply; unrelated blockers plus a new check's targets may exceed the
16-blocker limit and will be rejected before commit.

The helper queues both the declared milestone and a full blocker-only update in
the same business transaction. `failed` and `unknown` produce blockers named
`invariant-CODE` on every target action; `passed` removes that check's blockers.
A new report replaces all action targets for its stable code, preserving other
codes, including other invariants and unrelated application blockers. Reserve
the `invariant-` blocker namespace for these checks. Preserve and pass the complete
application-owned blocker head under row locks, including earlier updates in this
callback; a partial or stale list can overwrite unrelated metadata. SDK helpers
return the new head so multiple checks can be chained. Continuing failures preserve
the existing blocker observation time through the usual counter logic.

History retains the structured payload and the dashboard displays status,
version, reason, and affected Operations. Current blockers drive workflow attention,
readiness, and business action previews. Readiness's `invariant_blockers` is a
subset of matching `blockers`, not an additional requirement; failures use
`application_blocked`. CLI overviews emit `workflow-invariant-blocker` rows.
A historical failed fact is not proof that a violation still exists: inspect the
current blocker head. A fact submitted through the plain milestone API records
history only; use the invariant helper to queue the corresponding blocker update.

Transactional guards also deny transitions targeted by pending invariant blockers
in the same callback, before those reports are published. A passing check queued
locally does not override already-retained blockers in remote readiness; publish
the update and recheck. Helper/validation errors prevent commit even when caught.
A `failed` or `unknown` report itself is a valid observation and can commit, so
applications can record existing bad data or unavailable checks. Applications must
independently reject proposed business writes that would violate an invariant.
If a guard rejects the transaction, queued observations roll back too; use a
separate authorized inspection Operation when diagnostics must be retained.

Checks do not automatically create decision or policy evidence, resolve blocker
history, or authorize actions. Publication and receipt replay use the existing
outbox and customer ownership boundaries. Source metadata and observations can
lag after commit; continue locking and validating authoritative business rows.
No migration or new endpoint is required.

### Required invariant evidence per transition

Declare a passing check as an optional immutable transition requirement:

```yaml
transitions:
  - from: ready
    to: shipped
    operation: ship-order
    requires_invariants:
      - {milestone: shipping-checked, code: shipment-reference, version: "1"}
```

Declare the invariant milestone schema and workflow step as described above.
Requirements need an explicit target Operation, a declared milestone, a stable
invariant code, and an exact opaque version. Up to 16 requirements with unique
milestone names are allowed. No requirements preserves existing behavior. Deploy
a new immutable contract version to change them.

Readiness accepts `invariants: [{milestone, invariant}]` together with actual planned
milestone names. Evidence must match the requirement, selected workflow/instance,
source state, and target Operation in `invariant.operations`, with status `passed`.
Missing, mismatched (including version/state/target), failed, and unknown checks
return `invariant_evidence_required` plus structured `unmet_invariants`. Action
previews and dashboard overviews use an empty plan, so required checks appear as
missing. CLI emits `workflow-invariant-required` rows.

SDK transactional guards derive planned invariants from the actual milestone
payloads they queue, replacing caller-supplied plans. In Node use
`businessInvariantPayload(report)`; Python uses `report.to_payload()`; Go uses
`preview.OperationBusinessInvariantPayload{Kind: "gregale.business-invariant.v1",
Invariant: report}`. Evaluate the check on locked business rows for this proposed
transition, and pass this payload as an actual fact to the guard. The report's
`state` is the transition's source state. A passing fact does not automatically
clear already-retained invariant blockers; update/publish that blocker head before
retrying if needed.

Precommit validation requires referenced passing evidence from the same
transaction. Publication validates the referenced payload again, and prevents
using one required invariant fact for another state report within the same
Operation. Receipt replay with the same report identity remains idempotent.
Historical passing facts, unreferenced facts, facts belonging to another Operation,
wrong-version reports, and failed/unknown checks cannot satisfy the requirement.
Direct transition publication enforces these requirements too. Metadata-only
updates and explicitly enabled reconciliation snapshots retain their existing
semantics and do not pretend to be business transitions.

The application evaluates the invariant and authorizes/locks the business rows;
Gregale validates the reported evidence and its binding, not the truth of the
check. No database migration is required.

### Business effect evidence

An application can report what a business action accomplished: a shipment,
refund, reservation, or other effect with an opaque reference. The versioned
`gregale.business-effect.v1` envelope contains workflow, instance, resulting
state, target Operation, effect code/version, status, reference, and public
explanation. Statuses are `pending`, `failed`, and `confirmed`. Confirmed effects
require a nonempty reference; a pending report does not imply success.

Declare a transaction-backed milestone with
[the effect payload schema](schemas/business-effect.json), bind its workflow step
to `/effect/instance_id`, and optionally require it for completion:

```yaml
# In the existing Operation definition:
milestones:
  shipment-effect: schemas/business-effect.json
# In the corresponding operation_workflows entry:
transitions:
  - from: shipping
    to: shipped
    operation: confirm-shipment
    requires_effects:
      - {milestone: shipment-effect, code: shipment-created, version: "1"}
steps:
  - {name: shipment-effect, label: Shipment effect, operation: confirm-shipment, milestone: shipment-effect, instance_id_from: /effect/instance_id, position: 5}
```

Use a unique step name and position. Deploy the new immutable contract before
reporting evidence. The reporting Operation and workflow's declared resulting
state must match the payload. Effect code/Operation/state are lowercase slugs
(maximum 64 bytes), workflow 63, opaque version 64, reference/instance 256, and
public description 256 UTF-8 bytes. Text is nonempty where required and has no
control characters. Optional `amount_minor` and `currency` are supplied together:
a nonnegative integer up to 9007199254740991 and a three-letter uppercase currency.
Amounts stay in minor units; Gregale does not convert currency or infer precision.
Use the reference to identify the confirmed business effect, not credentials or
private provider response bodies.

```ts
// Inside the transaction, after locking and authorizing the business row,
// and checking the stored provider confirmation or local business effect:
const effect = {
  workflow: 'order-fulfillment', instance_id: workflowRunID,
  state: 'shipped', operation: 'confirm-shipment',
  code: 'shipment-created', version: '1', status: 'confirmed' as const,
  reference: row.shipment_reference,
  description: 'The carrier confirmed creation of the shipment.',
};
await tx.guardedWorkflowTransition(readinessRequest,
  [{name: 'shipment-effect', payload: businessEffectPayload(effect)}],
  request => customerClient.workflowReadiness(request));
// Commit the application's state change through tx.
```

`tx.businessEffect(name, effect)` records an ordinary effect milestone without
queuing a transition; it can be used for pending/failed observations or a
completion fact alongside a separately queued transition. Go exposes
`tx.BusinessEffect(name, preview.OperationBusinessEffect)` and its payload type.
Python exposes `tx.business_effect(name, OperationBusinessEffect(...))`, with the
model from `faas_sdk.preview.business_effects`; `effect.to_payload()` supplies a fact to
its transactional guard. Node exports `businessEffectPayload`. Guards derive
planned effects from the actual payloads they queue, replacing caller plans.
Do not queue the same fact separately when the guard already queues it.

Readiness accepts `effects: [{milestone, effect}]` with planned milestone names.
`requires_effects` needs exact milestone/code/version, the same workflow/instance,
resulting `to_state`, target Operation, and confirmed status. Missing, mismatched,
pending, or failed evidence returns `effect_evidence_required` and structured
`unmet_effects`. Previews use an empty plan and show missing effects. Up to 16
unique required milestones are supported, under the existing overall schema,
evidence, payload, and milestone limits. A milestone cannot simultaneously supply
policy, invariant, and effect envelopes. Existing contracts without requirements
retain their behavior.

Precommit validation requires referenced confirmed evidence in the same
transaction; publication validates it again. Required effect facts cannot be
reused for another report in the same Operation; replay of the same report remains
idempotent. Metadata updates and reconciliation snapshots do not synthesize
completion evidence. The existing milestone API returns structured payloads,
the dashboard shows status/reference/amount and unmet requirements, and CLI
previews emit `workflow-effect-required` rows. History records the observation
at that time; it does not track subsequent provider reversals automatically.

For external effects, use the application's durable provider outbox, idempotency
keys, and verified confirmation flow. Initiate asynchronously, keep the business
workflow pending, then use a separate authenticated completion Operation after
confirmation. Do not execute external side effects inside the SQL transaction or
mark an effect confirmed merely because an outgoing request was queued. For
local effects, evidence and the actual database write commit together. Gregale
validates reported structure and transition binding; the application verifies
that the effect occurred, prevents duplicate business effects, and performs
authorization. Publication retry only republishes facts; it does not repeat an
external action. No migration or new endpoint is required.

### Compensation workflows

Record recovery of a confirmed business effect through an application-owned
compensation workflow. The `gregale.business-compensation.v1` envelope links
`source_effect.operation_id` and `source_effect.milestone_id` to an existing
confirmed effect fact, and identifies the compensation workflow/instance,
reported state, reporting Operation, stable code/version, public explanation,
and status. Status is `required`, `pending`, `failed`, or `confirmed`; confirmed
compensation requires its own nonempty reference, such as a refund or reservation
release reference.

Declare a transaction-backed milestone with
[the compensation schema](schemas/business-compensation.json) and bind its step
to `/compensation/instance_id`:

```yaml
# Inside the existing compensation Operation definition:
milestones:
  compensation-reported: schemas/business-compensation.json
# Inside its operation_workflows entry:
steps:
  - {name: compensation-report, label: Compensation report, operation: compensate-order, milestone: compensation-reported, instance_id_from: /compensation/instance_id, position: 1}
```

Define the workflow's actual business states and transitions using the existing
contract facilities. The reported state must be declared; the Operation must
match the reporting Operation. Publish the updated immutable contract before
using the helper. Optional completion transitions can additionally require a
confirmed business effect, such as `refund-effect`, through `requires_effects`.
The compensation report itself does not automatically change workflow state,
create a transition, schedule execution, clear blockers, or satisfy an effect
requirement. Queue the appropriate explicit workflow state/transition alongside
it when the application's business write actually changes state.

```ts
tx.businessCompensation('compensation-reported', {
  workflow: 'order-compensation', instance_id: compensationRunID,
  state: row.status, operation: 'compensate-order',
  code: 'release-reservation', version: '1', status: 'pending',
  source_effect: {operation_id: originalOperationID, milestone_id: reservationEffectID},
  description: 'Reservation release is waiting for confirmation.',
});
```

After verifying the reversal, report `confirmed` with its reference through the
same application workflow instance in a new authorized Operation. Use a new run
ID for a separate compensation attempt if the application distinguishes attempts.
Go exposes `tx.BusinessCompensation(name, preview.OperationBusinessCompensation)`
and `OperationBusinessEffectReference`; Python exposes
`tx.business_compensation(name, OperationBusinessCompensation(...))`, with models
from `faas_sdk.preview.business_compensation`. Node exports `businessCompensationPayload`;
Python offers `report.to_payload()`; Go exports the payload type.

Source IDs are canonical lowercase nonzero UUIDs. Workflow names are at most 63
bytes; state/Operation/code are lowercase slugs of at most 64; opaque version 64;
reference/instance 256; description 256 UTF-8 bytes. Text is nonempty where
required and has no control characters. Existing milestone/schema byte and count
limits apply.

Precommit and publication validation verify that the source is a retained,
confirmed `gregale.business-effect.v1` milestone belonging to the same account,
customer, application, and environment. Business subjects may differ within
that scope; applications authorize the relationship between the original effect
and the compensation business row. Unknown, expired, cross-scope, pending, failed,
or non-effect references fail with the same source-validation error. The source
must already be published; a fact still queued in the current transaction cannot
be used as a compensation source. Keep source retention long enough for recovery:
new observations cannot reference an expired source. Retry of an already-published
compensation milestone is idempotent even if the source subsequently expires.
Source validation can also fail after commit if source retention ends before
publication; the existing publication-incomplete/outbox behavior applies.

The existing milestone API exposes the structured link and status. Dashboard
workflow history shows each compensation observation and links to the source
Operation. Existing CLI milestone inspection displays its payload. These are
historical observations; the application reports current workflow state explicitly
and decides whether recovery remains necessary. No automatic reverse-impact
registry or status machine is inferred from these facts.

For external reversals, use an application-owned durable outbox, provider
idempotency keys, verified confirmations, and authorization. Do not perform the
external reversal inside the SQL transaction. A pending or required report does
not imply that reversal occurred. Receipt replay republishes queued observations
without rerunning the callback or repeating external actions. Report only public
customer-visible recovery metadata. No migration or new endpoint is required.

Managed realtime consumer groups can opt into dead-letter retention (one hour to
30 days; zero defaults to indefinite). Ordered groups require `skip`. Configure
through the admin/MFA `dead-letter-retention` route. Signed
`realtime.consumer_group.dead_letter_expiring` warnings precede cleanup by at least
five minutes from the persisted decision; delivery failure does not block cleanup.
The inspector and dead-letter API expose upcoming expiry and cumulative removals.
Pause/drain do not suspend retention. Replay must commit before cleanup to protect
the snapshot. Migration/runtime validation is pending; see
[ADR 1041](adr/1041-realtime-consumer-group-dead-letter-retention.md).

Consumer-group retry policies support opt-in exponential backoff with jitter and
exact permanent failure codes. Configure the admin/MFA `retry-policy` route with
an enabled flag, 1–300 second base/cap delays, jitter flag, and optional code list.
Reported failures without an explicit delay use policy backoff; explicit delays
(including zero) override it. Permanent codes and attempt exhaustion dead-letter
immediately. Policies do not change already scheduled retries or lease-expiry
reclamation. Node helpers use `useGroupRetryPolicy: true` to omit their default
delay. The inspector shows the earliest delayed ready snapshot. See
[ADR 1042](adr/1042-realtime-consumer-group-retry-policies.md); migration/runtime
validation remains pending.

Bulk realtime dead-letter recovery provides bounded preview and replay APIs.
POST a code/range filter to a group's `dead-letters/preview` route (read scope/MFA),
review up to 32 materialized failures, then POST explicit sequence/dead_since pairs
and the returned group UUID to `dead-letters/replay` (deploy-write scope/MFA).
Preserve timestamps exactly; old selections report stale instead of replaying a
later failure. Per-item ordering conflicts allow eligible messages to proceed.
Missing messages may have expired, but the API does not infer their removal cause.
Pause/drain, external idempotency and fencing remain relevant after replay. No new
migration is needed; runtime validation is deferred. See
[ADR 1043](adr/1043-realtime-consumer-group-bulk-dead-letter-recovery.md).

Realtime consumer-group delivery deadlines are opt-in at creation through immutable
`deadline_metadata_key`. Values are RFC3339 timestamps frozen from message metadata;
missing values have no deadline, malformed values become terminal failures. Ordered
groups require skip policy. Expired work becomes dead during claim/replay maintenance,
leases are capped at the deadline, and replay cannot extend it. The Node helper
cooperatively aborts handlers at expiry; external writes still need their existing
idempotency/fencing controls. Signed `realtime.consumer_group.delivery_expired`
events and inspector `expired_deliveries` counters record both expired and invalid
rejections. There is no background expiry timer; configure compatible dead-letter
retention to reclaim snapshot capacity. Migration/runtime validation is pending.
See [ADR 1044](adr/1044-realtime-consumer-group-delivery-deadlines.md).

Consumer-group claim rate limits can protect downstream services during replay or
backlog recovery. Configure the admin/MFA `claim-rate` route with a rate of 1–1000
new leases per second and burst 1–32; zero rate disables it. Admission state is
persisted and shared across all workers and replicas. Renewals and settlements
continue normally. Batch claims can return admitted work plus a millisecond retry
hint; single claims use a specific 429 with Retry-After. Node helpers wait for these
hints. Inspector counters track throttle decisions rather than withheld messages.
Migration/runtime validation is pending; see
[ADR 1045](adr/1045-realtime-consumer-group-claim-rate-limits.md).

Consumer groups support immutable creation filters over `metadata.event_type` and
exact metadata values. Event types use OR; metadata predicates and the type condition
use AND. Nonmatches advance assignment without creating snapshots and increment
`filtered_messages`, visible with the filter in group status and the inspector.
Up to 128 assignment events are scanned per batch transaction; `filter_scan_limited`
indicates committed progress and clients can poll again. Retention gaps still require
explicit recovery, and raw lag may include nonmatching unscanned events. Existing
pause/drain, capacity, rate and ordering controls remain in force. Migration/runtime
validation is pending; see
[ADR 1046](adr/1046-realtime-consumer-group-message-filters.md).

Consumer-group worker heartbeats distinguish idle live workers from stale reporters.
Account POST/GET `heartbeats` routes require ownership and MFA; scoped POST requires
an explicit heartbeat grant. Report readiness and a short diagnostic every 15 seconds;
timeout is 60 seconds. Node helpers opt in with `heartbeat: true`. Sharing credentials
aggregates identity, so use separate credentials for independent processes.

Inspector health counts include only reporting workers and flag raw pending work
with no ready reporters. Signed worker_lost/worker_recovered events record sampled
liveness transitions, independently of readiness. Reporting does not renew leases
or consume claim credit. Tracking is capped at 32 identities; lost history is bounded
and may be replaced. Migration/runtime validation remains pending; see
[ADR 1047](adr/1047-realtime-consumer-group-worker-heartbeats.md).

Python receive-only realtime can opt into `RealtimeFallbackClient` for HTTPS SSE
when WebSocket connections fail. Deploy the SSE subscription history bounds
(ADR 1098) before enabling Python fallback. Configure proxy streaming without
response buffering and allow SSE heartbeats through. Fallback rejects features
requiring socket control messages; filtered SSE still transfers all channel
messages and filters locally. Transport snapshots can be included in redacted
realtime diagnostics. Runtime fallback validation remains deferred.

Python realtime channel handles support `pause()` and `resume()` (ADR 1099).
Paused channels retain their completed cursor and capacity reservation while
unsubscribing from delivery. Resume replays retained history, using snapshot
recovery if required. Pausing the last active channel releases the shared socket;
an unsubscribe failure may trigger shared connection recovery. Pause can wait for
an active handler indefinitely; use explicit unsubscribe to cancel stuck work.
Standalone recovery coordinators support the same pause boundary for WebSocket
and SSE. Runtime concurrency validation remains deferred.

Python realtime exposes local queue backpressure (ADR 1100). Each WebSocket
subscription reports queue depth/capacity, oldest queued age and overflow count.
Default busy/overloaded thresholds are 50%/90% occupancy or 5/30 seconds waiting.
Callbacks can drive UI warnings or application pause decisions; the monitor does
not change delivery or ACK policy. Channel diagnostics include these payload-free
measurements. Disconnect and pause clear queued observations and stop age timers.
SSE users can report coordinator handler timing separately; this is not a measure
of server lag or intermediary buffering. Runtime monitoring checks remain deferred.

Python retained-channel workers can use graceful drain (ADR 1101) during shutdown.
`subscription.drain(deadline_seconds=30)` freezes local admission, processes its
buffer and flushes the final ACK before closing and releasing the channel slot.
Dedicated native WebSocket coordinators expose the same operation. Inspect the
returned completion, cursor, durable confirmation, expiry and unfinished local
entry count; transport failure does not reconnect while draining. Events arriving
after cutoff remain for later replay. SSE, inboxes and ephemeral helpers are not
covered by this retained-channel drain. Budget time for cleanup after a deadline
and require cooperative handler cancellation. Runtime shutdown checks are deferred.

Retained channel message TTL (ADR 1102) adds `ttl_seconds` to account-authenticated
single retained appends/publishes. Zero disables expiry; 1–86400 seconds produces a
first-commit deadline preserved across retries and edits. Expired stream events
become checkpoints, preserving cursor continuity; REST history suppresses their
payload/metadata. TTL does not delete log rows or roll back previously delivered
state/reducer effects. Normal retention pruning cascades expiration rows.

Roll out migration 20261012102700541 before updated apid/realtimed readers and SDKs,
then enable TTL publishers. Mixed old readers or unfiltered clients may fail to
honor expiry/checkpoints. Legacy fan-out drops expired queued messages; stream
expiry is checked when selecting output, so already selected/in-flight data is not
retracted. Inbox/job processing is outside this delivery-expiry scope. Batch, scheduled,
transactional and scoped-publisher message TTL are described below. Rollback removes deadlines and can expose old
payloads again. Migration application and runtime validation remain deferred.

Retained batch publishing supports TTL defaults and item overrides (ADR 1103).
Batch `ttl_seconds` defaults to zero; omitted/null item overrides inherit, while
zero explicitly disables expiry. Every effective TTL must be 0–86400. Deadlines
share the original batch creation timestamp, are committed atomically with all
messages, and are preserved by idempotent retries. Equivalent resolved TTLs retain
the same batch identity; changed effective lifetimes conflict. Existing no-TTL
batch hashes remain compatible. No additional migration is introduced beyond
ADR 1102's companion table, which remains unapplied in this session. Expired batch
positions use existing WebSocket/SSE/REST checkpoints. Runtime checks are deferred.

Scheduled realtime messages also accept `ttl_seconds` (0–86400, default 0).
Each recurring occurrence expires relative to actual publication, so delayed
execution and failed attempts do not consume its lifetime. The TTL is immutable
for a schedule ID; rescheduling and retrying preserve it. Apply migration
`20261012102700544_realtime_schedule_message_ttl.sql` after the retained-message
TTL migration and before deploying updated scheduler workers.


Transactional realtime TTL (ADR 1105) uses an optional customer outbox
`ttl_seconds` column. New installations include it; existing owners explicitly
apply `pkg/commit/realtime_ttl_upgrade.sql` in their own database. The relay reads
older outboxes with TTL zero, so the column upgrade is required only before opting
into TTL. No platform migration or automatic customer DDL is needed beyond the
retained-message expiration migration. Upgrade relay/readers and checkpoint-capable
clients before enabling expiring publishers.

Inside an active business transaction, Node `setRealtimeCommitTTL(tx, seconds)`
sets a connection-local default for subsequent realtime inserts and changefeed
captures. `insertRealtimeCommitEvent` accepts `ttlSeconds` overrides; omitted
inherits, explicit zero disables expiry. Defaults clear on commit/rollback. The
helper does not begin or commit transactions. TTL starts when the relay publishes
the retained message, not when the customer writes the outbox or commits its
business transaction. There is no exact customer commit timestamp in this outbox.
Publication receipt retries preserve the original deadline even after retained
history pruning; changing effective TTL for an accepted event conflicts.

Scoped realtime publisher messages accept `ttl_seconds` (0–86400, default zero)
for delivery expiry. This differs from credential issue/rotation `ttl_seconds`,
which controls token lifetime. Issuance can set `max_message_ttl_seconds`; zero
means uncapped. A positive cap requires every message to supply a positive TTL
at or below the cap: omission and zero are denied with the existing 403 grant
error. Credential rotation preserves the cap. Use a newly issued credential to
change policy. Retried idempotency keys must keep the same TTL and preserve the
original deadline while the existing receipt is retained. Keys may be accepted
anew after normal receipt expiry. Token revocation does not retract accepted events.

Apply `20261012102700546_realtime_publisher_message_ttl.sql` after the retained
expiry migration and before deploying updated apid. Existing credentials remain
uncapped and existing messages default to no expiry. Checkpoint-capable readers
and SDKs must precede opt-in TTL publishers. Runtime checks and migration
application remain deferred.

Expiry diagnostics (ADR 1107) show TTL, UTC deadline and current status in the
realtime event inspector. Expired payloads and metadata are suppressed; the
status is evaluated when the page loads. Retained rows and cursors remain intact.

`realtimed_expired_checkpoints_total{transport="websocket"|"sse"}` counts successful
WebSocket queue admissions or SSE writes of `message_expired` checkpoints.
`realtimed_expired_outbound_drops_total` counts live payloads discarded after
expiry when dequeued. These process-local counters count delivery attempts per
subscriber, including repeat observations; they do not count unique events or
confirm client receipt. Sum across realtime nodes; restart resets them. Existing
queue-capacity drop metrics retain their meaning. No endpoint/channel labels or
payloads are added to metrics.

Node `onMessageExpired` and Python `on_message_expired` are optional synchronous
observers on native retained WebSocket and SSE clients. Node fallback forwards the
same observer to both transports. Hooks receive only code, channel, sequence and
transport after validation, before cursor persistence/ACK and checkpoint
coalescing. Keep hooks brief; exceptions are isolated, and hooks must not perform
business effects. Reconnects or failed cursor persistence can repeat observations.
SDK `RealtimeExpiryDiagnostics` collectors retain only bounded counters, with no
payloads or identifiers. Python `export_realtime_diagnostics(..., expiry=status)`
includes those counts as decimal strings. No new migration is required.

Retained direct inbox sends now accept `ttl_seconds` (0–86400, default zero).
Positive TTL requires `delivery=retained`. `delivery_expires_at` is the delivery
deadline; history `expires_at` still means retention expiry. Retries and message
edits preserve the original delivery deadline. Expired REST rows have
`expired: true` and empty `data_base64`; skip their payload while advancing the
sequence. TTL expiry does not prune positions or advance the retention floor.

Inbox WebSocket clients receive payload-free `inbox_checkpoint` frames with
`message_expired` and acknowledge through normal per-device cursors. Updated
Node/Python clients skip processors and expose expiry observations, with separate
inbox diagnostic counts. `realtimed_expired_checkpoints_total` adds the fixed
`inbox_websocket` label. Counts describe observations, including replay repeats,
not confirmed client delivery. Frames selected before expiry cannot be retracted.
SSE inbox delivery is not supported.

The owner realtime inspector accepts an explicit inbox principal and separate
inbox cursor after endpoint ownership checks. It displays lifetime/deadline/status
and suppresses expired payloads. It does not enumerate principal identities.

Notification fallback TTL (`notification_ttl_seconds`) remains independent:
inbox delivery TTL does not cancel fallback/push work. Configure both lifetimes
when both online delivery and fallback alerts must expire. Apply migration
`20261012102700548_realtime_inbox_message_ttl.sql` before updated apid; upgrade
realtime nodes and SDKs before enabling inbox TTL. Schema application and runtime
validation remain deferred. Removing TTL data can expose old payloads again.


### Blocker acknowledgement and follow-up

Applications can report `acknowledged_at` and `acknowledged_by` together on an
active blocker, with an optional `follow_up_at` deadline. Actor identifiers use
the existing public actor bounds. All times are finite RFC3339 timestamps;
acknowledgement must follow the known first observation and cannot be later than
the report. Follow-up must be at or after acknowledgement. A past follow-up
is valid and immediately overdue at the queue evaluation time.

These fields are application observations, not verified platform identities.
Publish the full replacement blocker list through the application's business
transaction and outbox. Preserve acknowledgement metadata on subsequent updates;
clearing it explicitly returns the blocker to the unacknowledged queue. Upgrade
all writers before relying on these optional fields.

`reason=unacknowledged` selects blockers without acknowledgement;
`reason=follow_up_overdue` selects deadlines reached at the fixed queue evaluation
time. Owner, code, target and reason must match the same blocker. Summaries expose
`unacknowledged_blocker_count` and `follow_up_overdue_blocker_count`, including per
owner groups. Older blockers with no acknowledgement fields count as
unacknowledged. Refresh the queue to observe newly elapsed follow-up deadlines.

Acknowledgement neither resolves a blocker nor resets its first observation or
escalation threshold. Existing revision history retains acknowledgement changes.
No notification, reassignment or business action is performed by Gregale.


### Business impact and priority queues

Applications may add `priority` (`low`, `normal`, `high`, or `urgent`) and
`business_impact` to each reported blocker. The impact is public UTF-8 text,
limited to 512 bytes without control characters. Omitted priority is treated as
`normal` for queues and counts. The application determines business urgency;
Gregale does not infer urgency from customer identity or financial information.

For example, a shipping blocker can report `priority: urgent` and
`business_impact: Order cannot ship; customer promised delivery tomorrow`.
Set the workflow's existing `deadline_at` to the promised business deadline.
Publish this metadata through the normal application transaction and outbox,
preserving the entire replacement blocker list and its acknowledgement fields.
Priority changes preserve blocker age, escalation timing, and acknowledgement.
They grant no execution authority and trigger no notifications.

Use `priority=urgent` on attention queues and summaries. Priority, owner,
unassigned, code, target and blocker-specific attention reason must match the
same blocker. Workflows with no blockers do not match a priority filter. Queue
entries still return their full reported blocker snapshot. Summary totals and
per-owner groups expose `low_blocker_count`, `normal_blocker_count`,
`high_blocker_count`, and `urgent_blocker_count` for the selected blockers.

Use `sort=deadline` to list the earliest workflow business deadline first;
workflows without deadlines appear last. Equal deadlines use descending update
time and workflow identity as deterministic tie-breakers. `sort=updated_at` is
the default. Summary groups keep their existing alphabetical group ordering.
Cursors bind both priority and sort order as well as the existing access scope
and filters. Refresh from the first page after changing filters or metadata;
this is an evaluated retained-state view, not a database snapshot across writes.

SDK transaction helpers, CLI flags `--priority` / `--sort`, dashboard filters,
and revision history retain and display this application-reported context.
All writers must preserve these optional fields before relying on them.


### Verify cleared blockers with retained business evidence

Clearing a blocker and confirming its resolution are separate observations.
Applications can opt a resolution into verification by reporting paired
`verification_milestone_id` and `verification_milestone_name`, with an optional
`verification_operation_id` and `verification_owner`. The evidence Operation
defaults to the Operation publishing the resolution. Allocate an exact milestone
ID in the application transaction; publish it with the business confirmation
(e.g. `payment-confirmed` or `inventory-reserved`) in the same transaction or a
later publication. If confirmation will occur in another Operation, report that
Operation's exact ID as well. These public owner identifiers use the existing
128-byte actor limits; they do not verify platform identity.

Gregale checks the exact Operation, milestone ID and name. Evidence must be
retained in the same account, app, customer, environment, business subject,
workflow instance, and the resolution report's contract version. Its pinned
workflow mapping must identify that instance. Matching a milestone name alone,
a fact in another customer's workflow, or a fact in another contract version
cannot verify the resolution. This is evidence presence, not independent
validation of the application's business conclusion. Applications decide whether
the chosen fact establishes a fix.

Until matching proof is retained, the finding is `awaiting_verification`; with
matching proof it is `verified`, with `verified_at` equal to proof publication
time. Absence, expiry, or a mismatched reference remain awaiting verification.
A resolution without verification fields remains a legacy resolution and does
not create a new obligation. The existing source-report ownership and revision
checks continue to apply when admitting resolutions.

Obligations come from retained resolution history, so later state reports and
terminal workflow states do not silently clear them. Identical repeated claims
collapse by original blocker occurrence and exact evidence reference; the earliest
revision supplies immutable verification ownership. Different evidence references
are distinct claims. Use an accurate reference and owner when publishing the
resolution. Upgrade all writers before relying on these optional fields.

`reason=awaiting_verification` selects pending obligations and accepts the same
owner/unassigned, blocker-code and target filters against one resolution. Pending
ownership is `verification_owner`, separately from the active blocker owner or
`resolved_by`. Priority filters continue to select active blockers; pending
obligations have no priority and do not match a priority filter. Default attention
queues include retained workflows with pending verification even if otherwise
unblocked or terminal. Summary totals and owner/code/target groups expose
`awaiting_verification_workflow_count` and
`awaiting_verification_resolution_count`; cleared blockers do not become active
blockers again in these summaries.

Attention entries and selected workflow-instance views expose a preview of up to
16 findings, with matching pending items first, and exact
`awaiting_verification_count` / `resolution_verification_count` totals over all
retained distinct obligations. Selected workflow-instance history pages include
`resolution_verifications` for each report so older obligations remain inspectable
beyond the preview. Queue evaluation time freezes which published claims and
proofs can participate across cursor pages; refresh to observe newly published
confirmation. Retention and state changes still make this an evaluated retained
view rather than a transactionally frozen database snapshot.

The dashboard and CLI display proof identity and status. Pending proof marks the
observational workflow decision as needing attention, including terminal workflows.
It does not re-block an Operation, change workflow state, grant action authority,
or alter readiness enforcement. No notification or business action is performed,
and the existing JSON storage needs no migration.

### Inspect workflow bottlenecks

Selecting a workflow and instance in the business milestone view returns `workflow_instance.bottlenecks`, also shown in the dashboard and CLI. Analytics read the latest 1,024 retained reports independently of visible history pagination. Each breakdown returns the 32 longest duration groups, with separate truncation flags; scalar totals include every group in the window.

State time and blocked time use application `occurred_at` timestamps between consecutive revisions. Metadata updates preserve elapsed time. Terminal states stop the clock. Blocked time counts each interval once, while breakdowns attribute that interval to each reported blocker code, Operation, owner and contract version. Concurrent blockers therefore overlap. Ownership changes take effect at the next reported snapshot; `first_observed_at` does not invent earlier ownership history.

Verification wait uses resolution report publication through proof publication, or evaluation time while pending. Proof already retained before the resolution has zero wait. Wait is grouped by the original verification owner, and concurrent obligations can overlap. A resolution whose original report falls outside the retained window contributes an unknown-start count instead of an inferred duration.

`history_complete` describes coverage from revision 1 to the selected current report. `incomplete_reasons` identifies retention gaps, duplicate revisions, inconsistent or future event times, contract changes, state discontinuities and missing verification starts. Intervals across uncertain boundaries are excluded. These figures measure observed history, not business time before the first report. Nonterminal tails extend to `evaluated_at`; results can change as retention expires or new reports arrive. Durations are whole seconds, and rounded breakdowns may not sum exactly to scalar totals.

### Compare workflow performance across instances

`GET /v1/apps/{slug}/workflow-performance/summary` compares retained instances for an explicit `scope` and `workflow`. Account readers with MFA can optionally select `tenant_id`. Customer readers use `GET /v1/platform-tenant-self/workflow-performance/summary` with explicit `app_id`, `scope` and `workflow`; ownership comes from their credentials and tenant overrides are rejected. These read-only endpoints support no pagination or date-range selector.

The dashboard's **Workflow performance** view is available from customer operations and a selected workflow's bottleneck panel. Completed and ongoing cohorts are calculated separately. Each considers the latest 100 matching retained instances, ordered by current report update time with a stable identity tie-breaker. `matching_workflow_count` covers all matches; `sampled_workflow_count` and `cohort_truncated` describe the selected sample. Completed means the latest reported state is terminal, independently of outcome or pending verification.

Only instances with `history_complete` enter duration distributions. Missing or inconsistent histories, contract changes and histories exceeding the latest 1,024 reports are excluded and counted explicitly. An excluded instance does not get replaced by an older complete instance. Exclusion reasons can overlap; their counts need not sum to the excluded count. This is a recent retained sample, not a population estimate or historical archive. If every sampled instance is incomplete, distributions have `workflow_count: 0` and zero-valued durations; the dashboard shows that no comparison is available.

Each eligible instance contributes one accumulated duration to state-time, blocked-time and verification-wait totals, including zero blocker/verification values. Breakdowns contribute one accumulated duration per instance that reported that state, blocker tuple or verification owner; absent groups do not introduce zero samples. Repeated visits to a state and recurring blockers accumulate within an instance. States and blockers preserve contract version; blockers preserve Operation, code and owner. Verification owner groups sum observed waits for that owner within each workflow, with pending obligation counts. A terminal workflow can still accrue verification wait while proof is pending. Concurrent blockers and obligations overlap.

Distributions expose `workflow_count`, `total_seconds`, `p50_seconds` and `p95_seconds`. Both percentiles use nearest rank (the sorted value at ceiling of percentile times count), including the lower central value for an even-sized p50 sample. Ongoing time measures elapsed observation through `evaluated_at`; it does not predict completion. The 32 groups with the largest total seconds appear for each dimension, with deterministic identity ties and separate truncation flags. Cohort totals include every group before truncation. Refreshing may change results as reports arrive, states reopen or retention expires.

### Investigate performance contributors

Performance tables link to ranked contributing instances. Account readers use `GET /v1/apps/{slug}/workflow-performance/instances`; customer readers use `GET /v1/platform-tenant-self/workflow-performance/instances`. Both require the same explicit app/environment/workflow selection as summaries, plus `cohort=completed|ongoing` and a `dimension`:

- `state_time`, `blocked_time`, or `verification_wait` selects the overall measure and rejects group selectors.
- `state` requires exact `state` and `contract_version`.
- `blocker` requires exact `operation`, `code`, `contract_version`, and either `owner` or `unassigned=true`.
- `verification_owner` requires either `owner` or `unassigned=true`.

Pass the summary's opaque `cohort_token` to retain its evaluation time and verify the same sampled instances, matching counts, history and verification evidence. Tokens bind account/customer role and app/environment/workflow/customer selectors. If current instance updates, retention expiry or missing evidence change that cohort, the API returns `409` with problem code `workflow_performance_cohort_changed`; refresh the summary. New evidence published after the evaluation time is excluded from that evaluated view. A token verifies retained evidence rather than storing a database snapshot. Without a token the endpoint evaluates a fresh cohort and returns a token for that view.

The response includes all complete-history contributors to the selected group, at most 100, sorted by observed seconds descending with stable identity ties. No pagination is supported. Its `duration` distribution uses exactly those items and reproduces the corresponding summary measure when the cohort token remains valid. Overall dimensions include eligible zero values; exact groups include instances that reported that group. Matching/sample/coverage counts describe the whole selected cohort before filtering to contributors, and incomplete instances remain excluded.

Each contributor exposes its business subject, reported current state, application blockers with public owner and next action, and a pending-first verification preview of up to 16 obligations with exact retained counts. Customer responses omit tenant identifiers. The dashboard links to workflow history and the reporting Operation. Historical blockers or owners that contributed elapsed time may differ from the displayed current snapshot. Workflow history links open the current retained view; they do not freeze the earlier performance evaluation.

### Observe business state SLA budgets

Workflow manifests can declare optional `state_sla_budgets`, for example `payment-approval: 30m`. Definition APIs expose the equivalent `state_sla_budget_seconds` map. Budgets must be positive whole seconds, at most ten years, and name declared nonterminal states. All steps of one workflow definition must agree. Publish a new workflow contract version when changing budgets; retained Operations continue to use their pinned declarations.

A selected workflow, performance contributor, or attention entry can expose `state.sla`: the budget, evaluation time, status (`within_budget`, `at_risk`, `breached`, or `unknown`), and current-visit coverage. When entry is known, it also includes observed entry/due times, whole elapsed/breached seconds and remaining seconds rounded up. A breach starts at the due time, including exact equality. Terminal states have no current SLA.

The clock starts at the first report of the current state visit. Consecutive metadata updates in the same state preserve that clock; leaving the state and later returning starts a new visit. Retained history must establish entry from revision 1 or a contiguous preceding different state. Revision gaps/duplicates, inconsistent event times, changed contract versions or different budgets or warning thresholds during a visit make its entry unknown. Reads examine up to 1,024 retained reports plus one boundary witness; absence does not imply a breach. The clock measures application-reported occurrence times and does not infer business time before the first observation.

Use `reason=sla_breached` in the attention API, dashboard or CLI. A known breach joins the default attention queue even without blockers. Existing owner/code/target selectors retain their matching rules; an SLA does not invent an owner. Queue summaries report breached workflows and unknown current entries among the matching queue. Unknown SLA status alone does not place a workflow in the default queue.

Performance cohorts expose configured, evaluated, breached and unknown SLA workflow counts, plus evaluated/breached visit counts for each state/contract group. Completed workflows retain breaches from earlier visits. Evaluation requires complete whole-workflow history under the existing cohort coverage rules and consistent budgets within each visit. Configured means the pinned contract or observed history declares a budget, even if no budgeted state was visited. Evaluated means the observed run was examined; each workflow counts once as breached if any evaluated budgeted visit reached its budget. Incomplete histories and ambiguous policy changes contribute unknown counts, rather than inferred violations. Consequently a current state visit can have a known breach while the whole workflow remains unknown in performance comparisons. State visit counts may exceed workflow counts when workflows revisit a state.

Budgets provide observations for application decisions. They do not reject transitions, change readiness, reassign work, invoke recovery, or send notifications.

### Warn before a state SLA breach

Add optional `state_sla_warning_percent` to the workflow contract alongside its budgets:

```yaml
state_sla_budgets:
  payment-approval: 30m
state_sla_warning_percent:
  payment-approval: 80
```

The API uses the same `state_sla_warning_percent` map with `state_sla_budget_seconds`. Each threshold must be a whole percentage from 1 through 99 and reference a state with a positive SLA budget. At most 32 states can be configured. Steps within one definition must agree; change the workflow contract version when changing thresholds. Omitted states keep their existing behavior without an early warning.

For a known current visit, `warning_at` is the observed entry time plus the budget multiplied by `warning_percent / 100`. A 30-minute budget at 80% warns at 24 minutes with 6 minutes left. Threshold arithmetic preserves fractional seconds; it does not round to whole elapsed seconds to determine status. Exact warning-time equality starts `at_risk`; exact due-time equality starts `breached`, which replaces the risk status. Metadata updates preserve both times. Terminal states have no current SLA. Unknown history exposes the configured percentage but omits the warning time and never implies risk.

Use `reason=sla_at_risk` in the attention API, dashboard or CLI to select known visits that reached their warning threshold and have not yet breached. These visits also enter the default attention queue. Summaries include `sla_at_risk_workflow_count` separately from breached and unknown counts, respecting the existing scope, customer, workflow, owner, code, target-operation and priority filters. SLA risk does not invent blocker ownership or urgency. Cursor pages evaluate warning and due times at the original queue evaluation time; refresh to observe elapsed-time changes.

Selected workflow views and performance contributors expose the warning percentage/time alongside due time and remaining seconds. Historical performance breach counts remain breach counts; early warnings are current-visit observations. Warnings do not enforce readiness, send notifications, or perform application actions.

### Retained prerequisite recovery plans

Missing, expired, and invalidated readiness results include an optional `recovery`
object derived from the pinned prerequisite selector. It identifies `reason`,
`operation`, `milestone`, and three ordered steps: `perform_operation`,
`publish_milestone`, then `recheck_readiness`. Verified evidence omits recovery.
For invalidation, `after_occurred_at` is the exclusive invalidator occurrence
threshold; for expiry it is the prior prerequisite occurrence. Any freshness
limit must still be satisfied when readiness is checked again.

The application authorizes and performs the actual business action, commits its
milestone with the true occurrence time, and publishes it before rechecking the
original transition. The new fact must match the existing ownership, subject,
workflow instance, and contract version. Never backdate or fabricate a fact to
satisfy a recovery plan. Missing retained evidence does not prove the business
action never happened: inspect authoritative rows before retrying an action.

Plans are bounded by the existing 16 retained prerequisites per edge, with exactly
three steps per result. They do not recursively resolve producer dependencies or
assert that a producer can run from the current state. The application must check
that operation's own requirements. API previews and overviews return these same
plans; shared dashboard/CLI guidance and Go, Node, and Python SDK types expose
them. No new lookup, manifest rule, migration, or execution permission is added.

### Alternative retained prerequisite groups

Declare named `retained_milestone_groups` on a transition when different retained
business facts can satisfy the same requirement. For example:

```yaml
transitions:
  - from: payment-pending
    to: fulfilled
    operation: fulfill-order
    retained_milestone_groups:
      - id: settlement
        any_of:
          - {operation: receive-payment, milestone: payment-received}
          - {operation: approve-credit, milestone: credit-approved}
```

Any one verified alternative satisfies a group. Every group must be satisfied,
alongside all mandatory `requires_retained_milestones` and the edge's other
requirements. An edge accepts up to 8 uniquely named groups, each with 2–16 unique
operation/milestone selectors. Group IDs and alternatives are canonicalized; changes
require a new workflow version. The existing 32-distinct-source lookup bound
covers mandatory facts, alternatives, and invalidation sources across the workflow.
All alternatives must bind to declared transaction-backed workflow steps.

Freshness and invalidation rules can target either mandatory selectors or group
alternatives. Evaluation uses the same ownership, subject, workflow instance,
contract version, retention, and server clock as mandatory prerequisites. Planned
facts cannot satisfy alternatives. Failed alternatives retain their evidence and
recovery diagnostics but do not block a group with a verified alternative.

Readiness returns `retained_milestone_groups` with each group's `id`, `satisfied`,
and complete `alternatives` findings. An unsatisfied group adds
`retained_milestone_group_unmet`; mandatory findings remain in `retained_milestones`.
Dashboard/CLI guidance names a satisfied path or explains that recovery paths are
choices, not cumulative actions. SDK guards require complete matching group
findings and at least one usable proof, including that alternative's pinned
freshness policy. Missing or contradictory group findings fail the guard.

Flat groups express alternatives between individual retained facts; named
compound paths are described below. No business choices are inferred. Gregale evaluates every option
and does not execute a path. Applications still authorize actions and check locked
business rows; readiness remains a point-in-time observation. No database or
application-schema migration is required.

### Compound retained prerequisite paths

Use named `paths` instead of a flat `any_of` list when an alternative business
path needs multiple facts together:

```yaml
retained_milestone_groups:
  - id: settlement
    paths:
      - id: card
        all_of:
          - {operation: receive-payment, milestone: payment-received}
          - {operation: check-fraud, milestone: fraud-check-passed}
      - id: business-credit
        all_of:
          - {operation: approve-credit, milestone: credit-approved}
          - {operation: accept-purchase-order, milestone: purchase-order-accepted}
```

Every fact within a path must be verified at the same readiness evaluation time.
The group passes when at least one complete path passes. Payment received plus
purchase order accepted cannot combine across paths to satisfy settlement.
Existing mandatory prerequisites and every other group still apply. Flat `any_of`
groups retain their existing behavior; a group cannot combine both forms.

Each group accepts 2–8 uniquely named paths with nonempty `all_of` lists. Selectors
must be unique within each path and bound to transaction-backed workflow steps.
The sum of fact occurrences across all paths is bounded to 16 per group, including
shared selectors counted once in each path. This preserves the existing expanded
evidence budget. The workflow-wide lookup remains bounded to 32 distinct sources.
Path IDs and selectors are canonicalized; changes require a new workflow version.

Freshness and invalidation policies apply to each selector, including shared facts.
`retained_milestone_groups[].paths` returns every path's `id`, `satisfied`, and
`requirements` findings, including fact identities and recovery plans. Compound
groups omit flat `alternatives`; flat groups omit `paths`. A failed path does not
block a different complete path. Guidance identifies a satisfied path or lists
what each blocked path needs. SDK guards verify complete matching facts for one
whole path and reject missing or contradictory path findings.

Recovery choices belong to the application: authorize and check business rows
before performing any actions, publish actual facts, then recheck readiness.
Gregale does not choose or execute a path. Arbitrary recursive Boolean expressions
are unsupported, and no database or application-schema migration is required.

### Report the selected business path

Applications can supply `selected_paths` in a readiness request and transition
report. Each entry identifies `group_id` and `path_id` in the pinned edge:

```javascript
await tx.guardedWorkflowTransition({
  ...proposal,
  selected_paths: [{group_id: 'settlement', path_id: 'card'}],
}, facts, checkReadiness);
```

Readiness still evaluates every path for diagnostics, but only the selected path
can satisfy that group. A valid credit path cannot make the chosen card path ready.
Without a selection the read-only check continues to show available paths.
Unknown group/path pairs deny readiness through `path_selection_invalid`.
Malformed IDs, duplicate group selections, and more than 8 selections are rejected.

Guarded transitions require an explicit selection for every compound group and
verify that its complete evidence passes. SDK guards compare the response's
`selected_path_id` against the proposed choice and queue that same choice with
the transition. Choices are copied before passing the request to a checker to
prevent callback mutation from changing what the guard verifies. Flat groups keep
their existing behavior and cannot receive named-path selections.

For direct reporting, Node `tx.workflowTransition` accepts selections as its fifth
argument; Go `tx.WorkflowTransition` accepts variadic
`preview.OperationWorkflowPathSelection` values; Python `tx.workflow_transition`
accepts `selected_paths=[OperationWorkflowPathSelection(...)]`. Guards obtain
choices from the readiness proposal. Direct reporting validates choices against
the pinned contract, while retained-evidence readiness remains observational.
Choices must belong to a declared business transition; metadata-only updates and
initial state snapshots cannot carry them. Omission remains supported for legacy
reporting and does not imply a path choice.

Choices commit in the application outbox, publish with the report, participate in
its canonical fingerprint, and appear in receipts and retained workflow history.
Publication requires the returned choices to match before acknowledging the row.
The dashboard and CLI history identify each selected group/path. Choices describe
that transition, not a persistent preference for later edges; metadata reports
omit them and do not remove prior transition history.

Before upgrading the platform, apply `20261010114245301_customer_operation_selected_paths.sql`. Application database owners
must reinstall the SDK's additive customer Operation schema before upgrading the
adapters. The new `selected_paths` JSON column defaults to an empty array, so old
pending reports remain publishable. Gregale records the application's decision
without choosing a path, authorizing actions, or changing business state.

### Explain a selected business path

A selected path can reference a business decision committed in the same application
transaction. Queue the decision before the transition and use the SDK helper to
copy its stable fact ID, reason, decision code, rule ID, and rule version:

```typescript
tx.businessDecision('credit-decided', {
  workflow: 'order-approval', instance_id: runID,
  code: 'credit-approved', description: 'Customer passed the business credit policy.',
  rule_id: 'business-credit', rule_version: 'v3',
});
const choice = tx.workflowPathDecision('payment', 'business-credit', 'credit-decided');
tx.workflowTransition('order-approval', runID, 'pending', 'approved', [choice]);
```

Declare the decision milestone as a step of this workflow with its instance pointer
bound to `/decision/instance_id`, in the same pinned contract version as the edge.
The SDK precommit validator verifies the referenced fact before the business write
commits. Publication requires that same fact to have been published for this
Operation. The reference must appear in the transition's `evidence_milestones`.
Both validation paths check the decision payload, workflow instance, and contract
version; a copied reason that differs from the fact is rejected.

Go uses `choice, err := tx.WorkflowPathDecision(group, path, milestone)` after
`tx.BusinessDecision`, then passes `choice` to `tx.WorkflowTransition`. Python uses
`choice = tx.workflow_path_decision(group, path, milestone)` after
`tx.business_decision`, then `tx.workflow_transition(..., selected_paths=[choice])`.
Helpers require exactly one queued decision fact with the requested milestone name.

The optional wire fields on each selection are `decision_milestone_id`,
`decision_code`, `decision_description`, `decision_rule_id`, and
`decision_rule_version`. Supply all five together, or omit all five. The validated
explanation remains in receipts and workflow history and appears in dashboard and
CLI views even when the original fact leaves the current page. Readiness checks
validate the bounded selection shape; decision evidence is verified during report
validation and publication. Applications still own decisions and authorization.
Existing selections remain compatible. This uses the existing selected-path JSON
columns; no additional platform migration or customer schema installation is needed.

### Declare the decision rule for a business path

A named retained prerequisite path can declare an optional `decision` requirement:

```yaml
retained_milestone_groups:
  - id: payment
    paths:
      - id: business-credit
        all_of:
          - operation: approve-credit
            milestone: credit-approved
        decision:
          milestone: credit-decided
          code: credit-approved
          rule_id: business-credit
          rule_version: v3
      - id: card
        all_of:
          - operation: charge-card
            milestone: payment-confirmed
```

Declare `credit-decided` as a milestone in this Operation and a step of the same
workflow contract version, with its instance pointer bound to
`/decision/instance_id`. A requirement contains an exact milestone, decision code,
rule ID, and rule version. Changing these expectations requires a new workflow
contract version. Optional requirements are copied into pinned contracts.

Readiness compares each path's requirement with planned `decisions` and
`milestones`. A path passes only when both its retained facts and decision rule
match. Each constrained path returns `decision.requirement`, `decision.reported`
when a decision was supplied, and `decision.satisfied`. Guidance names the expected
and reported code and rule version. A selected path that fails its decision rule
cannot be replaced by a valid sibling. Without a selection, readiness continues
to explore which complete paths can pass; planned evidence is not committed proof.

Pass the typed business decision payload among the facts supplied to an SDK
`guardedWorkflowTransition` / `GuardedWorkflowTransition` /
`guarded_workflow_transition` call. The guard validates decision diagnostics against
those facts and automatically attaches the matching staged fact reference to the
chosen constrained path. If a reference was already supplied, it must match the
staged fact exactly. Exactly one staged milestone may have the required name.
The existing precommit and publication validators then verify the actual linked
fact and reason against the pinned path rule.

For direct transitions, queue the decision and build the selection using the
[SDK decision reference helper](#explain-a-selected-business-path). A group with
any decision-constrained path requires an explicit path choice for a business
transition report. Selecting a constrained path also requires its matching linked
decision; selecting an unconstrained sibling requires no decision. This applies
when the contract opts into path rules; older contracts retain their behavior.
Applications continue to own selection, authorization, and execution. No new
platform migration or customer outbox schema update is required.

### Compare retained workflow revisions

Each entry in a selected workflow's `workflow_state_history` and
`workflow_instance.transitions` now includes a derived `comparison`. It compares
with the preceding retained entry for that tenant and workflow run on the same
page, using consecutive application revisions and the same pinned contract.

A `compared` result contains a concise summary and structured changes to reported
state, blockers, selected paths, and decision code, reason, or rule version. Each
change identifies its kind and key and includes public before/after snapshot text.
The comparison links the previous report and Operation and includes deduplicated
reported milestone and decision references from both revisions. Expected resolution
verification references are labeled `verification_expected`; they are not proof
that verification succeeded. Evidence links describe reported support, not causality.

The dashboard shows summaries, before/after values, and links to the relevant
Operations. CLI output includes `workflow-revision-comparison`,
`workflow-revision-change`, and `workflow-revision-evidence` lines. All SDKs expose
the comparison models through the existing business history response.

`baseline_unavailable` marks the first entry for a run on a page. `revision_gap`
marks missing intervening revisions. `ambiguous_revision` marks duplicate revisions
visible on the page. `contract_changed` identifies a version change without
comparing business snapshots across contracts. These results do not invent missing
changes. Page boundaries and retention remain explicit limitations: following the
next cursor does not fetch an extra prior report or reconstruct expired history.
Account operator comparisons never mix tenants.

Path selections are per business transition. An omitted selection on a metadata
update does not mean the application removed its prior path. A later selection
without a selection on the preceding revision is shown as `path_selected`, with
no claimed previous choice. Removing a blocker from a report does not prove the
business issue was resolved. A path change does not prove which prerequisite
expired or caused it; applications must report those facts explicitly.

The derived response is bounded to 64 changes and 64 evidence references per
comparison, based on existing report limits. It is read-only, uses the existing
retained page, and requires no migration or customer outbox schema update.

### Report the application's reason for a workflow change

A workflow transition or same-state metadata update may carry one optional
`change_reason`: a business decision milestone ID and a validated snapshot of its
`decision` payload. Queue the decision and workflow update in the same application
transaction, then attach the reason to the most recently queued update:

```typescript
tx.businessDecision('review-decided', {
  workflow: 'order-approval', instance_id: runID,
  code: 'credit-limit-exceeded', description: 'The customer exceeded the business credit limit.',
  rule_id: 'business-credit', rule_version: 'v3',
});
tx.workflowTransition('order-approval', runID, 'pending', 'manual-review');
tx.workflowChangeReason('review-decided');
```

Declare `review-decided` as a workflow step in the same pinned contract version,
with its instance pointer bound to `/decision/instance_id`. Go uses
`tx.WorkflowChangeReason(milestone)` after `tx.BusinessDecision` and the workflow
update; Python uses `tx.workflow_change_reason(milestone)` after
`tx.business_decision` and the update. For guarded transitions, include the decision
payload among the guard's planned facts, await a successful guard, and attach the
reason before committing. The helper requires exactly one queued decision with
that milestone name and permits one reason per report.

Initial snapshots cannot carry change reasons. Same-state blocker, deadline,
outcome, or dependency updates may include exactly the reason fact as evidence;
other metadata evidence remains disallowed. Business transitions retain their
existing evidence requirements and limits. The reason refers to this update only;
it is not inherited automatically by later reports.

Precommit validation checks the actual decision from the same transaction.
Publication checks the referenced fact from this Operation. Both require matching
workflow instance, pinned contract version, decision code, reason, and rule fields.
A reason that differs from the fact is rejected. The reason is part of canonical
report identity and receipt acknowledgment, and all SDK publishers preserve it
through replay. Omission preserves existing report identities and behavior.

Workflow history, latest state, and revision comparison objects expose
`change_reason`. Dashboard and CLI show it as the application's reported reason
and link its fact identity. Comparisons retain the reason even when the preceding
revision is missing, without inventing the missing change or inferring independent
causality. This is business context supplied by the application, not execution
permission or proof from an external service.

Apply the additive platform `customer_operation_change_reason` migration and
reinstall the SDK customer Operation schema before upgrading the outbox adapters.
Both columns default to JSON null for existing rows. The migration is provided in
this change; it is not applied automatically to running databases.

### Application-defined workflow playbooks

Workflow contracts can declare advisory playbooks for exact blocker codes,
missing milestones or dependency workflows, pending verification blocker codes,
and unmet readiness reason codes. Add `playbooks` to an existing
`operation_workflows` entry alongside its `name`, `version`, and `steps`:

```yaml
playbooks:
  - id: stock-review
    version: 1
    match_kind: blocker
    match_code: stock
    summary: Confirm inventory availability with the warehouse team.
    owner: warehouse
    required_evidence:
      - Inventory reservation reference
      - Warehouse confirmation
    runbook_url: https://docs.example.com/runbooks/inventory
    action_url: https://app.example.com/inventory/review
```

Each workflow contract permits 32 unique playbooks. IDs are unique within the
workflow; explicit rule versions start at 1. Guidance is read from the immutable
operation definition that produced the current workflow report and its matching
workflow contract version. Publish revised guidance through a new definition;
existing reports continue using their original definition until the application
reports from the new one. Repeated steps may repeat an identical rule, but
conflicting definitions of an ID are rejected.

The workflow case API returns `guidance`, `guidance_status`, and
`guidance_truncated`. Missing or stale reports never match guidance; unavailable
reporting definitions are not replaced by a newer deployment's playbooks.
Terminal reports do not match active blockers or prerequisites. A responsible
team is a suggestion and does not change follow-up ownership. Evidence lists are
human checklists, not automatically verified requirements. Runbook and action
links require absolute HTTPS URLs without embedded credentials. Gregale does
not fetch or execute them; the application authenticates, authorizes and handles
any resulting action.

### Durable playbook checklist progress

A matched playbook can now start an advisory checklist through
`POST /v1/apps/{slug}/workflow-readiness/playbook-progress` or the corresponding
`/v1/platform-tenant-self/workflow-readiness/playbook-progress` endpoint.
Use the same exact case query selectors as the case overview, without pagination.
Account requests require `tenant_id`, write scope and MFA. Customer identity is
always authentication-derived and requires operations-manage scope.
The start body includes `playbook_id`, `playbook_version`, `contract_version` and
`reporting_operation_id` from matched guidance. Missing, stale, terminal or changed
matches cannot start new work. Repeated starts for the same case, definition,
workflow version and rule version return the existing checklist without resetting it.

The checklist freezes the whole rule, required evidence labels, original reporting
operation and immutable definition. Deployment changes and newer reports never
upgrade a started checklist. Read all retained checklists in the case overview's
`playbook_progress` field, including those no longer matching current guidance.
At most 100 checklists are stored per exact case. They survive subscription deletion,
are removed with their owning application/customer/account, and have no timed expiry.

Update one item with `PATCH .../playbook-progress/{id}` and a body containing
`revision`, zero-based `item_index`, `status` (`pending` or `completed`), a required
review `note`, and an explicit `evidence` array. Each reference has `operation_id`
and optional `milestone_id`. Operation evidence must belong to the same account,
customer, application, environment and business subject. Milestone references must
also be retained facts for the pinned workflow, instance and contract version.
Each edit accepts up to eight evidence references and a note of at most 1,024 UTF-8
bytes; request bodies are capped at 32,768 bytes. Completed items require at least
one reference. The authenticated reviewer and server
clock are assigned by Gregale; callers cannot impersonate a reviewer. A concurrent
revision change returns 409 without overwriting committed work. Pending edits can
reopen an item. Labels and pinned guidance cannot be edited.

Case overviews show counts and remaining items. The dashboard supports starts and
item reviews, and Go, Node and Python SDKs expose both write endpoints. The timeline
includes starts and the latest 64 item review snapshots per checklist; truncation is
explicit. Evidence is validated on edits, not continuously, and retention may later
remove referenced facts. Human review and checklist completion do not change
application-owned blockers, readiness, authorization, execution or business outcomes.

### Check whether pinned playbooks still apply

Every checklist returned in a workflow case overview now includes an optional
`applicability` observation. Its status distinguishes `applicable`, `trigger_absent`,
`terminal_reported`, `contract_changed`, `guidance_changed`, `evidence_unavailable`,
`definition_unavailable` and `evidence_incomplete`. The observation includes a server
check clock, current report identity and revision where available, reporting-definition
change information, a review suggestion, and the number of pending checklist items.
The dashboard renders the observation and links to its reporting operation. Go, Node
and Python SDKs expose the same response metadata.

Checks use the current exact-case application report and its immutable reporting
definition, sharing the evidence used to match case guidance. Missing, stale or
incomparable state cannot establish applicability or trigger absence. A changed
workflow contract requires review. Within the same workflow version, compare the
whole canonical rule, including its rule version, evidence labels and links; changed
or removed guidance is reported even if its old trigger still appears. A new reporting
definition with identical guidance can still be applicable and is explicitly flagged.

Positive trigger matches can be observed in incomplete evaluations; `evidence_complete`
records that limitation. An absent trigger is reported only with complete relevant
coverage: full current blockers, full internal verification observations, or all
current-state evaluated transitions as appropriate. Verification uses the complete
internal observations rather than the public proof preview. A workflow with no
current-state transitions has no transition-based trigger in that retained evaluation.
Terminal reports remain distinct from both successful outcomes and trigger absence.

The live applicability field is an advisory read, is not stored, does not advance
checklist revisions, and is omitted from start/update responses. Human review decisions
can separately retain the observation reviewed as durable decision context.
They never close or reopen checklist items, replace frozen guidance, alter reviews,
change application-owned blockers/readiness, or infer business success. Existing
attached evidence is not revalidated by this trigger check. Refresh the case before
acting; the application continues to own verification, outcome interpretation and execution.

### Review changed or obsolete playbook work

Record a human coordination decision with
`POST /v1/apps/{slug}/workflow-readiness/playbook-progress/{id}/reviews` or its
`/v1/platform-tenant-self` equivalent, using the exact case query selectors without
pagination. Existing account write/MFA and customer operations-manage authorization
apply. Send `revision`, `decision` (`continue`, `retire` or `replace`) and a required
`reason` of at most 1,024 UTF-8 bytes without control characters. Reviewer identity,
server time and an applicability observation are recorded by Gregale, not the caller.

`continue` records the reviewed choice and keeps coordination active, including when
application evidence is unavailable; it is a human choice, not execution permission.
`retire` and `replace` permanently close source coordination and reject subsequent
item edits or review decisions with 409. Pending items, completion counts, notes,
evidence and pinned guidance remain intact. Neither closure nor an explicit decision
to continue asserts readiness, verification or business success. Existing records
with no `disposition` are treated as active.

For `replace`, first start an appropriate checklist from current matching guidance.
Then send its `replacement_progress_id` and `replacement_revision`. The target must
be distinct, active, applicable at the observation, and in the same exact case.
Its revision and active state are rechecked atomically with the source update under
the shared writer lock; concurrent retirement or editing produces a conflict. Closed
sources cannot later be replacement targets, preventing cycles. A replacement can
later itself be replaced; all earlier links remain retained. No item evidence or
completion is copied. Other decisions reject replacement parameters.

Both decisions and item edits advance the same checklist revision. The source stores
`latest_review` and the latest 32 `reviews`, with `reviews_truncated` when older choices
are pruned. Review observations are retained context; live applicability remains
separate, and application evidence is not transactionally locked with human writes.
Case overviews derive `replaces_progress_ids` from retained predecessor decisions;
these inverse links are not stored or accepted in requests. Dashboard controls show
closure, review context and links, and SDKs expose the review endpoint. Timeline
entries retain the decision, reason, reviewer, clock and replacement identity.

### Live evidence requirements for checklist items

A playbook can bind each `required_evidence` label to one optional
`evidence_requirements` selector. Selectors are frozen when a checklist starts:

```yaml
playbooks:
  - id: payment-review
    version: 1
    match_kind: blocker
    match_code: payment
    summary: Review payment before fulfillment.
    required_evidence:
      - Payment verified
    evidence_requirements:
      - label: Payment verified
        kind: prerequisite
        operation: fulfill-order
        from_state: paid
        to_state: fulfilled
        prerequisite_operation: capture-payment
        milestone: payment-captured
```

The transition and retained prerequisite must already exist in the contract.
`prerequisite` uses the exact current-state transition and producer/milestone;
optional `group_id` and `path_id` select an alternative group and named path.
The single fact uses existing freshness/invalidation policies. Its satisfaction
does not assert that the whole path or decision is satisfied. Unevaluated edges
are unavailable.

`milestone` needs `operation` and `milestone`, and checks the selected contract's
declared step and latest retained fact without adding age/invalidation policy.
`verification` needs `operation` and `blocker_code`, and requires every matching
obligation on the exact current report to have retained verification. Absence of
a current obligation or a verification observation from a different report is
unavailable, not successful verification. Fields from
other kinds are rejected; each selector uses a unique existing label, with the
existing 16-label limit. YAML and TOML support the same fields.

Case reads add `items[].evidence_check` for configured labels. Status is
`satisfied`, `missing`, `stale`, `invalidated` or `unavailable`, with a diagnostic
reason, observation clock and current report identity. Prerequisites include
existing freshness and invalidating-fact diagnostics. Stale state, changed
contract/guidance, incomplete verification coverage or missing declarations
cannot establish satisfaction. Completed human items in active checklists without satisfaction set
`review_suggested`; the dashboard shows this beside the preserved review and
links current milestone evidence.

These observations evaluate declared requirements, not the validity of attached
human references. They do not edit checklist status, revisions, history or
business state, and are absent from stored progress and mutation responses.
Reload the case to refresh them. Satisfaction means retained application-reported
evidence, not external correctness, business success or execution permission.
Existing playbooks without selectors remain human-only.

### Monitor checklist evidence in a durable inbox

Explicitly enroll a pinned checklist through
`POST /v1/apps/{slug}/workflow-readiness/playbook-progress/{id}/evidence-watch`
or its `/v1/platform-tenant-self/...` counterpart. Use the same exact case query
selectors as checklist edits, an existing readiness subscription in the same app,
environment and caller role, and the current human checklist revision:

```json
{
  "subscription_id": "00000000-0000-4000-8000-000000000001",
  "progress_revision": 2,
  "revision": 0,
  "enabled": true,
  "owner": "payments"
}
```

The example UUID is a placeholder; use your existing subscription ID. Enabling
requires an active checklist with live evidence selectors. A customer-scoped
subscription must match that customer; a declared subscription workflow must
match the checklist. Other readiness discovery filters do not constrain an
explicit checklist watch. Account writes require existing write scope and MFA;
customer writes use tenant operations-manage scope and authenticated identity.

The response provides a watch UUID, configuration revision and saved observations.
For subsequent configuration changes, include `watch_id`, the current watch
`revision`, the current `progress_revision`, and the desired `enabled` and `owner`.
Read `playbook_evidence_watches` from subscription lists or inbox metadata to get
current configuration. The dashboard's checklist controls load inbox destinations,
show monitoring status and enable or stop the selected watch. Polling does not
advance configuration revisions. Enabling or editing configuration captures a
new silent baseline; changes while disabled are not replayed.

Watches are durable in the subscription, independently of the latest-100 inbox
retention window. Each subscription retains at most 20 watches. New enrollment
may reclaim a stopped slot; a fresh watch UUID prevents old configuration tokens
from affecting a recreated watch. The existing worker rotates through five active
watches per pass, nominally every 30 seconds. This is bounded polling and can miss
short-lived states; failures retain previous observations and retry while healthy
watches can publish. Watch changes invalidate in-flight subscription leases.

A later requirement status or diagnostic-reason change produces one grouped
`playbook_evidence_changed` notification, including missing/stale/invalidated/
unavailable evidence, recovery to satisfied, and changed-contract/guidance reasons.
Observation clocks, report revisions, human completion flags and evidence-ID
renewal alone do not produce repeat notices. Each notice preserves the exact case,
pinned playbook identity and before/after requirement observations. New notices
are unread with the configured owner; older coordination is never reassigned.
The dashboard renders changed labels and can open the current workflow case.
Retained notices also appear in the exact case timeline. Manually scheduled
follow-ups keep the same case attribution in both stores.

Publication rechecks checklist identity, active disposition and observed human
revision under the shared writer lock. A prior retirement/replacement prevents
publication and stops monitoring; a concurrent item/review edit defers the poll
until another observation. Disabling or deleting the subscription also prevents
stale worker publication. Missing checklist records stop the watch. No automatic
item reopen, checklist completion, application action or external delivery occurs.
The notification is a saved observation, not a current transition-readiness
baseline; open the case for fresh requirements. Completion references on these
notices remain scoped to the notification's exact customer and subject.

No migration or new worker is required: existing JSON subscription and inbox
storage retain configurations and observations. Existing subscriptions without
explicit checklist watches preserve their behavior.

### Track the application result expected by a playbook

Playbooks may declare `expected_outcome_code`, using the same bounded lowercase
outcome slug as application reports (for example, `payment-confirmed`):

```yaml
playbooks:
  - id: payment-review
    version: 2
    match_kind: blocker
    match_code: payment
    summary: Confirm payment and review fulfillment.
    expected_outcome_code: payment-confirmed
    required_evidence:
      - Payment verified
```

The code is part of the immutable rule and definition fingerprint. Starting a
matched checklist freezes the expectation and original `start_state_revision`.
Later deployments do not change this goal. Live `business_outcome` appears beside
progress in case reads, independently of checklist completion and evidence
satisfaction. The dashboard links the result's reporting operation.

Status is `pending` for fresh comparable nonterminal state, `achieved` when an
explicit fresh terminal outcome equals the expectation, `different_outcome` for
another explicit terminal code, or `unavailable` when state is missing/stale,
the contract differs, retained state precedes the start revision, the pinned rule
is invalid, or terminal state has no outcome code. The nested `evidence` contains
the shared outcome evaluator's report identity, revision, clock, reported code
and explanation; its `unknown` maps to playbook `unavailable`. Diagnostic codes
under unavailable are not comparable business results. Changed guidance is shown
by applicability separately; it does not silently replace the frozen expectation.

The existing opt-in evidence-watch endpoint also accepts playbooks with only an
expected outcome and no live checklist selectors. Enabling captures a silent
result baseline. Subsequent result status/reason changes, or a different comparable
terminal outcome code, produce `playbook_outcome_changed` in the chosen durable
inbox. The notice may also contain simultaneous requirement changes. Clock,
report-counter and description-only updates stay quiet. Watches retain their
highest comparable `outcome_state_revision`, including across configuration edits
and unavailable observations, so pruning cannot make an older report establish a
new result. Current case reads compare with the start revision; watch observations
also compare with this retained latest-observation revision.

Retirement/replacement and stale human revisions use the same publication fences
as evidence notices. The worker reuses its scoped evidence read and introduces no
additional queries or polling phase. Result observations are ephemeral on progress
and durable only in watch baselines/inbox notices. Neither achieved results nor
human completion closes the other, and neither establishes that playbook work
caused the application's outcome. Legacy playbooks omit the expectation and keep
their existing behavior. No migration, external delivery or business action is
introduced.

### Declare a playbook business deadline and assigned follow-up

Add an optional `outcome_deadline` policy to a playbook that already declares an
`expected_outcome_code`:

```yaml
expected_outcome_code: payment-confirmed
outcome_deadline:
  after_seconds: 1800
  owner: payments
  follow_up_after_seconds: 900
  note: Review the payment result and retained application evidence.
```

Durations are whole seconds. The expected wait is 1 second to ten years; the
assigned follow-up delay is 1 second to one week. Owner is a required existing-style
team label (at most 256 bytes); note is optional plain text (at most 1024 bytes).
The policy enters the immutable rule fingerprint. Canonicalization copies the
policy so callers cannot mutate pinned guidance through their input pointer.
YAML/TOML and all SDKs expose the same fields.

Starting a checklist freezes `outcome_deadline_at` as its creation time plus
`after_seconds`. Deployment changes, item edits, coordination reviews, watch
configuration and enrollment do not reschedule it. Case results expose the due
clock and whether the current observation is at/past it; this does not prove when
an application result historically arrived or declare business failure.

Enable the existing opt-in checklist watch to receive deadline attention. The
first successful due poll records `deadline_checked_at`. If the expected result
is not currently observed as achieved, it publishes one
`playbook_outcome_deadline_reached` notice per watch. Pending, different-outcome
and unavailable evidence can require review; source read failures retain previous
observations and defer the check. The notice is unread and assigned to the frozen
policy owner, with the policy note and follow-up time equal to actual delivery
plus `follow_up_after_seconds`. This owner is independent of the ordinary watch
notification owner. Simultaneous evidence/result changes share the same notice.

An achieved result at the first due poll suppresses deadline escalation, including
later evidence loss. An already-achieved due enrollment/configuration observation
also records fulfillment after the retained-revision guard. A later unmet
observation is not retroactively declared a missed deadline. Disabled monitoring
sends nothing; re-enabling retains any previous due marker and escalation reference.
A new watch in another inbox or after a reclaimed stopped slot has its own identity
and first due check. There is no global escalation deduplication across explicit
inbox destinations.

Late result observations refresh `playbook_escalation.current` on the retained
assigned notification and keep its first `success_observed_at` flag, even through
later corrections/evidence loss. Its original `playbook_evidence.business_outcome`
and deadline are delivery evidence. Human owner, note, acknowledgment, reminders,
follow-up time, coordination revision and completion snapshots remain untouched.
The existing inbox/follow-up controls and reminder worker handle this assigned
work. Completion references retain exact-case customer/subject validation.

Deadline checks, baseline updates, assigned notice creation and retained evidence
refresh use the existing subscription lease and shared checklist-writer fences.
Retirement/replacement before publication stops monitoring; human revision drift
defers it. An escalation reference survives latest-100 inbox pruning; a pruned row
is not recreated and its lost evidence/history cannot be reconstructed. Refresh
ends when the watch stops or subscription is deleted. Dashboard, follow-up queues
and all SDKs show the deadline and separate current evidence/human coordination.
No migration, additional worker, external delivery or application action is added.

### Require acknowledgment of a playbook deadline escalation

A playbook's `outcome_deadline` can set `acknowledge_after_seconds` (1–604800 seconds) and an optional `fallback_owner`, distinct from `owner`. For example, use `owner: payments`, `acknowledge_after_seconds: 900`, and `fallback_owner: operations` to request acknowledgment within 15 minutes of the assigned escalation's actual delivery. Omission or zero disables this response target; a fallback requires a positive target. These fields are frozen with the playbook policy.

The existing subscription worker monitors the retained assigned inbox row independently of live business reports or checklist-watch retirement. It sends one `escalation_response_overdue` notice when the response deadline passes. Fallback assignment occurs only if the owner is still the initial team and no manual assignment history exists. It increments the coordination revision and records assignment history, preserving notes, scheduled follow-up time, reminders, and completion. Human acknowledgment or completion takes precedence under the publication lock. Clearing the follow-up closes response monitoring; rescheduling it does not reset the original acknowledgment clock.

The first acknowledgment fulfills the target even if someone later marks the notice unread. Business success neither acknowledges nor closes the assigned escalation. Inbox expiration or subscription deletion ends response monitoring. The dashboard and SDKs expose the response status, original target, first acknowledgment, overdue notice time, and bounded assignment history. An overdue event identifies either its readiness `source` or its exact scoped `playbook`; playbook events do not manufacture a readiness baseline.

### Record a playbook escalation's business resolution

Complete an assigned playbook escalation through the existing notification coordination endpoint with `completion.playbook_resolution`: `expected_outcome_achieved`, `alternative_accepted`, or `follow_up_canceled`. Use coordination `outcome: resolved` for the first two and `outcome: no_longer_applicable` for cancellation. Supply `note` and `evidence_operation_ids` (empty string and empty array are allowed in this mode). Optional evidence references must still belong to the exact account, application, scope, tenant and subject. This mode excludes caller-selected expected outcomes and new verification or outcome watches; the application's frozen escalation expectation is authoritative.

The server reads current application state for the exact source workflow and records `completion.playbook_outcome`, including the human resolution, application observation and alignment. Expected success matches only a fresh explicit expected terminal outcome. An accepted alternative matches only a fresh different terminal outcome permitted by any explicit pinned acceptance policy. Nonterminal or contrary results produce `mismatch`; missing, stale, older or different-contract reports produce `unavailable`. Cancellation records `not_applicable` and asserts no business result. These results inform the reviewer and do not block human closure. Storage or report-read failures fail the request, preserving the open follow-up.

The evidence is an advisory observation before the coordination write, not an atomic application-state assertion. Optimistic coordination revisions still guard concurrent human edits and fallback assignments. Closing stops reminders and acknowledgment monitoring; subsequent live escalation observations do not rewrite the immutable completion record. Inbox and timeline views retain the disposition and captured report identity. Legacy generic completion remains supported with its existing evidence and note requirements.

### Review business results after playbook completion

Business-aware playbook completion can set `playbook_review_after_seconds` from 1 to 604800 (up to seven days); omission or zero disables review. The window starts at the actual completion write. The source notification exposes `playbook_review`, with its frozen owner, expiration, status, highest comparable revision and latest observation. Its initial baseline is the immutable evidence captured at closure, so enrollment sends no notice.

The existing subscription worker rotates through retained reviews together with business outcome watches. Meaningful status, reason or comparable terminal-code changes produce a `playbook_outcome_changed` notice with `playbook_review_event`, linking the original closed notification and recording before/after evidence and the original human resolution. Clock changes, refreshed report identities and unchanged results stay quiet. A pending result becoming achieved, a reported result correction, evidence loss and recovery can each notify the frozen owner. Notifications do not reopen the original escalation or assert application execution authority.

Missing, stale, different-contract or older retained reports are recorded as unavailable. Infrastructure read failures preserve the prior observation and retry without manufacturing evidence loss. The highest comparable revision survives unavailable observations. Publication rechecks the exact review identity/revision and expiration under the existing lease and source row lock; expired reviews publish no further notices. No expiration notice is sent. Source inbox expiration or subscription deletion also ends review; stopped checklist watches do not. The original completion and evidence remain unchanged even when live observations change.

### Declare acceptable alternative business outcomes

A playbook can define `outcome_acceptance: {alternative_outcome_codes: [payment-refunded, payment-voided]}` alongside `expected_outcome_code: payment-confirmed`. Up to 16 distinct lowercase outcome slugs are allowed; the primary expected code cannot appear in the alternatives. Codes are copied and sorted during canonical validation. Explicit `alternative_outcome_codes: []` permits only the expected result. An absent policy preserves legacy reviewer acceptance of any different terminal result.

The policy is pinned with the checklist and retained on its business observations as `acceptance_policy`. Explicit policies classify fresh comparable reports as `expected`, `acceptable_alternative`, or `unexpected`; nonterminal and unavailable evidence are classified separately. The underlying business status remains `different_outcome` for any alternative. An allowed alternative does not satisfy the primary expected-outcome deadline or claim success.

Business-aware `alternative_accepted` completion matches only an explicitly allowed terminal alternative when a policy is present. Unexpected results produce a mismatch, while human closure remains allowed. Completion and post-completion reviews use the frozen policy, including through evidence loss and recovery; later deployments cannot silently rewrite the acceptance rules. Changes in reported terminal codes notify during the existing bounded review window, and the dashboard exposes application acceptance separately from the human resolution. Legacy saved closures are unchanged.

### Explain playbook business outcomes

A playbook can declare `outcome_guidance`, with up to 17 entries: one for its expected outcome and one for each explicitly acceptable alternative. Each entry has `outcome_code`, a required `description`, and an optional `suggested_follow_up`. For example, describe `payment-refunded` as “Payment returned to the customer” with a suggestion “Notify the customer and release inventory.” Guidance does not change acceptance, the primary outcome deadline, or application execution authority.

Codes must refer to the expected outcome or a code in the explicit `outcome_acceptance.alternative_outcome_codes` policy; duplicate or unknown codes are rejected. Text is trimmed UTF-8 with no control characters and at most 1024 bytes per field. Canonicalization copies and sorts entries by code, preserving caller input. Guidance is optional, and existing playbooks remain unchanged.

Descriptions and suggestions are pinned with the checklist version. Business observations retain the complete `outcome_guidance` set so later result changes use the same meanings, even after deployments or checklist-watch retirement. The `guidance` observation field selects only a fresh comparable reported terminal outcome. Pending, stale, missing, different-contract and older retained reports do not get matching current-outcome guidance. Cases show the declared meanings; escalations, immutable completion records and post-completion notices show matching guidance as literal text. No suggested follow-up is executed automatically.

### Track outcome-specific follow-up steps

Each `outcome_guidance` entry can declare an ordered `follow_up_steps` list, for example `[notify-customer, release-inventory]`. Up to 16 unique, nonempty plain-text labels are allowed per outcome; labels must be trimmed UTF-8, control-free, and at most 256 bytes. Order is preserved, copied and pinned with the original checklist. Steps are optional and never executed by Gregale.

Use the existing playbook progress update endpoint with optional `outcome_code` to review a step in that outcome's list. `item_index` is local to the selected list. Omit the selector to retain the original checklist behavior. On the first successful review, the server requires a fresh comparable terminal report of that exact outcome and saves its advisory pre-write observation. The original guidance must have declared steps for that outcome. Subsequent reviews keep that original report and guidance, even if application results change.

`outcome_follow_ups` on progress records contains each started list, its original observation, reviewed items, completed count and checklist-complete flag. Notes are required, and completed steps require scoped operation or milestone evidence using the existing reference validation. The original progress revision fences all edits; shared bounded review history records the outcome selector and timeline entries distinguish outcome-step reviews. Original evidence checklist counts do not include outcome steps. Retiring or replacing progress makes both types of review read-only; historical work stays visible.

The case dashboard shows declared steps before work begins and permits first review only when that outcome is freshly observed. A state change between the displayed case and submission can require refresh. First-review evidence is observed before the coordination write and does not make an atomic claim about application state. Step completion records human review, not proof of application execution, workflow readiness or business success.

### Assign outcome follow-up teams and deadlines

Outcome guidance can add `follow_up_assignments`, for example `[{label: notify-customer, owner: support, complete_after_seconds: 3600}]`. Labels must select declared `follow_up_steps` and be unique within the outcome. Owner labels are required, trimmed, control-free UTF-8 up to 256 bytes; windows range from 1 to 604800 seconds. At most 16 assigned steps are allowed across the entire playbook. Canonicalization copies and sorts assignment policies while preserving step order.

The first successful list review pins every configured step's `owner` and `due_at`; each deadline starts at that review, independent of later deployment, item review, or inbox delivery. The existing step update accepts optional `owner` with `outcome_code`: omission preserves ownership and an empty string clears it. Reviews and ownership changes share the original progress revision and bounded review history.

Enable a playbook evidence watch to deliver pending assigned steps into its durable inbox as `playbook_outcome_step_assigned` notices. Existing follow-up queues show the team, original deadline, step and outcome. Existing due reminders mark overdue work once per scheduled deadline. Delivery is at most once per step and watch identity; saved references survive disabling, re-enabling and inbox pruning. Different explicitly configured watches can deliver to different destinations. Completed steps are not newly assigned.

Successful, revision-fenced polls synchronize step status and checklist ownership into retained notices. A manual inbox ownership change preserves that queue override even if the checklist assignment changes later. Notes, acknowledgment, human inbox completion and manual scheduling remain untouched. Human step completion clears its queued follow-up on the next accepted poll without manufacturing an inbox completion record. Reopening a step does not reset its deadline or automatically reschedule a previously cleared queue item; operators can reschedule the retained notice. Queue ownership history is bounded to 16 changes, alongside the original step review history.

Watch disablement, retirement or replacement stops checklist synchronization; retained queue entries remain under human coordination and reminders can continue until cleared. Infrastructure read failures retain assignments and retry. As with other watches, publication rechecks progress identity/revision and the subscription lease. No application action is executed by assignment, reminders or human review.

### Require acknowledgment of an assigned outcome step

Step assignment policies can set `acknowledge_after_seconds` (1–604800 seconds) and optional `fallback_owner`, for example `acknowledge_after_seconds: 900` and `fallback_owner: operations`. The fallback must be a distinct trimmed control-free UTF-8 team label, at most 256 bytes, and requires a positive window. Omission or zero disables the target. Configuration is pinned with the playbook; the acknowledgment clock starts when the queue assignment is delivered, independently of its original completion deadline.

Assigned step notices expose the same retained `response` model as escalations: target, first acknowledgment, missed-target notice time and bounded assignment history. The existing response worker emits one `escalation_response_overdue` notice and applies fallback once only when queue ownership still matches its delivery-time owner and no ownership changes were recorded. A fallback protects the queue assignment from later automatic checklist synchronization. Manual queue assignments, notes, reminders, scheduling and human completion are preserved.

Before publication, both backends refresh active checklist ownership and completed-step state under their existing writer locks, using retained progress without consulting application business reports. This lets concurrent checklist reassignment or completion take precedence even when business evidence fails. Checklist and inbox ownership changes prevent fallback, including changes later reverted to the original owner. First acknowledgment fulfills the target even after a later unread edit; completion or clearing follow-up closes it. Rescheduling does not reset the acknowledgment clock. Completion of a checklist step closes its response target when synchronized, without claiming business success or fabricating inbox completion.

Response checks can continue after a watch is disabled while the assigned notice remains retained. They refresh active source progress for the response target; general watch synchronization still follows the watch lifetime. Missing or retired progress does not manufacture acknowledgment or completion. Retention or subscription deletion ends monitoring. New response policies do not retrofit legacy assignment notices.

### Sequence outcome follow-up work with prerequisites

Outcome guidance can add `follow_up_dependencies`, for example `[{label: release-inventory, prerequisites: [confirm-refund]}]` alongside its declared follow-up steps. Each dependent label is unique and may name up to 15 unique prerequisites in the same outcome list. Unknown labels, self-dependencies, duplicate edges and cycles are rejected. Canonicalization copies and sorts the graph without reordering the declared steps. Omission preserves independent-step behavior.

The case exposes ephemeral step `readiness` with `ready` or `waiting`, direct `waiting_for` labels and `review_suggested`. A prerequisite must be reviewed complete and its own prerequisite chain satisfied. The update endpoint rejects completing a waiting step with a conflict, while pending reviews and ownership edits remain possible. Review, note and scoped evidence requirements still apply; this is human work sequencing, not application transition authorization.

Independent assigned steps start their completion window on first list review as before. Dependent assigned steps receive their immutable `due_at` when they first become ready during a successful review write. Opted-in watches publish their queue assignment only while ready. This prevents waiting work from consuming its completion window before prerequisites finish. A dependent's acknowledgment clock still starts at its eventual queue delivery.

Reopening a prerequisite makes downstream readiness waiting transitively. Completed reviews are preserved and flagged for review; they are never silently erased. Existing assignments, owner overrides, acknowledgment targets and deadlines remain unchanged. A previously queued waiting step remains visible with its blockers, and operators can use existing rescheduling controls. Regaining readiness neither recreates an assignment nor restarts its deadline. Case readiness is recomputed; retained queue readiness refreshes on accepted polls. Legacy playbooks without dependencies keep their existing timing and review behavior.

### Notify owners when follow-up prerequisites regress

Enabled playbook evidence watches now retain `dependency_states` for started outcome follow-up lists. Enabling or re-enabling saves a silent current baseline, including steps already waiting; a newly observed list also acquires its baseline silently. Successful accepted polls emit `playbook_dependency_regressed` when downstream work changes from ready to waiting, gains an unmet prerequisite, or is affected by another newly reopened ancestor while already waiting.

Each `dependency_regression` notice groups affected steps by their current checklist owner, using the watch owner for unassigned steps. It records the observation time, relevant reopened prerequisites (including transitive causes), affected outcome/step labels and completed-step review suggestions. The notice links to the exact existing workflow case through its playbook evidence identity. Inbox ownership overrides remain separate and are not rewritten. Notices have no automatic follow-up deadline and do not create duplicate step queue assignments.

The baseline and notice are committed together under the existing subscription lease and exact progress revision checks. Stale polls, retired/replaced progress and infrastructure failures do not advance the baseline or invent regressions. Unchanged waiting states, notes, ownership changes and clock refreshes stay quiet. Recovery advances the baseline, allowing a later distinct regression to notify again; saved regression references also enable linked recovery notices. A sampled change that is reopened and resolved entirely between polls is not reconstructed; this is retained observation monitoring, not a complete review audit.

Existing step reviews, human inbox coordination, assignments and all clocks remain intact. The inbox and timeline show regression context, while the current case supplies the latest prerequisite state. Delivery remains scoped to explicit watches and bounded by inbox retention.

### Notify owners when regressed dependencies recover

Watches now retain the latest delivered regression reference on affected `dependency_states`. A successful accepted waiting-to-ready observation with that reference emits `playbook_dependency_recovered`, grouped by current checklist owner and linked regression notice. Initial prerequisite completion without a delivered regression stays quiet; newly eligible assignments still use the existing queue publisher.

The `dependency_recovery` payload records the observation time, regression notification UUID/sequence/creation time, and recovered steps. Partial recovery reports only the ready subset. Pending steps can resume; previously completed reviews remain completed. Different steps may link different regression notices if additional blockers were observed while already waiting. A current owner change routes the recovery to that owner, using the watch owner when a step is unassigned; inbox ownership overrides remain separate.

Reference clearing, recovery delivery and the new baseline are atomic under the existing progress revision and subscription lease fences. Repeated ready observations do not duplicate notices. Errors or stale polls retain waiting references. Edits to an already active watch preserve the prior dependency baseline so routing changes cannot consume an undelivered recovery. Re-enabling stays silent: still-waiting steps keep their known regression links, while steps already ready at re-enrollment do not produce retroactive recovery notices. Historical regressions without saved references are not backfilled. A referenced notice may be outside inbox retention; recovery keeps the saved identity without reconstructing missing rows.

The inbox links the prior regression and the current case; timeline entries identify the recovered work and prior sequence. No existing notice is auto-acknowledged or completed, and no review, assignment, original deadline or acknowledgment target is reset. Changes fully reopened and resolved between polls remain outside this sampled observation model.

### Reviewing completed steps after prerequisite regression

Reopening an outcome follow-up prerequisite durably marks every completed downstream step with `dependency_review_revision`, in the same revision-fenced write. The prior completion, note and evidence remain intact. Restoring prerequisites does not clear this requirement. Unreviewed work does not count toward follow-up checklist completion and cannot satisfy downstream prerequisites.

Use the existing progress update endpoint with `outcome_code`, `item_index` and the current progress revision. Mark the step `completed` with an explanation and scoped evidence to confirm the work remains valid, after its prerequisites are satisfied; mark it `pending` with an explanation to request a redo. Both decisions clear its review obligation and retain the preceding review in history. Repeated upstream regressions renew the obligation at the latest progress revision. Owners and original deadlines are preserved.

### Assigned dependency validity reviews

An enabled playbook evidence watch delivers a durable inbox task for each observed `dependency_review_revision`. It routes to the checklist step owner, falling back to the watch owner, and enters the follow-up queue with immediate attention unless a pinned review window is declared. The task captures the prior completion note, actor and scoped evidence references; it also captures the reopened ancestor label if the corresponding progress history entry is retained. Recovery alone leaves the task open.

Review the exact workflow case to confirm validity with a note and evidence, or mark the step pending for redo. On the next accepted watch poll, the task leaves the follow-up queue and records `confirmed` or `redo_requested`. A newer unresolved regression supersedes the preceding episode and delivers a new task. Resolution uses the first retained source review after the obligation; if that history is unavailable, the old episode is marked superseded without claiming a specific human decision. Stable polls, recovery, watch configuration changes and disable/re-enable do not duplicate the same episode. Manual inbox ownership overrides remain protected. Inbox acknowledgment does not validate checklist work.

Delivery and source synchronization require an active watch and retained inbox rows. A regression reviewed before any poll does not create a retroactive task. A stopped watch does not auto-resolve retained tasks. Each watch retains up to 272 review delivery references; use another subscription/watch for additional episodes after that limit. Notification retention can remove older tasks without deleting the durable checklist obligation.

### Dependency review deadlines and fallback ownership

Applications may add `review_after_seconds` (1–604800) and optional `review_fallback_owner` to a frozen outcome `follow_up_assignments` policy. The fallback must differ from the declared step owner; it requires a positive review window. Omitted or zero windows preserve immediate review attention without automatic review escalation.

```yaml
follow_up_assignments:
  - label: Verify downstream delivery
    owner: fulfillment
    complete_after_seconds: 3600
    review_after_seconds: 1800
    review_fallback_owner: operations
```

When a prerequisite reopens, the server stores `dependency_review_due_at` on the affected completed step in the same revision-fenced write as its obligation. This clock starts at reopening, not watch delivery; recovery, watch reconfiguration, acknowledgment and inbox rescheduling cannot extend it. Each new regression creates a new episode with its own deadline, independent of the step's original completion deadline. Existing obligations are not retroactively assigned new clocks.

Active watches deliver the review task with its pinned follow-up time and deadline target. The retained-inbox response worker rechecks exact active progress before escalation, even if the delivery watch is disabled, without requiring fresh application reports. An unresolved target emits one `escalation_response_overdue` notice. It transfers the task to the fallback only if the original owner is unchanged, assignment history is complete and empty, and the task has not been manually completed. Source owner changes, manual routing and automatic fallback remain recorded and protected.

Inbox acknowledgment, manual completion or clearing/rescheduling inbox attention do not validate the checklist and do not satisfy this review target. A source confirmation, redo request or superseding episode closes it. Notification retention and inactive source progress retain their existing limits; retired progress does not manufacture a review decision.

### Business impact of unresolved validity reviews

Applications can add `review_impact` to a frozen outcome follow-up assignment policy. It declares `kind` (`milestone` or `outcome`), a result `code`, a required business-risk `description`, and `priority` (`normal`, `high` or `critical`). Outcome targets must be the pinned expected outcome or an explicitly acceptable alternative. Milestone targets use workflow milestone slugs; the declaration itself does not establish milestone existence or completion. Descriptions are trimmed, control-free UTF-8 text limited to 1024 bytes.

```yaml
follow_up_assignments:
  - label: Verify downstream delivery
    owner: fulfillment
    complete_after_seconds: 3600
    review_after_seconds: 1800
    review_fallback_owner: operations
    review_impact:
      kind: milestone
      code: delivery_confirmed
      description: Confirm that the delivery remains valid after payment verification reopens.
      priority: critical
```

The impact is copied from the pinned policy when the outcome follow-up list is first enrolled and preserved through subsequent reviews. Review inbox tasks retain the same declaration alongside prior completion evidence, effective inbox owner and review deadline. Case views group unresolved validity obligations by protected business result, show contributing steps, their declared risk, checklist owner and deadline, and list critical results first. The highest declared priority represents a group containing several review steps. Effective inbox ownership may differ after fallback or manual routing.

These groups describe human confidence awaiting review, independently of fresh or unavailable application outcome reports. They disappear after explicit validity confirmation or redo requests clear the obligations; redeployments, prerequisite recovery, acknowledgment and inbox retention cannot silently revalidate the work. Existing playbooks without impact declarations retain their current behavior. Priority changes presentation only and does not authorize execution, alter deadlines or replace the reported business outcome.

### Triage follow-ups by business impact

Account and tenant-self follow-up queues accept `impact_priority` (`normal`, `high`, `critical`), `impact_kind` (`milestone`, `outcome`) and `impact_code`. Code filtering requires a kind so milestone and outcome names cannot be confused. These filters match the pinned impact of retained, unresolved dependency validity review tasks; ordinary follow-ups and resolved review episodes do not match. They compose with existing environment, customer, owner, workflow, due and reminder filters.

Use `sort=business_impact` to order overdue follow-ups first, then unresolved review priority (critical, high, normal, undeclared), then follow-up time and stable subscription/notification identity. The default `sort=due` preserves existing due-time ordering. For example, append `impact_priority=critical&impact_kind=milestone&impact_code=delivery_confirmed&sort=business_impact` to a scoped follow-up queue request to find critical reviews protecting that milestone. The dashboard exposes the same controls.

Filtering, counts and ordering happen before live pagination through the shared memory/PostgreSQL projection. Results remain limited to retained inbox notifications and the existing bounded candidate window; live pages can move after edits or watch updates. Due filters and ordering use the current inbox follow-up schedule. Pinned validity review deadlines remain visible and authoritative for review escalation even if an operator reschedules inbox attention. Effective inbox ownership remains visible alongside declared business impact.

### Business impact summaries across workflow cases

Follow-up queue responses now include optional `business_impact_summary`, computed across the full matching retained candidate window before pagination. Groups use workflow plus protected result kind and code, so identical milestone names in different workflows remain distinct. Existing environment, customer, workflow, owner, due, reminder and impact filters apply to the summary.

Each group reports unique unresolved reviews, retained inbox tasks, affected workflow cases, critical and high-priority reviews, overdue reviews and critical overdue reviews. A review is identified by its progress ID, outcome list, step index and regression revision; multiple subscriptions delivering that obligation count once as a review. A case uses tenant, subject and workflow instance identity rather than playbook or inbox identity. A unique review is overdue if any matching delivery has overdue queue attention.

Owner breakdowns count actual queue tasks using effective inbox ownership, including manual changes and fallback teams. They can therefore total more than the unique review count. Groups put critical overdue reviews first, then critical reviews and review volume, with stable workflow/result ties. Owners sort by overdue tasks, task volume and label. The dashboard shows these counts and offers group/owner drill-down controls that load the matching filtered queue while retaining the other active selectors.

These summaries reflect retained unresolved human confidence work, not application business failure. Notification retention, disabled watchers, live source synchronization and the 2000-candidate queue limit still apply. Changing pages does not narrow the summary to visible rows. Queue attention dates retain their current coordination meaning, independently of pinned review escalation deadlines.

### Live watches for protected business results

Subscription creation accepts up to 16 unique `business_impact_watches`, each declaring a workflow, result `kind` and `code`, and optional notification `owner`. Watches share the subscription's application, customer and environment scope; transition operation/owner discovery filters do not narrow impact sampling. Configuration is fixed until the subscription is deleted and recreated. The dashboard offers one optional watch during subscription creation; SDKs support the full list.

```json
{"name":"delivery-impact","scope":"prod","business_impact_watches":[{"workflow":"fulfillment","kind":"milestone","code":"delivery_confirmed","owner":"operations"}]}
```

The initial successful observation is silent. Later zero-to-positive critical or overdue review transitions emit one durable `business_impact_changed` inbox notification, with before/after summaries and reasons `critical_review_added` or `review_became_overdue`. Stable counts, owner changes, priority changes that keep critical counts positive, and repeated polling do not duplicate those notices. Once a condition returns to zero, a later first occurrence can notify again. Notices include a control to open the matching review queue and preserve advisory sample times rather than asserting application business success.

A `reviews_cleared` reason requires both an empty matching retained review queue and source confirmation that every tracked obligation is resolved. Clearing inbox attention, manual inbox completion or pruning notifications cannot manufacture this transition. The worker reads exact scoped progress for tracked cases and also checks for ongoing matching review obligations in those retained case rows. Missing progress or unresolved source work preserves the prior baseline and marks `coverage_incomplete`. Infrastructure read errors retry without advancing the affected watch baseline.

Monitoring is sampled and retained: changes between polls can be missed; newly queued playbook work is observed on a subsequent poll. Each watch tracks up to 64 distinct source obligations per active review episode. Exceeding this bound or the 2000-candidate queue window prevents a clearance assertion for that episode. Group baselines and notifications publish together behind the subscription lease, and deleting the subscription invalidates pending publication. Clearance covers tracked retained reviews, not unseen cases or application execution.

### Acknowledgment targets for business-impact alerts

A business-impact watch can declare `acknowledge_after_seconds` (1–604800) and optional `fallback_owner`. A positive target requires a nonempty notification `owner`; the fallback must be distinct and requires a positive target. Omitted or zero targets preserve existing informational notices. The dashboard exposes these fields during subscription creation, and SDKs accept them on watch definitions.

Critical-review and overdue-review alerts receive a frozen response deadline starting at notification delivery. Clearance notices do not receive acknowledgment targets. Targeted alerts enter the existing follow-up/reminder queue with their protected-result context, effective owner and deadline; they have no invented individual subject or application transition. Alert follow-ups do not contribute to underlying review impact counts or match review-impact filters, avoiding feedback loops.

The retained-inbox response worker emits one `escalation_response_overdue` notice after a missed deadline and transfers ownership to the fallback only when original ownership is unchanged and assignment history is complete and empty. Manual routing, including reverting an owner change, prevents automatic takeover. An acknowledgment satisfies the response target even when the notification is later marked unread; it does not clear source review obligations or change business outcomes. Source review clearance does not automatically acknowledge a previously delivered alert.

Manual completion or clearing inbox follow-up attention closes the target under the existing coordination rules. Rescheduling attention does not extend the original acknowledgment deadline. Acknowledgment alone leaves the scheduled follow-up under human control; clear or reschedule it as appropriate. Escalation evaluates retained coordination without needing current business reports and preserves notification retention, subscription deletion and polling limits.

### Owning the response to a business-impact alert

The existing notification coordination update accepts optional `review_response` on an original critical/overdue `business_impact_changed` alert. Declare a responsible `owner`, nonblank `plan`, `status` (`active` or `resolved`) and a future `check_in_at` for active work. On first creation, provide 1–16 `reviews` references containing source `subscription_id` and `sequence`. Each must identify an authorized retained dependency review task in the same application/environment and match the alert's protected workflow, result category and code. A customer-scoped alert cannot link a different customer's case.

Saving a response acknowledges the alert and assigns inbox attention to the responsible team at the check-in time. This fulfills the alert response target but does not validate the underlying checklist work. The response records authenticated creation/update actors and times plus the latest 16 owner/plan/status/check-in changes. Plans permit line breaks and tabs within the 1024-byte UTF-8 bound. The dashboard offers an affected-review picker and can claim/edit the response in the original inbox; the follow-up queue shows its plan, check-in and linked review identities.

Review links are fixed after creation. Omit them on later edits, or supply exactly the existing set; dropping or replacing links is rejected. The server captures exact source progress/case identities and the original obligation revision, so a response can be resolved after linked notification rows are pruned if its source progress remains available. The parent alert and response remain subject to inbox retention. Account responses can include captured source customer IDs; customer responses omit those IDs and derive identity from authentication.

Set `status=resolved` and omit the check-in only after every linked source step has cleared its dependency review obligation through a validity confirmation or redo decision. Missing progress or a currently reopened linked step rejects resolution. A successful resolution clears response queue attention and records `resolution_checked_at`, an advisory pre-write source observation rather than execution authority. Later regressions do not rewrite historical closure; an explicit response reopen requires a future check-in and retains the fixed links.

The existing coordination revision fence protects plan edits and reassignments. While a response is active, generic completion, owner changes or clearing/rescheduling attention without an explicit response update are rejected, so accountability cannot silently disappear. Use the response form to make those changes. Alert acknowledgment, response disposition and application business success retain separate meanings.

### Missed check-ins for active review responses

Review response updates accept optional `fallback_owner`: omit it to preserve the existing policy, send an empty string to clear it, or declare a different team. When an active check-in is first scheduled or explicitly rescheduled, the server records its initial responsible team and starts a new check-in period. Human owner changes within that period prevent automatic takeover, including changes that later revert to the original owner.

The existing retained-inbox worker checks active responses independently of application reports and impact discovery. A missed check-in produces one durable `review_response_check_in_overdue` notice with the original deadline, plan, fixed review links and ownership snapshot. It transfers both response responsibility and inbox ownership only when a distinct fallback exists, the initial response owner is known and unchanged, coordination ownership still agrees, and the coordination revision is writable. The plan, review links and missed check-in time are preserved; the source response remains active and overdue until an explicit human update.

The notification marker, fallback and notice publish together under the subscription lease and notification lock. Source resolution or a newly scheduled check-in cancels a stale read-phase publication. Stable overdue polls do not duplicate notices. Rescheduling starts a new period and permits one later missed-check-in notice. Resolving stops checks. An older response window without a recorded initial owner can notify but cannot automatically transfer until explicitly rescheduled.

Both human edits and system missed-check-in events appear in the bounded response history. The dashboard exposes fallback policy and links overdue notices to the original response. Existing due-reminder markers remain available for queue attention; this feature adds accountable handoff and an inbox notice without treating acknowledgment or business outcome changes as response resolution. Parent inbox retention, subscription deletion and bounded periodic evaluation retain their existing limits.


Active response plans also sample their fixed linked reviews before check-ins. The first successful sample establishes a silent baseline. New blocked prerequisites, unsatisfied application-declared checklist evidence requirements, and retired, replaced or unavailable progress produce a durable `review_response_blocked` inbox notice routed to the current response owner. Notices link back to the original plan and identify new blockers by linked review position. Stable blockers stay quiet; a blocker that clears and later returns can notify again. Resolution stops sampling. Plan edits and ownership changes fence stale publication; baseline and notice commit together.

Sampling rotates through at most five retained active plans per subscription pass and reads only their scoped cases. Source read failures retry without advancing that plan's baseline. Baselines are bounded by retained active inbox plans. Evidence checks cover pinned, declared live checklist requirements across the linked playbook, not human attachments or a guarantee that a particular follow-up cannot execute. Observations are advisory samples: they do not change the plan, deadline, review disposition, ownership or application business result, and may miss changes between samples.


Response plans also deliver `review_response_recovered` notices to the current owner when previously observed blockers are positively verified as cleared. Fresh active source progress must expose the linked step before prerequisite or source-availability recovery can be reported; declared checklist evidence must explicitly become satisfied. Missing progress, read errors, absent evidence checks and changed failure reasons cannot establish recovery. Unverified disappearances remain in the baseline for later verification. Partial recovery lists only cleared blockers, and simultaneous new blockers produce a separate warning. The first sample stays silent, stable recovery does not repeat, and notices link to the original plan without resolving it or changing its check-in.


Response records expose a `health` summary in inbox and follow-up responses, with matching SDK types and dashboard rendering. The bounded worker samples active plans and reports `unavailable`, `blocked`, `review_required` or `clear` in that precedence order. It includes remaining blockers (including unverified disappearances), the most recent verified recoveries, and each fixed linked review's latest attempt and last available observation. Returned blockers are removed from the recovery list. No summary is present before the first attempt. Source errors publish unavailable health without advancing blocker baselines or emitting recovery notices; backend error details are not exposed. The last complete check means all scoped reads completed, even if a source was missing or retired.

Health updates preserve human response timestamps, history and coordination revision. Human edits retain the latest observation; resolution freezes it as historical context. Clear observations do not prove workflow completion or business success, and source observations may change between polls. Existing owner and check-in fields remain the accountability record.


Existing active response plans accept optional `decision` intent (`continue`, `revise`, `pause`) with a nonblank reason and the `health_fingerprint` the operator reviewed. The server captures authenticated actor, responsible team, decision time and an immutable health snapshot. Latest decisions remain on the response; explicit decisions also appear in its bounded 16-entry human history. Saving without a decision preserves the latest decision and does not duplicate it into history. Decisions use the existing owner, plan and future check-in fields; pause leaves the response active and monitored. No decision executes, authorizes or stops application work.

Health fingerprints include sampled progress and application state revisions, source availability, blockers and validity obligations, excluding polling clocks and recovery-display history. Legacy observations without a fingerprint must refresh first. Decision preparation re-reads scoped linked sources and rejects a changed fingerprint; publication also checks the stored health digest under the notification lock and existing coordination revision fence. These are advisory pre-write source samples, not an atomic lock on application state: a later or concurrent application report can still change conditions after sampling. The dashboard requests refreshed health when a decision conflicts. Resolving responses remains a separate explicit action requiring source-review clearance.


Active responses with an explicit decision now latch `decision_review` when sampled health differs from that decision's reviewed fingerprint. The existing bounded health worker atomically records the first changed condition and one `review_response_decision_review_required` notification under its subscription lease and source lock. The notice goes to the response's current responsible team and captures the prior immutable decision, changed health and differences in blockers, availability, progress or application-state revisions and review obligations. Read failures can require revalidation without establishing business failure. Polling timestamps alone do not trigger the alert.

Subsequent condition changes and a return to the original condition remain quiet for that decision; live health continues updating. Acknowledgment, ordinary plan edits, owner changes and rescheduling preserve the review flag. A new explicit decision against freshly checked health clears it and starts a new episode. Resolution stops monitoring and retains the flag as historical context. Inbox and queue views show the requirement and link to the response decision form. Original intent, check-ins, human audit history, source review obligations and application execution are not changed automatically. Notifications and response context remain subject to existing inbox retention and sampled observations.


Decision revalidation uses a response-level `decision_review_after_seconds` window, defaulting to 24 hours and configurable from 60 seconds to seven days. Each newly required review pins `due_at` from its original `required_at`; later policy changes affect future review episodes. Legacy flags without deadlines derive and persist a deadline from their original requirement time and effective policy. Human edits preserve the pinned episode before changing its policy.

The retained-inbox worker independently rotates through at most five pending review deadlines per subscription pass, without application-state reads. An active episode that passes its deadline receives exactly one `review_response_decision_review_overdue` notice routed to the current response owner; the deadline and overdue marker commit together under source and subscription locks. A new explicit decision or response resolution wins against stale publication. Acknowledgment, attention edits, owner changes, a rescheduled check-in and condition recovery do not satisfy the review. New decisions reset the episode; resolution ends monitoring and retains historical context.

Follow-up queues expose `attention_due_at`, the earlier of the existing check-in and active review deadline, and use it for due filters, counts and ordering before pagination. Coordination's original follow-up timestamp remains the human check-in, so existing acknowledgment and edit requests preserve their contract. Review deadline alerts are separate from check-in reminders. Dashboard views show both dates and the immutable overdue marker. These are retained, bounded advisory workflows; application execution remains application-owned.


Responses accept an optional `decision_review_fallback_owner` for future revalidation episodes. Request omission preserves it, explicit empty string clears the future policy, and a provided nonempty team must be distinct from the submitted owner and satisfy existing owner-label limits. A newly required review pins its initial owner and fallback alongside its deadline. Existing or legacy episodes do not gain a retroactive fallback; unknown initial ownership prevents automatic reassignment.

At the one-time overdue evaluation, a distinct pinned fallback takes over both response and inbox ownership only if the initial owner remains unchanged, no ownership-change flag was recorded, coordination still agrees, no completion exists, and its revision can advance. Human ownership moves (including a move away and back) and automatic check-in handoffs latch protection independently of retained history. A decision-review handoff also protects the existing check-in period from another automatic takeover. The response history records `decision_review_overdue`, and the episode and alert preserve previous/effective owners and whether reassignment occurred. Failed guards still publish the overdue alert to the current owner. Plans, check-ins, deadlines, immutable decisions and validity review obligations remain intact. A new explicit decision starts a fresh episode; ordinary future-policy edits do not rewrite an active episode.


Handed-off reviews expose explicit acceptance through the existing response update: `handoff_acceptance` supplies the exact episode decision and requirement timestamps plus a nonblank note. The response must stay active, retain the handed-off owner in both response and coordination, and commit a future check-in. Acceptance and a new decision cannot be combined. Preparation and locked publication reject stale episodes, ownership changes, resolved responses and repeated acceptance. Existing account/tenant authorization applies; the authenticated actor records acceptance for the team label rather than proving team membership.

The episode stores immutable accepting owner, actor, time, note and committed check-in. Human history records `handoff_accepted` and retains the acceptance snapshot even when a later explicit decision supersedes the episode, within the existing 16-entry history bound. Ordinary edits retain the original acceptance; later check-in edits or ownership moves do not rewrite it. Dashboard inbox and queue views distinguish a current handoff awaiting acceptance from accepted or historical handoffs. Acknowledgment alone is insufficient, and acceptance does not clear decision review, reset its deadline or overdue marker, verify business completion, or execute application work.


Handoff acceptance uses `handoff_accept_after_seconds`, defaulting to one hour and configurable from one minute to seven days. The actual overdue transfer pins `acceptance_due_at` from the handoff timestamp. Future policy changes and check-in rescheduling do not extend it. Legacy reassigned episodes with a known handoff timestamp derive and persist a deadline from that original timestamp and effective policy; unknown handoff time cannot invent an acceptance clock. Ordinary edits pin legacy deadlines before changing future policy.

An independent retained-inbox cursor checks at most five pending handoffs per subscription pass without reading application state. An active handoff still owned by its assigned team and lacking explicit acceptance receives one `review_response_handoff_acceptance_overdue` alert when its pinned window expires. Publication atomically stores the overdue marker with the notice and rechecks decision/requirement/handoff timestamps, deadline, owner agreement and acceptance under source/subscription locks. Acceptance, a new decision, resolution or an ownership move wins against stale publication. Notification acknowledgment never counts as acceptance. Alerts do not reassign ownership or alter the original decision-review deadline, plan, check-in or business execution. Late acceptance remains allowed and retains its overdue history.

Dashboard inbox and follow-up views show time awaiting acceptance, the pinned acceptance deadline and overdue alert time alongside the original review deadline. Existing queue urgency remains based on the earlier check-in or review deadline; acceptance does not remove an unresolved review from the overdue queue. Monitoring and historical context remain subject to retained inbox bounds.


A current unaccepted handoff can be explicitly declined with matching decision/requirement timestamps and a nonblank reason. Existing account/tenant authorization applies; an authenticated operator records the decline for the responsible team label without proving team membership. Preparation and locked publication reject accepted, already declined, stale, resolved or differently owned episodes. Decline cannot combine with acceptance or a new decision and must retain the owner, plan and exact saved check-in, even if overdue. It does not extend either deadline or automatically return ownership to the previous team.

The episode latches `reassignment_required` and stores immutable decline owner, previous owner, actor, time and reason. History records `handoff_declined`. Acceptance and its reminder clock stop for that declined episode. Manual ownership reassignment clears the routing flag; a new decision supersedes the episode, and retained decline history remains available for audit. The original decision-review obligation remains until a new decision or explicit resolution.

An independent bounded cursor publishes one `review_response_handoff_declined` notice to the captured previous owner and commits its notification marker with the source under the existing subscription lease and notification locks. It scans retained decline history so immediate reassignment, resolution or a new decision does not discard the notification intent. Notifications are historical and link to current ownership for manual routing; no automatic reassignment occurs. Original inbox pruning or the existing 16-entry history bound can limit delivery. Dashboard forms distinguish decline from acceptance and show pending manual reassignment.


A declined handoff requiring routing now accepts explicit `handoff_reassignment` with matching decision, requirement and decline timestamps, a reason, and a different responsible team in the parent owner field. Authorized operators retain the exact plan and check-in (including an overdue one), and cannot combine routing with a decision, acceptance or another decline. Preparation and locked publication recheck the current declining owner and episode. The existing owner-label authorization model applies without proof of team membership.

Reassignment atomically transfers response and coordination ownership, preserves original review requirement/deadline and immutable decline history, and starts a fresh acceptance window at its own `handed_off_at`. The old acceptance-overdue marker is replaced for the new route; the new team is not automatically accepted. Acceptance and decline must supply the current handoff timestamp after manual routing, preventing old route requests from applying to a later route. Legacy automatic handoffs can omit the timestamp. The decision-review overdue marker remains historical and is not restarted. Current routing clears the manual-reassignment flag; a new decline can require another explicit route.

A bounded independent history cursor publishes one `review_response_handoff_reassigned` notification to the new team with reason and prior decline context, committing its marker with the source under existing locks and lease. Later decision changes or immediate re-decline do not discard retained notification intent; history and inbox bounds can still limit delivery. Dashboard forms offer explicit reassignment, show the new handoff clock and required acceptance, and preserve audit records. Plain owner edits remain available but do not create a new handoff/acceptance episode. Application execution and business outcomes remain application-owned.


Future handoffs can configure `handoff_acceptance_escalation_owner`: a control-free team label, at most 256 UTF-8 bytes, distinct from the explicitly submitted response owner. Omission preserves policy and an empty string clears it. Each automatic or manual handoff pins the destination in `acceptance_escalation_owner`; later policy edits do not reroute that handoff. Legacy handoffs without a pinned destination receive their existing overdue notice only.

When an active, unaccepted and undeclined handoff misses its acceptance deadline, the existing bounded deadline worker atomically records `acceptance_deadline_notified_at` and, for a distinct pinned team, `acceptance_escalated_at`, publishing both the assigned-team reminder and one `review_response_handoff_acceptance_escalated` inbox notice. The escalation includes the decision, original review deadline, current route, reason and prior decline snapshot when manually routed. Acceptance, decline, resolution, ownership changes or superseding decisions are rechecked under the existing locks before publishing. No ownership transfer or deadline extension occurs. A destination equal to the current owner is deduplicated to the ordinary notice. A fresh explicit handoff resets escalation for its own acceptance window. Dashboard controls configure future policy and show the pinned destination and historical delivery marker.


An active, escalated and still-unaccepted handoff can record one explicit `escalation_acknowledgment` through the original response. Supply matching decision, requirement, handoff and escalation timestamps, a coordinator label, action plan and future `due_at` within seven days. The parent response must retain its exact owner, plan and check-in, including an overdue check-in. Acknowledgment cannot combine with a decision, acceptance, decline or reassignment; notification acknowledgment alone creates no commitment. Preparation and locked publication validate the episode and reject duplicate commitments. Existing operator authorization applies; coordinator labels do not prove team membership.

The immutable record captures coordinator, plan, deadline, actor and server time in the current review and `escalation_acknowledged` history. An independent bounded cursor publishes one `review_response_escalation_commitment_overdue` notice to the coordinator if the deadline passes while this handoff remains escalated and unaccepted. Final publication rechecks coordination revision, decision and route identity, acknowledgment and deadline. Acceptance, decline, resolution, owner changes or a superseding decision suppress follow-up; a new manual route resets the current commitment while retaining history. The delivery marker is committed with the notice and mirrored in retained history without changing human update times. History and inbox retention bounds still apply. Acknowledgment never transfers response ownership, extends original deadlines, accepts the handoff or satisfies decision revalidation. Dashboard controls distinguish the commitment from acceptance and show the action deadline and delivery marker.


Explicit `escalation_completion` closes one retained coordinator commitment on an active response. Supply exact decision, requirement, handoff, escalation and acknowledgment timestamps, a nonblank action summary (UTF-8, at most 1024 bytes) and one to eight distinct zero-based `review_indices` from the current response. The server copies those saved references into immutable evidence, recording actor and time; it does not infer fresh application verification or business success. Parent owner, plan and exact check-in are preserved, even when overdue, and parent reviews must be omitted. Completion cannot combine with acknowledgment, decision, acceptance, decline or reassignment.

Preparation and locked publication reject stale episodes, changed ownership and duplicate completion. Work can still be recorded after acceptance or decline for the same retained episode; a new route, decision or resolved response supersedes it. The current acknowledgment and its retained history entry receive the completion record, and a separate `escalation_commitment_completed` history entry preserves the audit. Pending follow-up publication rechecks completion under existing locks, so closing the commitment prevents a stale deadline alert. Previously published overdue markers and notices remain historical. Ownership, handoff acceptance, review obligations and their deadlines are unchanged. Dashboard forms distinguish completing coordinator work from resolving the response and link the saved evidence.


Explicit `escalation_action_review` observes only the current completion's immutable evidence references using the existing scoped playbook observation logic. Exact episode and completion timestamps, original owner/plan/check-in and omitted parent reviews fence the request. The recorded actor/time and health snapshot distinguish clear, blocked, review-required and unavailable evidence; source indices refer to completion evidence order. Read failures and unavailable backend produce unavailable observations, never success. Cancellation aborts publication. This is a scoped observation, not proof of causality or business completion, and application state can change after the read. The current completion keeps the latest review and retained `escalation_action_reviewed` history preserves prior snapshots.

When the handoff is still unaccepted and undeclined and its completed action has a recorded non-clear review, explicit `escalation_revision` can create a revised coordinator commitment. It supplies the recorded review timestamp, coordinator, revised plan and future deadline within seven days. Preparation reads completion evidence again and rejects newly clear conditions; locked publication fences current episode, completion and prior review. The new commitment records the fresh basis and previous completion time, resets its own follow-up delivery marker, and appends `escalation_commitment_revised` history while retaining the previous completed work. Repeated revisions form bounded audit history rather than nested chains. Ownership, acceptance and original review/check-in deadlines remain unchanged. All response actions are mutually exclusive. Existing notification and history retention limits apply.


Coordinator completion can opt into one scheduled evidence check with `review_after_seconds` (60–604800 seconds). Server completion time pins `review_due_at`; omission leaves checks manual. An independent timeout and bounded rotation read at most five due retained actions per pass using the same scoped observation as manual reviews. Read failures produce unavailable observations; timeout never infers clear evidence. The check occurs on the next successful worker evaluation at or after its due time, rather than at an exact wall-clock instant.

Locked publication rechecks coordination revision, decision, handoff, escalation, acknowledgment, completion and due time, and current ownership. Resolved responses, superseding decisions/routes or revised commitments suppress an obsolete check. Acceptance or decline alone still permits observation of completed work in the same retained episode. Ordinary plan edits and manual reviews do not consume or postpone the schedule. The worker commits its `scheduled_review_at` marker, fresh evidence snapshot and one `review_response_escalation_action_checked` notice to the captured coordinator together, mirroring retained completion history and recording a system review history entry without changing human update timestamps. Notifications show clear, blocked, review-required or unavailable evidence and never infer causality or business success. A revised commitment remains an explicit operator action; the scheduled check does not change ownership, acceptance or original deadlines. Inbox pruning and bounded history still limit retained evidence.


Explicit `scheduled_review_cancellation` cancels one pending scheduled check on the current completed coordinator action. Supply matching episode, acknowledgment, completion and due timestamps and a nonblank UTF-8 reason (at most 1024 bytes, line breaks and tabs allowed). Parent owner, plan and exact check-in remain unchanged, including an overdue check-in; parent reviews must be omitted. All response actions remain mutually exclusive. The existing authorized operator model applies; cancellation does not require reading application sources.

Preparation and locked publication require an active retained episode, matching current ownership, a scheduled due time and no published or canceled check. Due or in-flight checks can still be canceled before publication. The immutable audit captures original completion/due times, actor, server time and reason in the completion, mirrored history and a `scheduled_review_canceled` change. The worker checks cancellation again under the same lock before committing observation, delivery marker and notice. If cancellation wins, no scheduled result is published; if publication wins, cancellation returns conflict and preserves that historical result. Manual evidence reviews remain available. Cancellation changes neither coordinator completion, evidence, ownership, handoff acceptance nor review deadlines, and cannot restart the schedule. Superseding or resolved episodes reject stale cancellation; history and inbox retention limits still apply. Dashboard forms distinguish a canceled schedule from a published check and retain the cancellation audit.


Explicit `scheduled_review_reschedule` moves a pending evidence check to a distinct future time within seven days. Supply exact current episode/completion timestamps, the current `due_at`, replacement `new_due_at` and a nonblank UTF-8 reason (at most 1024 bytes, line breaks and tabs allowed). Preserve parent owner, plan and exact check-in, even when overdue; omit parent reviews and do not combine other response actions. Published, canceled, superseded or resolved checks reject rescheduling. Due or in-flight checks can be moved before publication without reading application sources.

Preparation and locked publication fence current ownership, coordination revision, completion and due time. The completion retains `original_review_due_at`, the current `review_due_at` and immutable latest `review_reschedule`; each `scheduled_review_rescheduled` history entry records completion identity, previous and replacement due times, actor, server time and reason. Legacy pending checks pin their existing due time as original on first reschedule. Repeated reschedules preserve the original even after bounded history pruning. Matching completion/acknowledgment history is mirrored. The worker's existing revision and due-time checks reject observations captured against an older schedule, even if a later change returns to the same due time. If publication wins first, rescheduling conflicts and preserves that result. Ordinary edits and manual reviews still cannot move the schedule. Cancellation uses the current replacement due time. Completed work, evidence, ownership, handoff acceptance and original review/check-in deadlines remain unchanged. Dashboard forms show current and original schedule times and the change audit.


The exact workflow case overview now returns `next_actions`, `next_action_count` and `next_actions_truncated`. It projects current source follow-ups before the existing follow-up page cap, matching aggregate response plans through their immutable linked review identities (customer, subject, workflow and instance). Matching completion evidence is filtered to the case, and `shared_plan` identifies actions whose source also spans other cases. It reads no additional inboxes and never derives work from historical delivery snapshots.

Rows bring together response check-ins, decision revalidation, unaccepted or declined handoffs, escalation acknowledgment, incomplete coordinator commitments, pending scheduled evidence checks and optional evidence-review/revision suggestions, plus exact-case prerequisite reviews and other follow-ups. Each carries a responsible label, its own deadline/overdue flag, a human/automatic/suggested trigger and source subscription/sequence; available linked review references support inspection. Resolved plans, fulfilled commitments and canceled/published scheduled checks do not recreate completed obligations. Suggested revision requires a current non-clear completion review and an unaccepted, undeclined handoff.

Actions are sorted overdue first, then dated work by due time, with deterministic source/kind ties. The response caps at 100 action rows; count covers retained matching rows before the cap rather than global tasks or workflow count. Existing coordination retention, independent-read and authorization limitations remain. The dashboard shows the unified list, distinguishes scheduled work from human obligations and suggestions, and links to source action controls and case evidence. It does not perform or authorize application actions and an empty retained list does not establish business completion. Go, Node and Python SDKs expose the same projection.


The readiness work-queue endpoint now supports `view=next_actions` with owner, workflow, customer, due, `action_kind` and `action_trigger` selectors. It reuses the exact-case action projection over retained source records without additional case/evidence reads. Shared source actions are deduplicated by subscription, sequence and kind; affected matching cases and evidence are combined, and `shared_plan` warns that source controls can affect other cases. Account customer selectors match linked case identities rather than aggregate notification ownership. Platform scopes remain implicit and omit customer identifiers.

Owner and overdue/upcoming filters use each action's owner and own deadline, after projection; undated items appear only in all-due views. Rows distinguish human obligations, automatic checks and optional suggestions and sort overdue first, then due time and stable source/kind ties. Pagination/counts describe matching action rows, not workflow cases; next-action pages use `next_actions` and leave `items` empty. Offset is bounded by the existing retained source candidate limit times the eleven action kinds (22000); follow-up view keeps its existing 2000 bound. Reminder and business-impact selectors are rejected in action view, while action selectors require that view. Existing follow-up behavior remains the default when view is omitted. Retention and independent live-read limitations remain; live edits can move pages. The dashboard defaults its work-queue selector to next actions, links to existing source controls and evidence, and retains the follow-up/reminder view. Go, Node and Python SDKs expose query selectors and queue rows.


The dashboard next-action queue now supports up to 20 named team-view presets per browser/application context. Presets store only validated filter strings (environment, customer, owner, workflow, action kind/trigger, due selector and page size), never task snapshots, pagination offsets, credentials or application results. Saving an existing name updates its filters; deletion removes only that browser preset. Browser storage can be unavailable, in which case sharing still works. These presets are browser-local rather than server-managed team configuration.

A shareable/bookmarkable `#readiness-work?…` fragment opens the same next-action filters in the existing application dashboard, then performs a fresh authenticated queue read from offset zero. Links carry the application identity, reject a mismatched application context, and do not grant access or alter the account/application endpoint. Unsupported, duplicate, oversized or invalid filter choices are rejected before use, and the backend retains normal query and authorization validation. Shared views can be saved under a local name. Task edits and saved-filter edits remain separate; stale in-flight reads are discarded when a view changes. Other tabs refresh their preset selector on browser storage changes. Filter names and labels are rendered as text; links include filter metadata, so share them only with intended recipients. Existing live-pagination and retained-coordination limitations still apply.


Readiness subscriptions can configure immutable `next_action_digest` policy with a required exact owner/team label, optional workflow/action-kind/trigger selectors, cadence (60–604800 seconds, default 3600) and approaching-deadline window (60–604800 seconds, default 86400). Owner is both action filter and inbox recipient; existing operator authorization applies without proving team membership. Subscription customer/environment scope is inherited while transition discovery filters remain independent. The first digest evaluation is due one cadence after creation. Policy changes require a new subscription; deleting one removes its inbox and fingerprint tracking.

A separate worker timeout reads retained source work once and projects the same cross-case queue. Overdue items, deadlines through the approaching window and unaccepted handoffs qualify; undated suggestions do not. It tracks fingerprints by source subscription/sequence/action kind, including owner, summary, deadline/overdue phase, case identities and immutable review links, excluding polling times. Stable delivered items are not repeated. Newly qualifying, changed, newly overdue or reappearing work can notify. At most 20 changed rows enter one `next_action_digest` notice; undelivered changed rows remain eligible at later cadences. Tracking is bounded by retained queue candidates and retired when work no longer qualifies. Empty/unchanged evaluations are silent and advance cadence; read failures preserve the due time and mark retrying. Evaluation/delivery times and next due time are exposed on the subscription.

Digest fingerprints, cadence state and the unread notice are saved together under existing subscription leases and persistence transactions. Source records are read independently, so digest items are historical snapshots and can change before publication; source links require inspection before action. No source ownership, deadline, acceptance, commitment or business outcome is changed. Digest deliveries are not projected back into source work, preventing notification feedback. Counts cover retained qualifying actions rather than all global tasks, and source/inbox pruning still limits coverage. Dashboard controls configure cadence and filters and render grouped action links; Go, Node and Python SDKs expose policy, delivery metadata and payloads.

Digest entries include source-keyed explanations: overdue, approaching deadline, and/or waiting for handoff acceptance; changed projected fields compared with the last delivered snapshot; and advisory next steps pointing to existing source controls. Snapshots are saved atomically with delivery, not on undelivered evaluations. Existing fingerprint-only records report unavailable history on their next changed delivery; older notices remain readable without explanations. Retired qualifying items lose comparison history, so later re-entry is a first retained delivery. Evidence changes compare linked references, not live evidence health or hidden escalation state. Team filtering can hide reassignment to another owner; absence never proves completion. Explanations grant no execution permission and never mutate source work.

Checking a digest notification reconciles at most 20 saved entries through retained source inbox reads using the original application, environment and customer boundary. Current action projection preserves the subscription workflow filter and omits the digest owner and deadline filters, so reassignment and postponed deadlines remain visible. Results distinguish outstanding, changed, explicitly completed source follow-ups, no longer outstanding, and unavailable. Resolved source coordination returns its immutable completion receipt; canceled or otherwise vanished action projections never imply completion. Reads are independent snapshots and preserve the delivered digest. Expired/deleted/missing or inaccessible source records are unavailable; unexpected backend failures fail the check. The dashboard performs this check when opening a digest and offers refresh. No state writes, ownership changes, execution decisions, or live evidence-health claims occur.

Next actions now carry deterministic obligation identities from source subscription, sequence, kind and persisted epoch timestamps (and dependency-review revision where available). Review requirements, handoff attempts, coordinator commitments and completion evidence checks remain distinct across repeated cycles. Deadline edits and owner changes preserve the epoch; a new response check-in deadline defines a new check-in obligation. Digest deduplication includes identity. Reconciliation distinguishes a replaced obligation from changed details and exposes only lifecycle receipts anchored to the saved epoch. Coordinator completion receipts match acknowledgment time; scheduled check cancellation/reschedule/review receipts match completed-action time; current handoff receipts match the handoff epoch; reassignment receipts match the declined-at epoch. Unanchored historical acceptance/decline entries are omitted. Retained receipts can establish an old obligation's completion even while a newer action is outstanding. Receipt coverage is partial and limited by source history; legacy actions without identity cannot be assigned an epoch retroactively. Source-level resolved closure remains distinct from precise per-obligation lifecycle evidence. No workflow mutation or execution permission is introduced.

Source mutations now save obligation-linked receipts in the same notification JSON as the source update, under its existing lock/transaction. Human coordination writes, leased worker source updates, playbook assignment synchronization and new notification publication share capture logic. New obligations receive assignments; ownership/deadline/plan changes receive linked receipts; removed obligations record matched completion/cancellation or an explicit supersession without asserting fulfillment. A new explicit response decision can fulfill the exact prior latched review. Digest reconciliation prefers retained ID-linked receipts across later source cycles. Each source retains at most 128 receipts with a persistent truncation flag; legacy history is not backfilled, deleted sources remain unavailable, and bounded lifecycle coverage is always explicit. Receipt capture adds no destinations, application authorization or source execution.

The exact workflow case includes an obligation history view grouped by source subscription, sequence and obligation identity. New receipts retain affected case identities at mutation time, including shared plans; history selection uses those saved identities rather than the source plan's current reviews. The case page reuses the retained inbox scan already used for its timeline, including closed source follow-ups, then joins current actions from the independent next-action projection. At most 100 most-recent obligation groups and 40 latest events per group are shown, with counts, timestamps, caps/pruning indicators and bounded-history warnings. Customer views omit tenant identifiers and unrelated case references; operator views require the exact selected customer. Legacy unscoped receipts are excluded, deleted/pruned sources are unavailable, and no current action never proves completion. Existing source controls are linked, with shared-source warnings; no source state or application authorization changes.

Mutation-time receipts now record paired same-source predecessor/successor links. Same-kind removed/new obligations and explicit handoff reassignment, reviewed coordinator revision, or decision-to-check-in transitions can link when their saved case coverage overlaps. A later cycle may link the most recent retained terminal same-kind predecessor. Existing identities reactivated later are not linked again, preventing cyclic lineage. Links retain a recorded decision/reassignment reason when changed in this write, an explicit revised-commitment context, or a clearly labeled mechanical new-cycle explanation; succession never proves business causation or fulfillment. Edge receipts carry only intersecting case coverage, so case views do not expose relationships to unrelated cases. The case history can expand and jump to a related retained obligation; missing/pruned groups remain explicitly unavailable. Links share the source write transaction and existing receipt caps; legacy history is not reconstructed.

Digest lifecycle results include successor edges only when saved edge coverage matches a delivered case and customer identity; unrelated shared-source edges are omitted.

Outstanding case obligations now have coordination assessments alongside a shared current workflow-requirement context. Acceptance, human routing, scheduled worker checks and evidence reviews are distinguished; fresh reports enable review while missing/stale reports remain explicit. Free-form response/check-in and coordinator plans remain fulfillment-unassessed because no transition binding is declared. Workflow context reuses the existing exact-case instance readiness, including missing milestones, prerequisites, policies, blockers and verifications, with links to named dependency cases. Unknown/stale evidence cannot produce a positive transition verdict; partial transition/report/verification coverage remains explicit. Blocking transition requirements do not automatically block human review or ownership decisions, and overdue work remains independently dated. The view performs no new source reads, infers no dependencies from history and grants no application execution permission.

Free-form response check-ins, coordinator commitments and generic follow-ups can now bind an exact obligation cycle/case to a declared current-state transition and pinned contract version. The case page lets users select a declared transition or explicitly clear a binding with a reason. The existing revision-checked inbox PATCH prepares the binding using fresh exact-case evidence and verifies the active source obligation under its write lock. Binding-only writes preserve status, owner, note and deadline and cannot combine with plan/completion changes. Source coordination retains at most 32 case-specific active-cycle bindings; unrelated edits preserve them and new cycles never inherit them. Obsolete cycles can be pruned during later binding writes; their bounded receipt snapshots remain available. Binding changes and clear reasons are captured atomically in case-scoped history. Current assessments distinguish satisfied target requirements, unmet prerequisites and unavailable/changed/stale declarations, exposing the exact readiness evidence. Observations and persistence are independent; binding never authorizes execution, establishes fulfillment, or automatically closes work.

Bound obligation targets are observed automatically by the readiness subscription worker, rotating through at most five retained sources per pass. The first observation establishes a baseline. Later changes between waiting, requirements satisfied and target unavailable create an owner-addressed durable inbox notice with the exact case, cycle, target and observation time. Unchanged observations produce no notice; cleared or retired bindings stop producing notices. Read failures retain the previous observation for retry rather than asserting recovery. Source revision and binding validation fence concurrent edits, and observations commit atomically with delivery. Notices are retained advisory evidence: inspect the current case before acting. Requirements satisfied never completes an obligation or authorizes or executes its transition.

Bound obligations support explicit fulfillment through the revision-checked source inbox PATCH. Supply the full saved binding, a reason, and a reporting operation/report ID. The server verifies that the exact case and obligation cycle remain active, that the binding is unchanged, and that the retained report belongs to the same application, environment, customer and subject. Its operation name, workflow/instance, from/to states and contract version must match the target, and it must be published after binding; metadata-only reports and readiness alone cannot fulfill work. Lookup scans at most 1000 exact-case reports. Evidence is checked independently before the locked source write and retained with the human actor/time and responsible owner; it does not assert current business success. Fulfillment removes only that case/cycle from current next actions and target monitoring, preserving other cases and source plan controls. New cycles remain outstanding. Case history shows the immutable receipt and evidence; saved digest checks count a cycle completed only when all delivered cases have fulfillment receipts. Source coordination keeps up to 128 immutable receipts and rejects further fulfillment at capacity instead of pruning into reopened work. Fulfillment cannot be combined with plan, source completion or binding edits and never authorizes or executes application transitions.

Fulfilled bound obligations can be explicitly reopened with the full saved binding, the exact latest fulfillment time, and a reason through the source inbox PATCH. The source revision and current unsuppressed action projection must still match that case/cycle; cleared or changed bindings, superseded cycles, closed sources and removed cases reject reopening. Other action edits cannot be combined, and owner, status, note and deadline are preserved. The immutable reopening receipt records the actor, time, current responsible owner and original fulfillment identity; original receipts and evidence remain retained. Only this case/cycle returns to next actions and target observation. Re-fulfillment requires matching application evidence published after the latest reopening. Case history exposes reopening controls and receipts, and saved digest checks consult the latest fulfillment/reopening state for each delivered case. The worker rotates through five retained sources and publishes at most 20 pending owner notices per pass, committing delivery checkpoints atomically with durable notices; source revision and receipt validation fence stale observations. Notices retain the reopening observation and link to current case/source controls; subsequent source changes are not hidden. At most 128 reopenings are retained per source; capacity exhaustion rejects further reopening instead of losing prior decisions. Application evidence never automatically reopens obligations or authorizes execution.

The readiness worker now reviews the latest retained non-reopened fulfillment for each exact case/cycle, rotating through five cases per pass even on large shared plans. It checks the original proof or latest reaffirmation against retained operation/report coverage, fresh current state and contract version, and explicitly named resolution/verification evidence. Missing reports or stale/unavailable current state, contract changes, reopened named resolutions and unavailable formerly verified proof flag human review; ordinary workflow progression is not invalidation. Each exact-case report lookup scans at most 1000 reports; outside-coverage evidence is unavailable, not proven deleted. Unexpected read errors retain prior observations for retry. Reasons remain latched even after evidence recovers, and semantic fingerprints suppress unchanged owner notices. Review writes, notices and source-scoped receipts commit together under lease/source revision checks; outdated bases cannot overwrite later reaffirmation or reopening. Fulfillment remains intact and no current task is automatically reopened.

Case history exposes current retained review flags and explicit reaffirmation controls alongside reopening. Reaffirmation requires the original binding/fulfillment identity, saved review fingerprint, reason and fresh full application state report published after review became required. The report must belong to the same application, scope, customer and subject, attest the exact workflow/instance under the fresh current contract, and pass a separate current-evidence observation before the revision-checked write. The server retains the original fulfillment and all immutable reaffirmations, clears only the matching flag, and monitors the new proof thereafter. At most 128 reaffirmations are retained per source; exhausted capacity rejects writes without pruning decisions. Case history and digest lifecycle views filter these receipts to their saved case coverage. Closed source plans can still receive evidence reaffirmation, while reopening continues to require an active source cycle. These are human review receipts, not application execution permission or assertions of current business success.

Pending fulfillment reviews are independent human work items in case and team next-action queues. Their identities include the exact saved case, fulfillment, evidence basis and first-required time; the original obligation remains fulfilled. Owners, latched reasons, evidence-check time and case links expose reaffirm/reopen controls. Reviews have no inferred deadline and qualify for team digests even when undated or their source plan is closed. Digest comparison ignores poll timestamps and ordinary report revisions, while reasons, owner and semantic review changes remain deliverable. Multiple reviews on shared sources retain separate identities and explanations, with customer case boundaries preserved. Each source contributes at most 128 reviews plus ordinary actions; the bounded next-action offset is 278000. Case pages retain their 100-row display cap. Explicit reaffirmation completes the exact review cycle; reopening cancels that review while restoring original work. Closed source coordination alone never counts as completing a saved review. Recovered evidence remains pending until an explicit decision.

Pending fulfillment reviews can be assigned and scheduled independently through `review_coordination`. Supply the review work-item ID, saved review fingerprint, source coordination revision, owner and reason while preserving the source's status, owner, note and follow-up deadline. An empty review owner explicitly unassigns it; omitting the review deadline clears it. The worker preserves these choices for that pending review cycle. New review cycles start without a manually chosen owner or deadline. Dated reviews participate in overdue and due-soon queues/digests; undated reviews remain eligible for review digests. Immutable changes retain the previous and new owner/deadline, actor and reason (up to 128 per source). Stale, resolved or superseded reviews reject changes. Original work ownership and fulfillment receipts remain intact.

An active readiness subscription now delivers `fulfillment_review_overdue` inbox notices for missed explicit review deadlines. A separate cursor rotates at most five overdue reviews per pass, independently of evidence observation and discovery filters. Under the source/subscription locks it rechecks the exact review cycle, saved fingerprint, deadline and active fulfillment; reaffirmation, reopening, supersession and changed deadlines suppress stale work. The notice routes to the current review owner (or remains unassigned) and includes the saved case, reasons and evidence context with explicit reaffirm/reopen controls. Publication and `fulfillment_review_reminders` receipts commit atomically. Each review ID/deadline is delivered once across retries, restarts and restoring an already notified deadline; different deadlines and new review cycles are eligible independently. History retains delivery time, notification ID and saved review owner/context. Reminder delivery does not change the semantic review fingerprint or mark work complete. Coverage remains limited to retained inbox sources and active subscriptions; each source retains at most 128 immutable reminder receipts and stops publishing further reminders at capacity.

Fulfillment review coordination supports optional `escalation_owner` and `escalate_after_seconds` (1–604800 seconds). The team must differ from the review owner and requires an explicit deadline. Omitted policy fields preserve the policy when the reviewer stays the same and a deadline remains, including rescheduling; empty escalation owner or zero grace clears it. Reassignment or removing the deadline clears policy when both fields are omitted. Changes record prior/new policy with the assignment reason and actor; worker observations preserve policy for that pending review cycle, and new cycles start without it.

The escalation grace begins at durable overdue-reminder delivery, so late worker runs do not shorten the response window. A separate bounded cursor checks at most five eligible reviews per pass. Final source/subscription locks recheck review identity, fingerprint, current owner, deadline, policy, reminder receipt and grace expiry before atomically saving one `fulfillment_review_escalated` inbox notice and immutable `fulfillment_review_escalations` receipt. Reassignment after the reminder permanently cancels its escalation window even if reverted; choose a new deadline for a new window. Reaffirmation, reopening, supersession and rescheduling suppress stale delivery. Policy changes may defer or redirect an unsent escalation but never repeat a delivered escalation for the same review ID/deadline. Destination notices retain original reviewer, case, reasons and evidence context; they do not transfer ownership or change fulfillment. Case/digest history includes destination, triggering reminder, scheduled escalation time and delivery time. Coverage depends on retained sources and active subscriptions, with 128 immutable escalation receipts per source; at capacity further escalation delivery stops.
