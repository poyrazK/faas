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
at the moment of extraction). The daemon's `go.mod` targets Go 1.26.0
and selects Go 1.26.9 as its toolchain; the SDK
stays on 1.23 so a customer pinned to an older Go toolchain can still
consume it.

The SDK also verifies inbound Gregale webhook deliveries. See
[`docs/webhook-receiver-verification.md`](../../docs/webhook-receiver-verification.md)
for raw-body handling and delivery-ID deduplication guidance.

Managed Go workloads can use the runtime Flags client, request middleware, and
service transport. See [the Go Flags guide](../../docs/flags.md#use-flags-in-a-go-http-application)
for customer targeting, decision evidence, and propagation examples.

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

## Gregale Issues exception reporting

Create a deployment-bound issue ingest token with `gregale issues create-token`
and store it as `GREGALE_ISSUE_TOKEN`. The reporter queues bounded exception
evidence and sends it independently of trace sampling:

```go
// Also import context, os, time, and faas "github.com/poyrazK/faas/sdk/go".
issues, err := faas.NewIssueReporter(faas.IssueReporterOptions{
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
    issues.CaptureException(err, faas.IssueContext{
        RequestID: requestID,
        TraceID:   traceID,
        Route:     "/exports",
    })
    return err
}
```

Use `defer issues.RecoverAndRepanic(faas.IssueContext{SourceKind: "worker", InvocationID: invocationID})`
for a worker boundary that should report a panic while preserving normal panic behavior. Standard Go errors do
not retain creation-time stacks; capture records the current goroutine stack.
Call `Flush(ctx)` or `Close(ctx)` during graceful shutdown, and inspect
`Stats()` for queued, accepted, and dropped events. Delivery is best effort and
an ungraceful process exit can lose queued events. See
[`docs/issues.md`](../../docs/issues.md) for the full privacy and token guidance.

## Disposable agent executions

Runs execute in an isolated, networkless microVM with only ephemeral guest
scratch space. The SDK can reconnect an event stream from its last cursor and
return the terminal receipt in one call:

```go
receipt, err := c.Run(ctx, faas.CreateExecutionRequest{
    WorkflowID: "incident-42",
    StepLabel:  "collect logs",
    Runtime: faas.ExecutionRuntimeNode22,
    Source:  "console.log('hello')",
}, faas.RunOptions{
    OnEvent: func(event faas.ExecutionEvent) error {
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

## Object lifecycle

The public client exposes `GetObjectBucketLifecycle`,
`PutObjectBucketLifecycle`, `DeleteObjectBucketLifecycle`,
`CreateObjectLifecycleScan` and `GetObjectLifecycleScan`. Requests and responses
use exported `faas.ObjectLifecycle*` and `faas.ObjectBucketLifecycle*` types.
Configuration requires storage manage scope and a bucket write grant.

```go
days := int32(7)
policy, err := c.PutObjectBucketLifecycle(ctx, "demo", bucketID,
    faas.ObjectBucketLifecycleRequest{Rules: []faas.ObjectLifecycleRule{{
        ID: "temporary", Status: "Enabled",
        Filter: faas.ObjectLifecycleFilter{Prefix: "tmp/"},
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
