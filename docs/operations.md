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

Authorize the order before this call and recheck it under the business row lock. The SDK snapshots payloads, validates the batch against the pinned contract before commit, and stores each milestone with its stable UUID and occurrence time alongside the business write and receipt. After commit it publishes and acknowledges pending facts. Schema validation failure rolls back all three. Publication failure throws `OperationMilestonePublicationError` with `committed = true` and preserves the outbox. Reconcile and authorize recovery of the same Operation; receipt replay publishes pending facts without repeating the callback. A lost publication acknowledgement deduplicates on the platform.

Read retained facts using `GregaleOperationClient.milestones(operationID)` or `businessMilestones({appID, scope, subjectType, subjectID})`. The generated Go and Python clients expose the equivalent Operation and business-reference feeds, plus fenced validation/publication for application-owned outbox integrations. Account operators can inspect them with:

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
      - {from: fulfillment-in-progress, to: completed}
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

`state_stale_after` optionally maps active states to Go duration strings, such as `30m` or `2h`. Each threshold must be between one second and ten years, in whole seconds. Gregale pins the threshold and returns `stale`, `stale_after_seconds`, and the app-reported `occurred_at` with each current state. Age is measured from `occurred_at`, so delayed publication does not extend or shorten the state deadline. Business-reference reads accept `stale_only=true` to return only stale entries in `workflow_states`; milestone facts and state history remain unchanged. The dashboard exposes this filter after a business reference is selected, and the CLI provides `--stale-only`. Gregale marks a run for attention and leaves state changes and recovery to the application. See [ADR-724](adr/724-customer-operation-workflow-stale-state-detection.md).

The destination state must be declared by the pinned workflow, and `workflowTransition` must use a declared edge. The application checks the source state against its business row while holding the row lock. The Node SDK also compares `from_state` with the latest state saved on the per-run counter; a mismatch aborts the business transaction. The counter lock serializes this check across Operations, and the saved state survives cleanup of older outbox rows. Installing the current `customerOperationReceiptSchema` adds this state field and backfills it from retained reports. The first reported transition can establish the report history, so the application still checks its business row as the source of truth. Gregale validates the declared edge before the transaction commits. The SDK saves the report in its durable outbox and publishes it after commit under the current execution fence. Recovery republishes pending reports without rerunning the business callback; publication errors identify a committed transaction. Gregale keeps the newest revision, so a delayed publication cannot replace a newer state. Business-reference reads return the latest explicitly reported state alongside milestone facts, and the dashboard displays it with its update time and revision. No state appears until the application reports one, and Gregale does not derive state from milestones or execution status. See [ADR-719](adr/719-customer-operation-workflow-states.md), [ADR-720](adr/720-customer-operation-workflow-transitions.md), [ADR-722](adr/722-customer-operation-workflow-transition-continuity.md), and [ADR-723](adr/723-customer-operation-workflow-terminal-states.md).

Read the retained changes for one run by adding the same `workflow` and `workflow_instance_id` pair to a business-reference read. The response includes `workflow_state_history`, ordered by the app-assigned revision, with the prior and new state, application occurrence time, platform publication time, and the Operation that published the report. `workflow_state_cursor` paginates this history independently of the milestone `cursor`; both cursors remain bound to the business reference and ownership filters. The Node client accepts `workflowStateCursor`, Go uses `WorkflowStateCursor`, generated Python accepts `workflow_state_cursor`, and the CLI accepts `--workflow-state-cursor`. The dashboard links each observed run to its state history and links each change to its Operation. Expired settled Operations are omitted according to their normal result retention. See [ADR-721](adr/721-customer-operation-workflow-state-history.md).
