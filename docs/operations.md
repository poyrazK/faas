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
