# faas_sdk
A client library for accessing one-box FaaS REST API

## Default and preview packages

`faas_sdk` covers generally available and beta capabilities: the client
generated from `sdk/openapi.yaml` under `faas_sdk.api` and `faas_sdk.models`
and the helpers for those capabilities (ADR-20261023115741). Preview and
internal capabilities (realtime, operations, events, durable entities,
queues, object storage, managed Postgres, disposable runs, issues, commit,
data repair, AI gateway, flags and more) are in `faas_sdk.preview`: the
complete generated client under `faas_sdk.preview.api` and
`faas_sdk.preview.models`, and their helpers such as
`faas_sdk.preview.operations_runtime`. `faas_sdk.preview` re-exports
`faas_sdk`, and its `FaaSClient` adds the disposable Runs helpers. apid serves
a preview capability only to the accounts it is released to; elsewhere its
calls answer `404 no such API route`.

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

from faas_sdk.preview.api.realtime.publish_managed_realtime_channel import sync_detailed
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

## Batched trigger delivery

A trigger with `delivery: batch` sends one request with several records
(ADR-1321). `handle_trigger_batch` parses the body, runs your handler for each
record in delivery order and returns the response body; a record whose handler
raises is retried, the rest are complete:

```python
from faas_sdk.preview.trigger_batch import handle_trigger_batch, is_trigger_batch

def handler(request):
    # record.is_json, record.data, record.payload, record.text(), record.metadata
    return handle_trigger_batch(request.body, lambda record: save_order(record.data))
```

`is_trigger_batch(headers)` tells batch and single-record deliveries apart.
`handle_trigger_batch_async` takes async handlers and a `concurrency` (1-100)
for records that do not depend on each other's order. `stop_on_failure` retries
every record after a failure in order, and `on_error` sees each failure. A
malformed body raises `TriggerBatchError`; let the request fail so the whole
batch is retried.

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
from faas_sdk.preview import CustomerOperationPublicationError


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
from faas_sdk.preview import DevBridgeMiddleware, AsyncDevBridgeTransport

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

## Application Auth tokens

