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

HTTP handlers can import `GregaleOperations` from
`faas_sdk.operations_runtime` and use its `run_request(headers)` async context
manager around a trusted Gregale guest request. Call `progress(report)` or
`artifact(report)` with the generated request models and a stable `report_id`.
The helper fetches fresh workload identity for each report, rejects native
execution context and isolates authority between concurrent requests. Its public
`context()` omits the ephemeral capability. The caller owns the injected
`httpx.AsyncClient` and closes it during shutdown.

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
dead-letter tools apply. See [ADR-833](../../docs/adr/833-recovery-execution-completion-notifications.md).

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
See [execution recovery health](../../docs/adr/835-execution-recovery-health-alerts.md).

Use `faas_sdk.api.events.get_event_recovery_notifications` for a read-only report
of admission/execution capture and each selected receiver's current delivery.
Missing selection or pruned delivery evidence remains unknown; capture alone
does not prove acknowledgement. Retained dead deliveries link to independent
retry. See [notification delivery reports](../../docs/adr/836-recovery-notification-delivery-report.md).

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
