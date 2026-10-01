# Gregale Issues

Gregale Issues groups related failures across deployments and connects ownership,
resolution, release history, and retained request or worker evidence. It is a
preview feature on Hobby, Pro, and Scale.

## Connect exception reporting

Create an issue reporting token for the exact deployment that runs the code:

```sh
gregale issues create-token --app exports --deployment DEPLOYMENT_ID --name production --expires-in 24h
```

Store the returned `g_issue_…` token as an application secret. It is shown once;
listing tokens returns metadata. Rotate tokens on deployment and revoke old ones
with `gregale issues revoke-token TOKEN_ID --app exports`. Tokens can expire at
most 90 days after creation. The optional `--environment` must match a project
release environment; `application` is the default namespace for standalone apps.

Node:

```ts
import { createIssueReporter } from '@gregale/sdk-node';

const issues = createIssueReporter({
  baseURL: process.env.GREGALE_API_URL!,
  app: 'exports',
  token: process.env.GREGALE_ISSUE_TOKEN!,
});
try {
  await generateExport();
} catch (error) {
  issues.captureException(error, { request_id: requestID, trace_id: traceID });
  throw error;
}
// Flush during graceful shutdown; inspect issues.stats() for delivery/drop counts.
await issues.close();
```

Python:

```python
from faas_sdk import IssueReporter

issues = IssueReporter(api_url, "exports", reporting_token)
try:
    generate_export()
except Exception as error:
    issues.capture_exception(error, request_id=request_id, trace_id=trace_id)
    raise
issues.close()
```

Go:

```go
// Imports include context, os, time, and faas "github.com/poyrazK/faas/sdk/go".
issues, err := faas.NewIssueReporter(faas.IssueReporterOptions{
	BaseURL: os.Getenv("GREGALE_API_URL"),
	App:     "exports",
	Token:   os.Getenv("GREGALE_ISSUE_TOKEN"),
})
if err != nil {
	return err
}
defer func() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = issues.Close(ctx)
}()

if err := generateExport(); err != nil {
	issues.CaptureException(err, faas.IssueContext{
		RequestID: requestID,
		TraceID:   traceID,
		Route:     "/exports",
	})
	return err
}
```

For worker panics, `defer issues.RecoverAndRepanic(faas.IssueContext{SourceKind: "worker", InvocationID: invocationID})`
captures the panic best-effort and re-panics with the original value. Standard Go errors do not
carry creation-time stacks, so the helper records the current goroutine's stack
at the capture call. No locals, request bodies, or arbitrary context fields are
included.

`install()` registers exception hooks while preserving the prior application
hooks and fatal-exception behavior. Explicit wrappers capture then rethrow the
original exception. Delivery uses a bounded in-memory queue, timeouts, and stable
event IDs for retries. Process crashes can lose queued events. HTTP 429 and 5xx
are retried; invalid or revoked credentials cause drops visible in reporter
statistics. Report handled exceptions explicitly. Worker code can supply
`source_kind: "worker"` and `invocation_id` to link an app-scoped async execution.

The REST endpoint is `POST /v1/apps/{slug}/issue-events`. Its credential supplies
account/app/deployment/environment; callers cannot override those identities.
The event requires UUID `event_id`, RFC3339 `occurred_at`, and `exception_type`.
Optional stack frames, message, opaque request ID, trace/span hex IDs, invocation UUID,
route, and fingerprint are described in OpenAPI. Reuse the exact event payload
on retry. A reused event ID with a changed payload returns 409. Retry identity is
retained for the occurrence retention period, rather than indefinitely.

## OpenTelemetry

Export exception telemetry to
`POST /v1/apps/{slug}/issue-events/otlp/traces` or `/otlp/logs`, using the same
deployment-bound bearer token and `Content-Type: application/json`.
These endpoints accept OTLP JSON, not binary protobuf. Trace exports extract
`exception` and `http.server.request.exception` span events. Log exports extract
records with `exception.type` or `exception.message`. Standard exception type,
message, and stacktrace attributes are supported. Optional `gregale.request_id`,
`gregale.invocation_id`, and `gregale.issue.event_id` attributes supply correlation
or an explicit UUID. Unrelated logs/spans/resource tags are ignored.

