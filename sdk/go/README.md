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

For managed HTTP operations, use `OperationRequestFromHTTP(r, originalBody)` and
`WithOperationTransaction(ctx, db, operation, callback)`. The callback receives an
`OperationSQLTransaction` and returns an `OperationOutcome`. The wrapper commits
business writes and the result/webhook intent together; retries return the saved
body without repeating committed writes.

Install `OperationReceiptSchema` explicitly as the database owner and send
`response.Body` unchanged as `application/json`. `response.Replayed` reports
recovery. See the [transactional handler guide](../../docs/operation-transactions.md)
for scope checks, receipt retention, and `ErrOperationCommitUnknown` handling.

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
body, err := call.EncodeTransition(faas.DurableEntityTransition{
    Data: next, Result: next, Outbox: []faas.DurableEntityWebhookIntent{intent},
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

HTTP runtimes can call `ReportOperationProgress` and `AttachOperationArtifact`
with a fresh workload bearer and the invocation's `OperationRuntimeProof`.
The proof redacts its capability from formatted output and JSON. Do not persist
or share it between requests. See [Operations](../../docs/operations.md).

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

## Inspect durable entity metadata

The owner-only preview endpoint `GET /v1/apps/{slug}/entities/inspect` accepts
required `namespace` and `key`, plus optional `environment` and
`platform_tenant_id`. It returns the committed version, alarm status and pending
outbox metadata without running the guest or exposing state/message payloads.
It requires account `apps:read` or admin permission and preview app enablement.
Missing delivery history is `unknown`; an empty queue does not prove delivery.

```go
inspection, err := client.InspectDurableEntity(ctx, "reservations", faas.DurableEntityInspectRequest{
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
    recovery, err := client.RetryDurableEntity(ctx, "reservations", faas.DurableEntityRetryRequest{
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
handle, err := faas.NewDurableEntityHandle[map[string]int, CounterResult](client, "counter", faas.DurableEntityInspectRequest{
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
call, err := faas.DecodeDurableEntityCall[Counter, Input](body, Counter{})
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
and [ADR-848](../../docs/adr/848-typed-durable-entity-sdk.md). These local additions
have not been tested or built in this workspace.

## Application state schema migrations

`DecodeDurableEntitySchemaCall[State, Payload]` opts into stored
`{"schema_version": n, "data": ...}` envelopes. Supply a positive current
`DurableEntityStateSchema.Version`, pure `Initial`, `Validate`, and a complete
`Migrations[n]` chain where each function transforms JSON data from n to n+1.
Schema versions are uint32 values independent of the entity's commit version.

```go
schema := faas.DurableEntityStateSchema[Counter]{
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
call, err := faas.DecodeDurableEntitySchemaCall[Counter, Input](body, schema)
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
See [ADR-849](../../docs/adr/849-durable-entity-application-schema-migrations.md).
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
[ADR-851](../../docs/adr/851-durable-entity-owner-state-recovery-api.md).

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
See [ADR-853](../../docs/adr/853-durable-entity-application-validated-restore.md).

Use `ValidateDurableEntityRestore(ctx, slug, request)` and set `request.ValidationDeploymentID` for restore. Guest helpers are `DecodeDurableEntityRestoreValidationRequest` and `EncodeDurableEntityRestoreValidation`; errors become rejection without exposing error text.
