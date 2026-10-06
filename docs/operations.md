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

## Contract

`OperationDefinitionSpec` captures a named HTTP target, platform-tenant ownership,
JSON input/output schemas, progress stages, a completion webhook, and recovery
policy. Schema validation accepts bundled local references and rejects external
resources. Canonical JSON supplies stable input identity, rejects duplicate
members, and treats equivalent number spellings consistently.

Result artifacts declare a managed object reference, exact byte count, and
SHA-256. Attachment verifies the source object's ownership, scope, byte count
and digest, then creates a private retained copy. Downloads verify that copy
again and never fall back to a mutable customer object. Cleanup uses durable
receipts, including abandoned copies from interrupted uploads.

Uncertain effects require reconciliation by default. `safe_retry` is an explicit
declaration that repeating the handler is safe; it does not establish exactly-once
external effects. Confirmed results and delivery receipts have independent
lifecycles.

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

The browser-safe Node `GregaleOperationClient` accepts a credential callback,
refreshes it for requests and stream reconnects, and resumes from durable event
cursors. Go, Node and Python expose typed HTTP contracts. The Go download client
also verifies length and digest and refuses redirects carrying credentials.

Use `GregaleOperationClient.list({appID, scope})` to rebuild the customer's work
list after sign-in, then open an ID through the existing read and subscription
routes. The [export example](../examples/customer-operation-export/README.md)
connects discovery, progress, cancellation and download without an application
operation-state table or browser persistence.

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

## Bounded preview admission

The local continuation adds the operator TOML setting
`operations_preview_policy_path` to apid and gatewayd-internal. Its default is
empty and admits no new work. An absolute path selects a versioned JSON policy
with an explicit UTC window, account/app/environment cohorts, and individual
platform tenant IDs. No wildcard or all-customer grant is supported. Plan limits
and authentication still apply. Configuring workload trust alone never opens
admission.

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
deployment/definition/release pins and configuration presence. Completion
destination warnings stay separate from submission blockers. Unprobed gateway,
runtime, storage I/O, native lifecycle and fleet rollback checks remain unknown.
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