Trace and span IDs follow the [OTLP JSON encoding specification](https://opentelemetry.io/docs/specs/otlp/#json-protobuf-encoding). Unknown protocol fields are ignored.

Event IDs are deterministic across exporter retries. The full batch is validated
before ingestion; retrying after a partial database failure is safe. Each export
is capped at 64 KiB and 32 exception events. Configure an exception log exporter
or independent reporting helper when trace sampling would omit failures: a
server cannot recover an exception that instrumentation never exported.

## Triage and releases

Open an app's **Issues** page, or use:

```sh
gregale issues list --app exports --state open
gregale issues get ISSUE_ID --app exports
gregale issues assign ISSUE_ID --app exports --assignee ACCOUNT_ID
gregale issues resolve ISSUE_ID --app exports --deployment FIXED_DEPLOYMENT_ID
gregale issues reopen ISSUE_ID --app exports
gregale issues ignore ISSUE_ID --app exports --until 2026-10-02T12:00:00Z
```

Issue identity survives deployments. Grouping removes line numbers and known
container/build roots but preserves source directories and function names.
Use an explicit fingerprint for unsupported or unstable stack representations.
HTTP 5xx observations are a separate source from instrumented exceptions and
may produce a separate issue for the same incident.

Resolve against the deployment expected to contain the fix. A new occurrence
after resolution in that release or a newer deployment reopens the issue and increments recurrences.
Delayed older events and older co-serving deployments do not trigger recurrence.
Assignment accepts only the app owner or active organization members. Lifecycle
mutations require deploy-write permission and completed MFA for session users.
The dashboard uses scoped CSRF tokens for each action.

Customer counts use verified platform identity in a selected window (24 hours by
default). Missing identity is displayed as unattributed. Late request telemetry
can enrich an occurrence during maintenance. Counts cover accepted, retained
events; sampling, absent instrumentation, expired data, and rejected events
limit coverage. Stack traces and request/trace/worker links are evidence, not
proof of a generated causal explanation.

The API paginates issues and each detail collection with opaque cursors. Pass
`cursor` on the list or `event_cursor`, `release_cursor`, `activity_cursor` on a
detail request. Keep `since` consistent when paging occurrences. The Go client
offers `GetIssuePage` for independent collection cursors. Dashboard history links
load earlier pages.

When an occurrence links to retained request telemetry, the dashboard can send
that request's metadata to an enabled mirror deployment configured for the
deployment that served it. The action is CSRF-protected and selects only those
enabled targets. It queues the same bodyless replay used by the request
debugger: request bodies and credentials are not retained or sent. The issue
page shows the queued/running state and the bounded source-versus-mirror status
and latency comparison, with a link to the debugger receipt. Mirror targets are
available on plans that support mirror rules and require a matching enabled
rule; the action stays hidden when the occurrence has no linked request or
configured target.

## Notifications and recovery

Register an existing app webhook with filters `issue.created`, `issue.assigned`,
`issue.resolved`, `issue.reopened`, `issue.ignored`, or `issue.regressed`.
Payloads contain the issue, transition ID, and bounded action details. The issue
transaction snapshots recipients in the existing webhook outbox. The normal
relay and signed delivery ledger recover committed transitions after restart.
Receivers must deduplicate delivery IDs: network retries are at least once.

## Limits

| Plan | Issues/app | Retained events/app | Events/minute/app | Retention | Active tokens/app |
|---|---:|---:|---:|---:|---:|
| Hobby | 200 | 10,000 | 120 | 7 days | 20 |
| Pro | 1,000 | 50,000 | 600 | 30 days | 100 |
| Scale | 5,000 | 200,000 | 2,400 | 90 days | 200 |

Payloads are at most 64 KiB, with 32 frames, a 2 KiB message and 16 KiB stack.
Accepted timestamps must fall inside retention and within five minutes of the
server's clock. Limits are enforced transactionally across API replicas. Storage
quotas return 409 and minute limits return 429 with `Retry-After: 60`; limit
problems include the bound, attempted count, and documentation URL.
Resolved and ignored issues still count toward the issues-per-app limit because
their identity and release history are retained.

Maintenance runs every minute with bounded batches. Detailed occurrences expire
even when traffic stops. Aggregate issue/release totals and activity survive.
Expired tokens are removed; app deletion cascades all issue records. A Free
downgrade gates reporting and removes detailed occurrences on maintenance.

## Privacy and reproduction

Do not put secrets or personal data in exception text. Capture never includes
locals, request bodies, headers, cookies, or environment variables. Gregale
redacts known patterns before persistence and reports applied redactions; pattern
matching does not recognize every possible sensitive value.

Debugger links resolve only scoped, unambiguous retained request telemetry.
Missing evidence is shown explicitly. Request telemetry does not retain bodies,
so issue metadata cannot automatically reproduce an exception. Use the existing
sanitized replay corpus and a private environment with appropriate secrets and
external-service isolation; external state may prevent exact reproduction.

## Acceptance

`DATABASE_URL=postgres:///postgres make test-issues` creates private test databases
and runs real API, SDK-process, dashboard, attribution, webhook recovery, quota,
and retention checks. The gate refuses missing/disabled/unreachable PostgreSQL.
It does not deploy to production or require KVM because VM lifecycle is unchanged.

## Staging smoke test

`make issues-smoke` starts two minimal instrumented Node sample apps on loopback,
one per deployment token, and triggers an HTTP-handler failure and a worker
failure in each. It checks that each failure type forms one issue across both
releases and that the issue retains its occurrences and release history. Use a
dedicated staging app; the command groups its events into two smoke issues and
adds four retained events on every run. Events expire under the app's normal
Issues retention policy.

Create a short-lived issue token for each of two deployments of the same staging
app. Set the target before token creation so the CLI cannot fall back to its saved
API host, then save each one-time `g_issue_…` value securely:

```sh
export GREGALE_API_URL=https://api.staging.gregale.dev
export GREGALE_ISSUES_SMOKE_TARGET=api.staging.gregale.dev
export GREGALE_APP=issues-smoke
export GREGALE_ISSUES_SMOKE_ENVIRONMENT=staging
export GREGALE_ISSUES_SMOKE_DEPLOYMENT_A=DEPLOYMENT_UUID_A
export GREGALE_ISSUES_SMOKE_DEPLOYMENT_B=DEPLOYMENT_UUID_B
export FAAS_TOKEN='your-Gregale-API-key-with-issue-read-access'

FAAS_API="$GREGALE_API_URL" gregale issues create-token --app "$GREGALE_APP" \
  --deployment "$GREGALE_ISSUES_SMOKE_DEPLOYMENT_A" \
  --environment "$GREGALE_ISSUES_SMOKE_ENVIRONMENT" --name issues-smoke-a --expires-in 24h
FAAS_API="$GREGALE_API_URL" gregale issues create-token --app "$GREGALE_APP" \
  --deployment "$GREGALE_ISSUES_SMOKE_DEPLOYMENT_B" \
  --environment "$GREGALE_ISSUES_SMOKE_ENVIRONMENT" --name issues-smoke-b --expires-in 24h

# Load the one-time values from your approved local secret store.
export GREGALE_ISSUES_SMOKE_TOKEN_A='g_issue_token_for_deployment_a'
export GREGALE_ISSUES_SMOKE_TOKEN_B='g_issue_token_for_deployment_b'
npm ci --prefix sdk/node --ignore-scripts --no-audit --no-fund
make issues-smoke
```

The hostname confirmation must exactly match `GREGALE_API_URL`; the hostname
must also identify staging, preview, sandbox, dev, or test. The smoke command
rejects the configured production API, requires HTTPS except for loopback, and
has no default API target. The smoke script uses the API token only to list and
read issues; the two issue-ingest tokens can report events only for their bound
deployments.

For a request debugger link check, also set
`GREGALE_ISSUES_SMOKE_REQUEST_ID` to a request ID from deployment A that has
unambiguous retained telemetry within five minutes of the smoke run. For an
invocation link check, set `GREGALE_ISSUES_SMOKE_INVOCATION_ID` to a real worker
invocation UUID belonging to the app. The command reports these checks as skipped
when either ID is absent; it never fabricates platform evidence.
