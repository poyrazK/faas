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
at the moment of extraction). The daemon's `go.mod` is `go 1.26.9`,
but the SDK stays on 1.23 so a customer pinned to an older Go
toolchain can still consume it.

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

For customer Operations, use `NewCustomerOperationRuntime` and
`(*CustomerOperationRuntime).Transaction`. Install
`CustomerOperationReceiptSchema` as the database owner before enabling
transaction support. Capture the original request body before decoding it, and
authorize the caller before invoking `Transaction`, because receipt replay
skips the callback. Construct the runtime once during application startup:

Use this path only for requests delivered by Gregale's guest listener; the
reserved headers are context, not authentication.

```go
operations, err := faas.NewCustomerOperationRuntime(faas.CustomerOperationRuntimeOptions{
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
    func(tx *faas.CustomerOperationTransaction) (any, error) {
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
    var publicationErr *faas.CustomerOperationPublicationError
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

### Workflow blockers

Inside the customer Operation transaction callback, use `tx.WorkflowBlockers(workflow, instanceID, lockedRowState, []faas.OperationWorkflowBlocker{...})` to replace
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

Use `client.ListAccountWorkflowAttention(ctx, "orders", faas.OperationWorkflowAttentionOptions{Scope: "production", Reason: "blocked"})` to read one page of current retained blocked or stale workflows.
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
summary, err := client.SummarizePlatformTenantSelfWorkflowAttention(ctx, faas.OperationWorkflowAttentionSummaryOptions{
    OperationWorkflowAttentionOptions: faas.OperationWorkflowAttentionOptions{AppID: appID, Scope: "production"},
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

Use `tx.WorkflowDependencies(workflow, instanceID, state, []faas.OperationWorkflowDependency{...})` inside the business transaction to replace up to 16 direct workflow dependencies. Pass an empty list to clear them. Links stay within the same customer/application/environment; an optional required outcome distinguishes successful prerequisites from other terminal results. Other reports inherit current links. Apply the updated customer schema and upgrade all writers first. The existing workflow instance response includes `related_workflows` with retained states and explicit resolution statuses. See [workflow dependencies](../../docs/operations.md#workflow-dependencies) for complete examples and retention semantics.

### Dependency attention

Attention requests support `OperationWorkflowAttentionOptions{DependencyStatus: "waiting", RequiredOutcomeCode: "paid"}` and the `dependency` reason. The response includes `dependency_attention` references/statuses and summary counts `dependency_workflow_count` / `dependency_count`. Summaries also support `dependency_status` and `required_outcome_code` grouping. Both dependency filters must match the same unresolved reference. See [dependency-aware attention](../../docs/operations.md#dependency-aware-attention).

### Reverse dependency impact

Existing business milestones responses now include typed `workflow_instance.dependency_impact`: retained dependent workflows, required outcomes, prerequisite statuses, and affected-workflow counts. The list shows up to 100 items, affected sources first; counts cover all matches and `has_more` signals truncation. Unknown account-side prerequisites require an explicit customer; self reads always use the authenticated customer. See [reverse dependency impact](../../docs/operations.md#reverse-dependency-impact).

### Dependency root-cause tracing

Business milestones responses include typed `workflow_instance.dependency_trace` findings with linked reference paths and observed states. The trace follows unmet prerequisites, distinguishes cycles from shared workflows, and exposes missing reports, blockers, mismatched outcomes, staleness, and missed deadlines. Traversal is bounded; inspect `truncated` / `limits_reached` before treating coverage as complete. See [dependency root-cause tracing](../../docs/operations.md#dependency-root-cause-tracing).

### Workflow transition readiness

Use an authenticated operations reader to check a proposed transition:

```go
result, err := client.CheckPlatformTenantSelfWorkflowReadiness(ctx, faas.OperationWorkflowReadinessRequest{
    AppID: appID, Scope: "production", Subject: faas.OperationSubject{Type: "order", ID: orderID},
    Workflow: "fulfillment", InstanceID: runID, Operation: "ship-order",
    FromState: "waiting", ToState: "shipping", Milestones: []string{"shipment-created"},
    StateRevision: revision, ContractVersion: 1,
})
```

Inspect `readiness.ready`, denial reasons, missing milestones, unmet prerequisites, and advisories. Account readers use the account readiness endpoint with an explicit customer selector. Planned names are not committed evidence; business-row checks, authorization, and transaction-time workflow/milestone validation still apply. See [workflow transition readiness](../../docs/operations.md#workflow-transition-readiness).

### Guard a transition inside the business transaction

After locking and reading the business row, call `tx.GuardedWorkflowTransition(ctx,
request, []faas.CustomerOperationPlannedMilestone{{Name: "approved", Payload: payload}},
client.CheckPlatformTenantSelfWorkflowReadiness)` before writing. Set the request's
`AppID`, scope, subject, operation, workflow instance, locked `FromState`, proposed
`ToState`, positive `StateRevision`, and `ContractVersion`. The checker must use
credentials for the transaction's customer. Return the error; use `errors.As` with
`*faas.CustomerOperationReadinessError` to inspect its `Response`.

The helper derives milestone names from actual payloads, checks readiness, and
queues the transition and evidence. Any guard error prevents commit even if caught.
Existing contract and payload validation still runs before commit.

### Business decision evidence

`tx.BusinessDecision("approval-decided", faas.OperationBusinessDecision{Workflow: "order-approval", InstanceID: runID, Code: "manual-review-approved", Description: "An authorized reviewer approved the order.", RuleID: "manual-approval", RuleVersion: "2026-10"})` queues a bounded explanation with the business transaction. Declare the milestone payload schema and bind its workflow step to `/decision/instance_id`. It uses existing precommit validation and outbox replay; no schema installation is needed. See [business decision evidence](../../docs/operations.md#business-decision-evidence) for declaration and history details.

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