`pip install 'faas_sdk[auth]'`, then verify application Auth access tokens and
check permissions with `faas_sdk.preview.auth.AuthClient` (`verify`, `authorize` and
their `_async` forms). See
[Application Auth](../../docs/application-auth.md#in-your-backend).

## Runtime feature flags

Managed Python ASGI applications can use Gregale's customer-aware runtime flag
client. It evaluates locally against a bounded configuration snapshot and adds
used decisions to response evidence. The HTTPX transport forwards only used
decisions to managed Gregale services:

~~~python
import httpx
from faas_sdk.preview import (
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
`faas_sdk.preview.operations_runtime` and use its `bind_request(headers)` context
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



### Application standards

The generated `faas_sdk.api.orgs` modules include
`preview_application_standard_assignment`, `get_application_standard_review`,
`get_application_standard_operation`, `get_application_standard_enrollment`,
and `list_application_standard_exceptions`. Use their typed request/response
models through the authenticated client's `inner` transport.

Preview saves affected applications and blockers without activating an
assignment. Progress preserves desired, persisted and observed state.
Enrollment includes the installed exception deadline, while exception history
retains approval/revocation details, server `as_of` time and an exclusive UUID
cursor. Activation and exception mutation APIs remain gated pending runtime
acceptance.

## Object version protection

The Storage API supports typed retention/legal-hold reads and mutations, plus
protection operation inspection. Use an explicit owned public version UUIDv4
(or `null` in an eligible Object Lock bucket). Mutations require a stable UUIDv4
operation ID and return a durable receipt; retain the returned ID for retries
and status. Fixed GOVERNANCE/COMPLIANCE retention and independent ON/OFF legal
holds are supported. Event-hold changes and governance bypass are unsupported.
See [the protection contract](../../docs/object-storage.md#per-version-retention-and-legal-holds)
for enrollment, pending-operation fences and recovery behavior.

## Batch event publication

Use `faas_sdk.api.events.publish_event_batch.sync_detailed` or
`asyncio_detailed` with `PublishEventBatchRequest(events=[...])`. Batches contain
1–100 stable-id envelopes in at most 1 MiB. Inspect each parsed result: HTTP 200
can include rejected or unknown items. Retry with the original source/id/content;
duplicates retain their original receipt and do not create more deliveries.
See [batch publication](../../docs/event-driven.md#batch-event-publishing).

## Event retention health

The generated events service exposes `getEventRetentionHealth` (Node) or
`faas_sdk.api.events.get_event_retention_health` (Python). Filter receipt
observations by source/app and choose an expiry lookahead; storage utilization
remains account-wide. Recovery preflight also returns current receipt expiry
warnings and current retention holds. See [retention health](../../docs/event-driven.md#retention-health-and-expiry-alerts).

## Recovery receipt protection

Set `protect_receipts: true` when creating an event recovery job (Go:
`EventRecoveryRequest.ProtectReceipts`). Selected receipts remain held while
items are pending and the job is active, until its original 24-hour expiry.
Preview reserves nothing. Held receipts continue counting against account
storage limits. See [recovery protection](../../docs/event-driven.md#protect-receipts-during-bulk-recovery).

## Durable recovery outcomes

Existing recovery status and item reads prefer saved terminal results for the
exact admitted replay generation. `execution.source` is `recovery_result`, with
`recorded_at` and original `evidence_source`; job execution summaries include
`saved_results`. These results survive execution-history pruning until the
recovery job is pruned. Uncertain outcomes remain unknown. See
[terminal recovery results](../../docs/event-driven.md#durable-terminal-recovery-results).

Recovery webhook filters support `event_recovery.execution_finished`, separately
from admission completion. Its `EventRecoveryExecutionFinishedWebhookPayload`
contains saved terminal execution counts and `unresolved_count=0`; `all_succeeded`
refers only to queued executions. Recovery job `execution_finished_at` is capture
time, not webhook acknowledgement. Unknown evidence blocks capture. Only newly
created execution jobs with queued deliveries qualify. Update strict webhook
event-enum consumers before API rollout; existing webhook delivery retries and
dead-letter tools apply. See [ADR-915](../../docs/adr/915-recovery-execution-completion-notifications.md).

Existing recovery preview/create methods accept `parent_job_id` with
`mode=execution` to select only saved failed/dead-lettered deliveries from a
retained terminal recovery in the same app. Child creation requires a stable
`request_id` UUID: repeat it with the same normalized selection to return the same
retained child. Changed selections conflict, and the original audit reason wins.
Items expose historical `parent_job_id`/`parent_position` links. Changed or pruned
execution evidence is skipped at admission; newer replays are never substituted.
See [parent-scoped retries](../../docs/event-driven.md#retry-failures-from-one-recovery-job).

`get_event_recovery_health` includes optional execution health for the oldest
retained unresolved terminal-admission jobs. `counts_complete=False` marks
lower-bound counts; prolonged waits measure time since admission completion.
See [execution recovery health](../../docs/adr/917-execution-recovery-health-alerts.md).

Use `faas_sdk.api.events.get_event_recovery_notifications` for a read-only report
of admission/execution capture and each selected receiver's current delivery.
Missing selection or pruned delivery evidence remains unknown; capture alone
does not prove acknowledgement. Retained dead deliveries link to independent
retry. See [notification delivery reports](../../docs/adr/918-recovery-notification-delivery-report.md).

### Recovery notification health

Recovery health responses include `notifications`, with separate admission and execution
counts for overdue, dead, unknown, and no-receiver jobs. Overdue requires known
unacknowledged delivery evidence at least 15 minutes after capture. Phase
`counts_complete` flags cover evidence uncertainty and the 50-job candidate bound;
partial observations cannot clear alerts. Use the notification report for receiver
details. New alert metrics are optional and require the notification health migration.

### Selective recovery notification retries

Use the job-scoped notification retry preview to choose receivers explicitly.
Submit a stable UUID request ID and targets containing kind, webhook ID, delivery
ID, and expected replay generation. The API revalidates each receiver and saves
queued or skipped decisions atomically. Repeating identical intent returns the
original decisions; use a new ID and current evidence for later failures.
Decisions expire with the recovery job. See the events documentation for limits
and migration rollout.

### Recovery notification retry history

Use the retry history methods to list saved request summaries or inspect one
request by ID. Detail preserves each receiver’s original queued or skipped
decision and shows its current retained delivery status with a separate read
timestamp. Missing deliveries are reported as unavailable; job pruning removes
the history.

Retry history detail also exposes `retry_outcome` for the original queued
generation, `retained_attempt_count`, `attempt_count_complete`, and optional
`completed_at`. Later retries do not establish an earlier generation's outcome.
Missing terminal evidence reports unknown; skipped decisions are not applicable.

History list summaries now include succeeded, failed, pending, and unknown
counts for the originally queued generations, aggregate `status`,
`evidence_complete`, and optional `completed_at`. Completion time requires
terminal evidence for every queued target. All-skipped requests are inconclusive;
later retries never establish an earlier generation's outcome.

Retry history lists accept an optional comma-separated `status` union such as
`failed,inconclusive`. Statuses must be distinct values from succeeded, failed,
pending, and inconclusive. The response includes `matched_count` and `totals`;
totals count all retained requests before filtering, including a separate count
of requests with incomplete evidence. No matches returns an empty list and
preserves full totals. Request detail and waiting do not accept this filter.

### App-wide recovery notification retry backlog

Use the notification retry backlog list method to discover original-generation
retry outcomes across retained recovery jobs for an app. Default statuses are
failed, pending, and inconclusive; an explicit status union can include succeeded.
`page_size` counts inspected jobs (default 5, maximum 10), not request rows.
Totals have `counts_scope: job_page` and cover scanned jobs before filtering.
Continue with `next_cursor` even when `requests` is empty, retaining the same
status selection. Each page is a fresh read-only snapshot. Returned detail and
retry-preview paths use existing recovery inspection endpoints.

Application-scoped producer keys: use `faas_sdk.api.events.publish_app_event` with `AppPublishEventRequest(key="order-123-created", type_="order.created", data={"order_id": "123"})`. Preserve app/key/content after uncertain responses. The result includes `duplicate`, generated `app.<UUID>` source and the durable original receipt. Deduplication lasts while the receipt is retained; consumers still deduplicate side effects.

Read-only producer-key reconciliation: `faas_sdk.api.events.get_app_event_publish_status` accepts the app slug and original exact key. The response contains the retained receipt and paginated consumer evidence. `accepted` means routing settled, not consumer success; `processing` is still durably accepted. `unavailable` remains uncertain and must not automatically trigger publication. Follow `evidence.next_after` using `after`, with up to 200 recipients per page (default 100).

Read-only content verification: call `faas_sdk.api.events.verify_app_event_publication` with the original `AppPublishEventRequest`. It returns match/conflict/unavailable, comparing normalized type, schema version and semantic JSON data rather than occurrence time or trace metadata. Match and conflict include the retained acceptance receipt. No publish or retention refresh occurs, and missing evidence remains uncertain.

Acceptance guards: pass `expected_accepted_at` as the exact RFC3339 string from the saved receipt to status or verification. Query parameters deliberately use strings to preserve nanosecond precision rather than converting through Python's microsecond datetime. The independent acceptance result is same_acceptance/replacement_acceptance/unavailable. Replacement content may still match; never assign its consumer outcomes to the expected acceptance. Reconciliation remains read-only.
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

`CustomerOperationReadinessError` from `faas_sdk.preview.customer_operations` exposes
`.response`. All guard failures prevent commit even if caught. Await guards
sequentially in the callback. Existing contract and actual payload validation still
runs before commit.

### Business decision evidence

`tx.business_decision("approval-decided", OperationBusinessDecision(workflow="order-approval", instance_id=run_id, code="manual-review-approved", description="An authorized reviewer approved the order.", rule_id="manual-approval", rule_version="2026-10"))` (import the model from `faas_sdk.preview.business_decisions`) queues a bounded explanation with the business transaction. Declare the milestone payload schema and bind its workflow step to `/decision/instance_id`. It uses existing precommit validation and outbox replay; no schema installation is needed. See [business decision evidence](../../docs/operations.md#business-decision-evidence) for declaration and history details.

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

Dead-letter export is available through
`faas_sdk.preview.api.realtime.export_realtime_consumer_group_dead_letters` as a binary JSONL
file. `sync_detailed`/`asyncio_detailed` expose pagination headers and the response
content. The first JSONL line is a manifest with `group_id`, `through_sequence`,
`snapshot_at`, `next_after_sequence` and `has_more`; pass these cursor fields back
with the same failure-code filters to resume. Verify a complete download and manifest
record count before advancing. Downloads can be retained externally; export does not
extend server retention and replay/cleanup can remove records between pages.


### Background dead-letter replay jobs

Use `POST .../groups/{group}/replay-jobs` with `rate` (1–32), optional `failure_codes`, `after_sequence` (exclusive) and `through_sequence` (inclusive). Creation captures current failures only, up to 128; an empty selection is invalid. `GET .../replay-jobs` shows active jobs first and recent terminal jobs. `POST .../replay-jobs/{job}/{action}` accepts `pause`, `resume` or `cancel`. Account ownership and MFA apply; mutations require deploy-write scope. The inspector offers the same controls.

Rate is an upper limit per job; multiple jobs have independent limits. Known outcomes include replayed, stale, missing, not_dead, ordering_blocked, invalid_deadline and deadline_expired. Skips require a new recovery job. Safe processing errors and cumulative counts appear in status; database errors retry without advancing progress. Cancellation does not undo completed replay. Inspect recent jobs before retrying an uncertain creation response. Disabled endpoints suspend execution; group claim pause/circuit/rate settings still govern subsequent processing.

Apply migration `20261012102700533_realtime_dead_letter_replay_jobs.sql` before enabling jobs, with all API replicas upgraded. Cancel active jobs before downgrade. Migration and runtime/UI checks are currently deferred.


### Replay-job notifications

Create or update an app webhook with event filters `realtime.replay_job.completed`, `realtime.replay_job.cancelled` and/or `realtime.replay_job.processing_failed`. Existing webhook signing, retry policy, delivery status and dead-letter inspection apply. The inspector links to app webhook configuration.

Completion and cancellation notify once per transition. The processing warning notifies once per job when cumulative recorded processing errors first reach three; the job continues retrying. Known skips are not processing errors. No backfill for past transitions or newly added subscribers. Deduplicate the payload's `event_id` across delivery retries and do not assume delivery order.

`RealtimeReplayJobWebhookPayload` documents recovery counts, safe error diagnostics and a relative `inspector_path` to resolve against your console origin. Payloads exclude failed messages and credential secrets. Job history can be pruned after notification. Migration `20261012102700536_realtime_replay_job_notifications.sql` is required and currently unapplied.

### Reviewed dead-letter recovery

Use `faas_sdk.preview.api.realtime.create_realtime_replay_dry_run` with the existing filter/rate request model. Poll `list_realtime_replay_jobs` for a completed dry run, review eligible/skipped outcomes and timestamps, then call `control_realtime_replay_job` with the report UUID and the `start` action. The returned job is the linked real replay. Start retries return the same job while retained; actual replay rechecks identity, deadlines and ordering. A dry run changes report records only, shares job quotas and emits no replay lifecycle webhooks.

### Async realtime workers

`consume_realtime_consumer_group_with_credential` runs a group-bound `grw_` worker. Supply your async `handle_event(message, context)` function; await all application work and observe `context.stop_event` during long operations. Use `context.claim.group_id` and `fencing_token` for downstream stale-write protection. Handlers must be idempotent and must not block the event loop. The claim token is sensitive: do not log or cache `context.claim`.

```python
import asyncio
import os
import signal

import httpx
from faas_sdk.preview import (
    AuthenticatedClient,
    RealtimeWorkerOptions,
    consume_realtime_consumer_group_with_credential,
)

# Unix service example; handle_event is your async application handler.
async def main():
    stop = asyncio.Event()
    asyncio.get_running_loop().add_signal_handler(signal.SIGTERM, stop.set)
    async with AuthenticatedClient(
        base_url=os.environ["GREGALE_API_URL"],
        token=os.environ["GREGALE_WORKER_TOKEN"],
        timeout=httpx.Timeout(30),
    ) as client:
        await consume_realtime_consumer_group_with_credential(
            client,
            group_id=os.environ["GREGALE_GROUP_ID"],
            on_message=handle_event,
            stop_event=stop,
            options=RealtimeWorkerOptions(
                concurrency=4,
                batch_size=4,
                heartbeat=True,
                settlement_retries=3,
                settlement_batch_size=8,
                settlement_flush_milliseconds=10,
                release_on_shutdown=True,
                use_group_retry_policy=True,
            ),
        )

asyncio.run(main())
```

The caller owns and closes the client. Scoped credentials require `status`, `claim`, `renew`, `complete` and `retry`, plus `heartbeat`/`release` for those options. For account keys, use `consume_realtime_consumer_group(client, slug=..., id=..., channel=..., group=..., worker=..., on_message=..., options=..., stop_event=...)`.

Defaults: concurrency/batch size 1, long polling 10 seconds, empty polling 1 second; heartbeats and shutdown release off. Concurrency supports 1–32, batches 1–16 and long polling 0–20 seconds. Leases renew at one third of their configured duration; local expiry or delivery deadlines stop processing cooperatively. Successful handlers complete; exceptions retry with `handler_failed`. Supply a synchronous `failure_code(error, message)` classifier returning 1–64 printable ASCII characters without secrets. An optional async `on_handler_error(error, message)` runs after confirmed retry settlement. Explicit retry delay defaults to one second; `use_group_retry_policy=True` omits it, while `retry_after_seconds=0` requests immediate retry.

Known rate/circuit/dedup admission hints delay claims. Retention/capacity blockers process partial returned claims and then raise `RealtimeWorkerError`; infrastructure/auth/lease errors stop the worker. HTTP errors expose status/code without response bodies. Claims and renewals are never retried automatically. `settlement_retries` defaults to 0 and accepts integers 0–3. When enabled, complete/retry/release requests use one UUID receipt key per logical settlement and reuse the exact body for retries after network errors, timeouts, remote protocol errors or HTTP 408/500/502/503/504, with 100/200/400 ms backoff. Authorization, validation, conflicts, capacity errors and malformed responses stop retries. Cancellation stops requests and backoff; shutdown release retains its total five-second budget. Receipt retries require a server supporting settlement receipts (ADR 1055); they do not make application side effects exactly-once. Disable custom transport retries for these endpoints and configure timeouts above the long-poll wait.

Opt in to terminal settlement batching with `settlement_batch_size=2..32` (default 1 disables batching). `settlement_flush_milliseconds=1..100` defaults to 10. Flush when the size limit or delay is reached; waiting messages retain their concurrency slots, bounding pending work by concurrency. Complete/retry requests share a batch; renewals and releases remain immediate single requests. Batch receipt retries reuse one top-level UUID and ordered body, with no item-level keys. Every response must match the requested count, sequence and action; successful items confirm independently while `lease_lost` raises for its item and stops further claims. Handler-error callbacks run only after confirmed retry/dead results. A batch has a five-second total request/retry budget. Graceful shutdown flushes queued settlements and awaits in-flight batches; internal failure cancels requests and discards unsent batches. Queued or uncertain terminal settlements are never followed by release. Lease expiry can still occur during the flush delay; batching does not extend a lease.

Set `processing_timeout_seconds` to a finite number from 0.1 to 86400 to limit handler processing time; the default `None` disables it. The monotonic budget starts when the handler is invoked and excludes settlement waiting/transport. On timeout, the helper sets `context.stop_event`, cancels renewal and emits `handler_timeout` once. It continues awaiting the handler and its effects; it does not force-cancel the handler. If the handler exits while the lease/deadline remains valid, retry with reserved failure code `handler_timeout` (the custom classifier is bypassed), using your existing retry-delay/group-policy and batching options. `on_handler_error` receives a `TimeoutError` only after confirmed retry/dead settlement.

The local lease/deadline watcher remains active: expiry stops the worker and no retry is attempted with a known expired lease. A handler that ignores the stop signal can still retain its concurrency slot and delay shutdown indefinitely, although it no longer receives renewals. Cancellation of an in-flight renewal cannot undo an extension already accepted by the server. Caller shutdown uses existing release rules after the handler exits safely. Blocking the event loop delays signalling; an over-budget handler returning before the timer runs is still treated as timed out.

Optional lifecycle monitoring uses `RealtimeWorkerOptions(on_event=record_event)`, where `record_event(event: RealtimeWorkerEvent) -> None` is a synchronous, nonblocking callback. The immutable event reports `claimed`, `settled`, `lease_lost`, `handler_timeout`, `draining` or `shutdown`. Per-claim events include group UUID, sequence and delivery attempt; settlements/loss include monotonic `duration_seconds` since the helper began processing that claim, including settlement waiting/transport. Settlements include confirmed action and status. Shutdown includes `stopped`, `cancelled`, `failed`, `blocked` or `drained` and the observed group UUID when available. No payload, metadata, worker identity, fencing token, claim token, credentials or exception details are included.

```python
from faas_sdk.preview import RealtimeWorkerEvent, RealtimeWorkerOptions

def record_event(event: RealtimeWorkerEvent) -> None:
    # Use your application's bounded, nonblocking monitoring queue.
    try:
        monitoring_queue.put_nowait(event)
    except asyncio.QueueFull:
        pass

options = RealtimeWorkerOptions(on_event=record_event)
```

Hook exceptions (including callback-raised cancellation) are ignored and do not change delivery. Hooks are not awaited; accidentally returned coroutine objects are closed. Keep hooks short: synchronous blocking work delays the event loop and lease renewal. Emit `settled` only after a valid server confirmation; retries do not produce extra events, and uncertain requests produce none. Lease loss is emitted once per claim. Shutdown is emitted after owned-task cleanup, including an already-set stop event. Notifications are process-local observations, not a durable audit stream; use server history for durable inspection. Avoid sequence IDs as unbounded metric labels.

Pass a separate `drain_event=asyncio.Event()` to either worker entry point for graceful deployment draining. Calling `drain_event.set()` latches drain mode: stop issuing claims, keep renewing active leases, let handlers finish without setting their context stop events, and flush/confirm terminal settlements. A claim request already in flight is allowed to finish (up to your HTTP timeout/long-poll wait); returned claims are processed as owned work. An already-set drain event returns without HTTP requests. Clearing the event does not resume this worker; start a new worker invocation.

```python
drain = asyncio.Event()
stop = asyncio.Event()
worker_task = asyncio.create_task(
    consume_realtime_consumer_group_with_credential(
        client, group_id=group_id, on_message=handle_event,
        drain_event=drain, stop_event=stop,
    )
)
# Deployment controller requests a drain; the task returns after owned work settles.
drain.set()
await worker_task
```

Lifecycle hooks emit `draining` once and final shutdown reason `drained` on successful drain. Infrastructure/lease errors still fail the worker; retention/capacity blockers remain errors. Heartbeats continue while draining. Processing timeouts still signal/retry individual handlers. `stop_event.set()` or task cancellation interrupts a drain using the existing cooperative stop/release behavior. Drain has no automatic deadline; a stuck handler can hold it indefinitely. It drains this invocation's owned work, not the whole group backlog.

Set `stop_event` for normal shutdown. Cancelling the worker task also signals handlers, awaits owned tasks, then re-raises cancellation. With `release_on_shutdown=True`, still-valid leases release only after handlers and monitors stop, with a separate five-second request budget. Failed releases raise; internal failures and known lease loss leave work to server expiry. A terminal settlement already started is never followed by release. Unknown leases from cancelled claim requests expire normally. A handler that ignores its stop event can delay shutdown indefinitely. No backend migration is needed; runtime tests remain deferred.

### Expiring typing and named activity

Gregale already supports presence snapshots/change events, principal-scoped grouping of multiple sessions, and transient named signals. `RealtimeActivityTracker` adds Python-side expiry and ordering for signals delivered by your authenticated realtime transport; it does not open a WebSocket, join a channel or grant access. Construct it inside a running asyncio loop and use it only on that loop.

```python
from faas_sdk.preview import RealtimeActivity, RealtimeActivityTracker

# Called inside your async client lifecycle.
tracker = RealtimeActivityTracker(on_change=render_activity, max_entries=1024)

# Your existing authenticated transport's decoded named signal frame:
def on_signal(frame):
    if not frame.get("name"):
        return
    tracker.apply(RealtimeActivity(
        channel=frame["channel"], member_id=frame["member_id"],
        name=frame["name"], data=frame["data"],
        updated_at=frame["updated_at"], expires_at=frame["expires_at"],
    ))

# Presence leave: tracker.remove_member(channel, member_id)
# Reconnect/unsubscribe: tracker.reset(channel)
# Client shutdown: tracker.dispose()
```

Snapshots and synchronous `on_change` callbacks receive isolated activity data. Callback exceptions are ignored; callbacks must return quickly (async callbacks are unsupported). An asyncio timer removes stale typing even when no more frames arrive. Zero TTL clears an activity. Keys are channel/member/name; stale and duplicate revisions are ignored, with RFC3339 nanosecond ordering preserved. Updates outside a 30-second clock window are ignored; synchronize client/server clocks. Data is capped at 2048 JSON UTF-8 bytes and TTL at 30 seconds. Capacity defaults to 1024 identities including tombstones, configurable 1–8192; full capacity raises rather than evicting live state or ordering memory.

Use principal-scoped subscriptions consistently to group sessions by the verified user; connection-scoped subscriptions keep separate identities. Presence departure removes activity while retaining ordering markers briefly. Reconnect reset deliberately clears ordering because transient signals are not replayed. `snapshot()` also prunes expiry if the event loop was delayed. `dispose()` cancels the timer, clears state and rejects later updates. This is best-effort activity, not durable delivery or a replacement for server authorization. No migration is added; runtime validation remains deferred.

### Presence directory with activity

`RealtimePresenceDirectory` combines authenticated presence events with expiring named signals. It owns an activity tracker and calls one synchronous, nonblocking `on_change(summary)` callback when presence or activity changes. Construct/use it on one running asyncio loop. It opens no connection; wire it to your transport's presence/signal callbacks.

```python
from faas_sdk.preview import (
    RealtimeActivity, RealtimePresenceDirectory,
    RealtimePresenceEvent, RealtimePresenceMember,
)

directory = RealtimePresenceDirectory(on_change=render_summary)

def member(value):
    return RealtimePresenceMember(
        member_id=value["member_id"], state=value["state"],
        connection_count=value["connection_count"],
    )

def on_presence(frame):
    directory.apply(RealtimePresenceEvent(
        channel=frame["channel"], event=frame["event"],
        members=tuple(member(value) for value in frame.get("members", [])),
        member=member(frame["member"]) if frame.get("member") else None,
        member_id=frame.get("member_id"), complete=frame.get("complete", False),
    ))

def on_named_signal(frame):
    if frame.get("name"):
        directory.apply_activity(RealtimeActivity(
            channel=frame["channel"], member_id=frame["member_id"],
            name=frame["name"], data=frame["data"],
            updated_at=frame["updated_at"], expires_at=frame["expires_at"],
        ))

# summary = directory.snapshot("room-a")
# summary.viewer_count counts member identities; summary.connection_count counts sessions.
# Each summary.members entry contains isolated state and sorted, unexpired activities.
# Reconnect/unsubscribe: directory.reset(channel)
# Shutdown: directory.dispose()
```

Incomplete snapshot chunks remain staged; only `complete=True` replaces the visible directory. Live join/update/leave changes also update an in-progress snapshot. Completed snapshots replace membership and remove departed members' activity. Presence leave does the same, retaining the activity tracker's stale-packet protection. Member IDs are sorted and come from the server; use principal-scoped subscriptions to group sessions. State/labels are untrusted presentation data. Activity from members not in the directory is excluded from combined summaries.

Presence state is limited to a 512-byte UTF-8 JSON object, and connection counts to 1–512. `max_members` defaults to 1024 (1–8192) and counts both visible and staged entries across channels; channel count is bounded too. Invalid/capacity-exceeding events raise before replacing visible state. `max_activities` independently bounds the owned activity tracker (default 1024). Callback exceptions are ignored and callbacks must return quickly; summaries are isolated copies. Reset clears both presence and activity, while dispose also cancels expiry timers and rejects future ingestion. Presence membership itself follows server events; reset on transport loss/reconnect to avoid stale online state. Runtime validation remains deferred; no migration is needed.

### Publishing temporary activity

`RealtimeActivityPublisher` coalesces frequent updates and refreshes the latest value at half its TTL until explicitly cleared or its optional idle timeout elapses. Construct/use it on one running asyncio loop. Supply `async send(name, data, ttl_ms)` using your existing authenticated transport; await the write and raise on rejection. The helper creates no connection and cannot impersonate another member.

```python
from faas_sdk.preview import RealtimeActivityPublisher

publisher = RealtimeActivityPublisher(
    send_authenticated_signal, name="typing",
    ttl_milliseconds=5000, interval_milliseconds=250,
    idle_timeout_seconds=3,
    on_error=report_activity_error,
)
try:
    publisher.update({"active": True})  # latest value copied; rapid calls coalesce
    # Call publisher.update(...) as activity data changes.
    # Activity remains refreshed until you explicitly stop it:
    await publisher.clear()  # waits for the current write, then sends null/TTL 0
finally:
    await publisher.aclose()
```

TTL supports integers 100–30000 ms. Throttle interval supports integers from 50 ms to half the TTL. Default TTL/interval are 5000/250 ms. Data is JSON within 2048 UTF-8 bytes. One background task owns refresh and transport writes; values are serialized on ingestion and freshly decoded for each send. Each write has a five-second budget; clear has a five-second total budget including waiting for the current write. Slow transports/event-loop blocking can still let remote state expire. Signals remain best effort, with no receipt retries or subscriber acknowledgement.

`idle_timeout_seconds=None` keeps the existing indefinite-refresh behavior. Set a finite value from 0.1 to 86400 seconds to automatically stop activity after the last successful `update()` call. Every accepted update resets the monotonic idle deadline, even when data is unchanged; refresh writes do not reset it. When idle, the owned task sends null/TTL zero after any active write, then stops refreshing. The next update restarts a healthy publisher. If an update arrives during the idle clear, its latest value is retained and published afterward, provided it has not itself gone idle. Both the generic publisher and HTTP factory accept this option.

Idle timing is independent of the remote TTL: it determines when to stop publication, while TTL removes stale remote state if the clear cannot be delivered. A slow sender can delay the clear; each write keeps its five-second budget and errors use the existing failure callback/state. No extra timer/task or automatic transport retry is added.

Transport failures stop refreshing, set the sanitized `publisher.error`, and invoke the optional synchronous `on_error`; callback exceptions are ignored. Further updates on a failed publisher raise; close and create a new publisher after fixing the transport. `clear()` pauses publication and sends an explicit clear; later updates may restart a healthy publisher. Updates during clear are rejected. `aclose()` permanently disposes and clears by default; `aclose(send_clear=False)` cancels local work and lets remote TTL expire. Await close before closing your transport/client. Do not call clear/close concurrently. Cancellation can leave a remote write uncertain; TTL provides eventual cleanup. A sender must cooperate with cancellation.

For account-authenticated **backend** activity, use `create_managed_realtime_activity_publisher(client, slug=..., endpoint_id=..., channel=..., name=..., activity_scope=...)`. The factory wraps existing HTTP routes (scope optional) and uses server member ID `backend`, requiring existing deploy-write/MFA authorization. Backend activity is excluded from presence-directory summaries because it is not an online user; inspect a standalone activity tracker instead. Disable custom automatic mutation retries. No migration is needed; runtime validation remains deferred.

### Activity summaries and typing labels

`summarize_realtime_activity` projects a `RealtimePresenceSummary` into sorted `viewers` and `active` members with display labels. Filter by activity names and exclude the current verified member ID. Returned data is isolated; expired activities are omitted even if the input snapshot was captured earlier. Filters affect activity, not viewer membership; exclusion removes the member from both lists and their counts.

```python
from faas_sdk.preview import summarize_realtime_activity, format_realtime_typing_summary

def render_summary(presence):
    summary = summarize_realtime_activity(
        presence, names=("typing", "editing"),
        exclude_member_id=current_member_id,
        label=lambda member: display_names.get(member.member_id, member.member_id),
    )
    typing_text = format_realtime_typing_summary(summary, max_labels=1)
    # Examples: "Alice is typing." / "Alice and 2 others are typing." / ""
    # summary.viewer_count counts included identities, connection_count counts their sessions.
    # Render summary.active directly for editing status or localized text.

# directory = RealtimePresenceDirectory(on_change=render_summary)
```

Labels default to member IDs and must be strings at most 512 characters. The label callback receives copied member state; its errors propagate to the caller (directory monitoring callbacks retain their existing error isolation). Labels/state are presentation data, not authenticated identity: escape them for your UI context. Activity names accept at most 64 values; summaries accept up to 8192 members/activities and reject duplicate members or named activity within a member. `names=()` selects no activity while preserving included viewers.

`format_realtime_typing_summary` supports `max_labels=1..10`, returns an empty string when nobody is typing, and checks expiry again before formatting. It produces English text; use summary.active for localization. Formatting creates no timers, connections or subscriptions. No migration is needed; runtime validation remains deferred.

### Private activity scopes

Use `realtime_activity_scope_channel(parent_channel, scope)` to build the canonical channel for document- or thread-specific activity. It matches the server's unpadded base64url encoding of UTF-8 values. Your authenticated transport subscribes to that channel; publishing through the scoped HTTP route still takes the original parent channel and scope separately.

```python
from faas_sdk.preview import (
    realtime_activity_scope_channel, parse_realtime_activity_scope,
    create_managed_realtime_activity_publisher,
)

parent = "room-a"
scope = "document-42"
activity_channel = realtime_activity_scope_channel(parent, scope)
# Subscribe using activity_channel with your existing authenticated transport.
# parse_realtime_activity_scope(activity_channel) returns (parent, scope).
# parse_realtime_activity_scope(parent) returns None.

publisher = create_managed_realtime_activity_publisher(
    client, slug=slug, endpoint_id=endpoint_id,
    channel=parent, activity_scope=scope, name="editing",
    idle_timeout_seconds=3,
)
# Feed scoped presence/signals into your directory using activity_channel.
# Await publisher.aclose() before closing client.
```

Parents/scopes must be nonempty valid UTF-8 without leading/trailing Unicode whitespace, NUL, `/`, `?`, `#`, CR or LF. Parent channels allow at most 256 UTF-8 bytes; scopes at most 64; the complete encoded channel must fit 256 bytes. Scoped parents are rejected. The HTTP publisher validates the parent/scope combination synchronously during construction, before any request.

`parse_realtime_activity_scope` returns None for ordinary channels and raises ValueError for malformed reserved channels, including padding, invalid UTF-8, nesting and noncanonical encodings. Use the exported `REALTIME_ACTIVITY_SCOPE_PREFIX` only for recognizing reserved channels; use the builder to generate them. Encoding does not grant access: subscribers still need the server's separate `read_activity` authorization grant, and the parent channel does not imply scope access. HTTP activity remains published as backend. No migration is needed; runtime checks remain deferred.

### Confirmed read-progress tracking

`RealtimeReadProgressTracker` maintains bounded, monotonic local views of authenticated server receipts. It does not mark messages as read, subscribe, estimate unread counts or write server state. Feed the server's confirmed values and hashed principal key unchanged. Multiple sessions share that reader identity.

```python
from faas_sdk.preview import RealtimeReadProgress, RealtimeReadProgressTracker

tracker = RealtimeReadProgressTracker(on_change=render_read_badges)

def on_read_receipt(frame):
    is_inbox = frame.get("inbox", False)
    tracker.apply(RealtimeReadProgress(
        reader_id=frame["member_id"], inbox=is_inbox,
        channel=None if is_inbox else frame["channel"],
        sequence=frame["sequence"], unread=frame["unread"],
        oldest_sequence=frame["oldest_sequence"],
        latest_sequence=frame["latest_sequence"],
        history_unavailable=frame.get("history_unavailable", False),
    ))

# Channel-only views: tracker.snapshot(channel="room-a")
# Inbox views: tracker.snapshot(inbox=True)
# Reconnect/stream replacement: tracker.reset(channel="room-a") or tracker.reset()
# Client shutdown: tracker.dispose()
```

Keys separate channel/inbox streams and the server's 64-character lowercase SHA-256 reader ID. Exact duplicates and updates that move read/latest/oldest sequence bounds backward are ignored. Equal sequence bounds with changed unread/history status are accepted: edits/deletions can alter unread counts without advancing read progress. The wire protocol supplies no revision for ordering such equal-bound count changes, so refresh confirmed read state if a transport may reorder them. The tracker never invents progress or clears a history gap.

Default capacity is 1024 stream/reader entries, configurable 1–8192. Overflow and malformed receipts raise before mutation. Counts use exact nonnegative signed-64-bit Python integers; booleans/floats are rejected. Receipts and snapshots are immutable and sorted. Supply a synchronous nonblocking callback; callback exceptions are ignored. Use from one execution context. Reset local state on reconnect before feeding fresh receipts; it never resets durable server read markers. Inbox receipts must come only from the authenticated principal's authorized transport; this helper adds no authorization. No migration is added; runtime validation remains deferred.

### Coalesced explicit read updates

`RealtimeReadProgressPublisher` coalesces explicitly viewed sequences for one reader/stream. Supply an authenticated async `send(sequence)` that awaits its write and raises on failure; the helper owns no connection. Receiving or ACKing a message alone must not call mark_read.

```python
from faas_sdk.preview import RealtimeReadProgressPublisher

publisher = RealtimeReadProgressPublisher(send_authenticated_read, interval_milliseconds=250)
try:
    publisher.mark_read(42)  # person actually viewed through sequence 42
    publisher.mark_read(47)  # only highest pending sequence retained
    # pending_sequence is local pending work, never a confirmed read receipt.
    await publisher.flush()
finally:
    await publisher.aclose()  # flush latest pending progress before closing transport
```

Throttle supports integers 50–30000 ms (default 250). The first send runs promptly in the owned task; rapid changes coalesce to the highest accepted sequence. Duplicate/older positions return False and do not send. Exact nonnegative signed-64-bit sequences are required; zero is a no-op baseline. One task serializes writes, each with a five-second timeout. flush bypasses throttle and waits for all pending writes with a five-second total budget. aclose flushes by default; aclose(flush=False) cancels local work. Bind a new instance after changing reader or stream.

Transport failure stops publication and exposes a sanitized error plus optional synchronous on_error callback. Further marks raise; inspect pending_sequence and refresh authoritative state before constructing a replacement. No automatic retries are added. Cancelling/timing out flush stops/joins the owned task and fails closed; an in-flight write may still have been accepted remotely. A sender must cooperate with cancellation. Await close before closing transport and avoid concurrent flush/close operations. Pending values and successful transport handoff never update RealtimeReadProgressTracker; feed only confirmed server receipts there.

`create_managed_realtime_read_publisher(client, slug=..., endpoint_id=..., principal=..., channel=...)` wraps existing account channel read-progress POSTs; omit channel for the principal inbox. These are backend assertions with deployment-write/MFA checks, not a user WebSocket identity. The factory validates successful server response sequence against the assertion, but does not synthesize badge counts or hashed reader IDs. Refresh existing read-progress APIs/receipts for confirmed UI state. The caller owns the client. No migration is added; runtime validation remains deferred.

### Aggregate unread summaries

`summarize_realtime_unread` filters confirmed receipts to one hashed reader ID and optional channels/inbox, then exposes per-stream counts and retention gaps without fetching or estimating anything. Use receipts from one endpoint only.

```python
from faas_sdk.preview import summarize_realtime_unread

def render_read_badges(receipts):
    summary = summarize_realtime_unread(
        receipts, reader_id=current_reader_id,
        channels=("room-a", "room-b"), include_inbox=True,
    )
    # summary.total_unread: sum of known selected per-stream counts
    # summary.streams: sorted confirmed receipts with per-stream unread counts
    # summary.unread_stream_count: selected known streams with unread > 0
    # summary.history_gaps: receipts whose history_unavailable flag is true
    # summary.missing_channels / inbox_missing: requested data not loaded
    # summary.complete: requested receipts present, independent of retention gaps/freshness

# tracker = RealtimeReadProgressTracker(on_change=render_read_badges)
```

`channels=None` includes all available channel receipts and sets `complete=None` because the helper cannot know whether that covers every channel. An explicit channel tuple enables missing-channel reporting; `channels=()` selects no channels. `include_inbox` defaults to True; omitted inbox receipts are reported as missing rather than invented as zero. Missing data contributes nothing to the known-count sum, so label incomplete totals accordingly. A complete summary can still have retention gaps or stale receipts; no freshness guarantee is inferred.

Other readers never contribute. Duplicate stream/reader receipts, duplicate selected channels and malformed entries raise instead of double-counting. Inputs are bounded to 8192 receipts/channel selections and snapshots remain immutable. Counts use exact Python integers; totals can exceed a single receipt's signed-64-bit bound. Counts are summed per stream, without deduplicating a logical message copied between a channel and inbox. Keep endpoint/account sources separate. No requests, timers, read writes or migration are added; runtime validation remains deferred.

### Read-progress freshness

Configure `RealtimeReadProgressTracker(stale_after_seconds=30, on_change=..., on_refresh=...)` to distinguish recently confirmed unread counts from stale local state. The timeout accepts finite 0.1–86400 seconds; None disables age-based staleness by default. `freshness_snapshot()` returns immutable RealtimeReadProgressFreshness entries with progress, monotonic age_seconds and stale.

```python
def request_read_refresh(stale_receipts):
    # Enqueue refreshes through your existing authenticated transport.
    for receipt in stale_receipts:
        refresh_queue.put_nowait(receipt)

tracker = RealtimeReadProgressTracker(
    on_change=render_read_badges, stale_after_seconds=30,
    on_refresh=request_read_refresh,
)
# On disconnect: tracker.mark_stale()  # retain last confirmed counts
# On a UI/poll tick: tracker.refresh_stale()  # returns stale receipts; calls on_refresh

summary = summarize_realtime_unread(
    tracker.snapshot(), reader_id=current_reader_id,
    channels=("room-a",), include_inbox=False,
    freshness=tracker.freshness_snapshot(),
)
# summary.stale_streams: selected confirmed counts whose freshness is stale/unknown
```

Exact duplicate confirmed receipts renew age and clear forced staleness, even though apply returns False because progress did not change. Backward sequence-bound receipts are ignored without refreshing age. mark_stale supports the same channel/inbox filters as snapshots; a subsequent valid receipt clears that stream's forced flag. reset/dispose remove age/order memory. Requesting refresh does not make data fresh or write read positions. The on_refresh callback is synchronous and must enqueue nonblocking work; callback errors are isolated. Repeated calls can request repeated refreshes, so debounce through your application's transport queue.

Freshness is evaluated when snapshots/refresh_stale are called; no background timer or automatic network request is created. Drive periodic checks from your existing UI or transport lifecycle. Captured freshness metadata is a point-in-time view; take a new snapshot when rendering later. The unread summary retains last confirmed totals and reports stale_streams separately; None means no freshness metadata was supplied. Missing or mismatched freshness for a selected receipt is treated as stale. Completeness still means receipt coverage, not freshness or gap-free history. No server migration is needed; runtime validation remains deferred.

### Coordinating stale read refreshes

`RealtimeReadRefreshCoordinator` bounds and coalesces read-state fetches through an application-owned authenticated transport. Its fetch callback takes the requested confirmed receipt and returns a fresh `RealtimeReadProgress` with the same reader/stream. Bind the tracker/fetcher to one endpoint/account, and construct/use on one running asyncio loop.

```python
from faas_sdk.preview import RealtimeReadProgressTracker, RealtimeReadRefreshCoordinator

tracker = RealtimeReadProgressTracker(
    on_change=render_read_badges, stale_after_seconds=30,
    on_refresh=lambda receipts: coordinator.request(receipts),
)
coordinator = RealtimeReadRefreshCoordinator(
    tracker, fetch_authenticated_read_state,
    concurrency=4, max_pending=1024, timeout_seconds=5,
    on_error=report_read_refresh_error,
)
try:
    tracker.refresh_stale()
    await coordinator.wait_idle()
finally:
    await coordinator.aclose()  # before closing the application transport
```

`request(receipts)` admits distinct identities atomically, returning newly admitted count. Repeated queued requests replace their input; in-flight requests coalesce without a second fetch. `max_pending` counts queued plus active identities (default 1024, range 1–8192); concurrency is 1–32 (default 4); per-request timeout is finite 0.1–60 seconds (default 5). Overflow raises without partially admitting the batch. One request runs per identity, with at most concurrency owned tasks.

Validate confirmed response identity and nonregressing bounds before applying it to the tracker. Tracker monotonic rules still reject responses older than newer live receipts; valid duplicates renew freshness. Failures invoke optional synchronous `on_error(requested_receipt, sanitized_error)` and leave existing state unchanged. No automatic retry is added; later calls can request another refresh. Callback errors are isolated. Resetting any tracker scope invalidates all previously queued/in-flight refresh generations; disposing the tracker also prevents application.

`wait_idle()` waits for admitted work and does not raise per-request errors; cancellation of that waiter leaves owned requests running. `aclose()` permanently stops admission, drops queued requests and cancels/joins active ones. Senders must cooperate with cancellation. No read positions are advanced, raw principal IDs inferred, authorization granted or automatic polling started. Runtime validation remains deferred; no migration is needed.

### Unread badge presentation

`format_realtime_unread_badge(summary, max_count=99, show_zero=False)` returns immutable `RealtimeUnreadBadge` presentation data.

```python
from faas_sdk.preview import format_realtime_unread_badge

badge = format_realtime_unread_badge(summary, max_count=99)
# badge.text: "12", "99+", "Updating…", or "" for confirmed zero
# badge.accessible_label: full count plus freshness/history caveats
# badge.history_unavailable: render your retention-gap indicator
# badge.visible / updating / capped: explicit presentation flags
```

The visible count is capped only when it exceeds max_count (integer 1–999999); accessible text retains the exact known count and singular/plural. show_zero=True displays confirmed zero. Missing requested data, unspecified channel coverage or missing/stale freshness metadata produces Updating… instead of suggesting a confirmed count. This is a presentation state, not an automatic refresh request. `count_text` separately retains the capped known count and `total_unread` its exact value for custom UI.

Retention gaps are independent: expose history_unavailable and explain that counts cover retained messages. A gap keeps a confirmed-zero badge visible. Accessible text distinguishes known counts from complete/fresh counts. Strings are English; use structured fields for localization. Supply explicit channels and current freshness metadata when building a summary to display a confirmed badge. No timer, request, read assertion or migration is added; runtime validation remains deferred.

### Realtime connection-status observation

`RealtimeConnectionStatusTracker` observes your existing transport; it does not open sockets, retry, poll or own network tasks. Use from one execution context and feed readiness/failure callbacks with the attempt ID returned by begin_connect.

```python
from faas_sdk.preview import RealtimeConnectionStatusTracker

def invalidate_views():
    read_tracker.mark_stale()  # retain counts, mark them unverified
    presence_directory.reset()  # ephemeral membership must be rebuilt

status_tracker = RealtimeConnectionStatusTracker(
    on_change=render_connection_status, on_invalidate=invalidate_views,
)
attempt = status_tracker.begin_connect()
# Capture this attempt in your transport's callbacks:
# After authentication/subscription readiness: status_tracker.connected(attempt)
# On failure or closure: status_tracker.disconnected(attempt)
# On a new retry, call begin_connect and capture its new ID.
# Client shutdown: status_tracker.dispose()
```

Immutable status exposes connecting/connected/reconnecting/offline, last_connected_at (aware UTC or None), reconnect_attempts and views_stale. Initial state is offline. The first attempt is connecting with zero reconnects; every later begin_connect increments reconnect attempts since the last success, which resets the count to zero. An attempt's duplicate or late connected/disconnected callbacks return False, and an offline attempt cannot become connected without a new attempt. Last successful connection time remains visible during outages.

views_stale is a transport-level warning whenever disconnected/connecting; connected does not itself make unread receipts fresh or restore presence. Apply confirmed receipts and completed presence snapshots through existing helpers. on_invalidate runs before the status callback on each transition to a nonconnected state, including retries, so keep it idempotent and nonblocking. Callbacks are synchronous, exceptions are isolated and accidental coroutine results are closed. Coordinate refresh cancellation/reset with your transport lifecycle; this observer does not cancel its requests. Dispose leaves offline state and rejects new attempts while ignoring late callbacks. No backend migration or runtime validation is added.

### Observing realtime connection quality

`RealtimeConnectionQualityTracker` observes your authenticated transport's frames and correlated heartbeat responses. It sends no heartbeat or retry and owns no timer/task. Use on one execution context; call snapshot periodically from an existing UI/transport tick to detect silence even without new frames.

```python
from faas_sdk.preview import RealtimeConnectionQualityTracker

quality = RealtimeConnectionQualityTracker(
    on_change=render_quality, degraded_latency_milliseconds=1000,
    recovery_latency_milliseconds=500, silence_seconds=30,
    heartbeat_timeout_seconds=10,
)
# After your status tracker confirms readiness:
quality.start(attempt_id)
# For each received authenticated frame: quality.frame_received(attempt_id)
# When an actual heartbeat is sent, retain its local probe token:
probe = quality.heartbeat_sent(attempt_id)
# After the matching response: quality.heartbeat_received(attempt_id, probe)
# Existing periodic tick: current = quality.snapshot()
# Failure/closure: quality.disconnected(attempt_id)
# Client shutdown: quality.dispose()
```

Snapshots report unknown/healthy/degraded/offline, latest round-trip latency, age since the last received frame (or ready connection if none yet), and age of the oldest unanswered heartbeat. Before a latency sample, a connected quiet transport is unknown; silence or pending-heartbeat timeout can still degrade it. After a valid response, classify latency with hysteresis: at/above degraded threshold marks degraded, and at/below recovery threshold clears latency degradation. Ordinary frames clear silence but not a high-latency warning or outstanding heartbeat.

Latency thresholds accept finite 1–60000 ms; recovery must not exceed degradation. Silence/heartbeat timeout accept finite 0.1–86400 seconds. Defaults are 1000/500 ms and 30/10 seconds. Monotonic timing avoids wall-clock jumps. New attempts reset measurements and ignore old callbacks. A newer probe replaces the correlation token but preserves the oldest unanswered time, so repeated sends cannot mask missing responses. Use transport-provided correlation; never treat an arbitrary received frame as a heartbeat response.

Callbacks fire on quality transitions, new attempts and valid latency samples; callback failures are isolated. snapshot only emits on transitions, not every age tick. Degraded does not disconnect or prove server failure: idle traffic, missing heartbeats or event-loop blocking can affect observations. Offline retains the last latency for diagnosis; age fields become None. UI updates/read freshness remain application-owned. No backend migration or runtime checks are added.

### Combined realtime health

`summarize_realtime_health` builds one immutable UI view from current connection status, optional quality/unread summaries and presence snapshots, without opening transport or driving refresh.

```python
from faas_sdk.preview import summarize_realtime_health

def render_health():
    health = summarize_realtime_health(
        status_tracker.snapshot(), quality=quality_tracker.snapshot(),
        unread=current_unread_summary(),
        presence=(presence_directory.snapshot("room-a"),),
    )
    # health.label: Offline / Connecting / Reconnecting / Delayed / Updating / Live
    # health.reasons: quality_unknown, connection_degraded, unread_unverified,
    #                presence_pending, history_unavailable
    # health.unread_badge: structured count/freshness/accessibility fields
    # health.waiting_presence_channels / presence_ready / history_unavailable
    update_ui(health)

# Call from existing tracker callbacks and UI ticks.
```

Connection state takes priority, then current-attempt degraded quality, then unverified read counts/pending presence. Quality from an older attempt is excluded and flagged unknown. Retention gaps are a separate warning and do not claim a transport outage; the state can be Live for retained data while history_unavailable remains True. Optional observations omitted by the application are untracked, not verified fresh; presence_ready is None when no presence was supplied.

Presence summaries now include ready. Only a completed server snapshot sets readiness; joins/updates alone cannot prove complete membership. Incomplete chunks preserve visible members but mark pending; reset clears readiness even for empty completed snapshots and notifies observers. Nonconnected transport makes supplied presence unavailable for health readiness. On reconnect, reset directories through the status invalidation hook before accepting new snapshots. The helper cannot identify stale presence from a prior attempt if the application skips reset. viewer_count sums channel membership counts, so a user present in two channels counts twice.

Supply observations from one account/endpoint and regenerate freshness/quality on each UI tick. The helper owns no timer, subscription or callback dispatcher; labels are English and structured state/reasons support localization. No backend migration is added; runtime checks remain deferred.

### Reconnect catch-up progress

RealtimeCatchUpTracker observes one channel or principal inbox against an explicit replay target. It does not fetch, replay, ACK, advance read state or perform resync. The subscribed acknowledgement supplies a resume cursor, not a target: obtain a target from an authoritative retained-history/read-state snapshot or your transport.

```python
from faas_sdk.preview import RealtimeCatchUpTracker, summarize_realtime_health

catch_up = RealtimeCatchUpTracker(channel="room-a", on_change=render_catch_up)
generation = catch_up.begin(
    attempt_id, processed_sequence=restored_processed_cursor,
    target_sequence=authoritative_latest_sequence,
)
# Only after ordered application effects through this position finish successfully:
catch_up.processed(generation, processed_sequence)
# If target is not available at begin: catch_up.set_target(generation, target)
# On an explicit server gap:
# catch_up.require_resync(generation, oldest_sequence=oldest, latest_sequence=latest)
health = summarize_realtime_health(status_tracker.snapshot(), catch_up=(catch_up.snapshot(),))
```

States are idle/waiting_target/catching_up/caught_up/resync_required. A frozen target is reached when the successfully processed position reaches or exceeds it. Distance is remaining_sequence_distance, not a message count: filters, deletions and sequence gaps make those different. Count only completed ordered application work, never raw receipt or an out-of-order handler's maximum sequence. Duplicate/older processing updates and old generations are ignored.

Every begin creates a new local generation, including resubscription on the same connection attempt; older connection attempts are rejected. Capture the generation in all callbacks. A gap blocks processing/target updates until the application completes recovery and begins a new generation with its recovered baseline. The helper does not silently skip history. Disposing rejects begin and ignores late callbacks. Use inbox=True without channel for a principal inbox. Bind inputs to one endpoint/account.

Health shows Catching up or Resync required for current-attempt observations; missing targets or older-attempt observations produce Updating. Offline/reconnecting remains highest priority; resync outranks degraded quality, which outranks catching up. No migration or runtime validation is added.

### Detecting stalled catch-up

Set `RealtimeCatchUpTracker(..., stall_after_seconds=30)` to flag replay processing that stops advancing before its frozen target. None disables detection by default; finite 0.1–86400 seconds are accepted.

```python
catch_up = RealtimeCatchUpTracker(
    channel="room-a", on_change=render_catch_up, stall_after_seconds=30,
)
# Existing UI/lifecycle tick:
progress = catch_up.snapshot()
health = summarize_realtime_health(status_tracker.snapshot(), catch_up=(progress,))
# progress.state may be stalled; health.label becomes "Catch-up stalled".
# progress.progress_age_seconds: time since interval began or real progress advanced
# progress.stall_seconds: time beyond the configured no-progress threshold
```

Detection applies only when a target is known and processing remains below it. Beginning catch-up or installing its first target starts the interval. Strictly advancing processed positions reset it and return to catching_up/caught_up; duplicates, backward positions, frames and heartbeats do not. Waiting for a target, caught-up and resync-required states are not timed as processing stalls. New generations reset timing, and dispose freezes observation.

`snapshot()` evaluates elapsed time and emits a callback only on transition into stalled. It owns no background timer: poll through existing UI/transport ticks to detect silence. Progress callbacks report recovery normally. Take a fresh snapshot when rendering later; captured elapsed values are point-in-time. Health priority is connection outage, resync required, catch-up stalled, degraded transport and then normal catch-up/data readiness. Stalls are warnings, not proof of failure; long healthy processing may exceed the threshold. No ACK/replay/retry/resync policy changes or migration are added; runtime checks remain deferred.

### Subscription readiness

`RealtimeSubscriptionTracker` observes your transport's requested channel/inbox set separately from connection readiness. It sends no subscription, retry or authorization requests.

```python
from faas_sdk.preview import RealtimeSubscriptionTracker, summarize_realtime_health

subscriptions = RealtimeSubscriptionTracker(
    on_change=render_subscriptions, acknowledgement_timeout_seconds=10,
)
generation = subscriptions.begin(attempt_id, channels=("room-a", "room-b"), include_inbox=True)
# Capture generation in callbacks after issuing these subscriptions:
subscriptions.subscribed(generation, channel="room-a")
# subscriptions.denied(generation, channel="room-b", code="forbidden")
# subscriptions.require_resync(generation, inbox=True)
health = summarize_realtime_health(
    status_tracker.snapshot(), subscriptions=subscriptions.snapshot(),
)
# Reconnect invalidation: subscriptions.reset()
# Reissue the full required set via begin (also supported on the same attempt).
# Shutdown: subscriptions.dispose()
```

Entries are pending/subscribed/denied/resync_required. Denial is an explicit application classification of server authorization failure, not every transport error. Resync can invalidate a previously subscribed stream. Failed entries cannot be revived by a late acknowledgement; recover and begin a new generation. Duplicate or stale callbacks return False; unrequested current-generation streams raise.

Capacity defaults to 1024 channel/inbox entries (1–8192); begin validates the complete set atomically and rejects duplicate channels/overflow. Every begin issues a local generation, even on the same connection attempt. Old connection attempts are ignored. Initial/reset summaries are not ready; an explicitly begun empty required set is ready. reset invalidates callbacks and forgets requirements until begin reestablishes them.

Missing acknowledgements are flagged only if acknowledgement_timeout_seconds is configured (finite 0.1–86400; default None). snapshot reevaluates pending age on an existing UI tick, emitting a warning transition without a timer. Timeout keeps pending state; a later valid acknowledgement clears the warning. Callbacks are synchronous, nonblocking and error-isolated.

Health shows Subscription failed for explicit denial, Resync required for gaps, and Updating for pending/older-attempt requirements; reasons include subscription_ack_missing separately. Connection outages retain highest priority. Subscription readiness does not prove replay processing or presence snapshots complete. No migration or runtime validation is added.

### Waiting for subscription readiness

Await `subscriptions.wait_ready(generation)` after the application issues its required subscriptions. The helper observes tracker updates; it never subscribes or retries.

```python
from faas_sdk.preview import RealtimeSubscriptionWaitError

try:
    ready = await subscriptions.wait_ready(
        generation, channels=("room-a",), include_inbox=False, timeout_seconds=10,
    )
    # Only room-a is guaranteed subscribed in this returned selected summary.
    enable_room_actions()
except RealtimeSubscriptionWaitError as error:
    # error.reason: denied / resync_required / obsolete / disposed
    # error.subscriptions: selected terminal entries for denial/resync
    show_subscription_problem(error.reason)
except TimeoutError:
    show_pending_subscription()
```

By default channels=None selects all requested channels and include_inbox=None includes the inbox if requested. Explicit selections must already be requested; duplicates or unknown channels raise ValueError. Empty selections succeed immediately for a current active generation. A denial/resync in an unselected stream does not fail the selected wait. Denial takes precedence if selected streams contain both terminal types. Success means subscription acknowledgement, not processed replay, fresh unread state or ready presence.

Timeout accepts finite 0–86400 seconds or None (default waits indefinitely). Zero succeeds immediately if already ready, otherwise times out at the wait. TimeoutError and task cancellation leave subscriptions unchanged and remove owned waiter futures. Reset/new begin wakes old-generation waits with obsolete; dispose wakes them with disposed. State is checked before success and on every wake, so waits cannot silently follow reconnect generations.

Trackers support max_waiters=128 by default (1–8192); overflow raises before registering a waiter. All waits and state updates must run on one asyncio loop/execution context. No polling task, network request or automatic retry is added. Missing-ACK warnings remain separate from wait timeouts. Runtime validation remains deferred; no migration is needed.

### Awaiting catch-up completion

Await `catch_up.wait_caught_up(generation)` after subscription readiness to gate actions on successfully processed replay. It sends no ACK or replay request.

```python
from faas_sdk.preview import RealtimeCatchUpWaitError

try:
    caught_up = await catch_up.wait_caught_up(generation, timeout_seconds=30)
    enable_document_actions()
except RealtimeCatchUpWaitError as error:
    # reason: stalled / resync_required / obsolete / disposed
    # status: immutable progress observation for diagnosis
    show_catch_up_problem(error.reason)
except TimeoutError:
    show_catch_up_pending()
```

Success requires a known frozen target and processed progress at/above it. A wait begun while waiting_target remains pending until set_target supplies it. New begin generations fail older waits as obsolete; dispose wakes them as disposed; required resync is terminal. Default fail_on_stall=True reports a configured stall through RealtimeCatchUpWaitError. A wait owns only its deadline wakeup and observes the stall without a separate UI tick. Set fail_on_stall=False to keep waiting through a stall until progress/recovery, resync or overall timeout. This option neither clears the warning nor changes processing.

Timeout accepts finite 0–86400 seconds or None (default indefinite). Already caught-up state succeeds immediately even with zero timeout. TimeoutError/cancellation remove the waiter without advancing or resetting progress or affecting other waits. Active waits are bounded by max_waiters (default 128, range 1–8192); use one asyncio loop/execution context for waits and updates. No background polling task, transport retry or migration is added. Runtime validation remains deferred.

### Combined subscription and catch-up gate

`wait_realtime_ready` combines current connection readiness, selected subscription acknowledgements and processed replay targets under one deadline.

```python
from faas_sdk.preview import RealtimeCatchUpRequirement, RealtimeReadinessError, wait_realtime_ready

try:
    ready = await wait_realtime_ready(
        status_tracker, subscriptions, attempt_id=attempt_id,
        subscription_generation=subscription_generation, channels=("room-a",), include_inbox=False,
        catch_up=(RealtimeCatchUpRequirement(tracker=catch_up, generation=catch_up_generation),),
        timeout_seconds=30,
    )
    enable_document_actions()
except RealtimeReadinessError as error:
    # stage: connection / subscriptions / catch_up; reason is a safe terminal outcome
    show_readiness_problem(error.stage, error.reason)
except TimeoutError:
    show_readiness_pending()
```

All trackers must belong to one endpoint/account and loop. The gate requires an already-started current connection attempt and subscription generation; connecting/reconnecting may wait, while offline/disposal or replacement terminates it. Each catch-up requirement must match a selected channel/inbox and current attempt. Up to 32 distinct catch-up streams are supported. Catch-up may be omitted if only acknowledgements are required. Selected ACK success does not imply unselected channels ready. Targets remain caller-supplied and processing must be ordered/completed.

Default selection/timeouts/stall behavior match existing waits: channels=None chooses all requested channels, inbox defaults to requested membership, timeout is None or finite 0–86400, fail_on_stall defaults True. Structured errors preserve stage and reason: disconnected/obsolete/disposed, denied/resync_required, or stalled. One overall timeout covers all stages. The gate continues observing earlier completed stages and rechecks current generation/state before success.

Only bounded waiter futures/deadlines are owned; no network/poll/replay task is created. Per-tracker waiter bounds apply and connection gates are capped at 128 simultaneous pending waits. Cancellation/timeout remove only gate registrations and leave trackers/transport running. Success is a point-in-time view of supplied requirements, not a promise that future messages or outages cannot occur. No migration is added; runtime validation remains deferred.

### Local realtime diagnostics export

`export_realtime_diagnostics` returns versioned JSON from captured health and optional confirmed unread summaries. It does not upload, read files/environment variables or inspect a transport.

```python
from faas_sdk.preview import export_realtime_diagnostics

report_json = export_realtime_diagnostics(
    current_health_summary, unread=current_unread_summary, max_entries=256,
)
# Display or save report_json using your application's explicit export flow.
```

Format version 1 includes capture time (UTC), connection/quality, subscription statuses, catch-up progress, presence readiness/counts and unread freshness/gaps. With no unread detail argument, the health badge's known total/updating/gap flags remain available. The export reflects supplied point-in-time observations, not an atomic server snapshot.

Channel and hashed reader identifiers are replaced with channel_1/reader_1-style aliases by default. Aliases are consistent within the export and are not stable across exports. `include_identifiers=True` explicitly includes only those supplied identifiers; it never enables payload, metadata, presence state, worker/claim/fencing tokens, credentials, transport URLs, raw exception text, free-form diagnostic codes or display labels. Counts/times can still describe activity; redaction is not a claim of anonymous data.

Each array is capped at max_entries (default 256, range 1–8192), with omitted counts under truncated. Entire output is capped at 256 KiB; overflow raises and suggests reducing max_entries. Sequence and unread values are decimal strings to preserve integer precision across JSON consumers. Aggregate unread remains the full known sum even if detail rows are truncated. Omitted optional observations use null, not an invented healthy/zero value. No requests, timers or migration are added; runtime checks remain deferred.

### Bounded incident timeline

`RealtimeIncidentTimeline` records safe connection/subscription/catch-up transition observations from existing callbacks. It does not subscribe to trackers automatically or record payloads.

```python
from faas_sdk.preview import RealtimeIncidentTimeline, export_realtime_diagnostics

timeline = RealtimeIncidentTimeline(capacity=256, max_identities=1024)
# Wire these calls into your existing callbacks:
# timeline.record_connection(connection_status)
# timeline.record_subscriptions(subscription_summary)
# timeline.record_catch_up(catch_up_status)
report_json = export_realtime_diagnostics(current_health_summary, timeline=timeline)
# timeline.clear() resets events, deduplication memory, counters and elapsed origin.
```

Events have capture UTC time, monotonic elapsed seconds, local event ID, category/state, connection attempt, subscription generation and optional channel/inbox identity. No payload, presence state, reader ID, error code/body, credential, token, transport URL or display label is stored. Repeated identical state/attempt/generation/ACK-warning observations are suppressed; ordinary processed-position updates do not grow the history, while stalls and recovery do.

Capacity is 1–8192 (default 256), evicting oldest events and reporting evicted_count. Deduplication identities are independently bounded 1–8192 (default 1024), with least-recently-observed eviction; revisiting an evicted marker can record a repeated observation. Use from one execution context and one endpoint/account. Snapshot is immutable, oldest-first and contains supplied channel identifiers; share through diagnostics for default redaction rather than serializing it directly.

Diagnostics add optional timeline events under the existing version-1 format, retaining the newest max_entries in chronological order and reporting export truncation separately from ring eviction. Channel aliases use the same within-report mapping as other sections. Nothing is uploaded or persisted automatically. No timers, transport behavior or migration are added; runtime validation remains deferred.

### Reconnect and replay recovery

`RealtimeRecoveryCoordinator` orchestrates a caller-supplied authenticated
`RealtimeRecoverySession` for one channel or principal inbox. It opens a session,
subscribes from the last successfully processed cursor, replays through an
explicit authoritative history target, calls `on_ready`, then processes live
messages. Cancel its `run()` task to stop and join session cleanup. Read
`processed_sequence` to obtain the latest local completed position.

```python
from faas_sdk.preview import RealtimeRecoveryCoordinator, RealtimeIncidentTimeline

timeline = RealtimeIncidentTimeline()
recovery = RealtimeRecoveryCoordinator(
    channel="jobs",
    connect=open_session,  # async callback returning your transport adapter
    process=apply_event,  # async; advance only after application work succeeds
    load_snapshot=restore_snapshot,  # async; atomically restore and return its cursor
    processed_sequence=saved_cursor,
    timeline=timeline,
    on_ready=lambda cursor: show_ready(cursor),
)
await recovery.run()
```

The SDK does not supply a WebSocket implementation here. The session adapter
implements `subscribe`, `replay`, `events` and `aclose`. It must begin bounded
live buffering before capturing `RealtimeRecoveryTarget(oldest_sequence=...,
latest_sequence=...)`. A subscription ACK cursor is not a history target.
`replay(after=..., through=...)` must return ordered events in that range and
finish only after confirming the entire range, including sequence holes, was
scanned. `events()` drains buffered messages in order before continuing live;
duplicates at or below the completed cursor are ignored. Out-of-order events
stop the run. Buffer overflow must fail explicitly rather than drop messages.

Adapters raise `RealtimeRecoveryRetry` only for retryable transport failures
(including buffer overflow); a normal end of the live iterator also reconnects.
Authentication, permission, protocol and unknown failures stop with a sanitized
`RealtimeRecoveryError`. Application processing and snapshot failures never
retry automatically, even if they raise the retry exception. Retry delays use
full jitter with exponential caps (0.5–30 seconds by default), with a total
20-retry budget per run. Connect, subscribe, full replay, snapshot and cleanup
operations have a configurable 30-second deadline; a deadline failure stops the
run. Live idle waiting and application processing in live mode have no deadline.
Adapters and callbacks must cooperate with cancellation, and connect must clean
up partial acquisition if it fails or is cancelled.

If retained history starts beyond the cursor, `load_snapshot(target)` restores
application state atomically and returns its authoritative cursor within the
available range. Recovery then replays the remaining range. Without that callback,
the run stops with `snapshot_required`. Persist application effects and cursor
atomically if restart durability is needed; this does not guarantee exactly-once
side effects. Keep the coordinator, adapter and public observation trackers on
one event loop and one endpoint/account/principal. Do not mutate or dispose its
trackers during a run. Timeline callbacks capture state only, not event payloads.

### Native managed WebSocket client

Install the optional transport dependency with `pip install 'faas_sdk[realtime]'`.
`RealtimeWebSocketClient` supplies the recovery adapter; no custom socket layer is
needed for retained channel events or principal inbox notifications.

```python
from faas_sdk.preview import RealtimeRecoveryCoordinator, RealtimeWebSocketClient

async def access_token():
    return await identity_provider.current_token()

transport = RealtimeWebSocketClient(
    url="wss://realtime.example.com/ws",  # use your deployment's managed socket URL
    token=access_token,
)

async def apply_event(event):
    message = event.data  # RealtimeWebSocketMessage
    await update_application(event.sequence, message.data, message.event)

recovery = RealtimeRecoveryCoordinator(
    connect=transport.connect,
    process=apply_event,
    channel="jobs",
    processed_sequence=saved_cursor,
    load_snapshot=restore_snapshot,
)
await recovery.run()  # cancel this task to stop and close
```

The endpoint must support `gregale.realtime.v2` and authenticated read-state
queries, used to capture authoritative history bounds without marking events
read. Tokens are resolved for each connection, sent in Authorization headers and
never put in URLs. Only `wss` URLs without queries/credentials/fragments are
accepted. Redirects and automatic proxy discovery are disabled. Standard TLS
verification stays enabled. Native Ping/Pong keepalive defaults to 20 seconds
for interval and timeout; incoming server Pings are answered by the library.

Set `consumer="device-name"` on the transport and `inbox=True` on the coordinator
instead of a channel to consume principal notifications. Keep a unique consumer
per device and application state store. An inbox checkpoint behind local completed
work is replayed and acknowledged without repeating effects; one ahead of local
state stops with `unexpected_checkpoint`. Snapshot recovery resets the durable
inbox consumer cursor only after the application snapshot succeeds. Processing
ACKs are separate from user read markers; the transport never marks messages read.

The session buffers at most 256 events (configurable 1–8192), with additional
bounded library frame buffering. Frames are capped at the server's 8 KiB limit
and decoded payloads at 4096 bytes. Buffer overflow explicitly reconnects from
completed progress. Message sequence gaps, malformed frames, policy/auth failures
and unsupported frames stop. Retained messages expose bytes, mutation metadata,
message ID and version through `RealtimeWebSocketMessage`. Presence/signals are
ignored; multi-channel multiplexing uses the shared client below.
Use the multiplexed client below for shared channel connections.

For direct async iteration, `session = await transport.connect()`, call
`target = await session.subscribe(channel="jobs", inbox=False, after=saved_cursor)`,
then iterate `session.replay(after=saved_cursor, through=target.latest_sequence)`
and `session.events()`. Finish each yielded event's effects before requesting the
next event: advancing the iterator sends its processing ACK. Close the session in
`finally`. Prefer the coordinator for cancellation, retry and snapshot handling.

### Multiple channels on one WebSocket

`RealtimeMultiChannelClient` shares a native transport connection across up to
**eight retained channels**, matching the managed v2 connection limit. Subscribe
and unsubscribe dynamically; each channel has its own processor, completed
cursor, replay/snapshot recovery, bounded queue, retry budget and incident timeline.

```python
from faas_sdk.preview import RealtimeMultiChannelClient, RealtimeWebSocketClient

transport = RealtimeWebSocketClient(
    url="wss://realtime.example.com/ws", token=access_token, buffer_size=256,
)
client = RealtimeMultiChannelClient(transport=transport)
try:
    jobs = client.subscribe("jobs", process=apply_job, processed_sequence=saved_jobs)
    dashboard = client.subscribe("dashboard", process=apply_dashboard,
                                 load_snapshot=restore_dashboard)
    await jobs.wait_ready(timeout_seconds=30)
    await dashboard.wait_ready(timeout_seconds=30)
    report = jobs.diagnostics()  # existing redacted export + this channel's timeline
    await run_application()
    completed_jobs = await client.unsubscribe("jobs")
finally:
    await client.aclose()
```

`subscribe()` immediately returns a `RealtimeChannelSubscription` and starts
owned background recovery. `wait_ready()` waits only for that channel's replay
completion, including ACK completion; cancelling or timing out the wait leaves
recovery running. `ready`, `processed_sequence` and `error` expose its current
status. `recovery` exposes the existing observation trackers; do not mutate them.
Callbacks must cooperate with cancellation. Per-channel processing failures stop
that channel and leave others running. Failed handles retain their registration
and cursor until explicitly unsubscribed, so they still count toward the limit.
To restart one, unsubscribe it and pass the returned completed cursor to a new
subscription. No automatic retries of application/snapshot side effects occur.

The client owns one socket reader and routes frames directly into each channel's
bounded event queue. A slow processor does not block other channel processors.
A channel's buffer overflow or retention resync unsubscribes and resubscribes that
channel from completed progress. A physical connection failure wakes all affected
channels; they reopen one shared socket and resubscribe independently. An unknown
or malformed connection-level frame stops all affected streams. Retry budgets and
attempt IDs are local to each channel, not physical socket counters.

`unsubscribe()` cancels and joins that channel's processing/recovery and waits
for the server unsubscribe ACK (or `not_subscribed`) before permitting a
replacement. It returns the last completed local cursor. Cleanup rejects late
frames from that registration. If removal cannot be confirmed within the
operation deadline, the shared socket closes and remaining channels recover.
Removing the last channel closes the socket; a later subscription opens a new
one. `aclose()` rejects admission and joins all channel and socket tasks, including
when the caller is cancelled again during cleanup. Persist cursors with effects
for restart durability. Use the client and callbacks on one event loop and one
endpoint/account/principal. The multiplexed client covers retained channels;
principal inboxes remain supported by the dedicated single-stream client.

### Native presence and activity

`RealtimeWebSocketPresence` connects the native transport to the existing bounded
presence directory and activity tracker. Its `on_change` callback receives online
members, verified member IDs, connection counts, state and unexpired named
activities. Snapshot chunks become ready only when their final chunk arrives.

```python
from faas_sdk.preview import RealtimeWebSocketPresence

presence = RealtimeWebSocketPresence(
    state={"display_name": "Ada"},
    scope="principal",  # group this verified principal's connected sessions
    on_change=render_members,
    on_rejected=lambda code: show_ephemeral_rejection(code),
)
chat = client.subscribe("chat", process=apply_chat_message, presence=presence)
await chat.wait_ready(timeout_seconds=30)  # retained replay readiness
await presence.update_presence({"display_name": "Ada", "status": "busy"})
await presence.send_activity("typing", {"active": True}, ttl_ms=5000)
await presence.clear_activity("typing")
summary = presence.snapshot()
# summary.ready describes completion of the presence snapshot separately.
```

For a dedicated connection, pass `presence=presence` to
`RealtimeWebSocketClient`, then use its connect method with the single-channel
recovery coordinator. The binding is lazily created on the running event loop and
can follow reconnects for that same channel. Use a separate binding for each
multiplexed channel; `client.unsubscribe`/`client.aclose` dispose their bindings.
For dedicated clients, call `presence.dispose()` after stopping the coordinator.
Inbox subscriptions do not support channel presence.

`scope="connection"` is the default; `"principal"` requests server-side grouping
of the authenticated identity. State updates are copied JSON objects limited to
512 UTF-8 bytes and the latest requested state is reused on reconnect. Signals
are copied JSON limited to 2048 bytes. `send_signal(data)` sends an unnamed
transient event; optional synchronous `on_signal(RealtimeWebSocketSignal)` receives
both unnamed and named signals. `send_activity(name, data, ttl_ms=5000)` publishes
an expiring named signal (ASCII name up to 64 characters, TTL 0–30000 ms; zero
clears). Local expiry automatically removes named activity from summaries. Use
`RealtimeActivityPublisher(send=presence.send_activity, name="typing")` for
coalescing/automatic TTL refresh, and stop it separately with your subscription.
Signals and presence updates carry no user-supplied member ID: the server assigns
verified identities. Activity/presence are presentation data, not authorization.

Disconnect, resync/overflow failure or subscription close immediately clears
members, readiness and activity timers. Reconnect rebuilds from fresh server
snapshot chunks; transient signals are not replayed. Callbacks must be synchronous
and fast; exceptions are isolated. `on_rejected` receives only allowlisted
`presence_rate_limited`, `signal_rate_limited` or `signal_relay_unavailable` codes;
these leave retained-message recovery running. Sending only confirms a local
socket write, not server acceptance, and no automatic ephemeral retry is made.
Publishing while disconnected raises `presence_not_connected`. Multi-channel
redacted diagnostics include presence readiness/counts without member state or
signal payloads.

### Durable channel subscriptions

Supply `durable_subscription="logical-consumer"` to the dedicated native
`RealtimeWebSocketClient`, or to each multiplexed `client.subscribe(...)` call.
Gregale persists the processing checkpoint scoped by endpoint, verified principal,
subscription name and channel. Names are nonempty, trimmed strings of at most
128 UTF-8 bytes without NUL/CR/LF. Use distinct names for consumers/devices that
need independent delivery, and one active processor per logical identity.

```python
transport = RealtimeWebSocketClient(
    url="wss://realtime.example.com/ws", token=access_token,
    durable_subscription="dashboard-worker",
    checkpoint_timeout_seconds=10,
)
recovery = RealtimeRecoveryCoordinator(
    connect=transport.connect, channel="dashboard", process=apply_dashboard,
    load_snapshot=restore_dashboard,
)
await recovery.run()
```

For shared sockets, configure the name per channel:

```python
jobs = client.subscribe(
    "jobs", durable_subscription="jobs-worker", process=apply_job,
    load_snapshot=restore_jobs,
)
await jobs.wait_ready(timeout_seconds=30)
completed_locally = jobs.processed_sequence
persisted_on_server = jobs.confirmed_sequence
```

The initial `processed_sequence` seeds a new subscription; an existing persisted
checkpoint takes precedence on the server. On startup the coordinator adopts a
server checkpoint ahead of its local cursor. If local completed work is ahead
because an ACK was lost, it skips repeated effects and acknowledges redelivery.
Use the same identity only for application state that still reflects that logical
consumer's completed work. A server checkpoint does not restore a lost database or
UI state; rebuild such state or use a fresh consumer identity.

After successful application processing, durable channels wait for the server's
`acknowledged` confirmation before requesting the next event. Only that response
(or the initial loaded checkpoint / confirmed reset) updates `confirmed_sequence`.
Dedicated manual sessions expose the same property. ACK waits have a configurable
10-second deadline (0.1–300 seconds); timeout or server checkpoint-write failure
reconnects within the recovery retry budget. Retained replay readiness includes
the last persisted ACK. `processed_sequence` may be ahead of `confirmed_sequence`
while an ACK is pending or lost. Subscriptions without a durable name retain their
existing local-cursor behavior. On a shared client, the dedicated transport's name
is ignored: set `durable_subscription` explicitly on each channel registration.

When the persisted cursor is outside retained history, recovery invokes the
application snapshot loader, validates its cursor within the supplied bounds,
resets the server checkpoint, waits for matching `subscription_reset`, and
resubscribes before replaying. Missing snapshot recovery stops with
`snapshot_required`. Unsubscribe closes only the live subscription; it does not
remove the stored checkpoint. Names never appear in redacted diagnostics.

Native `RealtimeRecoveryTarget.resume_sequence` carries the loaded server cursor
separately from its authoritative `latest_sequence` replay target;
`resync_required` explicitly requests snapshot recovery. Custom adapters may use
these optional fields (defaults preserve prior behavior) only with authoritative
checkpoint/retention information. Direct iterator users can pass their baseline to
`replay`: the native adapter accounts for a newer loaded checkpoint. Inbox consumer
checkpoints remain a separate API; durable channel names cannot be combined with
inbox consumers on the dedicated client. No exactly-once effect guarantee is made:
use idempotent effects or transactional application deduplication for crash windows
between committing effects and persisting the processing ACK.

### Server-side channel event filters

`RealtimeEventFilter` supplies immutable exact-match **AND** conditions over
publisher metadata. Missing keys do not match, even when the requested value is
empty. Empty filters match every event. Validation follows server metadata bounds:
at most eight ASCII keys (`[a-z0-9_.-]`, 1–64 characters), string values within
128 UTF-8 bytes without NUL/CR/LF, and total encoded JSON within 4096 bytes.

```python
from faas_sdk.preview import RealtimeEventFilter

selected = RealtimeEventFilter({"project_id": "123", "event_type": "job.completed"})
jobs = client.subscribe(
    "jobs", event_filter=selected, durable_subscription="project-dashboard",
    process=apply_job,
)
await jobs.wait_ready(timeout_seconds=30)
```

For dedicated sockets, set `event_filter=selected` on
`RealtimeWebSocketClient`. The multiplexed client selects filters per channel;
the dedicated transport's filter is not inherited by shared registrations.
Filters affect retained channel messages only, not presence/activity signals.
Inbox filters are unsupported. Matching frames are checked against the filter
before delivery. Each skipped position arrives as a protocol checkpoint and
advances processing progress without invoking the coordinator's message handler.
ACKs remain ordered after earlier matching effects. Consecutive skipped positions
are coalesced in the bounded buffer; replay splits them at the frozen target so
all-nonmatching history can still complete catch-up without waiting for a match.

Restore a locally stored filtered cursor with its configuration identity:

```python
jobs = client.subscribe(
    "jobs", event_filter=selected, process=apply_job,
    processed_sequence=saved_sequence,
    cursor_filter_fingerprint=saved_filter_fingerprint,
)
# Persist these together with application effects:
sequence = jobs.processed_sequence
fingerprint = jobs.cursor_filter_fingerprint
```

Dedicated coordinators accept the same `cursor_filter_fingerprint` argument and
expose that property. A nonzero local cursor for a nonempty filter must supply its
matching fingerprint; mismatches stop rather than silently skipping events under
a new selection. A fresh zero cursor learns the filter identity from its native
session target. Keep the fingerprint whenever persisting filtered progress, even
if later switching to an unfiltered stream. Legacy unfiltered cursors without
fingerprints preserve their existing behavior. Native direct iterators expose
`RealtimeRecoveryEvent(checkpoint=True, data=None)` for skipped positions: skip
application effects for those events and advance the iterator to acknowledge them.

Nonempty filters derive an effective durable identity as
`selected.scoped_subscription("project-dashboard")`, using SHA-256 of the logical
name plus the canonical filter fingerprint. Input key order does not affect it.
This creates a distinct server checkpoint when a filter changes; it cannot silently
reuse the old filter's durable position. Empty filters preserve the original name.
Multiplexed handles expose the effective name through `durable_subscription`.
Keep logical names/configuration stable for restart recovery; existing externally
created filtered subscriptions with unsuffixed names are separate identities.
The server does not store the filter binding itself, so this guard applies to
these Python SDK identities, not clients that manually override the wire name.
Changing selection requires a new cursor or matching cursor/fingerprint pair and
may create another durable checkpoint. Filters are delivery selection, not an
authorization boundary. Filter values/fingerprints are omitted from diagnostics.

### Live read receipts and unread counts

`RealtimeWebSocketReadProgress` binds native channel/inbox receipt frames to the
existing read-progress tracker and unread presentation helpers. Configure the
**server SHA-256 reader key for the authenticated principal** explicitly; channel
broadcasts can contain receipts from other readers, so the client never guesses
identity from the first frame. The key must belong to the same endpoint/principal
as the socket. Raw account/user identifiers are not accepted.

```python
from faas_sdk.preview import RealtimeWebSocketReadProgress, format_realtime_unread_badge

reads = RealtimeWebSocketReadProgress(
    reader_id=verified_reader_key,
    on_change=lambda summary: render_badge(format_realtime_unread_badge(summary)),
    on_error=lambda code: show_read_error(code),
)
chat = client.subscribe("chat", process=apply_chat_message, read_progress=reads)
await chat.wait_ready(timeout_seconds=30)
# Only after the person actually sees the messages:
await reads.mark_read(chat.processed_sequence)
await reads.refresh()  # query unread counts without changing the read marker
badge = reads.badge(max_count=99)
report = chat.diagnostics()  # redacted unread details and freshness
```

For dedicated connections, pass `read_progress=reads` to
`RealtimeWebSocketClient` and use its connect method with the recovery coordinator.
This also supports `consumer="device-name"` with an inbox coordinator. Multiplexed
subscriptions select their read bindings explicitly per channel; they do not
inherit the dedicated transport's binding. Use separate bindings per stream and
never change the authenticated principal behind a reused binding. The application
can obtain the verified reader key from its identity integration or authenticated
receipt handling; first-broadcast inference is unsafe for channel membership.

Read receipts contain the hashed reader ID, read sequence, unread count, history
bounds and retention-gap flag. `summary()`/`badge()` select the configured reader;
`tracker.snapshot()` exposes all validated receipts within `max_readers` (default
1024, bound 1–8192). Missing receipts remain unknown/Updating rather than zero.
Counts are confirmed server values, never inferred by incrementing message counts.
Whole-channel unread counts do not narrow to a metadata filter's matching events.

Subscriptions enable receipt broadcasts and query read-state on each reconnect.
When new messages or filter checkpoints exceed the known receipt's history bound,
the badge becomes Updating and one coalesced query refreshes counts. Refreshes run
at most once per configurable `refresh_interval_seconds` (default 1, 0.1–60), with
one timer/task per binding and a five-second request deadline. Failures stop that
refresh rather than loop-retry; a later event or explicit refresh can request
again. Shared sockets have a server read-operation limit: choose intervals that
fit your combined channel/query/mark-read traffic. Optional `stale_after_seconds`
also lets periodic badge snapshots reflect aging receipts without its own timer.

Disconnect/channel failure retains last confirmed counts marked stale; matching
fresh receipts recover them after resubscription. `mark_read(sequence)` must not
exceed locally completed processing/ACK progress, and returns after the socket
write. It does not optimistically change the badge or promise server acceptance:
a subsequent receipt confirms the read marker. Processing ACKs, skipped filter
checkpoints, durable checkpoints and replay completion never mark messages read.
Marking a filtered stream position read asserts visibility through that whole
channel position, including skipped events; choose that action deliberately.
For coalesced UI assertions, `RealtimeReadProgressPublisher(send=reads.mark_read)`
can wrap the action; close the publisher separately with its subscription.

Safe `on_error` codes report read rate limits, marker limits, unavailable state,
relay failures and invalid assertions. Read-action errors leave retained delivery
running after initial target acquisition. Initial read-state errors still fail
recovery because its authoritative target cannot be established. Callbacks are
synchronous, fast and exception-isolated. Client shutdown cancels/joins refresh
work; multiplexed unsubscribe disposes the binding. Dedicated callers dispose it
after stopping recovery. Diagnostics include aliased reader identities/counts and
freshness, without raw identities, tokens or response bodies.

### Live-only direct messages

`RealtimeWebSocketDirectMessages` consumes backend messages addressed to the
socket's verified principal, independently of retained channels and inboxes.
Enable it on the native transport; a shared client uses the handler once per
physical socket rather than once per channel.

```python
from faas_sdk.preview import RealtimeWebSocketDirectMessages

async def handle_direct(message):
    await handle_ui_command(message.data, binary=message.binary)

direct = RealtimeWebSocketDirectMessages(
    on_message=handle_direct,
    on_error=lambda reason: show_direct_message_error(reason),
)
transport = RealtimeWebSocketClient(
    url="wss://realtime.example.com/ws", token=access_token,
    direct_messages=direct,
)
client = RealtimeMultiChannelClient(transport=transport)
try:
    jobs = client.subscribe("jobs", process=apply_job)
    await jobs.wait_ready(timeout_seconds=30)
    await run_application()
finally:
    await client.aclose()
    await direct.aclose()
```

The same option works on dedicated channel/inbox clients. The socket opts into
`direct_messages` through its active subscription; direct-only sockets without a
channel/inbox subscription are not provided by this API. Received
`RealtimeDirectMessage` contains optional `message_id`, payload bytes, `binary`
and `receipt_requested`. IDs/flags/base64 are validated; payloads are limited to
4096 bytes. Payloads are omitted from dataclass reprs and diagnostics.

One owned worker processes a bounded queue serially (default 128, bound 1–8192).
The async handler must finish its effects before returning. Requested processing
receipts send `direct_message_ack` only afterward; the reader confirms
`direct_message_acknowledged` without blocking retained frame routing. Failed or
unconfirmed receipts retry only the ACK, default twice with five-second per-attempt
deadlines (retries 0–3, timeout 0.1–60 seconds). Exhaustion reports
`direct_receipt_failed` and leaves retained delivery running. This callback is a
processing receipt, not proof that a human saw the message. No processing/read or
durable cursor is changed by direct messages.

A handler error reports `direct_handler_failed` and stops the owning connection's
recovery; it never automatically retries application effects or acknowledges the
failed message. Queue overflow reports `direct_message_overflow` and reconnects
through existing transport recovery. Transport write failures are also retryable.
Queued/in-flight live messages are cancelled/discarded on connection loss; they
are not replayed on reconnect. The handler must cooperate with cancellation.
Client/session cleanup cancels and joins the socket's direct worker; explicit
`direct.aclose()` disposes the reusable handler after stopping the client. Keep
one handler binding per physical connection on one event loop. `on_error` is a
fast synchronous callback with safe reasons only. Use retained inbox notifications
when delivery must survive disconnects or process restarts.

### Receive-only SSE fallback

```python
from faas_sdk.preview import RealtimeFallbackClient, RealtimeWebSocketClient

transport = RealtimeFallbackClient(
    websocket=RealtimeWebSocketClient(url=realtime_url, token=get_token),
    fallback_after=2,
    probe_interval_seconds=60,
)
# Pass transport.connect to RealtimeRecoveryCoordinator with your channel,
# processor and persisted completed cursor. For diagnostics use
# export_realtime_diagnostics(health, transport=transport.snapshot).
```

Fallback supports retained channels and exact metadata filters (applied locally
on SSE). It rejects inboxes, durable ACKs, presence, read progress and direct
messages. A healthy SSE stream stays connected; WebSocket is probed on a later
reconnect after the cooldown. Authentication failures stop recovery. The SSE
handshake requires server sequence/history bounds from ADR 1098. SSE event data
is `RealtimeSSEMessage`, whose `message_id` is `None`; use channel and sequence
for processing identity. Persist completed processing cursors as usual.

### Pause and resume subscriptions

```python
subscription = client.subscribe("jobs", process=process_event)
await subscription.wait_ready()
completed_cursor = await subscription.pause()
# subscription.state == "paused"; other active channels continue receiving.
await subscription.resume()
await subscription.wait_ready()  # replay missed events before reporting live
```

Pause finishes the current handler, removes the wire subscription and retains the
handle's completed cursor and filter identity. It keeps a slot in the client's
eight-channel limit. `state` reports `recovering`, `live`, `pausing`, `paused`,
`failed` or `closed`. Resume preserves terminal errors and uses the configured
snapshot loader when retention expired. Readiness is false throughout pause;
`wait_ready()` can remain pending until another task resumes the handle.

Call pause from outside the handler. Existing replay deadlines still apply. A hung
live handler keeps pause pending; explicit
unsubscribe can cancel processing. Cancelling a pause waiter leaves pause running.
Persist completed cursors with application effects as usual; pause does not promise
a durable ACK flush. Presence and read helpers reconnect on resume. For a dedicated
WebSocket or SSE recovery coordinator, use `await recovery.pause()`, then start
`recovery.run()` again to resume with its retained cursor.

### Backpressure observations

```python
from faas_sdk.preview import RealtimeBackpressureMonitor

monitor = RealtimeBackpressureMonitor(
    on_change=show_queue_status,  # fast synchronous callback; no payloads
    busy_ratio=0.5,
    overloaded_ratio=0.9,
    busy_age_seconds=5,
    overloaded_age_seconds=30,
)
subscription = client.subscribe("jobs", process=process_event, backpressure=monitor)
status = subscription.backpressure.snapshot()
# status.state: inactive / healthy / busy / overloaded
# status.queue_depth, capacity, oldest_queued_age_seconds, overflow_count
report = subscription.diagnostics()
```

Dedicated `RealtimeWebSocketClient` instances accept `backpressure=monitor` too;
one monitor belongs to one subscription. Defaults are available on every returned
WebSocket session and channel handle. Callbacks run on queue changes and age
threshold crossings, so stalled queues can change status without new traffic.
They observe delivery; applications choose whether to pause or show a warning.

Queue age measures local waiting, excluding the event currently handled. Filtered
checkpoint coalescing preserves the oldest waiting age. Paused or disconnected
monitors become `inactive` and clear queued observations while keeping overflow
counts. A full queue retains the existing reconnect policy. Internal socket and
server buffers are outside these measurements.

For WebSocket or SSE, `recovery.processing_snapshot()` reports current handler
age and last handler attempt duration, including failed or cancelled attempts.
These are local timings, not server sequence lag. Export them with
`export_realtime_diagnostics(health, processing=recovery.processing_snapshot())`;
WebSocket queue status can be supplied separately as `backpressure=monitor.snapshot()`.

### Graceful retained-channel shutdown

```python
result = await subscription.drain(deadline_seconds=30)
# The channel handle is now closed and its client slot released.
if result.complete:
    save_shutdown_cursor(result.processed_sequence)
else:
    report_shutdown_failure(result.error_reason, result.unfinished_event_count)
```

Draining stops local admission, finishes buffered events and the active handler,
flushes the final ACK and waits for durable checkpoint confirmation if configured.
Other shared channels normally continue receiving. The handle reports `draining`
until close. Repeated drain calls return the same result. Use a new subscription
with the completed cursor to restart a drained channel.

`RealtimeDrainResult` reports `complete`, `processed_sequence`,
`confirmed_sequence`, `final_ack_sent`, `deadline_expired`,
`unfinished_event_count` and `error_reason`. Unfinished work counts local buffered
entries and an uncompleted yielded event, including coalesced checkpoints; events
arriving after admission stops are outside that count and remain replayable.
A transport write is not a server checkpoint confirmation.

For dedicated WebSocket recovery, use `await recovery.drain(deadline_seconds=30)`.
Call from outside the processor after subscription activation, typically following
readiness. SSE and inbox consumers reject draining in this version. Pause/resume
cannot overlap a requested drain. Cancelling a waiter leaves drain running.
The deadline can cancel active processing; cleanup is joined afterwards and
handlers must cooperate with cancellation. Existing replay/checkpoint timeouts
still apply. Persist cursors with application effects or make effects idempotent.

### Retained message expiration

Single retained history append and channel `delivery=retained` requests accept
`ttl_seconds=60` for a one-minute delivery lifetime. Zero or omission disables
expiry; the maximum is 86400 seconds. Generated publishing request models expose
this field. Reuse the same TTL on idempotent retries: the server preserves the
first deadline. Message edits inherit it.

Python WebSocket and SSE recovery skip expired events through checkpoint entries,
advancing the processing cursor without calling the application processor. Message
DTOs expose optional `expires_at`; REST history exposes `expired` checkpoints with
empty payload and suppressed metadata. Expiration does not delete retained log
positions, retract earlier delivery or roll back application state.

Deploy the TTL migration and updated server/readers, then update SDK clients before
enabling TTL publishers. This initial API covers account-authenticated single
retained channel writes, not live-only, scheduled, credential or transactional
publishing, inboxes or consumer-group job deadline policies. Batch support is
described below. Runtime expiry checks
and migration application are deferred.

### Batch TTL defaults and overrides

`ManagedRealtimeChannelBatchRequest.ttl_seconds` sets a default delivery lifetime
for the batch. An item's `ttl_seconds` inherits that default when omitted or
`None`; an explicit zero disables expiry for that item. Positive values range
from 1 to 86400 seconds. For example, a batch default of 60 with item overrides
`UNSET`, `10`, and `0` gives lifetimes of one minute, ten seconds and no expiry.

All deadlines are saved atomically with the batch and share its original creation
time. Identical retries preserve those deadlines; changing an effective item TTL
conflicts with the batch ID. Existing non-expiring batch IDs remain compatible.
Expired messages use the same replay checkpoints as single-message TTL. This
requires ADR 1102's migration and updated servers/clients; runtime checks remain
deferred.

Scheduled realtime publishing accepts optional `ttl_seconds` (0–86400; zero disables expiry). Each occurrence starts its message lifetime at actual publication. Rescheduling and retries preserve the schedule setting and cannot extend a committed message deadline.

Scoped publisher message models expose `ttl_seconds` (0–86400; zero disables
expiry). Credential issuance models expose `max_message_ttl_seconds` (zero means
uncapped); a positive cap requires a positive message TTL at or below it.
Omitted or zero message TTL is denied when capped. Issue/rotation `ttl_seconds`
is the credential's own lifetime, independent of message expiry. Rotation
preserves the message cap. Keep message TTL unchanged when retrying a key.

Use `RealtimeExpiryDiagnostics` with `on_message_expired=expiry.record` on
`RealtimeWebSocketClient` or `RealtimeSSEClient`. The optional synchronous hook
receives a frozen `RealtimeMessageExpiry` containing only code, channel, sequence
and transport after validation, before processing checkpoints. Observer errors
are isolated; hooks must stay brief and must not perform business effects.
Reconnects can report the same position again. Expired payloads never reach your
recovery processor. Counts saturate at the signed 64-bit maximum.

`expiry.snapshot()` returns `RealtimeExpiryStatus`; pass it as
`export_realtime_diagnostics(health, expiry=expiry.snapshot())` to include
WebSocket/SSE checkpoint counts as decimal strings. The collector and exports
retain no channel identifiers, payloads or credentials. Reuse a collector across
reconnections to retain observations, or create a new one to reset them.

Retained principal-send models accept `ttl_seconds` (0–86400; zero disables
expiry) with `delivery="retained"` required for positive TTL. Send responses expose
`delivery_expires_at`; inbox history adds `ttl_seconds`, `delivery_expires_at`
and `expired`. Existing history `expires_at` remains retention expiry. Skip payload
processing for REST rows marked expired while advancing their sequences. Retries
and edits preserve deadlines. Notification-fallback TTL is independent.

Inbox WebSocket recovery handles `inbox_checkpoint` expiry positions without
calling the processor and uses the existing durable ACK path. The same
`on_message_expired=expiry.record` observer receives frozen events with
`inbox=True`, consumer, sequence, code and transport (`channel=None`), without
principal/payload. `expiry.snapshot()` and `export_realtime_diagnostics` add
`inbox_checkpoints` separately from channel WebSocket/SSE counters. Active inbox
messages expose optional `expires_at`. Observations can repeat after reconnect;
hooks are best effort, synchronous and must not perform business effects.
Upgrade migration/apid/realtime nodes/SDKs before enabling inbox TTL.

### Transactional event consumers

After authenticating an internal subscription delivery, construct input with
`event_consumer_request_from_headers(headers, raw_body, account_id, app_id)`.
`with_event_consumer_transaction(connection, input, handler)` owns the synchronous
psycopg transaction; `awith_event_consumer_transaction` supports async connections
and callbacks. Exclusively lease an idle `autocommit=True` connection. Callbacks
use only their supplied cursor and return UTF-8 JSON bytes/text. Acknowledge only
after success using the returned `body`; `replayed` identifies saved results.

Install exported `event_consumer_receipt_schema` explicitly and retain receipts
across retries/replay. Changed content raises `EventConsumerConflictError` without
running the callback. On `EventConsumerCommitUnknownError`, retry the same input.
Do not control transaction lifecycle or perform external effects in the callback.
See [synchronous and asynchronous examples](../../docs/event-consumer-transactions.md).

Enqueue follow-up Commit work inside the event-consumer callback with `insert_commit_event(cursor, ...)` or awaited `ainsert_commit_event(cursor, ...)`. Install both schemas in the same database and configure a Commit source. Business writes, follow-up work and the receipt commit together; duplicate inputs return the saved result. See [the combined transaction examples](../../docs/event-consumer-transactions.md#update-and-enqueue-follow-up-work-atomically).

For transactional publication to subscribers, configure an immutable `event` Commit destination with contract version 3. The existing insert helpers accept an optional schema version and run in the consumer callback transaction. See [publication setup and receipt recovery](../../docs/gregale-commit.md#version-3-application-event-publication-adr-935).

`await await_commit_subscriber(client, publication, subscription_id, timeout=300)`
waits for one original application subscriber through an authenticated generated
client. Only `result["status"] == "completed"` proves original execution success.
See [subscriber waits](../../docs/gregale-commit.md#wait-for-an-individual-publication-subscriber).

`await aget_commit_delivery_report(client, source, limit=50, timeout=120)` samples
recent original-subscriber outcomes. Check per-item evidence status and cursors;
read failures raise `CommitDeliveryReportError` with the partial report. Counts
cover sampled evidence only.

Delivery reports accept `recipient_pages` (1–20, default 1) and `resume=saved_report`.
Resume fixes selection/filter and merges recipients without duplicate counts.
Omit selection/filter arguments when resuming; saved completed coverage is not
refreshed. The saved dictionary is copied before reads.

Use `aget_commit_delivery_report(client, source, refresh=saved_report,
recipient_pages=5)` to reread a fixed selection. Fresh counts, observation times,
and changes describe new evidence. Omit resume and selection/filter arguments.

Delivery report rows now separate original `status` from latest retained replay
`recovery` evidence. `recovery_counts` and refresh changes with `kind: recovery`
help identify remaining failures; missing evidence does not prove no replay occurred.

Use `aplan_commit_recovery(client, source, saved_report, recipient_pages=5)` to build a read-only recovery plan from fresh guarded receipt evidence. Plans include skip reasons and publication coverage issues; they never execute recovery actions.

Use `aapply_commit_recovery(client, source, reviewed_plan, limit=50, recipient_pages=5)` for sequential, freshly revalidated execution recovery. Unknown writes stop the batch and require inspection. Routing apply now submits the matching acceptance guard from a fresh server offer; older unguarded offers remain skipped. See the Commit documentation for limits and partial results.

Selective routing replay accepts `expected_accepted_at`; a replaced acceptance returns 409 without requeuing the consumer. Refresh and review older plans before using guarded routing apply.

Routing recovery offers include an `expected_generation` guard when available.
Commit recovery apply skips changed generations as `generation_changed`, and
missing guards as `generation_guard_unavailable`. Direct selective replay can
supply the nonnegative generation together with `expected_accepted_at`; the
server checks both and failed state atomically before requeue.

Read-only recovery status accepts the reviewed plan and saved apply result.
It retains each attempt alongside fresh delivery state and routing generation;
unknown attempts remain unknown even if delivery advances. Missing evidence
stays unresolved, and partial read failures retain output. The helper never
submits replay or follows saved action URLs. See
[the recovery-status guide](../../docs/gregale-commit.md#inspecting-recovery-results-without-replay).

Use await aget_commit_recovery_status(client, source, plan, applied, limit=50, recipient_pages=5); CommitRecoveryStatusError.result retains partial output.

Recovery status can resume saved output, preserving successful observations
and their timestamps. Unread items are inspected before failed reads are
retried. Selection snapshots bind resume to the original plan, apply result,
publication acceptance and subscriber filter. Counts and remaining coverage
are recomputed, and unknown attempts remain unknown.

Pass resume=previous to aget_commit_recovery_status.

Older status files without selection snapshots require a fresh status read. Resume keeps historical samples; omit resume to reread them.


Refresh saved Commit recovery observations with `aget_commit_recovery_status(client, source, plan, applied, refresh=previous)`. Refresh clears current evidence and reports before/after changes against a compact saved baseline. Use resume to finish a bounded refresh pass; refresh and resume are mutually exclusive. Unknown apply attempts remain unknown. See [the Commit guide](../../docs/gregale-commit.md#refresh-saved-recovery-observations).


Use `awatch_commit_recovery_status` for bounded recovery monitoring with detached progress callbacks. Partial passes resume before the next refresh. Queued attempts require matching terminal execution evidence; unknown attempts remain unknown. Timeout/read errors retain the last or partial status. See [monitoring usage](../../docs/gregale-commit.md#monitor-acknowledged-recovery-executions).


Opt into transient recovery-watch read retries with `read_retries`. The total budget is 0..20, defaults to zero, and shares the overall timeout. Partial results reach progress callbacks before backoff; permanent errors stop immediately. See [retry behavior](../../docs/gregale-commit.md#retry-transient-monitoring-reads).


Recovery watch results include `monitoring` with stop reason, started passes, consumed retries, elapsed milliseconds and conservative terminal coverage. Progress snapshots report running; final results and attached errors report the outcome. Counters restart on resume. See [structured outcomes](../../docs/gregale-commit.md#structured-monitoring-outcomes).


Optionally require recent recovery evidence with `max_observation_age` (default disabled; 100ms..24h). Stale and missing/future-timestamp observations are reread within existing budgets before settlement. Monitoring includes freshness counts. See [freshness policy](../../docs/gregale-commit.md#require-recent-recovery-observations).

Optional `diagnostics=True` includes bounded exact-recipient delivery blocker diagnostics, separate from replay attribution. Missing evidence and settled routing never prove handler completion. See [diagnostics](../../docs/gregale-commit.md#diagnose-recovery-delivery-blockers).

Exact routing lookup: `faas_sdk.api.events.get_event_recipient_blocker.asyncio_detailed(client=client, source=source, id=event_id, subscription_id=subscription_id, expected_accepted_at=accepted_at)`. Pass the original timestamp string to preserve fractional precision. `not_waiting` describes routing only.

Diagnostic coverage rotates within the 50-read cap and resumes from checkpoints. `diagnostics_progress` reports separate coverage counts; `retained` samples keep their original timestamps. Diagnostic completeness never gates delivery settlement. See [resumable coverage](../../docs/gregale-commit.md#resume-diagnostic-coverage-across-large-selections).

Recipient diagnostic monitoring batches up to 50 exact acceptance-guarded lookups per request. The read-only `/v1/events/recipient-blockers` endpoint returns ordered independent results and errors under a shared five-second deadline. Diagnostic read counts remain recipient counts; coverage does not imply handler completion.

Use `diagnostics=True, max_diagnostic_age=30` for status or watch, or `adiagnose_commit_recovery_status(client, status, previous=previous, max_diagnostic_age=30)` for standalone diagnostics. Age is measured in seconds. Zero disables the policy; enabled ages range from 100ms to 24h. `diagnostics_progress.freshness` separates fresh, stale and unread samples and records the check time; `fresh_complete` is independent of historical coverage and delivery settlement. Stale samples refresh before fresh ones within the covered refresh pool. Saved counters and age policies are recomputed on resume.

Repeated diagnostic samples expose `comparison`, compact `previous_observation`, and field-level `changes` with both observation timestamps. Comparisons require the same exact acceptance and increasing timestamps. Missing evidence resets the baseline; routing changes never imply handler completion. Checkpoints retain only the latest pair and recompute saved changes. `diagnostics_progress.compared` and `changed` count newly compared and changed recipients per pass, excluding retained samples. See [routing blocker change tracking](../../docs/gregale-commit.md#track-routing-blocker-changes).

`awatch_commit_recovery_status` accepts `watch_diagnostics=True, max_diagnostic_age=30`. This enables diagnostics and waits for complete, fresh diagnostic coverage independently of delivery settlement. Monitoring exposes `watch_mode`, `diagnostic_coverage_complete` and stop reason `diagnostic_coverage_complete`. Age is rechecked after callbacks; the recorded check time defines completion. Resume retains existing coverage and cursors. Batch transport failures use the shared read retry budget; individual item failures rotate until timeout. Unavailable answers count as coverage, and coverage never proves handler success.

Diagnostic watches expose `monitoring.diagnostic_estimate`: remaining initial samples, actual sampling rate, average samples per pass, estimated remaining passes/milliseconds, and a full-refresh span estimate. States distinguish insufficient timing, deferred receipt evidence, estimates, and already-covered selections. `refresh_exceeds_max_age` is advisory; forecasts never predict delivery success or fresh coverage completion. Performance measurements reset on resume while coverage/cursors persist. See [coverage estimates](../../docs/gregale-commit.md#estimate-diagnostic-coverage-time).

### Inspect selected consumers

Diagnostic progress includes `consumers`, sorted by subscription ID. Each summary counts relevant selected recipients, fresh/stale/unread coverage, validated sample statuses, fresh and stale waiting reasons separately, and newly compared/changed recipients in this pass. Waiting reasons expose paused subscriptions, circuits, ordering, capacity and other routing conditions without additional requests. Unsupported recipients are excluded. A valid unavailable or replaced answer counts as sampled coverage but contributes no waiting reason.

`oldest_waiting` identifies the earliest accepted event among validated available samples, with its exact query, observation timestamp, freshness and waiting reason. Acceptance time is not the time a blocker started. Stale waiting reasons describe historical samples; these summaries are neither account-wide backlog totals nor delivery success. Saved totals are ignored and recomputed from validated rows at the progress freshness check time. CLI watch output includes these summaries when consumer evidence changes, without timestamp-only polling updates.

### Detect persistent sampled blockers

Enable watch stall detection with `--stall-threshold 30s --watch --diagnostics --max-diagnostic-age 10s` (both duration policies accept 100ms–24h; zero disables detection). Go watch options use `StallThreshold`, Node uses `stallThresholdMs`, and Python uses `stall_threshold` in seconds. Watch results include `monitoring.stall_threshold_ms` and `monitoring.consumer_stalls`, grouped by selected subscription. CLI alerts once per confirmed blocker episode and can alert again after confirmation breaks. Detection does not change watch completion, timeouts, retries or delivery actions. A coverage watch can complete before the stall threshold is reached.

Each consumer has recipient counts for `insufficient_evidence`, `not_blocked`, `observing` and `stalled`, plus overlapping indicators `routing_changed` and `advancing` for the most recent confirmed pair. Advancing specifically means pending to processing, or available to enqueued; it does not prove handler success. Other routing changes can improve or worsen conditions. Blocker details contain the exact query, reason, first/last sample timestamps, sample count and `monitored_ms`: the monotonic span between distinct samples in this invocation, never the original blocker duration.

At least two strictly increasing fresh samples of the same routing observation must span the threshold. Pauses, circuit/rate limits, ordering, capacity and retry backoff are eligible; ready and processing indicators are not blockers. Changes in routing state, target availability, capacity scope or exact ordering blocker reset the episode. Stale, future, unavailable, replaced, invalid and non-monotonic evidence interrupt confirmation; a gap exceeding the age limit also resets it. Retained samples cannot start an episode or extend its duration. Resume starts a new tracker and ignores checkpoint stall aggregates. Counts describe sampled selected recipients, not account-wide stalled consumers.

### Consume blocker incident transitions

Stall-enabled watch results include `monitoring.blocker_incidents` (currently confirmed incidents) and `monitoring.blocker_transitions` (events from the current pass). An incident opens when fresh samples reach the stall threshold. Its opaque ID stays fixed during that episode within this watch. Each transition has an increasing invocation-local sequence, incident ID, exact guarded query, original waiting reason, notification timestamp and optional compact routing evidence. Deduplicate repeated snapshots by incident ID and sequence. A newly confirmed episode receives a new ID. Without durable alerts, resume starts a new invocation and does not adopt saved incidents or replay their transitions.

Transitions are `opened` / `persistent_blocker`, `resolved` / `routing_not_blocked`, or `confirmation_lost`. Resolution requires a newly sampled, strictly later, fresh observation without an evidence gap: either retained routing has ended (`not_waiting`) or the sampled waiting reason is no longer an eligible blocker. This includes terminal routing failures/filters and never proves handler success. Changed blockers or episode resets report `confirmation_lost` / `episode_reset`; stale, unavailable, invalid or replaced evidence reports `evidence_unavailable`, excessive sampling gaps report `sample_gap`, and missing selected evidence reports `selection_unavailable`. Missing evidence never implies resolution.

Add `--alerts-json` to a stall-enabled CLI watch to emit one JSON transition per line on stderr and suppress routine watch progress text. The final status JSON remains on stdout; command errors retain their existing error reporting. The CLI deduplicates callback/final snapshot events and emits transitions discovered by final freshness checks. Without this flag, lifecycle alerts use readable text. Watch completion and deadlines are unchanged: a stopped watch may still have active confirmed incidents. Active state and current-pass events are bounded by the selected recipients; these results are not a durable incident history or a delivery success signal.

### Resume pending incident notifications

A stall-enabled CLI watch with `--checkpoint PATH` now enables a durable alert outbox. SDK watch options enable it with Go `DurableAlerts: true`, Node `durableAlerts: true`, or Python `durable_alerts=True`. SDK callers must persist progress and final snapshots themselves, before handing alerts to a consumer. `monitoring.alert_outbox` contains version 1, a notification session, its latest sequence, ordered `pending` transitions and saved `active` incidents. Consume `pending` when durable mode is enabled; `blocker_transitions` remains a per-pass view.

Resume validates saved alerts against the same frozen publication/acceptance/subscription selection and reviewed attempts, checks episode lineage and bounded queue shape, and preserves pending IDs, sequences and the notification session. This extends the invocation-only lifecycle behavior above for durable notification streams. Previously active episodes emit `confirmation_lost` with reason `watch_resumed`; saved samples never restore live confirmation. New episodes require fresh samples and receive new IDs in the continued stream. Historical pending alerts remain historical notifications even when current evidence has disappeared.

Printing or writing an alert is not acknowledgement from the consumer. After processing an alert, acknowledge its exact incident ID and transition sequence. CLI accepts an array such as `[{"incident_id":"0123456789abcdef0123456789abcdef-1","sequence":1}]` via `--ack-alerts PATH` with checkpoint and resume/refresh. The acknowledgement file is protected from checkpoint overwrite. SDKs accept initial `AlertAcknowledgements` / `alertAcknowledgements` / `alert_acknowledgements` and synchronous per-pass `OnAlertAcknowledgements` / `onAlertAcknowledgements` / `on_alert_acknowledgements` callbacks that return acknowledgement arrays after progress. Acknowledgements validate as a whole; duplicate pairs within a batch, wrong-stream IDs, future sequences and conflicting IDs for pending sequences fail without removing entries. Repeating an acknowledgement for an already removed sequence in this stream is an idempotent no-op. Callbacks should return promptly and acknowledge only completed consumer processing. Persist the next/final snapshot to commit removals; a crash before that save can replay acknowledged work.

The CLI saves each snapshot before emitting alerts, including final-check transitions, and re-emits unacknowledged pending entries once per resumed invocation. This is at-least-once delivery of checkpointed notifications, not exactly-once processing; deduplicate by incident ID and sequence, and apply only transitions newer than the last sequence processed for that incident. Partial acknowledgements can replay an older transition after a newer one was processed. Resume the latest snapshot of a single checkpoint stream; SDK callers must serialize checkpoint ownership and writes. Notifications that were never checkpointed have no durability guarantee. Consumer acknowledgements are explicit; stderr success does not remove pending alerts.

The queue is bounded at 1,024 slots, including a reserved closing transition for every active incident. If a new opening cannot reserve both slots, monitoring stops with `alert_queue_full` and retains queued notifications and the sampled status. Acknowledge pending work and resume. No queued transition is evicted. Notification sequences are bounded to the exact integer range shared by the SDKs. Invalid saved state/acknowledgements are rejected and do not overwrite the CLI checkpoint. Non-durable watches retain the previous per-pass behavior. Delivery mutations, receipt budgets and ordinary watch completion are unchanged.

Use `faas_sdk.api.events.get_event_consumer_delivery_health.sync(...)` or its
async counterpart with `slug`, `subscription_id`, and an authenticated `client`
for a combined subscription delivery summary. Component timestamps represent
independent reads. Inspect pending-sample truncation and execution retention
coverage; the latest successful completion is a retained observation, not an
all-time watermark.

Subscription delivery health includes optional `capacity_waits`: complete effective capacity-reason counts
for consumer/app/account scope, a pending total, explicit coverage/completeness,
consumer binding, and a separate observation time. Counts cover all retained pending
recipients independently of the bounded sample. A missing aggregate from an older
server is unavailable evidence, not zero waits.

Delivery health `waiting_reasons` provides a complete effective pending-reason
partition, including zero entries, with its own pending total, completeness,
coverage, observation time, and acceptance cutoff. Missing aggregates from older
servers are unavailable evidence, not zero. The backlog consumer model also
exposes `pending_waiting_reasons` for matching pending recipients before pagination.

## Inspect durable entity metadata

The owner-only preview endpoint `GET /v1/apps/{slug}/entities/inspect` accepts
required `namespace` and `key`, plus optional `environment` and
`platform_tenant_id`. It returns the committed version, alarm status and pending
outbox metadata without running the guest or exposing state/message payloads.
It requires account `apps:read` or admin permission and preview app enablement.
Missing delivery history is `unknown`; an empty queue does not prove delivery.

```python
from faas_sdk.api.invocations import inspect_durable_entity
inspection = inspect_durable_entity.sync(
    "reservations", client=client, namespace="reservations", key="reservation:123",
)
```

## Re-arm exhausted entity work

Use `POST /v1/apps/{slug}/entities/retry` after a fresh inspection reports the
selected alarm or outbox head as exhausted. Copy its version, recovery revision
and exact alarm deadline or head ID. Recovery requires account deploy-write or
admin permission and existing execution admission; diagnostic read permission
alone does not grant retry authority. Active owners block recovery.

A successful response resets retry metadata only. Workers must be enabled to
resume processing. Committed state and message identities stay intact; terminal
receiver deliveries are not resent. After a conflict or uncertain response,
inspect again before deciding whether to submit another recovery.

```python
from faas_sdk.api.invocations import retry_durable_entity
from faas_sdk.models.durable_entity_retry_request import DurableEntityRetryRequest
if inspection.outbox.exhausted:
    recovery = retry_durable_entity.sync("reservations", client=client, body=DurableEntityRetryRequest(
        namespace="reservations", key="reservation:123",
        target="outbox",
        expected_version=inspection.version,
        expected_recovery_revision=inspection.recovery_revision,
        head_id=inspection.outbox.head_id,
    ))
```

For an alarm, use the `"alarm"` target with `alarm_at=inspection.alarm.alarm_at`
and omit `head_id`. Preserve environment/customer selectors from inspection.

### Durable entity state export and restore

Owner preview APIs export application data and restore it through an expected
business version and stable request ID. Export needs read scope; restore needs
deploy-write scope and the existing mutation gates. Preserve the complete export
privately. Restore retains current alarms, receipts, outbox and delivery retries;
it does not invoke guest code or rewind effects. Check application schema
compatibility before restoring.

After timeout or an uncertain response, retry the identical request ID and body,
including the original expected version. Start a new operation only after resolving
the previous outcome. A successful retry can return `replayed: true`. Checksum
validation requires serialization fidelity; do not edit the exported data.

This implementation is local and unqualified; tests/builds are pending. See
[ADR-941](../../docs/adr/941-durable-entity-owner-state-recovery-api.md).

Use generated `faas_sdk.api.invocations.export_durable_entity` and `restore_durable_entity` sync/async methods. Restore models use `export` for the JSON `export` property.

### Backups and restore preview

Operator-enabled backups capture application data hourly with eventual seven-day
retention. Owner read-scope clients can list backup metadata, read an exact backup
and preview a restore. Preview reports observed versions, recognizable schema
versions and preserved pending work. Compatibility remains `unverified`; validate
application data separately. Preview does not reserve a version or promise storage
capacity. Actual restore still requires deploy-write scope, expected version and
stable request ID. See [the operator guide](../../docs/runbooks/FaasDurableEntityBackups.md).

Generated modules are `list_durable_entity_backups`, `get_durable_entity_backup` and `preview_durable_entity_restore` under `faas_sdk.api.invocations`. They provide sync/async methods.

### Application-validated restore

The default-off operator gate `FAAS_DURABLE_ENTITY_RESTORE_VALIDATION_ENABLED=1`
makes every new owner restore require `validation_deployment_id`. First call the
owner validation endpoint with the same selectors, exported data, expected version
and stable request ID. It needs deploy-write scope and execution permissions; it
runs application code and consumes normal invocation resources. A true verdict
names the checked deployment. Put that ID in the restore request; restore validates
again under its private claim. A false verdict commits no state. Receipt replay
skips validation. Keep the identical request, including the pin, for uncertain
restore retries.

The distinct guest route is `/__gregale/entities/validate-restore`; validators must
be synchronous and pure, returning only a versioned boolean verdict. No normal
transition, alarm or outbox output is admitted. External application I/O is not
independently disabled by the current runtime. Validators do not migrate data.
The read-only metadata preview remains separate and does not execute the guest.
Deployment selection is checked before/after validation, but deployment routing
and bucket publication are not atomic; avoid deployment changes during recovery.
See [ADR-943](../../docs/adr/943-durable-entity-application-validated-restore.md).

Use generated `faas_sdk.api.invocations.validate_durable_entity_restore`. The returned `deployment_id` and request `validation_deployment_id` use UUID values. Python guest helpers are not included in this slice.

### Blocker responsibility

Workflow blockers accept optional public `owner` and `next_action` fields;
source-linked resolution facts accept `resolved_by`. Use the existing workflow
blocker transaction helper to publish the complete replacement list with the
business write. Assignment is application-authorized and does not grant platform
permissions. Upgrade all writers to preserve these fields during metadata updates.
See [blocker ownership](../../docs/operations.md#assign-blocker-ownership-and-a-next-action)
for byte limits, reassignment, and resolution tracking.

Attention queues support an exact owner or unassigned-only filter, and summaries
can group by owner. Empty owner-group values represent unassigned work; shared
workflows may count in several groups. See [owner queues](../../docs/operations.md#find-work-by-owner)
for filter and count semantics.

Workflow steps can declare versioned `blocker_escalations` policies by blocker
code, with `after_seconds` and a recommended `owner`. Attention reads support
`reason=escalated` and return threshold findings and escalated counts; unknown
ages remain unknown. See [escalation policies](../../docs/operations.md#declare-blocker-escalation-policies).

Blockers also accept application-reported `priority` (`low`, `normal`, `high`,
`urgent`) and public `business_impact` (at most 512 UTF-8 bytes without control
characters). Omitted priority counts as normal. Preserve this context on later
blocker replacements; changing it keeps age and acknowledgement intact.
Attention queues and summaries accept a priority filter and expose counts per
owner. Deadline queue sorting uses the workflow's existing `deadline_at`, with
undated workflows last; update-time ordering remains the default. Summary groups
retain group-value ordering. Reuse cursors only with the same filters and order.

Resolution verification is opt-in: provide paired `verification_milestone_id` and
`verification_milestone_name`, optionally `verification_operation_id` (defaults
to the resolution's Operation) and a public `verification_owner`. Exact retained
proof must match the same app/customer/environment/subject/workflow instance and
resolution contract version. A name alone cannot verify a resolution.
Use attention reason `awaiting_verification` and owner summaries for outstanding
proof. Later state reports and terminal states preserve retained obligations.
Workflow-instance previews are bounded to 16 findings with exact totals;
paginated selected-instance history exposes each report's verification findings.
Verification observes retained evidence and never authorizes or runs an action.

### Workflow bottleneck analytics

The selected workflow milestone snapshot exposes `bottlenecks`: state durations, blocked intervals by code/Operation/owner, and verification wait by owner. Analytics use up to 1,024 retained reports independently of history pagination. Each breakdown shows up to 32 groups; totals cover the full window. Inspect `history_complete`, `incomplete_reasons` and truncation flags before treating durations as complete. Unknown verification starts are counted without inferred waits. State/blocker time uses application occurrence timestamps; verification uses publication timestamps. Concurrent blocker and verification groups can overlap.

### Compare workflow performance

The workflow performance summary endpoints require an explicit app, environment and workflow. Account readers can optionally select a customer; customer readers inherit ownership from credentials. Completed and ongoing cohorts each select the latest 100 retained instances, with matching/sample/coverage counts. Only complete retained histories enter state, blocker and verification duration distributions. Groups expose nearest-rank p50/p95 and total seconds from one accumulated duration per eligible workflow. Up to 32 groups are ranked by total time. Ongoing durations are elapsed observations, and completed workflows may still await verification. Inspect coverage and truncation before comparing results.

### Investigate performance contributors

The performance instance endpoints list complete-history contributors ranked by observed duration. Select `cohort` and `dimension`; state groups require state/version, blocker groups require Operation/code/version and an exact owner or `unassigned=true`, and verification owner groups require an exact owner or unassigned selection. Overall dimensions reject group selectors. Pass the summary’s `cohort_token` to preserve its evaluation and cohort; changed retained evidence returns `409 workflow_performance_cohort_changed` and requires refreshing the summary. Without a token this evaluates a fresh cohort. Each result exposes the business subject, current blocker ownership/next action and bounded pending verification findings. Historical contributing ownership can differ from current ownership.

### Workflow state SLA budgets

Workflow steps accept `state_sla_budget_seconds`, a map of declared nonterminal states to positive whole-second budgets. Publish a new workflow contract version when changing budgets. The current workflow state exposes optional `sla` data with `within_budget`, `breached`, or `unknown` status; updates that keep the same state preserve the visit clock. Missing or ambiguous retained entry history produces `unknown`.

Use the attention reason `sla_breached` to find known breaches. Performance cohorts expose configured, evaluated, breached, and unknown workflow counts, and state groups expose evaluated and breached visit counts, including visits that ended before completion. Budgets are observational and do not block application operations.

### SLA early warnings

Workflow steps optionally accept `state_sla_warning_percent`, mapping budgeted nonterminal states to whole percentages 1–99. For example, an 80% warning on a 1,800-second budget starts at 1,440 seconds. Pin threshold changes to a new workflow contract version.

Current `sla` data includes optional `warning_percent` and, when retained entry history is known, `warning_at`. Status becomes `at_risk` at that time, then `breached` at the due time. Unknown history implies neither status. Use attention reason `sla_at_risk`; matching summaries expose `sla_at_risk_workflow_count`. These observations do not enforce application actions.

## Reversible issue grouping

Issue actions support `merge` with `target_issue_id` and `split` with
`member_issue_id`. Canonical details expose `group_members`; occurrences and
activity retain `original_issue_id`. See [manual grouping and retained history](../../docs/issues.md#reversible-manual-grouping).

Managed compute storage client loading and rotation are described in the
[object storage compute guide](../../docs/object-storage-compute.md).
Use `faas_sdk.secret_reload_runtime.SecretReloadCoordinator` with
`storage.secret_consumer()` for startup, SIGHUP and manual adoption of atomic
credential snapshots. Start and finish on the main thread to manage SIGHUP
ownership and register every secret consumer. For automatic old-client cleanup,
pass `retire=...` to the storage binding and use `with storage.acquire() as lease`.
Consume/close response bodies before leaving that block. Managed bindings reject
raw `current()`. Stop admission and the coordinator before
`storage.wait_for_retirement(timeout=5)`; timeout never closes leased clients.
Without a hook, `current()` keeps caller-owned lifetime behavior.

Checked project release graphs use the preparation and activation endpoints.
Preparation returns a manifest with exact members, expected routing and policy
revisions; activation independently rechecks binding evidence and service
readiness. Internal service evidence is selected by `service_targets` together
with an exact caller deployment. See the [qualification and activation workflow](../../docs/networking.md#qualify-and-activate-a-project-release-graph)
for the CLI steps, consent requirements and graph limitations.


Checked graph recovery uses the `prepare_checked_project_rollback` and
`rollback_checked_project_release` project API modules to select retained history
and commit the verified manifest. Historical membership and expiry are checked
atomically under current binding policies. The new release records
`rollback_source_release_id` and `rollback_previous_release_id`. See the
[recovery workflow](../../docs/networking.md#recover-a-retained-project-release-graph).


Checked graph deactivation uses the `prepare_checked_project_deactivation` and `deactivate_checked_project_release` project API modules.
Preparation freezes one 100% weighted fallback per workload with current policy
and service targets. Qualify this `deactivate: true`, `ttl_seconds: 0` manifest,
then deactivate explicitly. Missing fallbacks, splits, stale intent and missing
readiness block the switch. The retired release's `deactivation` field contains
the durable receipt and exact fallback members. See the
[deactivation workflow](../../docs/networking.md#deactivate-a-project-graph-with-checked-fallback-routing).

Candidate service staging uses `POST .../release-sets/staging` with a complete
prepared forward or rollback manifest and a 1–1800 second `ttl_seconds` lease.
Read or cancel the stage with `GET` / `DELETE .../release-sets/staging/{stage}`.
Staging capacity is billable, preserves current routes, uses ordinary admission
limits, and excludes GitOps-managed workloads. Repeating the same manifest reuses
its original expiry. `ready` is observed replica readiness, not binding proof;
run verification and explicit activation/rollback afterwards. Cancellation or
expiry removes temporary capacity while active graph allocations remain.


Push queue canaries require an exact deployment, a declared safe `GREGALE_QUEUE_CANARY_BINDINGS` override and explicit publication consent. Create, get and cancel operations expose bounded publication/dispatch/completion status. The `handler_completion` queue release requirement checks fresh completion on the selected deployment plus normal consumer readiness. See [queue completion canaries](../../docs/networking.md#push-queue-binding-completion-canaries).

Durable single-app binding verification exposes create, status, resume, atomic
step admission, complete and cancel operations. Supply a stable run UUID and
review digest; original consent and deadlines are immutable, and observing an
existing task/message does not acquire ownership. See
[durable verification runs](../../docs/networking.md#durable-single-app-binding-verification).

Durable graph qualification preserves the reviewed manifest, member receipts and exact service targets. See [durable project graph verification](../../docs/networking.md#durable-project-graph-verification) for status, resume and cancel.

Binding verification review endpoints return an executable plan and opaque digests without admitting work. Call `ReviewBindingVerification` / `reviewBindingVerification` / `review_binding_verification`, or the corresponding project review method. Check `ready`, show blockers and required consent, then forward the returned selection and digest to receipt creation. Graph creation also forwards the aggregate `review_digest`, member `review_digests` and unchanged consent. No client-side digest calculation is required. A ready review is not a passing verification result.

### Run a durable binding verification receipt

Use the configured `FaaSClient` or its generated `inner` client after explicit review and receipt creation:

```python
from faas_sdk.preview import run_binding_verification, arun_project_binding_verification
run = run_binding_verification(client, "api", saved_run_id, concurrency=2,
                               on_progress=lambda progress: print(progress))
graph = await arun_project_binding_verification(client, "project", "production",
                                               saved_graph_run_id, concurrency=2)
```

Both app and graph helpers have synchronous and `arun_` asynchronous variants. The default poll interval is one second; concurrency defaults to two and ranges from one to eight unfinished resources across the graph. Progress callbacks run serially with frozen `BindingVerificationProgress` metadata; async helpers also accept async callbacks. A persisted passed receipt reports `qualifying` until server completion is reconfirmed.

`BindingVerificationRecoveryError` retains `run_id`, `cause` and the latest observed receipt dictionary. Resume the original ID after transport, callback or local stop errors; these never cancel server work or renew expiry. Use `stop_event=threading.Event()` for synchronous observation or `asyncio.Event()` for async observation. Synchronous stops are checked between HTTP calls, and the runner preserves configured HTTP timeouts while bounding them to remaining lifetime. Async task cancellation keeps `asyncio.CancelledError` semantics and carries `run_id`. Explicit server cancellation uses the existing generated cancel API. Review, effect consent, receipt creation and route activation stay explicit.

### Diagnose a stopped verification run

```python
from faas_sdk.api.apps import get_binding_verification_run_diagnostics
from faas_sdk.api.projects import get_project_binding_verification_run_diagnostics
diag = get_binding_verification_run_diagnostics.sync("api", saved_run_id, client=client.inner)
graph_diag = await get_project_binding_verification_run_diagnostics.asyncio("project", "production", saved_graph_run_id, client=client.inner)
```

Inspect `blockers`, whitelisted public `changes` and suggested `recovery` actions after a runner error. `complete` describes diagnostic read completeness; optional `check_passed` describes public evidence and conveys no admission or completion authority. Diagnostics preserve the original deadline and perform no execution, cancellation or receipt mutation. Missing domain permission fails the read. Continue with the same run ID only when current server checks allow it, or explicitly cancel and obtain a new reviewed receipt as directed.

Runner recovery errors now attach typed optional `diagnostics` containing server blockers, public changes and recovery suggestions. The read-only lookup has a two-second cap and respects the original receipt/caller deadlines. Unavailable, forbidden or mismatched diagnostics preserve the original cause and run ID. Cancellation and callback failures skip enrichment; no recovery action is executed automatically. Use `skip_diagnostics=True` to disable it.

### Create from a reviewed binding selection

Persist a canonical nonzero run UUID before creation. Pass a ready server review, lifetime and explicit effect consent; digests and selections are forwarded unchanged. Typed creation errors retain the UUID, original cause and whether creation was invoked. This flag does not prove acceptance. Reconcile uncertain creation by reading the same ID with the same review/options. Reconciliation never writes, resumes or replaces work; a failed read does not establish noncreation. Run a confirmed receipt explicitly using the existing runner.

```python
options = dict(run_id=saved_id, ttl_seconds=60, max_concurrency=2, queue_publish=True)
run = create_binding_verification_from_review(client, "api", review, **options)
# After an uncertain creation error, inspect the same identity:
recovered = reconcile_binding_verification_creation(client, "api", review, **options)
```

Graph helpers are `create_project_binding_verification_from_review` and `reconcile_project_binding_verification_creation`; all four have async equivalents prefixed with `a`. `BindingVerificationCreationError` exposes `run_id`, `attempted` and `cause`. Native async cancellation carries `run_id` and `attempted`.

Durable runner status GETs now retry temporary connection failures and HTTP 502/503/504 with bounded exponential jitter. Configure `status_read_retries` (default three; zero disables), maximum eight retries per read. Retry waits obey the original expiry and caller stop controls. Progress exposes `retrying`, `retry_attempt` and `retry_delay_ms`; callback failures stop observation. Lifecycle writes and invalid responses retain explicit recovery behavior.

### Discover retained verification receipts

Use `faas_sdk.api.apps.list_binding_verification_runs.sync(app, client=client, limit=20)` or `faas_sdk.api.projects.list_project_binding_verification_runs.sync(project, environment, client=client, limit=20)`; each also exposes `asyncio`. Filters include `cursor`, UUID `deployment_id` and `status`, plus app-only `scope`/`include_members`. Follow `next_cursor` with the same filters. The typed result contains `runs` and `observed_at`. Summaries describe retained receipt state without current requalification or private resource details. App lists omit graph members by default; included members carry `graph_run_id`. Project lists contain roots only. Existing retention remains unchanged.

After an eligible admission/completion transport failure or HTTP 502/503/504, durable runners reconcile through a bounded GET of the original receipt. They continue only after validating proof of the exact owned admission or fresh passed completion, and never automatically replay the uncertain write. Progress exposes `reconciling` with `operation` and admission selectors. Reads obey existing status retry settings, original expiry and caller stop controls. Unproved results preserve explicit recovery with the original ID and write cause. See [ADR-1366](../../docs/adr/1366-verification-write-reconciliation.md).

Reviewed creation now reconciles eligible transport failures and HTTP 502/503/504 through bounded reads of the original UUID. It returns only a matching usable receipt, without repeating creation or generating another UUID. Configure `status_read_retries`, `on_progress` and `stop_event`; read retries default to three, with zero disabling extra attempts. Progress emits `reconciling` with `operation: creation` and bounded `retrying` events. Recovery is capped at five seconds and the requested TTL measured from the attempt, plus caller context/stop controls. Unproved results preserve the typed creation error, attempted flag, original UUID and write cause. Explicit reconciliation remains a single read that can inspect terminal or expired receipts. See [ADR-1368](../../docs/adr/1368-verification-creation-reconciliation.md).

### Synchronous PostgreSQL pool runtime

Use `PostgresPoolBinding` to validate a fresh pool before atomic secret adoption and
retire old pools after their transaction leases release. Validation and retirement
callbacks are required; the application supplies its driver. Register the secret
consumer with the reload coordinator, hold each lease through driver connection
release, and invalidate before waiting for retirement at shutdown. See the
[pool lifecycle and SDK examples](../../docs/postgres-pool-runtime.md) and the
[runnable Node pg.Pool service](../../examples/postgres-pool-rotation/README.md).

### Async PostgreSQL pool runtime

Use `faas_sdk.preview.async_postgres_pool_runtime.AsyncPostgresPoolBinding` and
`faas_sdk.async_secret_reload_runtime.AsyncSecretReloadCoordinator` for async
drivers. Factory, validation and retirement hooks run on the same owning event
loop. Await coordinator startup before serving, hold `async with database.acquire()`
through transaction completion and connection return, then stop admission,
call `coordinator.stop()`, and await both `coordinator.wait_stopped()` and
`database.wait_for_retirement()` before closing the loop.

Manual reload supports an optional total `timeout` and `asyncio.Event` cancellation;
native task cancellation preserves `CancelledError`. Failed adoption/revocation
blocks new leases, existing transactions may finish, and cancelled late candidates
are cleaned up without publication. Register every consumer with one coordinator;
async consumers invalidate synchronously to fence work immediately. See the
[async lifecycle contract](../../docs/postgres-pool-runtime.md#async-python) and
[runnable FastAPI/psycopg service](../../examples/postgres-pool-rotation-python/README.md).

For a self-contained deployment with schema-backed readiness, separate release
migrations and bounded snapshot reconnect, use
`gregale init --template rest-api-postgres-python --path notes-api`. The
[built-in starter guide](../../cmd/gregale/templates/rest-api-postgres-python/README.md)
covers managed bindings, verified TLS, restore failure and cold-boot fallback.

### Async object-storage runtime

`faas_sdk.preview.async_object_storage_runtime.AsyncObjectStorageBinding` supplies async
factories, application validation and retirement on one event loop. Register its
secret consumer alongside PostgreSQL with one `AsyncSecretReloadCoordinator`;
the coordinator ACKs only after all consumers validate and invalidates them all
on failure. Keep `async with storage.acquire()` through complete upload/download
body closure. Failed and cancelled candidates are fenced and retired; old clients
close after their last lease. SDK core imports no S3 driver.

See the [async storage lifecycle](../../docs/object-storage-compute.md#async-python-clients-and-streaming)
and [FastAPI/aiobotocore streaming example](../../examples/object-storage-rotation-python/README.md)
for explicit credentials, readiness validation, disconnect cleanup, mixed binding
adoption and shutdown draining.

For a self-contained private file service, use
`gregale init --template object-storage-python --path files-app`. The
[built-in starter guide](../../cmd/gregale/templates/object-storage-python/README.md)
covers bearer authentication, bounded uploads, streaming downloads, managed
read/write bindings and bounded transport recovery after snapshot restore.

### Async deadline count alerts

`awatch_event_subscription_deadlines(client.inner, target, options)` supports
sustained total/reason deadline alerts, separate recovery counts, fresh-evidence
progress, task cancellation, checkpoint/resume and exact alert acknowledgments.
Configure `EventDeadlineTarget` and `EventDeadlineWatchOptions`; load saved state
with `validate_event_deadline_checkpoint` and apply offline acknowledgments with
`acknowledge_event_deadline_alerts`.

Python uses its own deadline-only checkpoint format; Go/CLI and Node checkpoints
are rejected. Applications own persistence and notification delivery. Persist before
publishing and deduplicate replayed alerts. See [configuration and cancellation](../../docs/event-deadline-python-watch.md).

`expired_ratio=EventDeadlineRatioOptions(warning_basis_points=1000,
recovery_basis_points=500, minimum_recipients=100)` adds an expired backlog ratio
condition with exact integer comparisons and retained source samples. Ratio watches
use version 2 Python checkpoints; count-only checkpoints remain version 1. See
[ratio policy and compatibility](../../docs/event-deadline-python-watch.md#expired-backlog-ratio-conditions).

`ainvestigate_event_deadline_alert(client.inner, checkpoint, reference, options)`
maps an exact retained Python incident/sequence pair to one current read-only triage
page. `EventDeadlineInvestigationOptions` controls page size, opaque continuation
and a cancellable read budget up to five seconds. Saved notification timestamps and
ratio samples remain separate from current observations. See
[investigation, validation and pagination](../../docs/event-deadline-python-investigation.md).

### Async deferred event acknowledgment worker

`AsyncEventAcknowledgmentWorker.from_headers(client.inner, app, headers, body,
account_id, app_id, options)` coordinates confirmed platform renewal, atomic
business SQL/consumer receipts and exact saved completion bytes. `run` invokes
the async handler with its cursor and read-only cancellation view; `complete_receipt`
recovers callbacks without business work. Explicit `retry`/`dead_letter` decisions
check receipts first and require known rollback, stopped work and exclusive job
ownership. Use an idle autocommit psycopg async connection and bounded driver calls.

Owner/native cancellation signals cooperative work, joins active SQL/renewal and
preserves confirmed receipt progress. Unknown COMMIT remains unknown. Configure
bounded callback retries and retain the protected ingestion job until terminal
confirmation. No schema, queue or host starts automatically. See the
[worker protocol](../../docs/event-acknowledgment-python-worker.md) and
[protected-job integration](../../examples/event-acknowledgment-python/README.md).

### Durable acknowledgment handoff

For side-effect-free preparation outside the final short SQL transaction,
`aclaim_event_consumer_processing(connection, request, duration_ms)` returns a
process-local lease with `renew` and `complete`. Explicitly install
`event_consumer_processing_schema` beside the receipt table. Use
`run_with_processing_lease` or `run_durable_with_processing_lease` for worker
composition. Existing receipts replay without the old lease; new writes require
its unexpired token/input fence. The host owns renewal during preparation and
must stop/join local renewal before final completion. See the
[Python processing protocol](../../docs/event-consumer-processing-python.md).

`run_prepared_durable(connection_factory, prepare, handler, options, stop_event=...)`
coordinates both leases during side-effect-free preparation, then commits the
same fenced receipt/intent transaction. `EventAcknowledgmentPreparedOptions`
requires `preparation_timeout_ms` and defaults `processing_lease_ms` to 30,000.
The synchronous factory supplies fresh exclusive idle autocommit async connection
scopes for each operation. Existing receipts skip preparation and new claims;
local renewal is joined before completion while platform renewal continues.
See [budgets, cleanup and uncertain commits](../../docs/event-acknowledgment-python-preparation.md).

Set keyword-only `EventAcknowledgmentPreparedOptions.on_event` for best-effort
preparation metadata. Frozen `EventAcknowledgmentPreparationEvent` variants report
bounded durations, acquisition/preparation, renewal failure, confirmed/unknown
commit and shutdown outcomes, without identities, payloads, tokens or raw errors.
Use a synchronous nonblocking callback and an application-owned bounded queue.
Observer exceptions preserve processing outcomes; no observer work is scheduled
or awaited. See [event fields and isolation](../../docs/event-acknowledgment-python-telemetry.md).

`EventAcknowledgmentPreparationMetrics` aggregates these events into fixed
counters and cumulative duration histograms. Pass `metrics.record` as `on_event`
and read detached JSON-safe `metrics.snapshot()` dictionaries from a separate
export path. Serialize access in one execution context; no exporter, background
task or retained event data is installed. Safe-integer saturation is explicit.
See [counting and export rules](../../docs/event-acknowledgment-python-metrics.md).

`format_event_acknowledgment_preparation_metrics(metrics.snapshot(), prefix=...)`
renders Prometheus text 0.0.4 with fixed labels, cumulative histograms in seconds
and saturation gauges. Serve with
`EVENT_ACKNOWLEDGMENT_PREPARATION_PROMETHEUS_CONTENT_TYPE` from the host's independent
scrape path. Complete validation precedes rendering; no registry, listener, task
or metrics dependency is added. See [formatting and the ASGI scrape example](../../docs/event-acknowledgment-python-prometheus.md).

The application-owned [shared Node/Python monitoring pack](../../examples/event-acknowledgment-monitoring/README.md)
provides a Python scrape configuration, guarded recording/alert rules, Grafana
dashboard and SDK-specific recovery runbook. Install one shared rule group per
evaluation domain, preserve target/process identity and tune thresholds through
your monitoring deployment process. No monitoring service is started by the SDK.

`ainspect_event_consumer_processing(connection, request, stop_event=...)` reads
frozen receipt/claim metadata for one original consumer request in one statement.
It reports matching/completed, active, expired, absent or conflicting evidence and
matching claim timing without selecting tokens, original bodies or saved results.
Use a separately leased idle autocommit async connection. Inspection grants no
ownership or acknowledgment proof; use existing live fences/receipt reconciliation
for recovery. See [Python inspection](../../docs/event-consumer-processing-inspection.md#python-inspection).

`run_durable` commits business SQL, the receipt and a completion intent together;
`save_completion_intent` stages a saved receipt without business work. Explicitly
install `event_acknowledgment_intent_schema` beside the inbox. A confirmed local
commit transfers callback responsibility from the ingestion job to recovery.

`arecover_next_event_acknowledgment_intent(client.inner, connection, account_id,
app_id, ...)` claims one due intent and sends only its saved callback. Separate
`aclaim_event_acknowledgment_intent` / `claim.deliver` calls and bounded
`alist_event_acknowledgment_intents` diagnostics are also available. Applications
own polling, protected storage, driver timeouts and retention. See the
[durable Python protocol](../../docs/event-acknowledgment-python-intents.md).

`arecover_event_acknowledgment_intents(client.inner, connection_factory, account_id,
app_id, EventAcknowledgmentIntentRecoveryPassOptions(...), stop_event=...)` runs one
finite concurrent pass. Each admission leases an exclusive connection scope and
uses existing intent fences. Empty probes/hard failures stop admission; active
work and cleanup join. Frozen aggregate `.result` progress accompanies failure,
native cancellation and uncertain-COMMIT exceptions. The host schedules later
passes. See [budgets, ownership and progress](../../docs/event-acknowledgment-python-recovery-pass.md).

Set `retry_policy=EventAcknowledgmentIntentRetryPolicy(initial_backoff_ms=1000,
max_backoff_ms=300000)` for capped exponential full jitter using persisted claims.
Single-intent recovery and `claim.deliver` accept the same option. Leave
`backoff_ms` omitted/None with a policy; without a policy the effective fixed
default remains 1000 ms. See the
[adaptive retry contract](../../docs/event-acknowledgment-python-retry-policy.md).

Durable staging accepts `retry_policy=EventAcknowledgmentIntentRetryPolicy(...)`
to atomically bind a new intent. Recovery uses its captured binding instead of
worker fallback, and status exposes frozen `retry_policy` metadata. Apply
`event_acknowledgment_intent_stored_retry_policy_schema` to existing installations
before upgrading current intent workers. See the
[shared binding and rollout contract](../../docs/event-acknowledgment-stored-retry-policy.md).

Use `aadopt_event_acknowledgment_intent_retry_policy` for existing unbound backlog.
Pass frozen `EventAcknowledgmentIntentAdoptRetryPolicyOptions` with a saved command
UUID and the exact timestamp/count from intent listing. Install the optional
`event_acknowledgment_intent_policy_adoption_schema` first. Frozen receipts resolve
original retries across SDKs; typed rejection errors expose their `.receipt`.
Python preserves full int64 generations, callback data, attempts and due time. See
the [adoption and uncertainty contract](../../docs/event-acknowledgment-policy-adoption.md#python-adoption-and-replay).

`save_retry_intent(connection, reason)` and `save_dead_letter_intent(connection,
reason)` stage explicit failure decisions for the same recovery host. Establish
known rollback, stopped work and exclusive ownership first, and persist the chosen
action/reason before staging. Receipt presence blocks rejection; identical saves
retain their claims/counters, and unknown COMMIT requires fresh-connection
reconciliation. See [failure decisions](../../docs/event-acknowledgment-python-intents.md#persist-explicit-failure-decisions).

### Acknowledgment recovery health

`aget_event_acknowledgment_intent_health(connection, account_id, app_id,
EventAcknowledgmentIntentHealthOptions(...), stop_event=...)` reads a bounded
summary of unfinished intents using one database snapshot/clock. The immutable
result includes due/leased/scheduled/stalled counts, expired claims, oldest ages
and explicit completeness. Use an exclusively leased idle autocommit async
connection, bounded driver calls, and always export `complete`/`truncated` alongside
metrics. See [Python recovery health](../../docs/event-acknowledgment-intent-health.md#python-summary).

`awatch_event_acknowledgment_intent_health(connection_factory, account_id, app_id,
EventAcknowledgmentIntentWatchOptions(...), notify, stop_event=...)` watches
sustained due/stalled/parked backlog. The factory returns a fresh async connection scope
per poll. Confirmation requires fresh complete evidence; failed reads or lost
coverage produce unknown states. Callbacks are awaited serially, cancellation
joins active work, and callback errors expose only the affected metadata alert.
State is in memory and resets on restart. See the
[Python watch](../../docs/event-acknowledgment-intent-health.md#python-watch).

`awatch_event_acknowledgment_intent_health_with_checkpoint(...,
EventAcknowledgmentIntentCheckpointWatchOptions(on_progress=..., resume=..., ...))`
adds stable incident IDs, a bounded pending outbox and fresh confirmation after
resume. Persist each checkpoint before notification attempts, and apply exact
pairs with `acknowledge_event_acknowledgment_intent_alerts`. Validate stored state
with `validate_event_acknowledgment_intent_checkpoint`. Storage fencing and
idempotent delivery remain application-owned. Cancellation exposes the detached
final result through `EventAcknowledgmentIntentCheckpointWatchCancelled.result`.
See the [Python checkpoint protocol](../../docs/event-acknowledgment-python-checkpoints.md).

Set the keyword-only `parked_threshold` to enable `parked_intents` alerts in
either watch, including a parked-only selection. Enabled checkpointed alerts use
version 2; zero/omission preserves version 1 and its saved policy keys/order.
Upgrade every host that can resume or acknowledge version 2 first. Incomplete
evidence stays unknown, and a lower parked count after operator resume or newer
staging does not establish callback confirmation. See
[parked evidence and rollout](../../docs/event-acknowledgment-python-checkpoints.md#parked-intent-alerts).

`EventAcknowledgmentIntentHealthMetrics(account_id, app_id, options)` caches one
bounded recovery-health sample. Feed completed reads explicitly or use
`awatch_event_acknowledgment_intent_health_metrics` with exclusive connection
scopes and host cancellation. Format a fresh snapshot per scrape with
`format_event_acknowledgment_intent_health_metrics`. The same thirteen Node gauge
families cover due/stalled/parked work, completeness and age, without identity
labels; missing reads omit sample gauges. See the
[Python cache/poller contract](../../docs/event-acknowledgment-python-recovery-prometheus.md)
and [shared Prometheus/Grafana integration](../../examples/event-acknowledgment-monitoring/README.md#recovery-health-python).

### Recovery claim budgets and parked callbacks

All durable staging methods accept `retry_budget=EventAcknowledgmentIntentRetryBudget(8,
EventAcknowledgmentIntentRetryPolicy(1000, 300000))`, mutually exclusive with
`retry_policy`. Install `event_acknowledgment_intent_retry_budget_schema` after the
v1 upgrade before current helpers, including v1-only hosts. Parked callbacks retain
protected inputs; recovery progress and health expose `parked`. Install the
optional `event_acknowledgment_intent_retry_resume_schema`, read exact metadata
with `aget_event_acknowledgment_intent_retry_resume_candidate`, durably persist
`EventAcknowledgmentIntentResumeRetryBudgetOptions`, then call
`aresume_event_acknowledgment_intent_retry_budget`. Exact original retries resolve
uncertain commits through historical receipts. Python retains full int64 generations
and exact microsecond fences. See
[budget installation, exhaustion and operator resume](../../docs/event-acknowledgment-retry-budgets.md#python-budgets-and-guarded-resume).

### Adopt recovery budgets for existing callback backlog

`aadopt_event_acknowledgment_intent_retry_budget` attaches v2 to selected active
unbound/v1 work with exact timestamp/count/policy guards, no live claim and no newer
attempt. Install `event_acknowledgment_intent_budget_adoption_schema`, select metadata
with `aget_event_acknowledgment_intent_budget_adoption_candidate`, then durably save
`EventAcknowledgmentIntentAdoptRetryBudgetOptions` before execution. None expected
policy requires unbound work; complete v1 timing is preserved. Frozen receipts
resolve exact original retries across SDKs, preserving native int64 generations
and microsecond fences. Known rejection errors expose their `.receipt`; uncertain
COMMIT requires retrying the saved command on a fresh connection. See
[budget adoption, guards and cancellation](../../docs/event-acknowledgment-budget-adoption.md#python-selection-adoption-and-replay).
