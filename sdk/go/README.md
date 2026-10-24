# gregale-go — Gregale Go SDK

> **PR 3 of issue #266** — the public Go SDK surface. The module is
> still internal to the monorepo and consumed only by the daemon's
> own tests; **publishing to the Go module proxy is gated on PR 13**.

This is the public import path for the Gregale platform:

```go
import faas "github.com/poyrazK/faas/sdk/go"
```

The package exposes:

- a typed `Client` covering every apid route, including disposable agent
  executions,
- bearer-auth + caller-supplied `Idempotency-Key` for replay safety,
- RFC 7807 error envelope + `errors.Is(err, faas.ErrNotFound)` sentinels,
- cursor pagination helpers (`ListDeploymentsAll`),
- SSE streaming via `Decoder` for app logs, deployment logs, dashboard events,
  and typed resumable disposable executions,
- server-side runtime flags with request middleware, bounded evidence, and
  managed-service propagation,
- functional `Option` for HTTP transport, retry, and logger.

## Install

```sh
go get github.com/poyrazK/faas/sdk/go
```

The SDK targets `go 1.23` (the floor of the daemon's own toolchain
at the moment of extraction). The daemon's `go.mod` is `go 1.26.10`,
but the SDK stays on 1.23 so a customer pinned to an older Go
toolchain can still consume it.

The SDK also verifies inbound Gregale webhook deliveries. See
[`docs/webhook-receiver-verification.md`](../../docs/webhook-receiver-verification.md)
for raw-body handling and delivery-ID deduplication guidance.

