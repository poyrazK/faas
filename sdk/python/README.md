# faas_sdk
A client library for accessing one-box FaaS REST API

## Usage
First, create a client:

```python
from faas_sdk import Client

client = Client(base_url="https://api.example.com")
```

If the endpoints you're going to hit require authentication, use `AuthenticatedClient` instead:

```python
from faas_sdk import AuthenticatedClient

client = AuthenticatedClient(base_url="https://api.example.com", token="SuperSecretToken")
```

Now call your endpoint and use your models:

```python
from faas_sdk.models import MyDataModel
from faas_sdk.api.my_tag import get_my_data_model
from faas_sdk.types import Response

with client as client:
    my_data: MyDataModel = get_my_data_model.sync(client=client)
    # or if you need more info (e.g. status_code)
    response: Response[MyDataModel] = get_my_data_model.sync_detailed(client=client)
```

Or do the same thing with an async version:

```python
from faas_sdk.models import MyDataModel
from faas_sdk.api.my_tag import get_my_data_model
from faas_sdk.types import Response

async with client as client:
    my_data: MyDataModel = await get_my_data_model.asyncio(client=client)
    response: Response[MyDataModel] = await get_my_data_model.asyncio_detailed(client=client)
```

## Managed realtime publish retries

Publish messages with the generated realtime operation and pass a stable
`idempotency_key` when retrying one logical event. Reuse it only with the same
delivery mode, decoded payload, and binary flag:

```python
import base64

from faas_sdk.api.realtime.publish_managed_realtime_channel import sync_detailed
from faas_sdk.models import ManagedRealtimeMessageRequest

payload = b'{"job_id":"job-42","progress":100}'
response = sync_detailed(
    slug="my-app",
    id="ENDPOINT_ID",
    channel="jobs",
    client=client,
    body=ManagedRealtimeMessageRequest(
        data_base64=base64.b64encode(payload).decode("ascii"),
    ),
    idempotency_key="job-event:job-42:complete",
)
```

The server replays the original queue outcome for 24 hours. A replay of a
partial publish does not retry subscribers that missed it; a different payload
or delivery mode with the same key returns `409`. An in-flight or uncertain
reservation also returns `409` while the key remains active.

For resumable delivery, opt into `delivery="retained"`. This preview requires
the apid retained-history and realtimed resume flags, a stable idempotency key,
and a payload no larger than 4 KiB. The response contains the committed channel
sequence; v2 clients replay from that cursor after reconnecting:

```python
retained = sync_detailed(
    slug="my-app",
    id="ENDPOINT_ID",
    channel="jobs",
    client=client,
    body=ManagedRealtimeMessageRequest(
        data_base64=base64.b64encode(payload).decode("ascii"),
    ),
    idempotency_key="job-event:job-42:complete:retained",
    delivery="retained",
)
```

By default, when you're calling an HTTPS API it will attempt to verify that SSL is working correctly. Using certificate verification is highly recommended most of the time, but sometimes you may need to authenticate to a server (especially an internal server) using a custom certificate bundle.

```python
client = AuthenticatedClient(
    base_url="https://internal_api.example.com", 
    token="SuperSecretToken",
    verify_ssl="/path/to/certificate_bundle.pem",
)
```

You can also disable certificate validation altogether, but beware that **this is a security risk**.

```python
client = AuthenticatedClient(
    base_url="https://internal_api.example.com", 
    token="SuperSecretToken", 
    verify_ssl=False
)
```

Things to know:
1. Every path/method combo becomes a Python module with four functions:
    1. `sync`: Blocking request that returns parsed data (if successful) or `None`
    1. `sync_detailed`: Blocking request that always returns a `Request`, optionally with `parsed` set if the request was successful.
    1. `asyncio`: Like `sync` but async instead of blocking
    1. `asyncio_detailed`: Like `sync_detailed` but async instead of blocking

1. All path/query params, and bodies become method arguments.
1. If your endpoint had any tags on it, the first tag will be used as a module name for the function (my_tag above)
1. Any endpoint which did not have a tag will be in `faas_sdk.api.default`

## Transactional operation handlers

