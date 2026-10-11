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
import { GregaleOperations } from '@gregale/sdk-node';

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
request isolation through `faas_sdk.operations_runtime.GregaleOperations`.
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
one-hour window. Each admission reads the file again. Missing, unreadable,
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
admission verification and rollback.

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
[]faas.OperationWorkflowBlocker{...})`; Python uses
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

Go accepts resolution facts as variadic `faas.OperationWorkflowBlockerResolution`
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
err := tx.WorkflowDependencies("fulfillment", orderID, "waiting", []faas.OperationWorkflowDependency{
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

Go provides `tx.BusinessDecision(name, faas.OperationBusinessDecision{...})`;
Python provides `tx.business_decision(name, OperationBusinessDecision(...))`
with the model from `faas_sdk.business_decisions`. Node exports
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
`OperationWorkflowReconciliationInput` from `faas_sdk.workflow_reconciliation`;
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

Go exposes `tx.ReportBusinessInvariant(milestone, faas.OperationBusinessInvariant,
currentBlockers)` and returns updated blockers plus an error. Python exposes
`tx.report_business_invariant(milestone, OperationBusinessInvariant(...), current)`
with the model from `faas_sdk.business_invariants`. Node also exports
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
`faas.OperationBusinessInvariantPayload{Kind: "gregale.business-invariant.v1",
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
`tx.BusinessEffect(name, faas.OperationBusinessEffect)` and its payload type.
Python exposes `tx.business_effect(name, OperationBusinessEffect(...))`, with the
model from `faas_sdk.business_effects`; `effect.to_payload()` supplies a fact to
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
Go exposes `tx.BusinessCompensation(name, faas.OperationBusinessCompensation)`
and `OperationBusinessEffectReference`; Python exposes
`tx.business_compensation(name, OperationBusinessCompensation(...))`, with models
from `faas_sdk.business_compensation`. Node exports `businessCompensationPayload`;
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