`github.com/poyrazK/faas/sdk/go/auth` verifies application Auth access tokens
and checks permissions in your backend: `auth.New`, `Verify`, `Require`
middleware and `Authorize`. See
[Application Auth](../../docs/application-auth.md#in-your-backend).

Managed Go workloads can use the runtime Flags client, request middleware, and
service transport. See [the Go Flags guide](../../docs/flags.md#use-flags-in-a-go-http-application)
for customer targeting, decision evidence, and propagation examples.

## Default and preview clients

`faas.Client` exposes generally available and beta capabilities only
(ADR-20261023115741). The methods of preview and internal capabilities
(realtime, operations, events, queues, object storage, managed Postgres,
disposable runs, issues, commit, data repair and more) are on
`preview.Client`, which embeds a `faas.Client`:

```go
import "github.com/poyrazK/faas/sdk/go/preview"

client, err := preview.NewClient("https://api.gregale.dev", token)
// or, for an existing *faas.Client: client := preview.Wrap(c)
```

The helpers for preview capabilities are in `preview` too: operation
handlers and transactions, the event outbox, acknowledgment and consumer
libraries, durable entities, data repair, commit, feature flags, issue
reporting, and the object storage and Postgres binding runtimes. apid serves a
preview capability only to the accounts it is released to; elsewhere its
calls answer `404 no such API route`. The examples below that use preview
capabilities import `preview` and use a `preview.Client`.

## Quick start

```go
package main

import (
    "context"
    "errors"
    "fmt"
    "log"
    "os"

    faas "github.com/poyrazK/faas/sdk/go"
)

func main() {
    c, err := faas.NewClient("https://api.example.com", os.Getenv("FAAS_TOKEN"))
    if err != nil {
        log.Fatal(err)
    }

    app, err := c.GetApp(context.Background(), "hello-world")
    if errors.Is(err, faas.ErrNotFound) {
        log.Fatal("app not found")
    }
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println(app.Slug, app.Status)
}
```

## Request tracing

The `tracing` package sends request, HTTP client and `database/sql` spans to
the Gregale debugger when the app sets `tracing.enabled`, using only the
standard library:

```go
import "github.com/poyrazK/faas/sdk/go/tracing"

shutdown := tracing.Start() // does nothing unless tracing is enabled
defer shutdown(context.Background())

http.DefaultTransport = tracing.Transport(http.DefaultTransport)
db, err := tracing.Open("pgx", os.Getenv("DATABASE_URL"), "postgresql")
log.Fatal(http.ListenAndServe(":"+os.Getenv("PORT"), tracing.Handler(mux)))
```

Pass the request's context to outgoing calls so they appear under the
request. See [`docs/tracing.md`](../../docs/tracing.md#go).

## Gregale Issues exception reporting

Create a deployment-bound issue ingest token with `gregale issues create-token`
and store it as `GREGALE_ISSUE_TOKEN`. The reporter queues bounded exception
evidence and sends it independently of trace sampling:

```go
// Also import context, os, time, and faas "github.com/poyrazK/faas/sdk/go".
issues, err := preview.NewIssueReporter(preview.IssueReporterOptions{
    BaseURL: os.Getenv("GREGALE_API_URL"),
    App:     "exports",
    Token:   os.Getenv("GREGALE_ISSUE_TOKEN"),
})
if err != nil {
    return err
}
shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
defer issues.Close(shutdownCtx)

if err := generateExport(); err != nil {
    issues.CaptureException(err, preview.IssueContext{
        RequestID: requestID,
        TraceID:   traceID,
        Route:     "/exports",
    })
    return err
}
```

Use `defer issues.RecoverAndRepanic(preview.IssueContext{SourceKind: "worker", InvocationID: invocationID})`
for a worker boundary that should report a panic while preserving normal panic behavior. Standard Go errors do
not retain creation-time stacks; capture records the current goroutine stack.
Call `Flush(ctx)` or `Close(ctx)` during graceful shutdown, and inspect
`Stats()` for queued, accepted, and dropped events. Delivery is best effort and
an ungraceful process exit can lose queued events. See
[`docs/issues.md`](../../docs/issues.md) for the full privacy and token guidance.

Set `IssueReporterOptions.GroupingStrategy` to `"failure-site-v1"` to group by
the nearest application reporting frame and a normalized message, or a stable
`IssueContext.ErrorCode`. `FingerprintOverride` takes precedence. The default
remains legacy grouping. Opting in creates version 2 groups without migrating
existing issue history; see the grouping transition guidance in `docs/issues.md`.

## Disposable agent executions

Runs execute in an isolated, networkless microVM with only ephemeral guest
scratch space. The SDK can reconnect an event stream from its last cursor and
return the terminal receipt in one call:

```go
receipt, err := c.Run(ctx, preview.CreateExecutionRequest{
    WorkflowID: "incident-42",
    StepLabel:  "collect logs",
    Runtime: faas.ExecutionRuntimeNode22,
    Source:  "console.log('hello')",
}, preview.RunOptions{
    OnEvent: func(event preview.ExecutionEvent) error {
        if event.Type == faas.ExecutionEventStdout {
            fmt.Print(event.Data.Chunk)
        }
        return nil
    },
})

summary, err := c.GetExecutionWorkflow(ctx, "incident-42")
if err != nil {
    log.Fatal(err)
}
fmt.Println(summary.StatusCounts, summary.Usage)
```

For long-lived consumers, call `c.WatchExecution` directly and repeatedly
call `Next`. `Cursor` exposes the latest replay position for checkpointing;
`Close` is idempotent and releases the active stream.

## Transactional operation handlers

Customer Operations HTTP definitions explicitly enable
`transaction_receipt: postgres_v1` with reconciliation recovery. Use
`CustomerOperationRequestFromHTTP` and `WithCustomerOperationTransaction`;
the callback returns ordinary `json.RawMessage`, without managed effects.
Approved recovery checks a scoped receipt before business code. The existing
`CustomerOperationReceiptSchema` is installed and retained by the application owner.
See [Customer Operations transaction adapter](../../docs/operation-transactions.md#customer-operations-http-adapter).

For managed HTTP operations, use `OperationRequestFromHTTP(r, originalBody)` and
`WithOperationTransaction(ctx, db, operation, callback)`. The callback receives an
`OperationSQLTransaction` and returns an `OperationOutcome`. The wrapper commits
business writes and the result/webhook intent together; retries return the saved
body without repeating committed writes.

Install `OperationReceiptSchema` explicitly as the database owner and send
`response.Body` unchanged as `application/json`. `response.Replayed` reports
recovery. See the [transactional handler guide](../../docs/operation-transactions.md)
for scope checks, receipt retention, and `ErrOperationCommitUnknown` handling.

For customer Operations, use `NewCustomerOperationRuntime` and
`(*CustomerOperationRuntime).Transaction`. Install
`CustomerOperationReceiptSchema` as the database owner before enabling
transaction support. Capture the original request body before decoding it, and
authorize the caller before invoking `Transaction`, because receipt replay
skips the callback. Construct the runtime once during application startup:

Use this path only for requests delivered by Gregale's guest listener; the
reserved headers are context, not authentication.

```go
operations, err := preview.NewCustomerOperationRuntime(preview.CustomerOperationRuntimeOptions{
    APIURL: os.Getenv("GREGALE_API_URL"),
})
if err != nil {
    return err
}

rawBody, err := io.ReadAll(r.Body)
if err != nil {
    http.Error(w, "invalid request", http.StatusBadRequest)
    return
}
orderID := r.PathValue("id")
if err := authorizeOrder(r.Context(), orderID); err != nil {
    http.Error(w, "forbidden", http.StatusForbidden)
    return
}

receipt, err := operations.Transaction(r.Context(), db, r, rawBody,
    func(tx *preview.CustomerOperationTransaction) (any, error) {
        var currentStatus string
        if err := tx.QueryRowContext(r.Context(),
            "SELECT status FROM orders WHERE id = $1 FOR UPDATE", orderID).Scan(&currentStatus); err != nil {
            return nil, err
        }
        if currentStatus != "fulfillment-in-progress" {
            return nil, errors.New("order is not ready for fulfillment")
        }
        if _, err := tx.ExecContext(r.Context(),
            "UPDATE orders SET status = $1 WHERE id = $2", "fulfilled", orderID); err != nil {
            return nil, err
        }
        if err := tx.Milestone("order-fulfilled", map[string]string{
            "order_id": orderID,
        }); err != nil {
            return nil, err
        }
        if err := tx.WorkflowTransition("order-lifecycle", workflowRunID,
            "fulfillment-in-progress", "completed"); err != nil {
            return nil, err
        }
        return map[string]string{"order_id": orderID, "status": "fulfilled"}, nil
    })
if err != nil {
    var publicationErr *preview.CustomerOperationPublicationError
    if errors.As(err, &publicationErr) && publicationErr.Committed {
        // Return a retryable response. Retrying the same Operation replays the
        // saved result and publishes pending facts without running the callback.
        http.Error(w, "committed; retry the same Operation", http.StatusServiceUnavailable)
        return
    }
    http.Error(w, "operation failed", http.StatusInternalServerError)
    return
}
w.Header().Set("Content-Type", "application/json")
_, _ = w.Write(receipt.Body)
```

Configure `APIURL` and, when needed, the loopback workload identity endpoint in
`CustomerOperationRuntimeOptions`. The runtime fetches a fresh
`gregale:operations` workload identity token for every validation and
publication call. `WorkflowState` records an explicit snapshot;
`WorkflowTransition` records a declared edge and checks its source against the
saved app-reported head. The application must also check its own locked business
row. A `CustomerOperationPublicationError` means the business write committed;
retry the same incoming Operation identity so the durable outbox can recover.
On success, send `receipt.Body` unchanged as `application/json`;
`receipt.Replayed` is true when the callback was skipped for a saved result.

Generate named workflow state types, constants, and transition helpers from the
manifest with `gregale customer-operations bindings --app orders --plan pro
--language go --output workflowbindings/bindings.go --package workflowbindings`.
The output directory must already exist. Call the generated `Transition_`
helper inside the transaction with the locked row's state and its required
milestone payloads. Propagate its returned error so the transaction rolls back.
Run the same command with `--check` in CI. See the
[binding guide](../../docs/operations.md#generate-application-workflow-bindings).

## Durable entity guest handlers

The separately gated guest protocol v2 accepts registered app-webhook intents
with a pure state transition. Use `DecodeDurableEntityHandlerRequest` on a body
bounded with `DurableEntityHandlerMaxRequestBytes`. Decode and validate your
business input before constructing a response:

```go
intent, err := call.WebhookIntent(webhookID, "reservation.confirmed", map[string]any{
    "reservation_key": call.Entity.Key, "quantity": 2,
})
if err != nil { return err }
next := json.RawMessage(`{"status":"reserved","quantity":2}`)
body, err := call.EncodeTransition(preview.DurableEntityTransition{
    Data: next, Result: next, Outbox: []preview.DurableEntityWebhookIntent{intent},
})
if err != nil { return err }
// Send body unchanged as application/json; Gregale performs the fenced commit.
```

The helper produces JSON only. Never send a notification or mutate an external
system during handler computation. V1 parsing/pure responses remain supported,
but outgoing work requires negotiated v2. Advertised batch limits come from
Gregale's central table; complete queue/snapshot/storage bounds remain platform
checks. Omit Outbox to append nothing; nil AlarmAt clears the alarm. Parsing an
envelope does not authenticate a public endpoint.

API retries retain the same entity `RequestID` and exact payload bytes; receivers
deduplicate the platform's stable message ID. Commit, transport acceptance and
receiver completion are separate outcomes. Both gates remain disabled pending
testing-agent qualification. See the
[reservation example](../../examples/durable-entity-reservations/README.md).

## Idempotency

Every mutating call (POST/PATCH/DELETE) carries an `Idempotency-Key`
header. The SDK **auto-mints a UUIDv4** if you don't supply one, so
naive `c.CreateApp(...)` calls are replay-safe out of the box.

For retries that need a stable key, pin one explicitly:

```go
ctx = faas.WithIdempotencyKey(ctx, "deploy-attempt-3")
dep, err := c.Deploy(ctx, slug, req)
```

A retried call with the same key returns the cached response from
the server's replay middleware (24h window).

## Service binding configuration

Create and read app policies with `ServiceCallerScopes`, `ServiceCallScope`,
and `ServiceBindingTransportHTTP` / `ServiceBindingTransportHTTPS`.
`AllowedServiceCallers` and `AllowedServiceCallScopes` in app responses preserve
unrestricted (`nil`) versus deny-all (non-nil empty) policies.

PATCH caller policies use `json.RawMessage` to preserve three states: leave the
field nil to retain its value, use `json.RawMessage("null")` to remove its
restriction, or use `json.RawMessage("[]")` for callers / `json.RawMessage("{}")`
for scopes to deny all. For grants, marshal typed values:

```go
scopes, err := json.Marshal(faas.ServiceCallerScopes{
    "frontend": {Methods: []string{"GET"}, PathPrefixes: []string{"/api"}},
})
if err != nil {
    return err
}
transport := faas.ServiceBindingTransportHTTPS
_, err = c.UpdateApp(ctx, "billing", faas.UpdateAppRequest{
    AllowedServiceCallScopes: scopes,
    ServiceBindingTransport: &transport,
})
```

Caller lists and scopes both apply when configured. HTTPS selects the canonical
injected URL scheme; it does not enforce HTTPS-only access. Set
`ExpectedServiceBindingFingerprint` from a reviewed app snapshot to reject an
update if its service configuration has changed.

`AppResponse.Manifest.ServiceBindingTransport` also exposes the scheme stored in
the workload manifest. Shared [SDK contract fixtures](../service-binding-contracts/README.md)
check these fields against Node, Python and OpenAPI in CI.

## Project release context

For app-to-app calls, capture the inbound release once and let the SDK
transport forward it to managed `*.svc.gregale` hosts. The transport strips
`X-Gregale-Revision` on those hops because a revision pin is scoped to the
caller app:

```go
serviceHTTP := &http.Client{
    Transport: faas.NewGregaleReleaseTransport(http.DefaultTransport),
}
handler := faas.GregaleReleaseMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, billingURL+"/health", nil)
    if err != nil {
        http.Error(w, "invalid billing URL", http.StatusInternalServerError)
        return
    }
    resp, err := serviceHTTP.Do(req)
    if err != nil {
        http.Error(w, "billing unavailable", http.StatusBadGateway)
        return
    }
    defer resp.Body.Close()
    // Handle resp.
}))
```

Wrap only the app handlers that receive Gregale ingress. The platform still
validates that the caller deployment belongs to the selected release; these
helpers preserve request context and do not grant deployment-selection
authority to the guest.

For a long-lived native client, use the client transport to capture a release
from its first managed API response and pin later calls from that transport
instance:

```go
releaseTransport, err := faas.NewGregaleClientReleaseTransport(
    http.DefaultTransport,
    faas.GregaleClientReleaseOptions{
        ManagedOrigins: []string{"https://api.example.com"},
        // Prefer a release supplied by SSR or application bootstrap when the
        // API must match the exact release that served the client.
        InitialRelease: releaseFromBootstrap,
    },
)
if err != nil {
    return err
}
apiHTTP := &http.Client{Transport: releaseTransport}
```

The first unseeded request is unpinned so Gregale can select the active set;
concurrent startup requests wait for its response before proceeding. Seed
`InitialRelease` when the client already knows the release that served it.
Only exact configured HTTP(S) origins receive or teach the pin. The transport
keeps the pin in memory per instance; use `Release` to inspect it and
`ClearRelease` only when intentionally starting a new client release context.
A 410 expired-release response is returned unchanged, without a retry or
fallback to the active release. Configure CORS to allow
`X-Gregale-Release` and expose it when the native client is cross-origin.

## Errors

Every 4xx/5xx with a Problem-shaped body returns `*faas.APIError`:

```go
app, err := c.GetApp(ctx, "missing")
if err != nil {
    var apiErr *faas.APIError
    if errors.As(err, &apiErr) {
        log.Printf("api: %s (%s)", apiErr.Problem.Code, apiErr.Problem.Detail)
    }
    // Or, for common cases:
    if errors.Is(err, faas.ErrNotFound) { ... }
    if errors.Is(err, faas.ErrRateLimited) { ... }
    if errors.Is(err, faas.ErrCapacity) { ... }
    if errors.Is(err, faas.ErrUnauthorized) { ... }
}
```

## Options

> **Three options are reserved until PR 12.** `WithBaseURL`,
> `WithToken`, and `WithDeployTimeout` return
> `errOptionUnsupported` today — the internal SDK's
> `baseURL` / `token` / `deployHTTP` fields are unexported and
> can't be mutated through the public wrapper yet. PR 12 promotes
> those fields and un-deprecates these options. Until then, callers
> needing to switch base URL, rotate a token, or set a deploy
> timeout must reconstruct the `Client` via `NewClient`.

```go
c, err := faas.NewClient(baseURL, token,
    faas.WithHTTPClient(myHTTPClient),           // custom transport
    faas.WithRetry(3, 200*time.Millisecond),     // bounded retry on 5xx/429 (PR 4)
    faas.WithLogger(slog.Default()),              // request/response logging (PR 4)
)
```

## Local development

```sh
cd sdk/go
go build ./...
go vet ./...
go test ./...
```

The CI gate is `.github/workflows/ci.yml::sdk-go` — a separate job
that runs `go build`, `go vet`, and `go test` inside `sdk/go/`. The
daemon's own `make test` walks only the daemon's package tree, so
the SDK needs its own gate (memory: `nested-go-module-needs-own-ci-gate`).

The module is a leaf: it imports only Go stdlib + the internal
`api` package. PR 12 trims `pkg/api/*` (in the daemon's main module)
to its server-only files; this module then becomes the canonical
home for the wire DTOs.

## Reference

- godoc: run `go doc -all ./...` from this directory.
- OpenAPI spec: `../../api/openapi.yaml` (canonical), `../../pkg/apid/openapi.yaml` (embedded).
- ADR-038 (issue #266): documents the split contract between the SDK and the daemon.
- PR plan: `/.claude/plans/lets-create-imp-plan-bubbly-engelbart.md` (the 14-PR sequence).


### Application standards

`PreviewApplicationStandardAssignment` saves affected applications, effective
settings and blockers without activating a change. `GetApplicationStandardReview`
reads that saved preview; its expiry and hash do not replace fresh approval
validation. `GetApplicationStandardOperation` returns queued/persisted/observed
progress. `GetApplicationStandardEnrollment` separates desired, persisted and
observed revisions and includes the installed exception deadline when present.
`ListApplicationStandardExceptions` returns UUID-ordered approval, revocation
and expiry history, including the server's `AsOf` time and an exclusive cursor.

Request and response types are exported from the SDK package, including
`ApplicationStandardReviewRequest`, `ApplicationStandardReview`,
`ApplicationStandardOperation`, and `ApplicationStandardExceptionList`.
`ApplicationStandardSettings` stores normalized `json.RawMessage` values by
field; `ApplicationStandardDefinition` stores typed rules with a JSON value.
Read routes require read scope and active organization membership; preview
requires write scope and owner/admin authority. Activation and exception
mutation APIs remain gated pending runtime acceptance.
## Object lifecycle

The public client exposes `GetObjectBucketLifecycle`,
`PutObjectBucketLifecycle`, `DeleteObjectBucketLifecycle`,
`CreateObjectLifecycleScan` and `GetObjectLifecycleScan`. Requests and responses
use exported `faas.ObjectLifecycle*` and `preview.ObjectBucketLifecycle*` types.
Configuration requires storage manage scope and a bucket write grant.

```go
days := int32(7)
policy, err := c.PutObjectBucketLifecycle(ctx, "demo", bucketID,
    preview.ObjectBucketLifecycleRequest{Rules: []preview.ObjectLifecycleRule{{
        ID: "temporary", Status: "Enabled",
        Filter: preview.ObjectLifecycleFilter{Prefix: "tmp/"},
        AbortIncompleteMultipartDays: &days,
    }}},
)
```

A replacement must contain at least one rule; use DELETE to clear it. Starting
or resuming a due scan returns its durable ID. A completed scan means discovery
finished; admitted cleanup can still be retrying. Removing rules preserves that
cleanup. See the [lifecycle guide](../../docs/object-storage.md#lifecycle-rules-and-discovery).
## Internal HTTP Operations preview

Operations is staged; production admission remains disabled. The typed client
includes `StartPlatformTenantSelfOperation`, status, cancellation, event pages
and resumable streams. Submission requires a caller-owned stable idempotency key.
`DownloadPlatformTenantSelfOperationArtifact` and `DownloadOperationArtifact`
verify the retained length and SHA-256 and reject credential-bearing redirects.
Account `RecoverOperation` requires the current generation and recovery evidence.
Account `InspectOperationRecovery` and `PreviewOperationRecovery` read confirmed
steps, uncertain attempts, retained file metadata and the proposed recovery plan.
Preview consumes no recovery or execution quota and grants no permission to repeat
external effects. Apply can supply `ExpectedInspectionRevision` to reject changed
execution evidence while preserving identical accepted receipt replay.

HTTP runtimes can call `ReportOperationProgress` and `AttachOperationArtifact`
with a fresh workload bearer and the invocation's `OperationRuntimeProof`.
The proof redacts its capability from formatted output and JSON. Do not persist
or share it between requests. See [Operations](../../docs/operations.md).

`GetOperationExecutionControl` uses the same fresh workload bearer and proof to
read cancellation intent, the admitted deadline and current lease. It creates
no report, event or renewal. Use the server's observation duration minus request
latency to bound application I/O and cooperate with cancellation. This read does
not fence external effects atomically or certify that stopped work can be retried.

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

Application-scoped producer keys:

```go
receipt, err := client.PublishAppEvent(ctx, "my-app", preview.AppPublishEventRequest{
    Key: "order-123-created",
    Type: "order.created",
    Data: json.RawMessage(`{"order_id":"123"}`),
})
```

The result includes `Duplicate` and the original durable `Receipt`. Preserve app/key/content on retry. Configure subscriptions against the returned `app.<UUID>` source. Deduplication lasts while the receipt is retained; consumer side effects still need deduplication.

Read-only producer-key reconciliation:

```go
status, err := client.GetAppEventPublishStatus(ctx, "my-app", preview.AppEventPublishStatusQuery{
    Key: "order-123-created",
})
```

`processing` and `accepted` prove retained acceptance, while accepted only means routing settled, not successful handler execution. `unavailable` cannot establish nonpublication and must not automatically trigger another publish. Follow `status.Evidence.NextAfter` using `After` (default 100, maximum 200 recipients). The standalone SDK exposes typed receipt metadata and lossless `json.RawMessage` recipient rows; the root Go SDK uses the existing full receipt DTOs.

Read-only content verification: `client.VerifyAppEventPublication(ctx, "my-app", originalRequest)` compares the original `AppPublishEventRequest` without publishing. Status is match/conflict/unavailable; match and conflict include the retained original receipt from the same snapshot. Comparison uses normalized type/schema version and semantic JSON data, excluding occurrence time and trace metadata. Matching acceptance does not prove consumer execution, and unavailable must not automatically trigger a publish.

Acceptance guards: parse the saved `accepted_at` with `time.RFC3339Nano` and pass its pointer as `AppEventPublishStatusQuery.ExpectedAcceptedAt`, or pass one optional `preview.AppEventAcceptanceGuard{ExpectedAcceptedAt: &savedTime}` to `VerifyAppEventPublication`. `Acceptance` reports same_acceptance/replacement_acceptance/unavailable independently of content/routing status. Comparison uses exact instants, without rounding; returned evidence describes the current retained acceptance even when it is a replacement. Existing unguarded calls remain supported.
### Workflow blockers

Inside the customer Operation transaction callback, use `tx.WorkflowBlockers(workflow, instanceID, lockedRowState, []preview.OperationWorkflowBlocker{...})` to replace
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

Use `client.ListAccountWorkflowAttention(ctx, "orders", preview.OperationWorkflowAttentionOptions{Scope: "production", Reason: "blocked"})` to read one page of current retained blocked or stale workflows.
The response includes public business references, workflow snapshots, blocker
reasons and a continuation cursor. Workflow and target Operation filters narrow
the queue; customer routes use identity from credentials. Continue with the same
filters and `next_cursor`; refresh the first page for the latest view. See
[the Operations guide](../../docs/operations.md#find-workflows-needing-attention).

### Explain a cleared blocker

Use `tx.WorkflowBlockers(workflow, instanceID, lockedState, remainingBlockers, resolution)` to attach an explicit public resolution fact to the transactional
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

```go
summary, err := client.SummarizePlatformTenantSelfWorkflowAttention(ctx, preview.OperationWorkflowAttentionSummaryOptions{
    OperationWorkflowAttentionOptions: preview.OperationWorkflowAttentionOptions{AppID: appID, Scope: "production"},
    GroupBy: "blocker_code",
})
```

Account clients also provide `SummarizeAccountWorkflowAttention(ctx, slug, opts)`.
`GroupBy` defaults to `workflow`; `target_operation` is also available, and
`customer` requires account mode. `BlockerCode` filters both queues and summaries.
Totals cover all matching workflows independently of paginated groups.

Install the updated `CustomerOperationReceiptSchema`. Transactional reports now
preserve each blocker's `FirstObservedAt` (optional RFC3339 string) until its
target/code is cleared. Legacy or unobserved continuity remains unknown;
applications may supply a known original date. Upgrade all writers so the
counter's blocker metadata stays current. Summary unknown-age counts distinguish
these blockers from known oldest ages.

#### Business deadlines

Inside the customer Operation transaction, call
`tx.WorkflowDeadline(workflow, instanceID, state, dueAt)` with a finite RFC3339
string to set/update the due time; `""` clears it. State comes from the locked
business row. The SDK preserves blockers on this update and inherits deadlines
on subsequent state, transition, and blocker reports. Install the updated
`CustomerOperationReceiptSchema` and upgrade every writer for counter continuity.
Queue/summary `Reason: "overdue"` selects active retained workflows whose due
time has passed; snapshots expose `DeadlineAt`, `Overdue`, and `OverdueSeconds`.

#### Explicit business outcomes

Inside the transaction, queue the required terminal transition and milestones,
then call `tx.WorkflowOutcome(workflow, instanceID, terminalState, code, description)`.
The pinned contract must declare that state terminal. The SDK preserves blockers
and due time, and retains the outcome on later reports of the same state.
State changes drop the inherited outcome. Install the updated receipt schema.

`ListAccountWorkflowOutcomes` / `ListPlatformTenantSelfWorkflowOutcomes` accept
`OperationWorkflowOutcomeOptions`. `SummarizeAccountWorkflowOutcomes` /
`SummarizePlatformTenantSelfWorkflowOutcomes` accept
`OperationWorkflowOutcomeSummaryOptions` with `GroupBy: "outcome"` (default),
`"workflow"`, or account-only `"customer"`. Each latest retained completed instance
with an explicit outcome counts once; these are not lifetime completion totals.

### Workflow prerequisites

Use `tx.WorkflowDependencies(workflow, instanceID, state, []preview.OperationWorkflowDependency{...})` inside the business transaction to replace up to 16 direct workflow dependencies. Pass an empty list to clear them. Links stay within the same customer/application/environment; an optional required outcome distinguishes successful prerequisites from other terminal results. Other reports inherit current links. Apply the updated customer schema and upgrade all writers first. The existing workflow instance response includes `related_workflows` with retained states and explicit resolution statuses. See [workflow dependencies](../../docs/operations.md#workflow-dependencies) for complete examples and retention semantics.

### Dependency attention

Attention requests support `OperationWorkflowAttentionOptions{DependencyStatus: "waiting", RequiredOutcomeCode: "paid"}` and the `dependency` reason. The response includes `dependency_attention` references/statuses and summary counts `dependency_workflow_count` / `dependency_count`. Summaries also support `dependency_status` and `required_outcome_code` grouping. Both dependency filters must match the same unresolved reference. See [dependency-aware attention](../../docs/operations.md#dependency-aware-attention).

### Reverse dependency impact

Existing business milestones responses now include typed `workflow_instance.dependency_impact`: retained dependent workflows, required outcomes, prerequisite statuses, and affected-workflow counts. The list shows up to 100 items, affected sources first; counts cover all matches and `has_more` signals truncation. Unknown account-side prerequisites require an explicit customer; self reads always use the authenticated customer. See [reverse dependency impact](../../docs/operations.md#reverse-dependency-impact).

### Dependency root-cause tracing

Business milestones responses include typed `workflow_instance.dependency_trace` findings with linked reference paths and observed states. The trace follows unmet prerequisites, distinguishes cycles from shared workflows, and exposes missing reports, blockers, mismatched outcomes, staleness, and missed deadlines. Traversal is bounded; inspect `truncated` / `limits_reached` before treating coverage as complete. See [dependency root-cause tracing](../../docs/operations.md#dependency-root-cause-tracing).

### Workflow transition readiness

Use an authenticated operations reader to check a proposed transition:

```go
result, err := client.CheckPlatformTenantSelfWorkflowReadiness(ctx, preview.OperationWorkflowReadinessRequest{
    AppID: appID, Scope: "production", Subject: preview.OperationSubject{Type: "order", ID: orderID},
    Workflow: "fulfillment", InstanceID: runID, Operation: "ship-order",
    FromState: "waiting", ToState: "shipping", Milestones: []string{"shipment-created"},
    StateRevision: revision, ContractVersion: 1,
})
```

Inspect `readiness.ready`, denial reasons, missing milestones, unmet prerequisites, and advisories. Account readers use the account readiness endpoint with an explicit customer selector. Planned names are not committed evidence; business-row checks, authorization, and transaction-time workflow/milestone validation still apply. See [workflow transition readiness](../../docs/operations.md#workflow-transition-readiness).

### Guard a transition inside the business transaction

After locking and reading the business row, call `tx.GuardedWorkflowTransition(ctx,
request, []preview.CustomerOperationPlannedMilestone{{Name: "approved", Payload: payload}},
client.CheckPlatformTenantSelfWorkflowReadiness)` before writing. Set the request's
`AppID`, scope, subject, operation, workflow instance, locked `FromState`, proposed
`ToState`, positive `StateRevision`, and `ContractVersion`. The checker must use
credentials for the transaction's customer. Return the error; use `errors.As` with
`*preview.CustomerOperationReadinessError` to inspect its `Response`.

The helper derives milestone names from actual payloads, checks readiness, and
queues the transition and evidence. Any guard error prevents commit even if caught.
Existing contract and payload validation still runs before commit.

### Business decision evidence

`tx.BusinessDecision("approval-decided", preview.OperationBusinessDecision{Workflow: "order-approval", InstanceID: runID, Code: "manual-review-approved", Description: "An authorized reviewer approved the order.", RuleID: "manual-approval", RuleVersion: "2026-10"})` queues a bounded explanation with the business transaction. Declare the milestone payload schema and bind its workflow step to `/decision/instance_id`. It uses existing precommit validation and outbox replay; no schema installation is needed. See [business decision evidence](../../docs/operations.md#business-decision-evidence) for declaration and history details.

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

Workflow final actions use the separate `OperationWorkflowRuntimeProof` with a
fresh Operations workload bearer. `UploadWorkflowOperationArtifact` streams an
`io.Reader` with an `OperationArtifactUploadRequest` containing the report ID,
filename, exact byte count and SHA-256. It needs no source URI or bucket writer.
Before retrying a lost transfer response, call `ReuseWorkflowOperationUpload`
with the same declaration. Only an authorized `Available == false` permits a new
transfer; errors do not. These low-level methods do not retry business work.
Keep the report ID and declaration stable across approved resumes: fresh native
proof rebinds the retained receipt without another transfer. Cancellation,
deadline expiry and stale proof deny both upload and receipt reuse. Files stay
private until confirmed final-step success or operator success reconciliation
publishes them through the existing download API.

For managed sources, `ReuseWorkflowOperationArtifact` and
`PrepareWorkflowOperationArtifact` remain available with a stable `obj://`
reference. Reconcile uncertain provider writes before authorizing another write.

Recovery decisions: `RecoverOperationWithReceipt` returns the public `OperationRecoveryDecision`. It acknowledges the original explicit
account-authorized decision, independently of current operation and delivery
status. Retrying the same decision ID and request never records a second
recovery. See [receipt-backed operator recovery](../../docs/ops/customer-operations-cli.md#resume-a-recovery-decision-after-losing-its-response).

Native Job Operation reporting uses a tokenless client and the scheduler's `OperationJobRuntimeProof`: `GetJobOperationExecutionControl`, `ReportJobOperationProgress`, and `PrepareJobOperationResult`. Control returns the verified account, app, customer identity and current task bounds. Preparing a result does not settle business success; the host confirms it on task exit. Direct Job retry/replay cannot replace account reconciliation. Native qualification is pending.

Job files use `ReuseJobOperationArtifact` and `PrepareJobOperationArtifact` on
the tokenless runtime client with `OperationJobRuntimeProof` and a stable
`OperationArtifactRequest`. Only an authorized `Available: false` response
permits a new source upload. Preparation verifies the owned private object and
retains its immutable copy; replay the same declaration after response loss.
A private receipt is published on host-confirmed success with a typed result
or explicit account success recovery. Approved Job retry clears file receipts.
Published files use the existing customer-scoped artifact download methods.

### Transactional event consumers

Install `EventConsumerReceiptSchema` in your application PostgreSQL database.
For authenticated internal subscription deliveries, construct an immutable
request with `EventConsumerRequestFromHTTP(r, body, accountID, appID)` and call
`WithEventConsumerTransaction(ctx, db, input, handler)`. The handler receives an
`EventConsumerSQLTransaction` and returns `json.RawMessage`. Business writes and
the receipt commit together; duplicate deliveries return the exact saved
`EventConsumerTransactionResult.Body` with `Replayed=true`. Acknowledge only after
the helper succeeds. The helper never installs or prunes application receipts.

Identity includes the subscription and durable acceptance ID, so consumers remain
independent and later reuse of a producer event ID is a new acceptance. Preserve
original body bytes on retry. Old deliveries without an acceptance header fail
closed. Authenticate the route; the header factory is not authentication for an
arbitrary public server. Callback code must use only its supplied transaction and
must not perform external effects or control the transaction lifecycle.

See [the transaction contract and retention rules](../../docs/event-consumer-transactions.md).

For long-running preparation, the opt-in [processing lease helper](../../docs/event-consumer-processing.md) provides durable claims, renewal and fenced transactional completion. HTTP success still follows completion.

Enqueue follow-up Commit work inside the event-consumer callback with `InsertCommitEventInTransaction(ctx, tx, event)`. Install both schemas in the same database and configure a Commit source. Business writes, follow-up work and the receipt commit together; duplicate inputs return the saved result. See [the combined transaction examples](../../docs/event-consumer-transactions.md#update-and-enqueue-follow-up-work-atomically).

For transactional publication to subscribers, configure an immutable `event` Commit destination with contract version 3. The existing insert helpers accept an optional schema version and run in the consumer callback transaction. See [publication setup and receipt recovery](../../docs/gregale-commit.md#version-3-application-event-publication-adr-935).

For Commit event publications, `WaitCommitSubscriber` waits for one original
application subscriber using guarded, paginated receipt reads. Supply a context
deadline; only `result.Status == "completed"` proves original execution success.
See [subscriber waits](../../docs/gregale-commit.md#wait-for-an-individual-publication-subscriber).

`GetCommitDeliveryReport` samples recent publication receipts and their original
subscriber outcomes. Counts cover sampled evidence only; use `NextAfter` and
`NextBefore` to continue. Read errors return the partial report alongside the error.

Use `GetCommitDeliveryReportWithPages` to traverse more recipient pages, or
`ResumeCommitDeliveryReport(ctx, saved, pages)` to continue the saved selection.
The per-publication budget is 1–20 pages. Resume merges subscribers without
counting an observation twice; saved completed coverage is not refreshed.

`RefreshCommitDeliveryReport(ctx, saved, pages)` rereads the saved selection,
returning fresh counts, observation timestamps, and changes against the baseline.
Missing evidence does not carry forward saved successes. Resume and refresh
have separate coverage semantics; see the Commit documentation.

Delivery report rows now separate original `status` from latest retained replay
`recovery` evidence. `recovery_counts` and refresh changes with `kind: recovery`
help identify remaining failures; missing evidence does not prove no replay occurred.

Use `PlanCommitRecovery(ctx, savedReport, pages)` to build a read-only recovery plan from fresh guarded receipt evidence. Plans include skip reasons and publication coverage issues; they never execute recovery actions.

Use `ApplyCommitRecovery(ctx, reviewedPlan, limit, pages)` for sequential, freshly revalidated execution recovery. Unknown writes stop the batch and require inspection. Routing apply now submits the matching acceptance guard from a fresh server offer; older unguarded offers remain skipped. See the Commit documentation for limits and partial results.

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

Use client.GetCommitRecoveryStatus(ctx, plan, applied, 50, 5).

Recovery status can resume saved output, preserving successful observations
and their timestamps. Unread items are inspected before failed reads are
retried. Selection snapshots bind resume to the original plan, apply result,
publication acceptance and subscriber filter. Counts and remaining coverage
are recomputed, and unknown attempts remain unknown.

Use client.ResumeCommitRecoveryStatus(ctx, plan, applied, previous, 50, 5).

Older status files without selection snapshots require a fresh status read. Resume keeps historical samples; omit resume to reread them.


Refresh saved Commit recovery observations with `RefreshCommitRecoveryStatus(ctx, plan, applied, previous, 50, 5)`. Refresh clears current evidence and reports before/after changes against a compact saved baseline. Use resume to finish a bounded refresh pass; refresh and resume are mutually exclusive. Unknown apply attempts remain unknown. See [the Commit guide](../../docs/gregale-commit.md#refresh-saved-recovery-observations).


Use `WatchCommitRecoveryStatus` for bounded recovery monitoring with detached progress callbacks. Partial passes resume before the next refresh. Queued attempts require matching terminal execution evidence; unknown attempts remain unknown. Timeout/read errors retain the last or partial status. See [monitoring usage](../../docs/gregale-commit.md#monitor-acknowledged-recovery-executions).


Opt into transient recovery-watch read retries with `WatchCommitRecoveryStatusWithRetry`. The total budget is 0..20, defaults to zero, and shares the overall timeout. Partial results reach progress callbacks before backoff; permanent errors stop immediately. See [retry behavior](../../docs/gregale-commit.md#retry-transient-monitoring-reads).


Recovery watch results include `monitoring` with stop reason, started passes, consumed retries, elapsed milliseconds and conservative terminal coverage. Progress snapshots report running; final results and attached errors report the outcome. Counters restart on resume. See [structured outcomes](../../docs/gregale-commit.md#structured-monitoring-outcomes).


Optionally require recent recovery evidence with `CommitRecoveryWatchOptions.MaxObservationAge` (default disabled; 100ms..24h). Stale and missing/future-timestamp observations are reread within existing budgets before settlement. Monitoring includes freshness counts. See [freshness policy](../../docs/gregale-commit.md#require-recent-recovery-observations).

Optional `CommitRecoveryWatchOptions.Diagnostics` includes bounded exact-recipient delivery blocker diagnostics, separate from replay attribution. Missing evidence and settled routing never prove handler completion. See [diagnostics](../../docs/gregale-commit.md#diagnose-recovery-delivery-blockers).

Exact routing lookup: `GetEventRecipientBlocker(ctx, EventRecipientBlockerQuery{Source: source, ID: eventID, SubscriptionID: subscriptionID, ExpectedAcceptedAt: acceptedAt})`. Inspect `Status`, `Reason`, `RoutingState` and optional `Recipient`; `not_waiting` describes routing only.

Diagnostic coverage rotates within the 50-read cap and resumes from checkpoints. `diagnostics_progress` reports separate coverage counts; `retained` samples keep their original timestamps. Diagnostic completeness never gates delivery settlement. See [resumable coverage](../../docs/gregale-commit.md#resume-diagnostic-coverage-across-large-selections).

Recipient diagnostic monitoring batches up to 50 exact acceptance-guarded lookups per request. The read-only `/v1/events/recipient-blockers` endpoint preserves ordered independent results and errors, with a shared five-second deadline. Diagnostic read counts remain recipient counts. A batch is not an atomic snapshot or proof of handler completion.

Use `CommitRecoveryWatchOptions{Diagnostics: true, MaxDiagnosticAge: 30*time.Second}` for watch, or `DiagnoseCommitRecoveryStatusWithOptions(ctx, status, previous, CommitRecoveryDiagnosticsOptions{MaxDiagnosticAge: 30*time.Second})` for standalone/resumed diagnostics. Zero disables the policy; enabled ages range from 100ms to 24h. `diagnostics_progress.freshness` separates fresh, stale and unread samples and records the check time; `fresh_complete` is independent of historical coverage and delivery settlement. Stale samples refresh before fresh ones within the covered refresh pool. Saved counters and age policies are recomputed on resume.

Repeated diagnostic samples expose `comparison`, compact `previous_observation`, and field-level `changes` with both observation timestamps. Comparisons require the same exact acceptance and increasing timestamps. Missing evidence resets the baseline; routing changes never imply handler completion. Checkpoints retain only the latest pair and recompute saved changes. `diagnostics_progress.compared` and `changed` count newly compared and changed recipients per pass, excluding retained samples. See [routing blocker change tracking](../../docs/gregale-commit.md#track-routing-blocker-changes).

`WatchCommitRecoveryStatusWithOptions` accepts `CommitRecoveryWatchOptions{WatchDiagnostics: true, MaxDiagnosticAge: 30*time.Second}`. This enables diagnostics and waits for complete, fresh diagnostic coverage independently of delivery settlement. Monitoring exposes `watch_mode`, `diagnostic_coverage_complete` and stop reason `diagnostic_coverage_complete`. Age is rechecked after callbacks; the recorded check time defines completion. Resume retains existing coverage and cursors. Batch transport failures use the shared read retry budget; individual item failures rotate until timeout. Unavailable answers count as coverage, and coverage never proves handler success.

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

For offline alert queue management, `AcknowledgeCommitRecoveryAlerts(status, acks)`
validates the historical outbox against its saved selection and returns a snapshot
with exact acknowledged pending entries removed. The input is unchanged. It does
not poll or restore live incident confirmation. Persist the returned snapshot
atomically under exclusive checkpoint ownership before treating acknowledgements
as durable. Passing no acknowledgements validates the queue for inspection/replay.

`client.GetEventConsumerDeliveryHealth(ctx, app, subscriptionID, "15m")` returns
routing health, bounded retained execution health, and an oldest-first pending
blocker sample in one request. Component timestamps describe independent reads.
Pending sample reason counts are sampled when truncated; execution history is
always incomplete. Latest successful completion is a retained observation, not
an all-time delivery watermark.

For local health threshold monitoring, call
`client.WatchEventConsumerDeliveryHealth(ctx, app, subscriptionID, options)` with
`EventDeliveryHealthWatchOptions`. Set `Interval`, `Timeout`, and `MaxAge`, and at
least one of `OldestPending`, `Retrying`, `DeadLetters`, `Paused`, `CircuitBlocked`,
or `CapacityBlocked`. Duration fields use `time.Duration`; count fields use int64;
zero disables a threshold. `OnProgress` receives detached health/condition snapshots
and latest-pass `Alerts` (`opened`, `resolved`, or `confirmation_lost`). Return an
error to stop. Deduplicate by incident ID and sequence, and persist externally if
needed: this watch is invocation-local and does not use a durable alert outbox.
Circuit/capacity durations require consecutive fresh samples; missing/incomplete capacity
aggregates and incomplete execution evidence cannot resolve alerts. Deadline/read/
callback failures return a partial result and error.

Delivery health `CapacityWaits` exposes complete effective capacity-reason counts
by scope with independent freshness and subscription binding. Local capacity watches
use these counts even when the pending event sample is truncated. Missing aggregates
from older servers remain unknown rather than becoming zero counts.

Delivery health `WaitingReasons` exposes the complete pending waiting-reason
partition, including zero entries, its own pending total, completeness, coverage,
observation time, and acceptance cutoff. `EventPendingWaitingReasonCountsComplete`
checks that a dictionary has the known keys, nonnegative counts, and an exact sum.
A nil aggregate from an older server means unavailable evidence. Backlog consumer
`PendingWaitingReasons` counts matching pending recipients before pagination.

Delivery-health watch options also accept `WaitingReasonCounts: map[string]int64{"ordering_blocked": 10, "retry_backoff": 100}` and `WaitingReasonDurations: map[string]time.Duration{"subscription_rate_limited": 5 * time.Minute}`. These use complete pending reason counts, independent of sample truncation. Duration thresholds measure consecutive fresh local nonzero observations (at least two), not individual recipient duration. Missing or invalid evidence loses confirmation. Both options use the existing invocation-local lifecycle notifications; see ADR-964.

For checkpointed delivery-health watches, set `DurableAlerts: true` and atomically persist `progress.Checkpoint` in `OnProgress` before sending its alert notifications. Resume with `Resume: &saved` and the same app, subscription, window and threshold selection. Saved incidents remain unknown until fresh evidence confirms or clears them; sustained durations restart while preserving incident IDs. `ValidateEventDeliveryHealthCheckpoint` checks historical state; `AcknowledgeEventDeliveryHealthAlerts` returns a copy with exact pending incident ID/sequence pairs removed. For offline acknowledgement, stop the watch, persist the returned checkpoint and resume it. For live acknowledgement, provide `PollAcknowledgements(ctx, checkpoint)` returning exact incident/sequence pairs. The watch proposes removals through `OnProgress` before adopting them in memory; persist that progress atomically. Repeated exact pairs are idempotent within the last 1,024 removed transitions, including across resumes. Foreign, future, fabricated or older evicted pairs fail without removing anything. The bounded outbox reserves closing capacity, never evicts notifications, and stops new openings when full. Replay is at least once: use session/sequence for consumer deduplication. Enabling durable alerts does not itself write files or deliver notifications. See ADR-965.

Configure watch `SLO: &EventDeliveryHealthSLOOptions{RoutingLatency: time.Second, RoutingFailureBudgetPct: 1, MinimumSamples: 10, Sustain: time.Minute}` for sustained window targets. `EvaluateEventConsumerDeliverySLO` exposes metric scope, sample counts and evidence eligibility; the watch separately enforces fresh advancing observations and at least two samples across the sustain duration. Retry and dead-letter targets are outcomes per second. Routing targets cover recorded outcomes only and become unknown when history was compacted. Execution window targets use durable per-subscription outcomes retained for 30 days, independently of receipt, invocation and attempt-history deletion. The response exposes `window_coverage`, window completeness, collection start, loss high-water mark and earliest evidence expiry. Retained invocation-state truncation does not disqualify complete ledger windows. Later windows can qualify once they start strictly after a loss boundary; legacy ledger baselines remain unknown pending reconciliation. Older responses retain the conservative ADR-968 checks. See ADR-969. Resume requires identical targets, minimum samples and sustain duration. See ADR-967.

## Inspect durable entity metadata

The owner-only preview endpoint `GET /v1/apps/{slug}/entities/inspect` accepts
required `namespace` and `key`, plus optional `environment` and
`platform_tenant_id`. It returns the committed version, alarm status and pending
outbox metadata without running the guest or exposing state/message payloads.
It requires account `apps:read` or admin permission and preview app enablement.
Missing delivery history is `unknown`; an empty queue does not prove delivery.

```go
inspection, err := client.InspectDurableEntity(ctx, "reservations", preview.DurableEntityInspectRequest{
    Namespace: "reservations", Key: "reservation:123",
})
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

```go
if inspection.Outbox.Exhausted {
    recovery, err := client.RetryDurableEntity(ctx, "reservations", preview.DurableEntityRetryRequest{
        Namespace: "reservations", Key: "reservation:123", Target: "outbox",
        ExpectedVersion: inspection.Version,
        ExpectedRecoveryRevision: inspection.RecoveryRevision,
        HeadID: inspection.Outbox.HeadID,
    })
    _ = recovery
    _ = err
}
```

For an alarm, use `Target: "alarm"` and `AlarmAt: inspection.Alarm.AlarmAt`,
omitting `HeadID`. Preserve any environment/customer selectors used for inspection.

## Typed entity handles and pure guest calls

```go
type CounterResult struct { Count int `json:"count"` }
handle, err := preview.NewDurableEntityHandle[map[string]int, CounterResult](client, "counter", preview.DurableEntityInspectRequest{
    Namespace: "counters", Key: "customer:456", Environment: "staging",
})
if err != nil { return err }
result, err := handle.Invoke(ctx, "stable-operation-id", map[string]int{"delta": 1})
```

Handles retain their app/entity/environment/customer selectors. Invoke always
requires an explicit request ID. After uncertain responses or result decoding
errors, retain the same ID and exact payload; result decoding can fail after a
successful commit. The returned typed result preserves acknowledged version and
replay status when its value cannot be decoded. `handle.Inspect` and
`handle.Retry` reuse the bound selectors; recovery never silently refreshes its
comparison fence.

Inside the existing trusted guest handler, use typed calls and a pure builder:

```go
type Counter struct { Count int `json:"count"` }
type Input struct { Delta int `json:"delta"` }
call, err := preview.DecodeDurableEntityCall[Counter, Input](body, Counter{})
if err != nil { return nil, err }
if call.Entity.Namespace != "counters" { return nil, errors.New("unexpected entity namespace") }
if call.Event == "alarm" { return call.Transition(call.State, nil).ClearAlarm().Encode() }
next := Counter{Count: call.State.Count + call.Payload.Delta}
return call.Transition(next, next).Encode()
```

Validate application fields, integer overflow and persisted schema explicitly.
Version-zero initialization is JSON-isolated; committed state is never reset on
decode failure. Transition builders preserve the existing alarm by default.
Use `ScheduleAlarm(deadline)` or `ClearAlarm()` explicitly, especially when
consuming alarm events. `Webhook(registeredID, event, payload)` appends an intent
and performs no send. A rejected intent poisons the builder even if its error is
ignored; `Encode` also enforces negotiated byte/batch bounds.

Guest computation must remain pure. Envelope decoding is not public endpoint
authentication, and encoding is not commit acknowledgement. Outgoing intents
require v2 opt-in. See [the typed example](../../examples/durable-entity-sdk/README.md)
and [ADR-938](../../docs/adr/938-typed-durable-entity-sdk.md). These local additions
have not been tested or built in this workspace.

## Application state schema migrations

`DecodeDurableEntitySchemaCall[State, Payload]` opts into stored
`{"schema_version": n, "data": ...}` envelopes. Supply a positive current
`DurableEntityStateSchema.Version`, pure `Initial`, `Validate`, and a complete
`Migrations[n]` chain where each function transforms JSON data from n to n+1.
Schema versions are uint32 values independent of the entity's commit version.

```go
schema := preview.DurableEntityStateSchema[Counter]{
    Version: 2,
    Initial: func() Counter { return Counter{} },
    Validate: func(state Counter) error {
        if state.Count < 0 { return errors.New("negative counter") }
        return nil
    },
    Migrations: map[uint32]func(json.RawMessage) (json.RawMessage, error){
        1: func(raw json.RawMessage) (json.RawMessage, error) {
            var old struct { Total *int `json:"total"` }
            if err := json.Unmarshal(raw, &old); err != nil { return nil, err }
            if old.Total == nil { return nil, errors.New("missing old total") }
            return json.Marshal(Counter{Count: *old.Total})
        },
    },
}
call, err := preview.DecodeDurableEntitySchemaCall[Counter, Input](body, schema)
if err != nil { return nil, err }
return call.Transition(call.State, call.State).Encode()
```

The final example performs a migration-only business transition; it still
commits through the normal invocation path and increments the commit version.
Production handlers should explicitly handle invocation/alarm input and consume
or replace due alarms. Preparation does not write state. `StoredSchemaVersion`
and `Migrated` describe the local upgrade; the schema call's `Transition` always
stores the current envelope and validates new state at encoding.

Missing steps, malformed/future envelopes and invalid outputs are rejected.
Unwrapped committed data requires an explicit `LegacyVersion`; do not set one
unless its decoder really matches existing data. Go struct decoding alone does
not enforce required JSON fields; use pointer fields/custom decoding and your
validator where necessary. All callbacks must be pure and deterministic.

Deploy schema-aware writers at the existing schema before upgrading. Legacy
application code that ignores the envelope is not fenced by this SDK contract.
Once upgraded state commits, rollback code must still understand that schema.
See [ADR-939](../../docs/adr/939-durable-entity-application-schema-migrations.md).
Tests and builds remain unverified in this workspace.

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

Use `client.ExportDurableEntity(ctx, slug, selectors)` and `client.RestoreDurableEntity(ctx, slug, request)`.

### Backups and restore preview

Operator-enabled backups capture application data hourly with eventual seven-day
retention. Owner read-scope clients can list backup metadata, read an exact backup
and preview a restore. Preview reports observed versions, recognizable schema
versions and preserved pending work. Compatibility remains `unverified`; validate
application data separately. Preview does not reserve a version or promise storage
capacity. Actual restore still requires deploy-write scope, expected version and
stable request ID. See [the operator guide](../../docs/runbooks/FaasDurableEntityBackups.md).

Use `ListDurableEntityBackups(ctx, slug, selectors, cursor)`, `GetDurableEntityBackup(ctx, slug, selectors, backupID)` and `PreviewDurableEntityRestore(ctx, slug, request)`.

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

Use `ValidateDurableEntityRestore(ctx, slug, request)` and set `request.ValidationDeploymentID` for restore. Guest helpers are `DecodeDurableEntityRestoreValidationRequest` and `EncodeDurableEntityRestoreValidation`; errors become rejection without exposing error text.

### Blocker responsibility

Workflow blockers accept optional public `Owner` and `NextAction` fields;
source-linked resolution facts accept `ResolvedBy`. Use the existing workflow
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

Blocker replacements also support paired `acknowledged_at` / `acknowledged_by`
and an optional `follow_up_at` deadline. Preserve these fields on later reports.
Acknowledgement keeps the blocker active and preserves its original age. Use
attention reasons `unacknowledged` and `follow_up_overdue` to find unattended work;
summary counts are available per owner. See the operations documentation for
public actor and timestamp validation rules.

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

Use `cause-chain-v1` to group wrapped exceptions by the deepest complete reported
cause. The dashboard preserves the outer exception and cause chain. See
[Issues cause-aware grouping](../../docs/issues.md#cause-aware-grouping) for
language semantics, incomplete chains, and compatibility.

Managed compute storage client loading and rotation are described in the
[object storage compute guide](../../docs/object-storage-compute.md).
Use `NewSecretReloadCoordinator` with `storage.SecretConsumer()` to adopt atomic
credential snapshots on startup, SIGHUP and manual refresh. Start with the
workload's lifetime context before serving requests. Opt into
`ObjectStorageBindingOptions[T]{Retire: ...}` and use `Acquire()`/`Release()`
through response-body closure to retire old clients safely. Managed bindings
reject raw `Current()` acquisition. After stopping admission and the coordinator,
use `WaitForRetirement(ctx)` with a fresh shutdown budget. Without a retirement
hook, `Current()` keeps caller-owned lifetime behavior. All registered consumers
must succeed before a revision is ACKed.

Checked project release graphs use the preparation and activation endpoints.
Preparation returns a manifest with exact members, expected routing and policy
revisions; activation independently rechecks binding evidence and service
readiness. Internal service evidence is selected by `service_targets` together
with an exact caller deployment. See the [qualification and activation workflow](../../docs/networking.md#qualify-and-activate-a-project-release-graph)
for the CLI steps, consent requirements and graph limitations.


Checked graph recovery uses `Client.PrepareCheckedProjectRollback` and
`Client.RollbackCheckedProjectRelease` to select retained history
and commit the verified manifest. Historical membership and expiry are checked
atomically under current binding policies. The new release records
`rollback_source_release_id` and `rollback_previous_release_id`. See the
[recovery workflow](../../docs/networking.md#recover-a-retained-project-release-graph).


Checked graph deactivation uses `Client.PrepareCheckedProjectDeactivation` and `Client.DeactivateCheckedProjectRelease`.
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

Save the receipt UUID before calling the existing review and create methods. Resume that same UUID after response loss:

```go
run, err := client.RunBindingVerification(ctx, "api", savedRunID,
    faas.BindingVerificationRunnerOptions{Concurrency: 2, PollInterval: time.Second})
// For a prepared project graph:
graph, err := client.RunProjectBindingVerification(ctx, "project", "production", savedGraphRunID,
    faas.BindingVerificationRunnerOptions{Concurrency: 2})
```

The runner returns success only after server-confirmed completion. One budget covers unfinished resources across all graph members. `OnProgress func(faas.BindingVerificationProgress) error` receives serial changes with receipt, member and resource IDs and statuses. Returning an error stops observation and preserves work. A persisted passed receipt reports `qualifying` until completion is reconfirmed.

Context cancellation and transport errors preserve work. Errors unwrap their original cause and expose the root ID through `*faas.BindingVerificationRecoveryError`; resume that ID without changing consent, targets or expiry. To cancel server work, call the existing cancel method explicitly. The runner does not create receipts or activate routes. Callbacks must return promptly; Go cannot interrupt a callback supplied by the caller.

### Diagnose a stopped verification run

```go
diag, err := client.GetBindingVerificationRunDiagnostics(ctx, "api", savedRunID)
graphDiag, err := client.GetProjectBindingVerificationRunDiagnostics(ctx, "project", "production", savedGraphRunID)
```

Inspect `blockers`, whitelisted public `changes` and suggested `recovery` actions after a runner error. `complete` describes diagnostic read completeness; optional `check_passed` describes public evidence and conveys no admission or completion authority. Diagnostics preserve the original deadline and perform no execution, cancellation or receipt mutation. Missing domain permission fails the read. Continue with the same run ID only when current server checks allow it, or explicitly cancel and obtain a new reviewed receipt as directed.

Verification receipt creation accepts `max_concurrency` (`MaxConcurrency` in Go), default two with a range of one through eight. This budget is frozen and enforced by the server across all drivers and graph members. Local runner concurrency can be lower; resume never changes the server budget. Runners wait and reread on `binding_verification_budget_full`, preserving the original receipt and deadline. Other capacity errors retain their recovery behavior. Diagnostics expose `budget` with the shared run ID, limit, unfinished resource count, waiting steps and fullness.

Runner recovery errors now attach typed optional `Diagnostics` / `ProjectDiagnostics` containing server blockers, public changes and recovery suggestions. The read-only lookup has a two-second cap and respects the original receipt/caller deadlines. Unavailable, forbidden or mismatched diagnostics preserve the original cause and run ID. Cancellation and callback failures skip enrichment; no recovery action is executed automatically. Use `SkipDiagnostics: true` to disable it.

### Create from a reviewed binding selection

Persist a canonical nonzero run UUID before creation. Pass a ready server review, lifetime and explicit effect consent; digests and selections are forwarded unchanged. Typed creation errors retain the UUID, original cause and whether creation was invoked. This flag does not prove acceptance. Reconcile uncertain creation by reading the same ID with the same review/options. Reconciliation never writes, resumes or replaces work; a failed read does not establish noncreation. Run a confirmed receipt explicitly using the existing runner.

```go
options := faas.BindingVerificationCreationOptions{RunID: savedID, TTLSeconds: 60, MaxConcurrency: 2, QueuePublish: true}
run, err := client.CreateBindingVerificationFromReview(ctx, "api", review, options)
// After an uncertain creation error, inspect the same identity:
run, err = client.ReconcileBindingVerificationCreation(ctx, "api", review, options)
```

Graph methods are `CreateProjectBindingVerificationFromReview` and `ReconcileProjectBindingVerificationCreation`. Use `errors.As` with `*BindingVerificationCreationError` (`RunID`, `Attempted`, `Unwrap`).

Durable runner status GETs now retry temporary connection failures and HTTP 502/503/504 with bounded exponential jitter. Configure `StatusReadRetries` (nil defaults to three; pointer to zero disables), maximum eight retries per read. Retry waits obey the original expiry and caller stop controls. Progress exposes `retrying`, `retry_attempt` and `retry_delay_ms`; callback failures stop observation. Lifecycle writes and invalid responses retain explicit recovery behavior.

### Discover retained verification receipts

Use `ListBindingVerificationRuns(ctx, app, BindingVerificationRunListOptions{Limit: 20})` and `ListProjectBindingVerificationRuns(ctx, project, environment, options)`. Options include `Cursor`, `DeploymentID`, `Status`, and app-only `Scope`/`IncludeMembers`. Follow `NextCursor` with the same filters. Results contain `Runs` and `ObservedAt`; summaries omit protected snapshots and resource output. Listed stored/expired states do not assert current qualification. App lists default to standalone receipts; included members identify `GraphRunID`. Project lists contain roots only. Receipt retention remains unchanged.

Durable runners wait for recognized final `qualification` conditions after successful probes. Progress reports `waiting_for_qualification` with typed public conditions; polling stays within the original receipt expiry and caller deadline/stop controls. Unknown conditions or review drift stop observation. Waiting does not admit new work or renew receipts. See [ADR-1365](../../docs/adr/1365-verification-qualification-waiting.md).

After an eligible admission/completion transport failure or HTTP 502/503/504, durable runners reconcile through a bounded GET of the original receipt. They continue only after validating proof of the exact owned admission or fresh passed completion, and never automatically replay the uncertain write. Progress exposes `reconciling` with `operation` and admission selectors. Reads obey existing status retry settings, original expiry and caller stop controls. Unproved results preserve explicit recovery with the original ID and write cause. See [ADR-1366](../../docs/adr/1366-verification-write-reconciliation.md).

Reviewed creation now reconciles eligible transport failures and HTTP 502/503/504 through bounded reads of the original UUID. It returns only a matching usable receipt, without repeating creation or generating another UUID. Configure `StatusReadRetries` and `OnProgress`; read retries default to three, with zero disabling extra attempts. Progress emits `reconciling` with `operation: creation` and bounded `retrying` events. Recovery is capped at five seconds and the requested TTL measured from the attempt, plus caller context/stop controls. Unproved results preserve the typed creation error, attempted flag, original UUID and write cause. Explicit reconciliation remains a single read that can inspect terminal or expired receipts. See [ADR-1368](../../docs/adr/1368-verification-creation-reconciliation.md).

### Go PostgreSQL pools

Use `PostgresPoolBinding` to validate a fresh pool before atomic secret adoption and
retire old pools after their transaction leases release. Validation and retirement
callbacks are required; the application supplies its driver. Register the secret
consumer with the reload coordinator, hold each lease through driver connection
release, and invalidate before waiting for retirement at shutdown. See the
[pool lifecycle and SDK examples](../../docs/postgres-pool-runtime.md) and the
[runnable Node pg.Pool service](../../examples/postgres-pool-rotation/README.md).

### Deferred delivery acknowledgments

Configure future subscription acceptances with `SetEventAcknowledgmentPolicy`.
An opted-in handler durably enqueues processing before returning HTTP 202; its
worker uses `RenewEventAcknowledgment` and `CompleteEventAcknowledgment` with the
secret delivery token and an appropriately scoped API credential. Inspect state
with `GetEventAcknowledgment`. See [the protocol and rollout requirements](../../docs/event-deferred-acknowledgments.md).

For saved application-owned callback intents,
`RecoverEventAcknowledgmentIntents` runs a finite pass with bounded attempts and
concurrency, joined context cancellation and scalar progress returned alongside
errors. It preserves committed settlements even when their callbacks return
uncertainty. See the [Go recovery pass contract](../../docs/event-acknowledgment-go-recovery-pass.md)
and [buildable host example](examples/event-acknowledgment-recovery/README.md).

Set `RetryPolicy: &preview.EventAcknowledgmentIntentRetryPolicy{}` on the pass for
capped exponential full jitter based on persisted recovery claims. Single-intent
recovery and claims expose `WithRetryPolicy` variants. Leave `Backoff` zero when
enabling the policy; omitted policies retain fixed delays. See the
[adaptive retry contract](../../docs/event-acknowledgment-go-retry-policy.md).

Use `RunDurableWithRetryPolicy` and the other binding-aware staging variants to
save a versioned policy atomically with a new intent. Captured bindings override
worker fallback and appear in `Intent.RetryPolicy` / listing metadata. Existing
installs need an explicit application schema upgrade before current Go intent
staging/recovery. See [stored policies and compatible-worker rollout](../../docs/event-acknowledgment-stored-retry-policy.md).

Use `AdoptEventAcknowledgmentIntentRetryPolicy` for existing unbound backlog,
guarded by the selected timestamp/count and absence of a live claim. Install the
optional `EventAcknowledgmentIntentPolicyAdoptionSchema` first and retain the
command UUID/original inputs for idempotent resolution of unknown commits. It
preserves callback data, count and due time. See the
[policy adoption contract](../../docs/event-acknowledgment-policy-adoption.md).

Use `RunDurableWithRetryBudget` and the other budget-aware staging variants to
bind a v2 total recovery-claim budget. Exhausted callbacks are durably parked
with their protected inputs retained; `ResumeEventAcknowledgmentIntentRetryBudget`
grants a larger total with timestamp/count/limit guards and idempotent receipts.
Listing, pass progress and bounded health expose parked work. Upgrade the schema
before current Go helpers and use current budget-aware Go, Node or Python hosts
exclusively for enabled scopes. See the
[budget, parking and resume contract](../../docs/event-acknowledgment-retry-budgets.md).

`WatchEventAcknowledgmentIntentHealthWithCheckpoint` retains stable incidents and
pending lifecycle alerts in application-persisted metadata. It provides exact
acknowledgments, fresh confirmation after resume and final state alongside context
cancellation. See the [Go checkpoint contract](../../docs/event-acknowledgment-go-checkpoints.md).

Set `EventAcknowledgmentIntentWatchOptions.ParkedThreshold` to enable sustained
`parked_intents` alerts in either Go watch, including a parked-only selection.
Checkpointed parked alerts use version 2 and the existing replay/acknowledgment
outbox; disabled parked alerts preserve version 1. Incomplete evidence stays
unknown, and a lower parked count does not prove callback confirmation. See
[rollout and evidence rules](../../docs/event-acknowledgment-go-checkpoints.md#parked-intent-alerts).

`NewEventAcknowledgmentIntentHealthMetrics` adds a concurrency-safe bounded cache,
with explicit `WatchEventAcknowledgmentIntentHealthMetrics` polling and a
cache-only `NewEventAcknowledgmentIntentHealthMetricsHandler` for `net/http`.
The thirteen Prometheus gauges share Node/Python evidence and missing-data rules;
failed reads omit backlog, and scraping starts no SQL or callback work. Reuse the
shared alerts/dashboard with the distinct Go target on loopback 9468. See
[metrics and host ownership](../../docs/event-acknowledgment-go-recovery-prometheus.md)
and the [buildable host](examples/event-acknowledgment-recovery/README.md#export-recovery-health-to-prometheus).

`RecoverEventAcknowledgmentIntentsWithMetrics` adds actual pass start/joined-return
observation with a separate scoped worker cache. It preserves original recovery
progress/errors and excludes health reads/scrapes from worker activity. Idle and
in-flight expectations, latest-pass outcomes and opt-in worker alerts distinguish
an overdue recovery host from failed database evidence. See
[Go worker liveness](../../docs/event-acknowledgment-go-worker-liveness.md).

For application event publication, `SaveEventOutbox` stages a producer-key request
in the business transaction; `RecoverNextEventOutbox` publishes retained due work
with leased recovery. Install the application-owned `EventOutboxSchema` explicitly.
See [the event outbox contract](../../docs/event-outbox.md), including the required
server identity guard and bounded retry/retention policy.

Use `ReconcileEventOutbox` to confirm a saved request from retained matching
acceptance without publishing, `ResumeEventOutbox` for explicit guarded control
within the original deadline, and `ListEventOutbox` for paginated unresolved
metadata. Unavailable verification never automatically resumes publication.

`GetEventOutboxHealth` summarizes retained unpublished work and
`WatchEventOutboxHealth` emits sustained local backlog, stall, blocked and expiry
alerts. See [the health guide](../../docs/event-outbox-health.md) for completeness,
freshness and existing-installation index requirements.

Set `CommandID` on outbox reconciliation/resume options for atomic retained
control results. `GetEventOutboxControlReceipt` recovers a command result and
`ListEventOutboxControlHistory` paginates one publication's retained controls.
See [command replay and retention](../../docs/event-outbox-control-history.md).

Outbox recovery can opt into `EventOutboxRetryPolicy` for capped exponential
jitter, Retry-After guidance, and lifetime claim limits. See
[policy and schema upgrade](../../docs/event-outbox-retry-policy.md) before enabling
it on an existing application outbox.

`RunEventOutboxWorker` adds bounded concurrent recovery, idle polling backoff and
cooperative shutdown draining around the existing outbox helpers. See
[worker lifecycle and callbacks](../../docs/event-outbox-worker.md) before wiring
it into application supervision.

Use `SaveEventOutboxWithRetryPolicy` for per-publication retry settings and
`GetEventOutboxRetryPolicy` to read their current revision. Guarded
`SetEventOutboxRetryPolicy` changes are always recorded in control history.
See [saved policies and required migration](../../docs/event-outbox-stored-retry-policy.md).

`SetEventOutboxPublishingPaused` controls app-scoped publishing with guarded,
replayable command receipts. `GetEventOutboxPublishingControl` reads current
state; command lookup/history APIs retain interventions separately from per-row
controls. See [pause fencing and upgrade](../../docs/event-outbox-publishing-control.md).

`SetEventOutboxRateLimit` configures an app-scoped token bucket shared by recovery
workers. Claim commit consumes capacity atomically with its lease and attempt;
rate-limited polls spend no attempts. Configuration uses revision guards and
replayable command receipts. Health exposes throttled work separately from due or
stalled work. Install the opt-in schema and replace older workers before relying
on enforcement. See [rate limits](../../docs/event-outbox-rate-limit.md).

For consumer admission, `SetEventSubscriptionRateLimit` configures a durable
per-subscription interval/burst bucket. Revision-guarded UUID commands return
applied or rejected receipts; lookup and bounded history support uncertain retries.
Existing pause, circuit and per-second caps remain in force. See
[consumer rate controls](../../docs/event-subscription-rate-limits.md).

`PreviewEventSubscriptionRateLimit` reviews proposed consumer bucket settings
without changing state. It returns the reviewed revision, projected capacity,
current overlapping controls and bounded deadline-risk evidence. Its rate-only
drain model is advisory and does not guarantee ordering or completion. See
[preview semantics](../../docs/event-subscription-rate-preview.md).

Set delivery-health watch `RateRisk: &EventDeliveryRateRiskOptions{ThrottledGrowth: 1,
DeadlineRisk: 1, DeadlineWithin: 5*time.Minute, Sustain: time.Minute, RecoverySustain: time.Minute}` to monitor
shared-bucket throttling with growing backlog, modeled deadline risk and approaching
effective deadlines. Both
warning and recovery require fresh sustained evidence. Progress exposes preview
coverage and contributors; incomplete pending samples become unknown. The conditions
support existing durable checkpoints and acknowledgements. See
[rate-risk alerts](../../docs/event-delivery-rate-risk-alerts.md).

`GetEventSubscriptionPendingSummary` reads full retained captured pending counts
and effective deadlines under fixed read budgets. Growth and approaching-deadline
watch conditions use that complete evidence beyond the preview's 1000-row cap;
modeled-risk conditions retain the cap. See [full summaries and checkpoint
compatibility](../../docs/event-subscription-pending-summary.md).

`ListEventSubscriptionDeadlines` lists retained pending delivery identities by
effective deadline with absolute bounds, expired/upcoming classification and
scoped cursors. Pages are live and read-only; no payloads or recovery actions are
included. See [deadline triage](../../docs/event-subscription-deadline-triage.md).

`GetEventSubscriptionDeadlineWindow` counts upcoming deadlines within a selected
horizon and partitions upcoming/expired cohorts by observed waiting reason. Set
`RateRisk.DeadlineWindow` to `EventDeliveryDeadlineWindowOptions{Within: 5*time.Minute,
Upcoming: 50, Expired: 1}` for sustained count alerts. The selected endpoint replaces
the standalone summary read. See [window counts and alert semantics](../../docs/event-subscription-deadline-window.md).

Set `DeadlineWindow.UpcomingReasons` and/or `.ExpiredReasons` to known-reason count
maps for independent sustained incidents, such as `subscription_paused: 10`. These
reuse the existing aggregate read and retain total-alert compatibility. See
[reason-specific deadline alerts](../../docs/event-subscription-deadline-window.md#alerts-for-selected-waiting-reasons).

Delivery-health `Confirmations` now explain rate/deadline warning and recovery
sustain, growth baseline waits, resume requirements and notification capacity.
Entries join conditions by metric and add no requests or checkpoint proof. See
[confirmation diagnostics](../../docs/event-delivery-confirmation-progress.md).

Deadline count alerts accept `UpcomingRecovery` / `ExpiredRecovery` pointers and
`UpcomingReasonsRecovery` / `ExpiredReasonsRecovery` maps for separate inclusive
recovery boundaries. Defaults remain warning minus one; explicit zero requires
zero. Recovery must be below an enabled warning count. See
[configuration and checkpoint compatibility](../../docs/event-subscription-deadline-window.md#separate-opening-and-recovery-counts).

Set `DeadlineWindow.ExpiredRatio` to `EventDeliveryDeadlineRatioOptions` for an
expired / pending-with-deadlines ratio condition. Select integer basis-point warning
and recovery thresholds plus an explicit `MinimumRecipients`; insufficient cohorts
remain unknown, never fabricated zero. The same full-count read and version 2
checkpoint handling apply. See [ratio alerts and exact comparisons](../../docs/event-deadline-ratio-alerts.md).

`InvestigateEventDeadlineAlert(ctx, app, alert, EventDeadlineInvestigationOptions{Limit: 25})`
maps a Go/CLI count or ratio alert to one read-only page of current deadline triage.
It preserves reason filters, scope checks and opaque pagination, returning a separate
alert reference and current observation timestamps. It does not reconstruct historical
membership or perform recovery actions. See [investigation usage and limits](../../docs/event-deadline-alert-investigation.md).

Set `EventDeadlineInvestigationOptions{Limit: 25, Explain: true}` to add one
acceptance-guarded blocker batch within the same five-second read budget. Explanation
mode requires a page limit at most 50 and preserves one result per triage row,
including unavailable/replaced evidence and independent read errors. Diagnostic
timestamps and ordering-blocker identities remain separate from triage observations.
Check `Explanations.ReadComplete` for read coverage. See
[blocker explanation semantics](../../docs/event-deadline-alert-investigation.md#blocker-explanations).

### MCP readiness notification queues

MCP readiness notification queues expose separate `Destinations` with independent
heads and retry status. Use `ListMCPReadinessNotificationQueues` to inspect them,
then `ActMCPReadinessNotification` with `Destination: "primary"` or `"fallback"`,
the head's `DeliveryVersion`, a stable request UUID and operator reason. Omit
`Destination` for the whole-event action. `GetMCPReadinessFallback` and
`SetMCPReadinessFallback` manage the rule's optional signed fallback;
`ListMCPReadinessIncidents` includes per-receiver delivery history. These APIs
require the normal app ownership, scopes and MFA checks.

### Adopt claim budgets for existing callback backlog

`AdoptEventAcknowledgmentIntentRetryBudget` attaches v2 to a selected active unbound/
v1 intent with exact timestamp/count/policy guards, no live claim and no newer
invocation attempt. Nil `ExpectedPolicy` requires unbound work; a v1 expectation
must preserve timing. The total must exceed consumed claims; counts and due times
remain unchanged. Install `EventAcknowledgmentIntentBudgetAdoptionSchema` beside
the budget-upgraded table and durably save each command before execution. Exact
retries resolve uncertain commits through historical receipts without another
COMMIT. See the [budget-adoption contract](../../docs/event-acknowledgment-budget-adoption.md).