Customer Operations HTTP definitions explicitly enable
`transaction_receipt: postgres_v1` with reconciliation recovery. Use
`customer_operation_request_from_headers` and
`with_customer_operation_transaction` / `awith_customer_operation_transaction`;
the callback returns the ordinary JSON result, without managed effects.
Approved recovery checks a scoped receipt before business code. Install and
retain `customer_operation_receipt_schema` as the application database owner.
See [Customer Operations transaction adapter](../../docs/operation-transactions.md#customer-operations-http-adapter).

For managed HTTP operations, build the context with
`operation_request_from_headers(headers, method, raw_target, raw_body)`, preserving
repeated headers. Use `with_operation_transaction(connection, operation, callback)`
or `await awith_operation_transaction(...)` with an idle, exclusively leased
psycopg connection configured with `autocommit=True`. The callback's business
writes and result/webhook intent commit together; retries recover the saved body.

Install `operation_receipt_schema` explicitly as the database owner. Send
`response.body` unchanged as `application/json`; `response.replayed` reports
recovery. See the [transactional handler guide](../../docs/operation-transactions.md)
for synchronous and async examples, retention, and uncertain commit handling.

### Customer Operation workflows

For customer Operations, install `customer_operation_receipt_schema` explicitly
as the database owner. The schema includes the response receipt, milestone
outbox, and workflow-state revision tables. Use an idle, exclusively leased
psycopg `AsyncConnection` configured with `autocommit=True`; construct
`GregaleOperations` once with an `httpx.AsyncClient` that the application owns.

Authorize the request before calling `transaction`, because receipt replay
skips the callback. Preserve the raw request target and body before framework
parsing. This FastAPI-style example uses the request's ASGI scope for the exact
path and query bytes:

```python
from fastapi import HTTPException, Request, Response
from faas_sdk import CustomerOperationPublicationError


async def fulfill_order(request: Request, connection, operations):
    raw_body = await request.body()
    raw_target = request.scope["raw_path"].decode("ascii")
    query = request.scope.get("query_string", b"")
    if query:
        raw_target += "?" + query.decode("ascii")
    order_id = request.path_params["id"]
    await authorize_order(request, order_id)

    async def apply(tx):
        await tx.execute("SELECT status, workflow_instance_id FROM orders WHERE id = %s FOR UPDATE", (order_id,))
        row = await tx.fetchone()
        if row is None or row[0] != "fulfillment-in-progress":
            raise ValueError("order is not ready for fulfillment")
        workflow_run_id = row[1]
        await tx.execute("UPDATE orders SET status = %s WHERE id = %s", ("fulfilled", order_id))
        tx.milestone("order-fulfilled", {"order_id": order_id})
        tx.workflow_transition(
            "order-lifecycle", workflow_run_id, "fulfillment-in-progress", "completed"
        )
        return {"order_id": order_id, "status": "fulfilled"}

    try:
        receipt = await operations.transaction(
            connection, request.headers, request.method, raw_target, raw_body, apply
        )
    except CustomerOperationPublicationError as error:
        # The write committed. A retry with this same Operation identity
        # replays the saved result and attempts publication again.
        raise HTTPException(status_code=503, detail="retry the same Operation") from error
    return Response(receipt.body, media_type="application/json")
```

Use this path only for requests from Gregale's guest listener; reserved headers
carry execution context and are not authentication. The SDK validates queued
facts and transitions before commit, assigns workflow revisions in the same
transaction, and publishes each saved report under the current request's proof.
`receipt.replayed` is true when the saved result was returned without running
the callback. See the [operations guide](../../docs/operations.md) for workflow
contracts, evidence, and recovery behavior.

Generate typed workflow names and transition helpers directly from your manifest:
`gregale customer-operations bindings --app orders --plan pro --language python --output workflow_bindings.py`.
Import the generated helper into the async transaction callback, pass the state
from the locked business row and its required milestone payloads, and let it
queue the facts and transition. Add `--check` in CI to reject stale bindings.
See the [binding guide](../../docs/operations.md#generate-application-workflow-bindings).

## Login-target observation

For a `POST` login route configured with `failed_responses`, central
coordination, and `observe_targets: true`, attach an opaque target to each
selected failed response. Use the exact normalization applied during account
lookup for both existing and unknown accounts:

```python
import os
from faas_sdk import PRE_AUTH_TARGET_HEADER, pre_auth_target_digest


def failed_login_headers(normalized_identifier: str) -> dict[str, str]:
    return {
        PRE_AUTH_TARGET_HEADER: pre_auth_target_digest(
            os.environ["GREGALE_ABUSE_TARGET_KEY"], normalized_identifier
        )
    }
```

Create a random key of at least 32 bytes, keep it server-side, and share it
across replicas. The helper takes an already-normalized identifier and returns
a lowercase HMAC-SHA256 digest. It does not decide which responses are login
failures. Attach the header exactly once only on failed responses. Gregale
removes it before returning the response to the client. See
[pre-auth security guidance](../../docs/security.md) for tenant-scoped
identifiers and key rotation. Call `failed_login_headers` with the normalized
lookup value after either an unknown account or an incorrect credential, and
set its result on the 401 response using your framework.

## Dev Bridge request context

Wrap a FastAPI/Starlette ASGI app and use an HTTPX transport that re-evaluates
routing context on each outbound request and redirect hop:

```python
import httpx
import os
from fastapi import Response
from faas_sdk import DevBridgeMiddleware, AsyncDevBridgeTransport

app.add_middleware(DevBridgeMiddleware)
service_client = httpx.AsyncClient(transport=AsyncDevBridgeTransport())

@app.get('/charge')
async def charge():
    result = await service_client.get(os.environ['GREGALE_SERVICE_PAYMENTS_URL'] + '/charge')
    return Response(result.content, status_code=result.status_code, media_type='application/json')
```

Close the shared HTTPX client in the application's shutdown hook. Synchronous
applications can use `DevBridgeTransport`; other adapters can capture a header
with `with_dev_bridge_context(value)`. ContextVar keeps concurrent requests
separate. Both transports strip explicit bridge authority and propagate only to
single-label `NAME.svc.gregale` or `NAME.internal` names without URL userinfo.
HTTPX redirect handling passes each hop through the transport, so an external
destination cannot inherit session authority. Application authentication remains
subject to HTTPX's normal redirect policy. Gregale authorizes scope at every hop.
See [the Dev Bridge guide](../../docs/dev-bridge.md) for local execution.

## Runtime feature flags

Managed Python ASGI applications can use Gregale's customer-aware runtime flag
client. It evaluates locally against a bounded configuration snapshot and adds
used decisions to response evidence. The HTTPX transport forwards only used
decisions to managed Gregale services:

~~~python
import httpx
from faas_sdk import (
    AsyncGregaleFlagsTransport,
    GregaleFlags,
    GregaleFlagsMiddleware,
)

flags = GregaleFlags(api_url="https://api.gregale.dev")
# Call await flags.start() in the application's async startup hook.
# Call await flags.close() in its shutdown hook.
app = GregaleFlagsMiddleware(app, flags)
service_client = httpx.AsyncClient(transport=AsyncGregaleFlagsTransport(flags))
~~~

Inside a request handler, call flags.boolean(key, fallback) or
flags.variant(key, fallback), then flags.used(key) when the selected behavior
is entered. Close both clients during application shutdown. See the
[feature flags guide](../../docs/flags.md) for workload identity, fallback,
propagation and trust-boundary details.

## Project release context

For app-to-app calls, wrap the ASGI application in
`GregaleReleaseMiddleware` and use a release-aware HTTPX transport for calls
to managed services. The transport forwards only `X-Gregale-Release` and
strips the caller-scoped `X-Gregale-Revision` header on those hops:

```python
import httpx
from faas_sdk import AsyncGregaleReleaseTransport, GregaleReleaseMiddleware

app = GregaleReleaseMiddleware(app)

async def call_billing():
    async with httpx.AsyncClient(transport=AsyncGregaleReleaseTransport()) as client:
        return await client.get("http://billing.svc.gregale:10080/health")
```

The middleware scopes context to each HTTP or WebSocket request. The proxy
still verifies release membership from the caller deployment's network
identity; the header is context, not authorization.


## Advanced customizations

There are more settings on the generated `Client` class which let you control more runtime behavior, check out the docstring on that class for more info. You can also customize the underlying `httpx.Client` or `httpx.AsyncClient` (depending on your use-case):

```python
from faas_sdk import Client

def log_request(request):
    print(f"Request event hook: {request.method} {request.url} - Waiting for response")

def log_response(response):
    request = response.request
    print(f"Response event hook: {request.method} {request.url} - Status {response.status_code}")

client = Client(
    base_url="https://api.example.com",
    httpx_args={"event_hooks": {"request": [log_request], "response": [log_response]}},
)

# Or get the underlying httpx client to modify directly with client.get_httpx_client() or client.get_async_httpx_client()
```

You can even set the httpx client directly, but beware that this will override any existing settings (e.g., base_url):

```python
import httpx
from faas_sdk import Client

client = Client(
    base_url="https://api.example.com",
)
# Note that base_url needs to be re-set, as would any shared cookies, headers, etc.
client.set_httpx_client(httpx.Client(base_url="https://api.example.com", proxies="http://localhost:8030"))
```

## Building / publishing this package
This project uses [Poetry](https://python-poetry.org/) to manage dependencies  and packaging.  Here are the basics:
1. Update the metadata in pyproject.toml (e.g. authors, version)
1. If you're using a private repository, configure it with Poetry
    1. `poetry config repositories.<your-repository-name> <url-to-your-repository>`
    1. `poetry config http-basic.<your-repository-name> <username> <password>`
1. Publish the client with `poetry publish --build -r <your-repository-name>` or, if for public PyPI, just `poetry publish --build`

If you want to install this client into another project without publishing it (e.g. for development) then:
1. If that project **is using Poetry**, you can simply do `poetry add <path-to-this-client>` from that project
1. If that project is not using Poetry:
    1. Build a wheel with `poetry build -f wheel`
    1. Install that wheel from the other project `pip install <path-to-wheel>`
## Internal HTTP Operations preview

Production Operations submission remains disabled. Generated
`faas_sdk.api.operations` modules expose typed submission, status, event,
cancellation, runtime reporting, artifact download and account recovery routes.
Account `inspect_operation_recovery` and `preview_operation_recovery` expose
retained execution evidence and a read-only recovery proposal. Preview starts no
work or file publication; eligibility does not establish that external effects
can safely repeat. A separate `recover_operation` request can provide the returned
`inspection_revision` as `expected_inspection_revision` to fence changed evidence.

HTTP handlers can import `GregaleOperations` from
`faas_sdk.operations_runtime` and use its `bind_request(headers)` context
manager around a trusted Gregale guest request. Call `progress(report)` or
`artifact(report)` with the generated request models and a stable `report_id`.
The helper fetches fresh workload identity for each report, rejects native
execution context and isolates authority between concurrent requests. Its public
`context()` omits the ephemeral capability. The caller owns the injected
`httpx.AsyncClient` and closes it during shutdown.

`await runtime.control()` reads current cancellation intent, deadline and lease
with fresh workload identity and the same attempt proof. It creates no report,
event or renewal. Applications manage their own cooperative scope: subtract read
latency from the server's observed duration, stop at the earlier lease/deadline,
and pass cancellation to I/O. Stopping does not undo external effects or certify
a safe retry; dispatched uncertainty retains its pinned recovery policy.

See [Operations](../../docs/operations.md) for the separate business and webhook
outcomes, retained result bytes, scoped credentials and rollout status.

Completion delivery inspection, attempt history, and immutable retry decisions
are exposed through the Operations APIs (`getOperationDelivery`,
`getOperationDeliveryAttempts`, `retryOperationDeliveryWithReceipt`; PascalCase
in Go and snake_case Python modules). New retries carry `retry_id`, `delivery_id`
and an explicit `expected_replay_generation`, including zero. Reuse the same
request after an uncertain reply; the returned `queued` receipt describes the
original decision. Read delivery status separately. Business results and
execution generations are unaffected. The legacy retry method remains available.


## Object version protection

The Storage API supports typed retention/legal-hold reads and mutations, plus
protection operation inspection. Use an explicit owned public version UUIDv4
(or `null` in an eligible Object Lock bucket). Mutations require a stable UUIDv4
operation ID and return a durable receipt; retain the returned ID for retries
and status. Fixed GOVERNANCE/COMPLIANCE retention and independent ON/OFF legal
holds are supported. Event-hold changes and governance bypass are unsupported.
See [the protection contract](../../docs/object-storage.md#per-version-retention-and-legal-holds)
for enrollment, pending-operation fences and recovery behavior.

### Workflow blockers

Inside the customer Operation transaction callback, use `tx.workflow_blockers(workflow, instance_id, locked_row_state, [OperationWorkflowBlocker(code="payment-pending", description="Payment confirmation is pending.", operation="fulfill-order")])` to replace
the public blockers while preserving the current state. Check customer authorization
and read that state from the locked business row. An empty list clears blockers;
a later normal state report without blockers also clears them. Propagate errors
out of the callback. `workflow_instance.decision.blockers` exposes the latest
reported list alongside declared next actions. These reports do not enforce
business rules or grant execution authority.

Before upgrading, reinstall the SDK's additive customer Operation database schema
to add the blocker outbox columns. See [the Operations guide](../../docs/operations.md#report-workflow-blockers)
for bounds, replacement, revision, and publication semantics.

### Workflow attention queue

Use `list_account_workflow_attention.asyncio("orders", client=client, scope="production", reason="blocked")` from `faas_sdk.api.operations` to read one page of current retained blocked or stale workflows.
The response includes public business references, workflow snapshots, blocker
reasons and a continuation cursor. Workflow and target Operation filters narrow
the queue; customer routes use identity from credentials. Continue with the same
filters and `next_cursor`; refresh the first page for the latest view. See
[the Operations guide](../../docs/operations.md#find-workflows-needing-attention).

### Explain a cleared blocker

Use `tx.workflow_blockers(workflow, instance_id, locked_state, remaining_blockers, resolutions=[resolution])` to attach an explicit public resolution fact to the transactional
blocker replacement. `OperationWorkflowBlockerResolution` includes the target
Operation, blocker code, explanation, and exact source Operation/report IDs and
revision. Current snapshots expose `operation_id`, `report_id`, and `revision`
for these references. The source must be a retained report within the same
customer, business reference, workflow run, environment and contract version;
it must contain the named blocker, which cannot remain in the replacement list.

Resolution facts survive outbox replay and remain in retained state history even
after a later snapshot replaces them. Reinstall the SDK's additive customer
Operation database schema before upgrading the adapter. See
[the Operations guide](../../docs/operations.md#explain-blocker-resolutions)
for bounds, source retention, publication recovery, and history reads.

#### Attention summaries and blocker age

```python
from faas_sdk.api.operations import summarize_platform_tenant_self_workflow_attention
summary = await summarize_platform_tenant_self_workflow_attention.asyncio(
    client=client, app_id=app_id, scope="production", group_by="blocker_code",
)
```

`summarize_account_workflow_attention` also offers sync/async methods.
Group by `workflow`, `blocker_code`, `target_operation`, or account-only `customer`.
Both queues and summaries accept `blocker_code`. Totals cover all matching
workflows independently of group pagination.

Install the updated `customer_operation_receipt_schema` before upgrading writers.
`OperationWorkflowBlocker.first_observed_at` is an optional RFC3339 string.
Transactional writers preserve it across repeats until the target/code clears.
Legacy or unobserved continuity retains unknown age. Applications may supply
a known original observation timestamp; upgrade all writers for reliable
continuity. Summary models expose oldest known ages and unknown-age counts.

#### Business deadlines

Call `tx.workflow_deadline(workflow, instance_id, state, due_at)` inside the
business transaction. A finite RFC3339 string sets/updates it; `""` clears.
The SDK preserves current blockers and inherits due times on subsequent state,
transition, and blocker reports. Install the updated application receipt schema
and upgrade every writer. Attention queue and summary endpoints accept
`reason="overdue"`; state models expose due times and overdue duration.
Terminal workflows do not count as overdue.

#### Explicit business outcomes

Call `tx.workflow_outcome(workflow, instance_id, terminal_state, code, description)`
after the terminal transition and required milestones inside the business
transaction. The SDK preserves blockers and deadline. Subsequent reports of the
same state inherit the outcome; state changes remove it. Install the updated
receipt schema and upgrade every writer.

The `list_account_workflow_outcomes`, `list_platform_tenant_self_workflow_outcomes`,
`summarize_account_workflow_outcomes`, and
`summarize_platform_tenant_self_workflow_outcomes` endpoint modules offer sync and
async methods. Filter by `workflow` or `code`; summary `group_by` supports
`outcome`, `workflow`, or account-only `customer`. Counts cover latest retained
terminal instances with explicit outcomes once each, independently of pagination.

### Workflow prerequisites

Use `tx.workflow_dependencies(workflow, instance_id, state, [OperationWorkflowDependency(...)])` inside the business transaction to replace up to 16 direct workflow dependencies. Pass an empty list to clear them. Links stay within the same customer/application/environment; an optional required outcome distinguishes successful prerequisites from other terminal results. Other reports inherit current links. Apply the updated customer schema and upgrade all writers first. The existing workflow instance response includes `related_workflows` with retained states and explicit resolution statuses. See [workflow dependencies](../../docs/operations.md#workflow-dependencies) for complete examples and retention semantics.

### Dependency attention

Attention requests support `dependency_status="waiting", required_outcome_code="paid"` and the `dependency` reason. The response includes `dependency_attention` references/statuses and summary counts `dependency_workflow_count` / `dependency_count`. Summaries also support `dependency_status` and `required_outcome_code` grouping. Both dependency filters must match the same unresolved reference. See [dependency-aware attention](../../docs/operations.md#dependency-aware-attention).

### Reverse dependency impact

Existing business milestones responses now include typed `workflow_instance.dependency_impact`: retained dependent workflows, required outcomes, prerequisite statuses, and affected-workflow counts. The list shows up to 100 items, affected sources first; counts cover all matches and `has_more` signals truncation. Unknown account-side prerequisites require an explicit customer; self reads always use the authenticated customer. See [reverse dependency impact](../../docs/operations.md#reverse-dependency-impact).

### Dependency root-cause tracing

Business milestones responses include typed `workflow_instance.dependency_trace` findings with linked reference paths and observed states. The trace follows unmet prerequisites, distinguishes cycles from shared workflows, and exposes missing reports, blockers, mismatched outcomes, staleness, and missed deadlines. Traversal is bounded; inspect `truncated` / `limits_reached` before treating coverage as complete. See [dependency root-cause tracing](../../docs/operations.md#dependency-root-cause-tracing).

### Workflow transition readiness

Use an authenticated operations reader to check a proposed transition:

```python
from faas_sdk.api.operations import check_platform_tenant_self_workflow_readiness
from faas_sdk.models import OperationWorkflowReadinessRequest, OperationSubject
result = check_platform_tenant_self_workflow_readiness.sync(client=client, body=OperationWorkflowReadinessRequest(
    app_id=app_id, scope="production", subject=OperationSubject(type_="order", id=order_id),
    workflow="fulfillment", instance_id=run_id, operation="ship-order",
    from_state="waiting", to_state="shipping", milestones=["shipment-created"],
    state_revision=revision, contract_version=1,
))
```

Inspect `readiness.ready`, denial reasons, missing milestones, unmet prerequisites, and advisories. Account readers use the account readiness endpoint with an explicit customer selector. Planned names are not committed evidence; business-row checks, authorization, and transaction-time workflow/milestone validation still apply. See [workflow transition readiness](../../docs/operations.md#workflow-transition-readiness).

### Guard a transition inside the business transaction

After locking the business row, call
`await tx.guarded_workflow_transition(request, [("approved", payload)], check)`
before writing. `request` is an `OperationWorkflowReadinessRequest` with the matching
app, scope, subject, operation, workflow instance, locked source state, proposed
state, positive locked state revision, and contract version. `check` is an async
callable returning `OperationWorkflowReadinessResponse` through the self readiness
endpoint, authenticated as the transaction's customer.

`CustomerOperationReadinessError` from `faas_sdk.customer_operations` exposes
`.response`. All guard failures prevent commit even if caught. Await guards
sequentially in the callback. Existing contract and actual payload validation still
runs before commit.

### Business decision evidence

`tx.business_decision("approval-decided", OperationBusinessDecision(workflow="order-approval", instance_id=run_id, code="manual-review-approved", description="An authorized reviewer approved the order.", rule_id="manual-approval", rule_version="2026-10"))` (import the model from `faas_sdk.business_decisions`) queues a bounded explanation with the business transaction. Declare the milestone payload schema and bind its workflow step to `/decision/instance_id`. It uses existing precommit validation and outbox replay; no schema installation is needed. See [business decision evidence](../../docs/operations.md#business-decision-evidence) for declaration and history details.

### Versioned policy requirements

Workflow transitions can declare `requires_policies` with a milestone, rule ID, exact rule version, and decision code. Transactional readiness guards derive planned decisions from actual milestone payloads, and precommit validation requires matching evidence for the same workflow instance. See [policy requirements](../../docs/operations.md#versioned-business-policy-requirements).

### Business state reconciliation

Reconciliation transaction helpers compare the locked application row with customer-scoped workflow history and queue a fresh explicit snapshot plus discrepancy evidence when needed. Business revisions stay separate from SDK report counters. Ahead/version conflicts record diagnostics without refreshing state. Declare the reconciliation milestone schema and bind its step to `/reconciliation/instance_id`. See [reconciliation usage](../../docs/operations.md#business-state-reconciliation).

### Transition-specific prerequisites

Declare `requires_dependencies` on a transition to select workflow names from the current instance's reported links. Omitted selects all links; `[]` selects none. Missing required links are structured readiness failures. SDK guards apply the selected edge's requirements. See [prerequisite usage](../../docs/operations.md#transition-specific-business-prerequisites).

### Business action previews

Read-only action preview helpers return current-state candidates with revision/version and all transition requirements. Candidates use an empty evidence plan. Use the transaction readiness guard with actual facts and locked business rows before performing an action. See [preview usage](../../docs/operations.md#business-action-previews).

### Business invariant reports

Invariant helpers queue a typed check fact and targeted blocker update with the business transaction. Failed and unknown checks block their target Operations; passed checks clear only their stable invariant code. Supply the complete locked blocker head and chain returned blockers for multiple checks. Guards also consider pending invariant blockers. See [invariant usage](../../docs/operations.md#business-invariant-reports).

### Required invariant evidence

Transitions may declare `requires_invariants` with a milestone, stable code, and exact version. Guards derive check plans from actual invariant payloads. Passing evidence must match the source state, instance, and target action and accompany the transaction. See [required invariants](../../docs/operations.md#required-invariant-evidence-per-transition).

### Business effect evidence

Effect helpers record pending, failed, or confirmed business facts with a reference and optional amount/currency. Transition `requires_effects` requirements need matching confirmed evidence. Guards derive plans from actual effect payloads. External effects still need application idempotency and verified confirmation. See [effect usage](../../docs/operations.md#business-effect-evidence).

### Compensation workflows

Compensation helpers record required, pending, failed, or confirmed reversal observations linked to a retained confirmed effect. Source ownership/app/environment are checked before commit and publication. Applications execute reversals and report workflow state explicitly. See [compensation usage](../../docs/operations.md#compensation-workflows).

Workflow final actions expose `upload_workflow_operation_artifact` and
`reuse_workflow_operation_upload` under `faas_sdk.api.operations`. Supply a current
workload bearer and the trusted run/step/generation/attempt/capability headers.
Uploads take a binary `File` plus report ID, filename, exact byte count and SHA-256;
receipt lookup takes `OperationArtifactUploadRequest`. No source URI or bucket
writer is needed. Before retrying a lost transfer response, look up the same
declaration. Only an authorized `available=False` permits another transfer;
errors never authorize writing. These low-level endpoints do not retry business
work. Keep report ID and declaration stable across approved resumes: fresh proof
rebinds a verified receipt without another transfer. Cancellation, deadline expiry
and stale proof deny upload and reuse. Publication waits for confirmed final-step
success or operator success reconciliation.

The managed-source `reuse_workflow_operation_artifact` and
`prepare_workflow_operation_artifact` endpoints remain available with a stable
`obj://` reference. Reconcile uncertain provider writes before writing again.

Recovery decisions: Generated `faas_sdk.api.operations.recover_operation_with_receipt` returns `OperationRecoveryDecision`. It acknowledges the original explicit
account-authorized decision, independently of current operation and delivery
status. Retrying the same decision ID and request never records a second
recovery. See [receipt-backed operator recovery](../../docs/ops/customer-operations-cli.md#resume-a-recovery-decision-after-losing-its-response).

For native batch Job Operations, construct `GregaleJobOperations(api_url, tokenless_async_client)` inside the scheduler-provided task environment. `context.platform_tenant_id` is verified against the control response. Call `await control()` before business entry and between units, `await progress(report)`, and `await prepare_result(result, report_id)` before normal exit. Retrying a lost report acknowledgement must reuse its report ID; never repeat business work automatically. Result preparation requires host-confirmed task exit for business success. Native qualification is pending.

Job file helpers: `await reuse_artifact(declaration)` checks an existing private
copy before your application bucket writer. Only `available=False` permits a
new upload; errors do not. After uploading an owned private managed object,
`await prepare_artifact(declaration)` verifies its exact size and SHA-256 and
retains an immutable copy. Use `OperationArtifactRequest` with a stable report
ID and source URI; replay that declaration after a lost acknowledgement.
Preparation does not publish the file. Host-confirmed success with the typed
result or explicit account success recovery publishes it. An approved Job
retry clears old receipts; reconcile uncertain uploads before writing again.
