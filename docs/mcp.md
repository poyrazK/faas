# Gregale management and MCP hosting

## Connect a local agent to Gregale

`gregale mcp serve` exposes Gregale management tools over stdio, with writes
disabled by default.
The agent launches the installed CLI as a subprocess and uses your selected
connection profile and its existing login. It does not deploy an MCP app or
require the MCP hosting entitlement.

```sh
gregale login
gregale mcp serve --transport stdio
```

The second command waits for MCP messages on stdin. Configure the agent to
launch it rather than running it manually alongside the agent. For a Claude
Desktop-style configuration:

```json
{
  "mcpServers": {
    "gregale": {
      "command": "gregale",
      "args": ["mcp", "serve", "--transport", "stdio"]
    }
  }
}
```

For Codex, add a local server to its MCP configuration:

```toml
[mcp_servers.gregale]
command = "gregale"
args = ["mcp", "serve", "--transport", "stdio"]
```

A client loads every listed tool's schema into the model's context on each
turn. `--tools` lists only the selected toolsets, comma-separated
(ADR-20261026152009):

| Toolset | Tools |
|---|---|
| `core` | Context, apps, app deployments, logs, metrics, health, error groups, local deploy and env changes, promote and rollback (18 tools, about a fifth of the catalog) |
| `release` | Image and Git source releases, service and worker rollouts, named environments, route budgets, preflight, account deployment history |
| `diagnose` | Request, analytics, profile, wake, trace, regression and incident diagnostics |
| `all` | Every tool (the default) |

For example, `gregale mcp serve --transport stdio --tools core,diagnose`.
Write tools still need `--allow-writes`. Resources and prompts are always
listed.

Use an absolute executable path if the client cannot find `gregale` on its PATH.
To select a named connection, place the prefix before the command, for example
`["--profile", "staging", "mcp", "serve", "--transport", "stdio"]`.
`FAAS_API` and `FAAS_TOKEN` retain their normal precedence over saved settings.
Use the profile's stored login or supply a scoped token through the client's
environment; do not put tokens in command arguments or commit them to a client
configuration. A missing credential fails startup on stderr. API authorization
failures are returned as MCP tool errors; the server never starts a login flow.

| Tool | Behavior |
|---|---|
| `gregale_preflight` | Read-only app/environment consent, release-review/health/operations compatibility and durable-state checks. Unknown capabilities require native review/admission. |
| `gregale_context` | Selected profile, API endpoint, account ID, plan, status, and platform capabilities. |
| `gregale_list_apps` | Apps sorted by slug; `limit` defaults to 20, maximum 100; continue with `next_offset`. |
| `gregale_get_app` | One app's status, URLs, deployment availability, and resource settings. |
| `gregale_list_deployments` | Account deployment history; bounded `limit`; continue with `next_before`. |
| `gregale_get_deployment` | Build/deployment status, commit and image identifiers, and failure diagnostics. |
| `gregale_get_logs` | Newest entries from a non-following log snapshot; optional deployment, level, literal substring, and RFC3339 timestamp filters. |
| `gregale_get_metrics` | App metrics over the requested `range`; the API's default is `5m`. |
| `gregale_get_request` | Exact retained public request, telemetry-row or trace ID: status, deployment, route, timing and bounded guest execution summary. Requires separate per-app diagnostic consent remotely. |
| `gregale_list_requests` | One bounded retained-request page, filtered by lookback, route, HTTP status, deployment, minimum latency or cold boot. Reuses diagnostic consent; continue with `next_arguments`. |
| `gregale_list_error_groups` | Bounded native error-fingerprint pages selected by inclusive last-seen windows, with separate group-history counters and explicit continuation context; excludes messages and raw samples. |
| `gregale_list_error_requests` | Bounded linked public request IDs for an exact error fingerprint, with independent retained history and safe continuations; evidence availability remains unverified until an explicit lookup. |
| `gregale_get_request_evidence` | Bounded timeline, correlation stages, dependency timings, selected request path, matching regression context, native dependency deployment comparison and structured findings with local references; no raw spans, native span IDs, SQL, headers or bodies. |
| `gregale_get_request_analytics` | Bounded route/method traffic, HTTP 4xx/5xx errors, cold boots, latency percentiles and optional guest execution/CPU/RSS, with historical windows and explicit measurement limits. |
| `gregale_get_request_analytics_timeseries` | Historical UTC hourly traffic, HTTP 4xx/5xx errors, cold boots and weighted latency percentiles, app-wide or for an exact route/method pair; preserves empty and partial buckets and output limits. |
| `gregale_get_profiles` | Retained sampled CPU for an exact deployment/runtime/window, with bounded function labels and explicitly scoped capture/attribution summaries. Requires diagnostic consent; no collection or enablement. |
| `gregale_get_wake_history` | Retained instance wake observations with explicit window-first keyset paging, triggers, readiness and admission measurements; older APIs retain latest-100 compatibility limits. |
| `gregale_get_wake_timeline` | One bounded oldest-first stage read for an exact wake ID, with selected producer timings, admission presence and restore fallback category; explicit keyset continuation when supported, with bounded compatibility reads on older APIs. |
| `gregale_get_debug_coverage` | Observed trace/span/wake/guest coverage within retained telemetry; full traffic collection completeness remains unknown. |
| `gregale_get_dependency_latency` | Bounded sampled dependency percentiles, error rates, parent-child impact edges, representative request lookups and native split-window trends, with explicit completeness/truncation. |
| `gregale_get_critical_paths` | Bounded native ordered path patterns, weighted latency/error trends and representative request lookups; sampled evidence and truncation remain explicit. |
| `gregale_compare_deployments` | Retained per-route p50/p95/p99 and represented request counts for two exact deployments, with native effective window and explicit missing-side evidence. |
| `gregale_plan_regression_action` | Review acknowledge, dismiss, resolve or reopen for an exact observation and environment; pins the app and native version. |
| `gregale_apply_regression_action` | Opt-in triage write with separate per-app consent, environment write access and atomic observation-version guard; durable same-plan retries. |
| `gregale_list_regressions` | Bounded native-visible latency/error regression inbox with detection timestamps, deployment scopes, triage state and dependency hypotheses; excludes SQL/query text. |
| `gregale_get_trace` | Exact W3C trace across up to five selected apps; independently bounded retained request metadata and durable invocation lifecycle history. Diagnostic consent required for every selected app. |
| `gregale_get_incident_bundle` | Required exact-request evidence plus coverage, visible regressions and optional comparison of two supplied deployments. Per-section provenance, native windows, bounded results and explicit partial errors; requires diagnostic consent. |
| `gregale_list_fleet_regressions` | Explicit selection of up to five apps, with independently guarded regression results, effective lookbacks, output limits and per-app code/status errors. Requires diagnostic consent for each app. |
| `gregale_get_running_causes` | Observed scheduler residency causes, floor/idle settings and bounded history; observation age and freshness uncertainty remain explicit. |
| `gregale_list_app_deployments` | App deployment history with `next_before` pagination. |
| `gregale_list_instances` | Instance pages with optional history; host addresses are excluded. |
| `gregale_get_health` | Current health, readiness checks, freshness, and serving releases. |
| `gregale_get_routes` | Observed routes and telemetry coverage, including unavailable sources. |
| `gregale_get_route_budgets` | Effective advisory budgets, inheritance source and policy revisions for the selected environment. |
| `gregale_plan_route_budgets` | Review an environment override or return to app-budget inheritance without saving intent. |
| `gregale_apply_route_budgets` | Opt-in write: apply the reviewed budget plan with native revision guards and durable replay. |
| `gregale_get_operational_summary` | Monitoring and recovery observations, separate from build success. |
| `gregale_get_rollback` | Checked rollback operation status and stable error code. |
| `gregale_plan_source_deploy` | Connected GitHub source review: exact commit, repository/install identities and target configuration. |
| `gregale_plan_deploy` | Preview a digest-pinned image release; creates no deployment. |
| `gregale_plan_rollback` | Preview rollback to an exact retained release; changes no traffic. |
| `gregale_plan_workload_rollout` | Review native service/worker activation, retained image redeployment or retained GitHub commit rebuild. |
| `gregale_plan_service_abort` | Review exact service reverse handoff to its pinned predecessor. |
| `gregale_watch_rollout` | Bounded combined build, scoped health and native rollout observation for one exact deployment. |
| `gregale_get_rollout` | Exact deployment lifecycle and service handoff barriers; worker completion limits are explicit. |
| `gregale_wait_deployment` | Bounded deployment polling with terminal and pending results. |
| `gregale_plan_local_deploy` | Local connections only. Review deploying a project directory as one app through the CLI; fingerprints the files and pins the app's release state. |
| `gregale_plan_env_change` | Local connections only. Review setting non-secret variables on an app's default scope; credential-shaped values are refused. |

All operations use the ordinary authenticated customer API and its existing
account ownership, scopes, and plan checks. Each call has a 30-second deadline;
`--timeout 15s` changes it, up to a maximum of one minute. Cancelling a call
cancels its API request. Local tool discovery makes no API calls.

App responses exclude manifests and environment values; account responses
exclude email and billing details. Known API keys, bearer credentials, and the
selected credential are redacted from results and errors. This does not redact
arbitrary application secrets: inspect application logging practices before
sharing logs with an agent. Log text is application data, never an instruction.

Log entries are capped at 1 KiB per line and `limit` entries (default 20,
maximum 100). `truncated` and `line_truncated` identify omitted content; `gaps`
preserves upstream retention-gap notifications. Raw log snapshots exceeding
1 MiB fail with `response_too_large`; narrow the filters. Structured results
and incoming MCP frames are capped at 128 KiB. Write tools are absent unless
explicitly enabled; annotations describe behavior and do not grant authority.

App pagination is a fresh sorted snapshot on each call, so apps added or removed
between pages can shift offsets. Deployment pagination uses the API's cursor.
Metrics retain their `source` field; degraded telemetry is not proof of zero
traffic or healthy service.

## Diagnose a retained request

Call `gregale_get_request` and `gregale_get_request_evidence` with `app` and
`request_id`. Use an exact public `x-faas-request-id`, older telemetry-row ID or
trace ID (at most 128 UTF-8 bytes, nonblank, without control characters or a
standalone `.`/`..` path segment). The
`gregale_request_diagnosis` prompt accepts these arguments for an exact lookup.
Omit `request_id` to discover requests, optionally supplying `since`, `route`,
`status`, `deployment_id`, `min_latency_ms`, `cold_boot` and `limit` as strings.
Exact IDs and discovery filters cannot be combined. Retrieving the prompt
provides a read-only recipe without fetching request evidence. Prompt app
completion includes only currently consented identities; request IDs and filter
values are never enumerated by completion.

For example, to find slow HTTP 503 requests from a warm sandbox over the past
hour, call `gregale_list_requests` with:

```json
{"app":"alpha","since":"1h","route":"/orders","status":503,"min_latency_ms":500,"cold_boot":false,"limit":20}
```

Discovery reads exactly one native page per call, with default 20 and maximum
50 rows. Route filters are exact paths/templates without query or fragment,
bounded to 256 bytes; `since` is a positive Go duration or whole-day alias such
as `3d` (at most 64 bytes). Status is 100..599, minimum latency is
0..86400000 ms and deployment filters are nonzero UUIDs belonging to the app.
`cold_boot: false` filters warm requests; omission includes both kinds.
Discovery retains the same app-wide consent as exact diagnostics, including
deployment filters in named environments. Ordinary deployment/environment tools
keep their own consent requirements.

The result preserves effective `since`, `window_start`, `window_end`,
`retention_clamped`, `complete` and the opaque native `next_cursor` (at most
8192 bytes). To continue, pass the returned `next_arguments` unchanged to
`gregale_list_requests`. It carries the original filters, requested lookback,
page size and cursor. Native cursors pin the window and filters; native errors
for changed filters, expiry or a changed retention horizon are returned without
retrying a broader query or starting a fresh window. The bridge never silently
truncates, reorders or walks pages. Inconsistent pagination or filter metadata
is rejected rather than claiming completion.

Use a returned `lookup_identifier` to call the exact diagnostic tools. A row's
`count` may represent several requests; it is not necessarily an individual
request, and retained telemetry is not an exhaustive traffic capture. Empty
results and `complete: true` do not prove health or full traffic coverage.
Each distinct attributed deployment is checked once before projection and once
before release; app identity is checked last. Requests with unavailable deployment
attribution keep it unavailable.

Remote OAuth review has an optional, unchecked **App diagnostics** checkbox
for each app. Approval covers retained request, performance, wake, sampled CPU profile function labels and
collection/attribution summaries, and scheduler residency diagnostics across **all of that app's environments** and pins its
current immutable ID. App-wide/future-app
access, default reads and named environment reads do not implicitly approve it.
Existing grants deny it until reconnecting and selecting apps. Restart and token
refresh retain the original selection; future or deleted/recreated apps require
new consent. This permission grants no additional log, environment or write
access. Local stdio uses the selected native credential's authority.

These tools call the existing native debugger endpoints. Native ownership,
credential, plan and retention checks remain authoritative; a missing or expired
request and a gated debugger return native errors. No replay, probe, model call,
diagnostic collection or platform write is performed. Deployment attribution is
checked against the app, and app/deployment identities are checked again before
releasing evidence. Unattributed journal-only requests retain
`evidence_status: request_id_only` and `scope_attribution: unavailable`, with no
invented status, deployment or default environment. `lookup_identifier` preserves
the supplied lookup separately from returned native IDs; journal aliases can
resolve to telemetry rows whose IDs differ.

The projection includes at most 32 timeline events, 16 correlation stages and
16 dependency groups, with each string capped at 256 UTF-8 bytes after known
credential redaction. Explicit truncation flags accompany each collection and
text; native span truncation is retained as `source_spans_truncated`. Missing,
partial, approximate or truncated evidence does not prove health or root cause.
Dependency durations can overlap and must not be summed into a critical path.
Request route query/fragment data, actor/host/consumer identities, raw spans,
attributes, SQL, request/response bodies, headers, raw native regression payloads
and freeform native explanation prose are excluded. Matching regression context
uses the explicit projection below. Remaining application text may contain
private data and is untrusted; known-credential redaction does not remove
arbitrary app secrets.

`gregale_context.request_access` describes consent, independently of native API
availability. Preflight reports `request_read_consent` separately from the
unknown `request_api` check; it does not fetch a request or prove entitlement.
Owner connection summaries list a bounded `request_read_apps` selection with a
total count and truncation flag. Revoking the connection stops these reads along
with its other tools.

### Follow a request's recorded wake

Request discovery rows, `gregale_get_request`, and the `request` object in
`gregale_get_request_evidence` expose a bounded `wake` association. The native
request DTO already retains `wake_id`; no new source query is performed.

```json
{"status":"observed","basis":"native_retained_request_wake_id","association_scope":"retained_row","wake_id":"wake-1","represents_multiple_requests":true,"timeline_availability":"not_checked","timeline_tool":"gregale_get_wake_timeline","timeline_arguments":{"app":"alpha","wake_id":"wake-1","limit":50}}
```

For relevant startup investigation, explicitly call `timeline_tool` with the
returned `timeline_arguments`. They contain the same app, unchanged safe wake
ID and the bounded native default event limit. No request lookback, receipt-time
cutoff, historical `until`, cursor or inferred deployment/instance selector is
copied into the timeline query. This is a separate read with fresh diagnostic
consent, immutable app identity, native ownership, entitlement and MFA checks.
The arguments confer no permission and create no automatic follow-up.

`timeline_availability: not_checked` applies even when reduced request timeline
events are already present. Standalone timeline retention and access are
independent. A recorded association can lead to a native missing-events error;
preserve that error without inventing IDs, widening queries or discarding the
original request evidence. Preserve the timeline's reported pagination mode and
distinct timing-boundary limits.

`association_scope: retained_row` preserves the native association, not an exact
individual startup execution. The current native aggregation key includes wake
ID, but `count > 1` still represents multiple requests; it does not make shared
wake events independent per-request timing samples or prove causal latency.
Preserve `represents_multiple_requests` and the existing represented count. A
wake ID alone does not establish `cold_boot`; a cold-boot flag alone does not
provide a wake ID. Do not derive one from request/trace/instance IDs or timestamps.

Other statuses return no ID, tool or arguments:

| Status | Meaning |
|---|---|
| `not_reported` | Usable retained request row has no native wake ID. |
| `unavailable` | No retained row identity, a journal-only record or no positive represented count. |
| `invalid_native_wake` | Native wake ID cannot be used unchanged as a safe bounded selector. |

Unsafe IDs are withheld, never shortened or redacted into different identities.
Other request evidence remains available. Missing/unavailable/invalid associations
do not establish warm execution, no wake, failed startup or healthy operation.
No instance, node or trace IDs are added to this projection.

A supported native `inspect_wake_timeline` recommendation can include `tool`
and `arguments` for the safe association, citing `/request/wake`. It remains an
investigation suggestion. Without a safe association, the existing suggestion
to review returned timeline/correlation evidence stays generic; the bridge does
not invent a selector or a new recommendation. Request/performance prompt
guidance includes this workflow and fetches no diagnostic evidence during
retrieval. The bridge makes no extra wake-timeline API calls during projection,
performs no probes/collection/replay/writes and grants no extra environment or
log access.

### Guest execution evidence

Request projections in `gregale_get_request`, `gregale_get_request_evidence`
and `gregale_list_requests` include a `guest` summary from the same native
request row. This adds no query or permission. Retained request identity and a
positive represented count are required; journal-only aliases retain
`status: unavailable` without execution timing.

The summary exposes closed runtime categories (`node22`, `node24`, `python312`,
`python313`, `go124`, `binary`), runner outcomes (`ok`, `http_error`,
`handler_error`, `timeout`, `canceled`, `missing`) and error classes (`http_5xx`,
`handler_exec`, `handler_protocol`, `timeout`, `canceled`). An absent error class
is `not_reported`, which does not establish success. Unreviewed category text
becomes `unknown` and is never forwarded. No payload, error message, stack trace,
CPU/RSS data or additional log access is exposed.

`status` is `observed` for supported consistent evidence, `partial` for unknown
categories, `unavailable` for absent/missing execution evidence, or
`invalid_native_guest` for contradictory outcome/class pairs or invalid duration.
Unavailable/invalid evidence has no `duration_ms`; observed zero duration is
preserved. The transport duration bound is 0–86,400,000 ms in
`pkg/api/limits.go`, matching existing native validation.

`duration_precision: native_retained_may_be_bucketed` reports that the publisher
can retain a bucket upper bound even for a row with count one. Exact preserved
and bucketed rows are not distinguished by the API. Do not treat the value as
exact execution time, subtract it from request/path latency, or sum it with
other phase durations. `represents_multiple_requests` preserves a collapsed
request row's count semantics. The runner outcome is independent of edge HTTP
status, and neither outcome alone establishes root cause or application health.

An approved native `guest_failure` finding requires a supported failed runner
outcome and consistent exposed summary. It cites `/request/guest`, with native
confidence remaining an uncalibrated label. Missing, successful, unknown or
invalid guest evidence does not produce a failure finding. The existing
`inspect_guest_failure` suggestion prefers this specific reference when present;
otherwise a supported HTTP failure can retain its existing request-level
reference. Returning the suggestion performs no additional read or action.

### Request-linked regression context

`gregale_get_request_evidence` includes `regression` from the native observation
already attached to the request. It adds no native evidence query or permission.
Its `basis` is `native_route_deployment_observation` and `source_completeness`
remains `unknown`. The original native route and deployment must exactly match
the retained request before redaction or truncation. Matching shortened or
redacted labels does not establish association. Existing request-deployment
ownership, scope and final consent checks protect the whole response.

The summary's `status` distinguishes:

- `observed`: a validated matched `observation` is present.
- `no_matching_observation`: native `unobserved` with no returned observation.
- `unavailable`: native enrichment failed, or the request lacks retained identity,
  a positive represented count or an attributed deployment.
- `unknown`: no observation and missing or unrecognized native availability metadata.
- `invalid_native_regression`: contradictory metadata, mismatched identity,
  unsupported kind/state, malformed metrics/timestamps, or a detected regression
  claimed without its observation.

Only `observed` includes an `observation`; all other states retain the remaining
request evidence. None of these states establishes healthy traffic. Observed
context reuses the inbox projection: baseline/current p95 and error rates,
decimal `regression_factor`, affected/error counts, detection and triage
timestamps, workflow state, deployment scope and an optional suspected dependency
with `basis: hypothesis`. SQL/query text is excluded. Query strings/fragments are
removed from route labels; `route_modified` labels cannot be used as exact filters.
Text bounds/redaction apply, with `text_truncated` propagated to the response.

The matched observation describes aggregate detector metrics for this route and
deployment, not latency or error probability for this individual request.
Detection timestamps are not sampling-window boundaries. There is no baseline
deployment ID, exact observation window or completeness proof. Native visibility
remains authoritative; MCP does not reinterpret dismissal timestamps using its
clock. Triage state records workflow, not health, and association does not prove
deployment causality. These sequential reads do not form an atomic snapshot.

Supported native `error_regression` and `performance_regression` findings cite
`/regression/observation`. They require the appropriate signal kind, increased
metrics and positive supporting counts; unknown, contradictory or missing support
is omitted. They remain aggregate approximations even when the request's count is
one. Native confidence remains an uncalibrated rule label. An optional
`compare_deployments` investigation suggestion requires user-supplied exact source
and target IDs, without inferring a previous or healthy baseline. Retrieving it
executes no comparison, triage update, replay, collection or rollback and grants no
other access.

### Request-linked dependency deployment comparison

`gregale_get_request_evidence` includes `dependency_comparison` from the native
request-evidence enrichment. It adds no evidence query or permission. The native
current deployment and original route must exactly match the retained request
before redaction or truncation, and the previous deployment must be distinct.
Both deployments must belong to the selected app. The current request ownership
lookup is reused, the previous deployment receives its own lookup, and both
ownerships/scopes are rechecked before the response is returned. Native lookup or
ownership failures remain tool errors. Invalid or missing enrichment makes no
previous-deployment lookup and leaves other request evidence available.

The object has `basis: native_route_deployment_dependency_comparison`,
`source_completeness: unknown` and these `status` values:

- `observed`: a validated pair and nonempty projected dependency metrics.
- `partial`: native, projection or text truncation limits the comparison.
- `no_retained_dependencies`: a validated pair with an empty native dependency list.
- `unavailable`: missing native enrichment or insufficient retained request
  identity/count/deployment; no pair or dependency records are exposed.
- `invalid_native_comparison`: mismatched route/current deployment, invalid pair
  identity or malformed projected dependency metrics; no pair or records are exposed.

Missing enrichment cannot distinguish absent history from a failed best-effort
native read. An empty, unavailable or invalid comparison does not prove health.
The object preserves `current_deployment_id`, `previous_deployment_id` and their
verified scopes. The native API selects the previous retained observed deployment;
MCP does not independently choose it, and previous does not mean healthy or the
immediately preceding release. This does not authorize another tool or an
automatic comparison/rollback.

Up to ten `dependencies` reuse the bounded dependency metric projection: weighted
calls/errors, percentiles, baseline/current p95 and error rates, native regression
flags and redacted category/name/error-type labels. SQL/query patterns, tags,
commit metadata and raw spans remain excluded. `native_truncated`,
`dependencies_truncated`, `text_truncated` and native/returned dependency counts
remain separate. Route query/fragment text is removed; `route_modified` labels
are not exact filters. Comparison text truncation propagates to the response and
explanation uncertainty.

These are sampled aggregates across deployments, distinct from time-split
dependency history and this individual request's execution. The native object
supplies no exact comparison window, per-side sample counts or complete-capture
proof. False truncation flags do not prove an exhaustive read. Overlapping
dependency durations cannot be summed, and metric changes establish neither
deployment causality nor statistical significance. Reads and ownership rechecks
are sequential, not an atomic snapshot.

Supported native `dependency_regression` and `dependency_failure_regression`
findings cite `/dependency_comparison/dependencies/N`. Canonical native labels
must resolve uniquely before redaction, with matching native flags, increased
metrics and positive supporting counts. Missing, ambiguous, contradictory or
collection-truncated support is omitted. Descriptions use fixed templates;
aggregate approximation and uncalibrated native confidence remain explicit.
`inspect_dependency_change` prefers supported failure evidence and grants no
additional logs, service access, collection, replay, triage change or writes.

### Structured request explanations

`gregale_get_request_evidence` includes an `explanation` projection from the same
native response; it makes no additional evidence query. Native findings are
accepted by approved code and checked against the structured facts actually
exported in this tool response. Descriptions use fixed templates and safe numbers
or phase names. Native headline, diagnosis, finding/recommendation prose,
reference labels/values and primary spans are omitted because those fields can
contain SQL, private labels or references outside the MCP projection.

Supported findings cover retained HTTP server/client errors, a recorded cold
boot, incomplete correlation stages and the longest observed stage. Status,
count, cold-boot and stage facts must agree with the finding. Stage references
must resolve to one known phase in the projected correlation list; duplicate,
missing or truncated phase selections are omitted. A longest-stage finding also
requires positive observed duration and no longer observed stage in the list.
Durations can overlap, so this does not establish a causal critical path.
Verified selected path segments can also support `slowest_span` and
`dependency_failure` findings, and the exposed guest summary can support
`guest_failure`. The matched regression context can support `error_regression`
and `performance_regression`. The exposed dependency comparison can support
`dependency_regression` and `dependency_failure_regression`. Other findings
depending on hidden spans, unexposed guest outcomes, unexposed regression fields
or unexposed deployment comparisons are omitted, as are
unknown or contradictory rules.

Each finding preserves the native `confidence` label (`high`, `medium`, `low`,
otherwise `unknown`) with `confidence_basis: native_rule_label`. This is an
uncalibrated label, not a probability or proof of root cause. `approximate`
preserves stage approximation, a request row representing multiple requests or
an aggregate regression observation.
`evidence_refs` contains JSON Pointers such as `/request` or
`/correlation/stages/0`, resolving within this response. They are not native span
IDs, URLs, new tool selectors or additional permissions. Missing supporting
references suppress the finding rather than leaving a dangling citation.

Output budgets in `pkg/api/limits.go` allow at most eight findings, five native
recommendation candidates and one supporting reference per finding. Native order
is preserved within each projected prefix. `findings_truncated`,
`recommendations_truncated`, `references_truncated`, native returned counts and
`omitted_finding_count`/`omitted_recommendation_count` describe this projection;
omitted counts include unsupported and unprojected rows. The native API does not
report synthesis/capture completeness, so `source_completeness` remains
`unknown`. `evidence_limited` reports incomplete correlation or truncation of
correlation, timeline, dependency, span or text evidence, and unknown, unavailable
or invalid regression enrichment, and unavailable, partial or invalid dependency
comparison enrichment. Recognized `native_status` and
`native_generator` values are preserved; other metadata becomes `unknown`.
Native `unobserved` means no active regression observation, not healthy traffic.

Explanation `status` is `available`, `partial`, `unavailable` for an absent/legacy
explanation, or `no_supported_findings` when none can be projected. Findings and
recommendations remain arrays even when empty. These statuses describe the
projection and cannot prove health. No substitute diagnosis is invented when
native findings are absent.

Only reviewed investigation actions for a supported finding are suggested:
`inspect_guest_failure`, `inspect_wake_timeline`, `inspect_stage_timeline` and
`inspect_dependency_failure`, `compare_deployments` and `inspect_dependency_change`.
Their descriptions use fixed text, duplicate actions are collapsed, and no
action executes automatically. Collection, query-plan and unsupported actions
are omitted. Suggestions grant no additional environment/log access or writes.
Request and performance diagnosis prompts explain confidence, references,
omissions and independent authorization. Existing app-wide consent, immutable
app identity, deployment ownership/scope checks and final authorization apply
to the complete response. No tool, prompt argument, API endpoint or migration is
added.

### Trace correlation across selected apps

Use `gregale_get_trace` with an exact nonzero W3C trace ID (32 lowercase hexadecimal
characters) and one to five explicit distinct app slugs:

```json
{"apps":["orders","mailer"],"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","limit":10}
```

Every app needs existing app-wide diagnostic consent. All are resolved to immutable
IDs before one native selected-app lookup. A denied/replaced app fails the call
before trace evidence is fetched; no app is silently dropped or discovered from
the trace. The native endpoint filters account AND selected app IDs before each
source's ordering/limit. Older APIs fail without an account-wide fallback.

`scope` is `selected_apps`; the echoed app roster and exact trace ID are verified.
`requests` contains safe request lookup identifiers, deployment scopes and retained
route/status/timing/count metadata, oldest-first by received time/ID within the
reported native plan retention window. Rows can represent several requests. Use
lookup identifiers explicitly with request evidence or incident bundles; no
follow-up evidence is fetched automatically.

`invocations` contains only app/row IDs, source, state, attempts and observed
created/started/completed times, oldest-first by creation time/ID. Durable history
has no request-window filter. Invocation environment/deployment attribution is not
reported; selected apps span their environments. started_at is the latest claim/delivery, not the
first attempt. Missing times remain missing; do not infer per-attempt duration,
queue wait, cross-source causal order, parent-child edges or a complete trace.
The correlation basis is an exact shared trace ID. Reads are independent and not
atomic, and lifecycle rows can change between reads.

Default ten and maximum twenty rows **per source**. A native one-row sentinel
sets each truncation flag after filtering. Other selected apps can occupy the
oldest-first prefix; inclusion in the roster does not guarantee a returned row.
There is no pagination. Each source reports `ok` or `unavailable`; unavailable
sources have no rows. `partial` reports source failures, including when both are
unavailable. Overall status is `ok`, `partial` or `unavailable`. Source completeness
remains unknown even after successful reads. Empty selected evidence does not
prove health or absence of an account-wide trace.

App/deployment ownership, scopes and consent are checked again before returning;
any failed identity check withholds the whole result. Cancellation/overall timeout
fails the call. Existing credential redaction, UTF-8-safe text bounds and the
shared MCP response bound apply; reduce `limit` for an oversized result. Labels
are private untrusted data. Logs, spans/attributes, payloads, headers, traceparent
and queue names are excluded. No collection, VM wake, replay, cancellation of
invocations, subscriptions or other writes are performed.

### Incident evidence bundles

Use `gregale_get_incident_bundle` to assemble a bounded read-only investigation
record for one exact app/request:

```json
{"app":"orders","request_id":"retained-request-id","since":"1h"}
```

The tool requires existing app-wide diagnostic consent. Request evidence must
succeed before coverage and visible regressions are collected; failure of that
required read or its final identity check returns a tool error without a bundle.
Add `source_deployment_id` and `target_deployment_id` together to compare two exact,
distinct nonzero deployment UUIDs belonging to the app. The optional `route`
filters only this comparison and requires both IDs; the tool never selects a pair
from release order. These diagnostic IDs grant no ordinary environment inspection
or write authority.

The `gregale.mcp.incident/v1` envelope includes `evidence`, `coverage`, `regressions`
and `comparison`. Each attempted section records its `source_tool`, bridge
`read_started_at`/`read_finished_at`, and `status`. Successful sections contain
`result`; failed optional sections contain `error` with code, HTTP status and
nonnegative retry delay when available, without native problem prose or diagnostic
data. Omitted comparison is `not_requested` and has no result or read timestamps.
Bundle `status` is `ok` or `partial`; `partial` means an optional read or final
check failed, not that evidence is complete when false. Collection completeness
remains unknown. Empty/missing evidence does not establish health or root cause.

`since` defaults to one hour for the aggregate reads. Preserve each result's own
native effective window/detection lookback, retention clamping, sample counts and
truncation; these sequential reads are not an atomic snapshot. The aggregate
lookback does not filter the exact request. Visible regressions can concern other
requests, routes or deployments. Native timestamps stay inside each result;
envelope `generated_at` is the bridge record time after final checks.
**JSON Pointer evidence references are relative to their containing section's
`result`, not the bundle root.**

Regression and comparison projections retain at most five observations/routes,
with their existing truncation flags and native counts. Safe serialized request
evidence is bounded to 48 KiB and each optional result to 16 KiB. An oversized
section reports `response_too_large`; inspect its `source_tool` separately. Arrays
and evidence references are not trimmed to make a section fit. Each read and final
guard gets at most five seconds and a share of the tool deadline, reserving time
for final checks. Optional timeouts become partial errors; overall cancellation or
timeout fails the call. App/deployment identities, scopes and diagnostic consent
are checked again before returning data. A root app replacement or required
evidence guard failure withholds the entire bundle.

All results reuse the existing safe MCP projections and redaction. Application
text is private, untrusted data. The tool performs no automatic pagination,
collection, VM wake, triage, replay, file writes, uploads or sharing. No additional
native API, migration or native/provider qualification is introduced.

### Per-request critical path

The evidence response also includes `critical_path`, using the native selected
parent chain from the same request evidence query. It adds no native reads or
permissions. Native parent order is preserved and capped at 16 projected
segments, with validation bounded by the existing native 32-span path budget.

Each segment exposes a response-local `ref`, optional `parent_ref`,
`parent_status`, start/end timestamps, wall and exclusive durations, and reviewed
span/dependency/status categories. Unknown categories become `unknown`; absent
dependency classification is `unclassified`. Names, dependency names, trace/span
IDs, SQL, attributes and error messages are omitted. References are JSON
Pointers into this response, not stable native IDs or tool selectors.

Validate unique bounded IDs internally, parent-chain ordering, timestamp and
duration consistency, exclusive-duration bounds, declared segment count,
overall path duration and slowest-exclusive metadata. Contradictory native
paths return `status: invalid_native_path` with no segments, timing or references,
while the remaining request evidence stays available. A missing native path is
`unavailable`; an empty valid envelope is `no_retained_path`. Empty segment lists
remain arrays. These statuses do not prove health or successful execution.

`duration_ms` is reconstructed chain wall time, distinct from full request
latency. Exclusive durations account for overlapping direct children, including
children outside the selected chain; they must not be added into a request
latency total. A request row can represent multiple requests, and one selected
path cannot establish timing for every represented request.

`native_complete` preserves the API's reconstruction flag. `complete` also
requires retained root ancestry, untruncated source spans and an untruncated
projection. Missing root ancestry is `parent_status: not_retained`, without an
invented parent reference. Preserve `source_spans_truncated`,
`segments_truncated`, `native_returned_segment_count`, `categories_limited` and
`source_completeness: unknown`. Completeness describes reconstruction within
retained evidence, not capture of all traffic. The path is `partial` if
reconstruction/projection is incomplete or categories are unknown.

`slowest_segment_ref` and `slowest_exclusive_ms` are present only when the
native maximum-exclusive segment is included. If it falls beyond the projected
prefix, `slowest_status` is `omitted_by_projection` and those fields are absent.
Never substitute the largest visible segment for the native maximum. All-zero
exclusive durations may have `slowest_status: not_reported`.

Span-based explanation citations require a unique retained source span matching
the projected segment's identity, parent, timing, duration and categories. The
native `span:0` alias for a longest-duration retained span is resolved through
the first source span, separately from literal span IDs, and suppressed when
the source is truncated or ordering cannot be verified. A dependency failure
requires an exposed error category, recognized dependency type and retained
HTTP server-error outcome. Findings reference only included verified segments;
unselected or unmatched support stays omitted. These are observations, not
causal proof. The fixed investigation suggestion for a dependency error executes
no action or additional read and grants no log/service permissions.

## Review route analytics

Use `gregale_get_request_analytics` to choose routes for further investigation:

```json
{"app":"alpha","since":"1h","until":"2026-10-16T11:00:03Z","limit":20}
```

The tool reuses separate per-app diagnostic consent across environments, native
ownership/plan checks and app identity checks before and after retrieval. `since`
accepts a positive Go duration, whole days (`3d`), or an RFC3339 start; omission
uses the native 24-hour default. Optional `until` is an exclusive RFC3339 end at
or before now. Time inputs are bounded to 64 bytes. Timestamp offsets are
normalized to UTC; duration-only performance tools still require lookbacks.

Report native effective `from`, `until`, `since`, query-time `as_of` and
`window_clamped`. The native retention cap limits the duration ending at the
selected `until`; a historical selection does not guarantee retained data.
`source_completeness` and `freshness` remain `unknown`. Retained requests can be
collapsed: counts and latency percentiles use represented request weights,
rather than independent samples or a complete traffic denominator. Summary and
route queries are sequential and their counts need not reconcile. An empty
result means `status: no_retained_requests`, never healthy traffic.

`summary` and each route expose requests, `error_requests`, `error_rate_pct`,
`cold_boots` and p50/p95/p99 milliseconds. `error_basis: http_status_gte_400`
includes HTTP **4xx and 5xx**; debugger regression tools use 5xx error signals.
Positive counts keep observed zero latency. Without retained requests,
`latency_status: no_retained_requests` omits percentiles instead of inventing
measurements. Routes retain native order and separate method labels.

Each route's `measurements` may include `cold_request_p95_ms`,
`wake_boot_p95_ms`, `guest_execution_p50_ms`, `guest_execution_p95_ms`,
`guest_cpu_avg_ms`, `guest_cpu_p95_ms` and `guest_peak_rss_max_mb`. An absent
field differs from observed zero. `measurement_coverage: not_reported` means
the native route aggregate supplies no measurement counts or coverage: do not
assume every represented request was measured. Retained values can be rounded
or bucketed. Guest execution is runner wall time. CPU measures supported
one-shot child processes including startup; it can exceed wall time and is not
available for every runtime. Peak RSS is their rounded/bucketed process
high-water mark, not allocated app/VM memory. Cold request p95 includes handling;
wake boot p95 covers the scheduler boot-start to RUNNING interval. These can
represent different populations; never subtract or sum their percentiles or
infer billing/resource changes.

`limit` bounds only the projected route prefix (default 20, maximum 50), not
native query size or pagination. Preserve `native_routes_limit`,
`native_returned_route_count`, `returned_route_count`,
`native_routes_truncated`, `native_groups_truncated`, `routes_truncated` and
`text_truncated` independently. Omitted routes do not imply zero traffic.
Route/method labels are bounded to 256 bytes with credential redaction and
query/fragment removal from routes. Modified labels can collide; the
`route_modified` and `method_modified` flags identify changes. `timeseries_filter_usable`
identifies an unchanged supported route/method pair for the hourly trends tool;
it can be true for a native method-prefixed route that discovery cannot accept.

Only an unchanged `route_filter_usable` label may be copied into supported
`gregale_list_requests` filters, as an independent read with its own window.
Discovery has no method filter or historical `until`: it cannot reproduce a
historical analytics route/method cohort. Do not strip a native method prefix
such as `GET /orders` or rewrite modified labels to manufacture selectors;
obtain the original safe selector from the user when needed.

The explicit projection excludes native dimensions/consumer identities, all
compute/deployment cost estimates, deployment observations/comparisons,
dependency payloads, request IDs, spans, SQL, headers and bodies. Retrieval
fetches no request examples and grants no extra environment/log/write access.
Native errors are returned without fallback queries. Missing or inconsistent
native window metadata fails the read; use a compatible API rather than
silently widening the selection.

## Review historical request trends

Use `gregale_get_request_analytics_timeseries` to inspect when retained traffic,
errors, cold boots or latency changed. Omit `route` and `method` together for an
app-wide series, or supply an exact pair from an unchanged
`timeseries_filter_usable` analytics row:

```json
{"app":"alpha","since":"24h","until":"2026-10-16T13:15:03Z","route":"GET /orders","method":"GET","limit":48}
```

Route labels are exact native data. Preserve a native method prefix such as
`GET /orders` and supply the separate method; do not strip or reconstruct it.
Route selectors are bounded to 256 bytes and reject query strings, fragments,
controls, invalid UTF-8 and credentials. Methods accept GET, POST, PUT, PATCH,
DELETE, HEAD and OPTIONS, normalized to uppercase. Changed labels are not safe
selectors; obtain the original pair from the user when needed. The tool reuses
per-app diagnostic consent across environments, native ownership/entitlement
and final app identity checks. `scope: app_wide` describes diagnostic access;
`series_scope` distinguishes `app_wide` from `route_method` observations.

`since` accepts positive durations, whole days or an RFC3339 start; omission
uses native 24h. Optional historical `until` is an exclusive RFC3339 end at or
before now. Timestamp offsets are normalized to UTC and time inputs are bounded
to 64 bytes. Preserve native effective `from`, `until`, `since`, `as_of` and
`window_clamped`. Native retention caps the duration ending at `until`, without
guaranteeing data at that historical time. `source_completeness` and `freshness`
remain `unknown`; query time does not measure telemetry freshness.

Each point has a UTC hourly `start`/`end`, and `window_from`/`window_until` clipped
to the selected native range. `partial: true` identifies a first/last hour with
less than a full hour selected. Counts apply only to that selected interval;
do not treat partial-hour counts as full-hour volume or rates. The exclusive
hour-aligned end contributes no additional bucket. The native PostgreSQL grid
uses microsecond precision (`until` minus one microsecond for its last hour);
sub-microsecond windows at an hour boundary can return no buckets. Such a
response has `status: no_returned_buckets`. The bridge validates native grid
ordering/continuity and never creates missing buckets or widens the window.

Points expose represented requests, `error_requests`, `error_rate_pct`,
`cold_boots`, `latency_status` and p50/p95/p99 milliseconds. Counts and percentile
ranks use collapsed request weights. `error_basis: http_status_gte_400` includes
4xx and 5xx, distinct from 5xx-only debugger regression signals. A native
`zero_filled: true` bucket means no retained requests: its latency percentiles
are omitted. Positive-count observed zero latency remains zero, with
`latency_status: observed`. Empty buckets do not establish zero incoming traffic,
complete capture or healthy performance. Guest execution/CPU/memory measurements
are not supplied by this native timeseries.

`limit` bounds only the oldest-first point prefix (default 48, maximum 168),
without changing the native query, window or bucket width. Report
`native_returned_point_count`, `returned_point_count`, `points_truncated` and
`order: oldest_first`. Later omitted buckets are not zero traffic. Top-level
`status: observed` or `no_retained_requests` describes the **full native series**,
including omitted points; an empty projected prefix may omit retained traffic.
To inspect a narrower period, make an explicit independent historical selection.
Do not silently retrieve extra ranges, aggregate/average hourly percentiles into
window percentiles, infer deployment causality or claim statistical significance.

`gregale_performance_diagnosis` includes the trends recipe and fetches no trend
data during prompt retrieval. Overview, trends and request discovery are
independent reads; discovery has no method filter or historical `until` and
cannot reproduce that route/method cohort automatically. Native errors or
inconsistent selectors, windows, grouping, bucket grids or metrics fail the
read without fallback queries. No grouped dimensions/consumer identities, raw
requests, spans, SQL, headers, bodies, cost/deployment payloads or future native
fields are forwarded. Retrieval fetches no examples, grants no extra
permissions and performs no collection, probes, replay, model requests or writes.

## Triage recurring errors

Use `gregale_list_error_groups` to inspect recorded error fingerprints:

```json
{"app":"alpha","since":"2026-10-16T10:00:00Z","until":"2026-10-16T11:00:00Z","limit":20}
```

The tool reuses app-wide diagnostic consent across environments, immutable app
identity checks and native ownership/policies. `since` and `until` accept
RFC3339 timestamps, bounded to 64 bytes. This API does not accept duration/day
aliases. Omitted `until` is resolved once to now; omitted `since` is that end
minus 24h. Both are normalized to UTC and truncated to native PostgreSQL
microsecond precision before querying, so returned `filters` and continuations
show the actual selection. A range that collapses at that precision is rejected.
Explicit `until` must be at or before now. Native maximum-window/retention caps
remain authoritative and `window_clamped` records a narrowed range.

`window_start` and `window_end` select **inclusive group last-seen timestamps**,
reported as `window_basis: group_last_seen_at` and `window_bounds: inclusive`.
They do not select individual error/request times. Each group exposes an exact
64-character lowercase hexadecimal `fingerprint`, a closed native `error_class`,
HTTP status, bounded route label, `count`, `request_count` and first/last-seen
values. The native `count` is an issue/merge counter; `request_count` is the
linked-request counter. `counts_basis: native_group_history_counters` makes
explicit that these describe stored group history. Neither is a selected-window
failure total, distinct incident count, current retained/page row count or
error-rate denominator. The two counters may differ; do not assume a ratio or
ordering. First-seen can predate the selected window. The SQL-backed native order
is count DESC, last-seen DESC, fingerprint DESC.

Use one returned fingerprint with `gregale_list_error_requests`:

```json
{"app":"alpha","fingerprint":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","limit":20}
```

This second read selects independent retained fingerprint history. It has no
`since`/`until` filter and can return requests outside the summary range, with
`selection_basis: retained_fingerprint_history_without_summary_window_filter`.
Rows expose unchanged public `lookup_identifier` values, receipt timestamps,
route, HTTP status and error class. `lookup_basis: public_request_id` and
`evidence_availability: not_checked` mean no debugger lookup happened. An
explicit `gregale_get_request` or `gregale_get_request_evidence` call for that
same app retains its own native entitlement, retention, identity and deployment
scope checks. It may return ID-only evidence or missing telemetry; that does
not negate a recorded error. The request page's class/route/status header is
`header_status: observed` when supplied, or `not_reported` on an empty terminal
page; missing status is not HTTP zero.

Both tools query one native page (default 20, maximum 50) and preserve native
ordering. Request rows use receipt time DESC, public request ID DESC. Follow
returned `next_arguments` unchanged; `next_cursor` is an opaque bounded MCP
context envelope containing the unchanged native cursor. It pins app slug and
identity, tool kind and page size, plus the summary's normalized requested and
effective windows or the drill-down fingerprint. Changed selector contexts,
changed app identities, changed effective windows, repeated native cursors or
unsafe/oversized cursor strings fail without fallback queries. The envelope is
selection data, not an authorization token: every page checks live consent and
native ownership again. It is bounded to 8192 bytes and never decoded or edited
by the user/client.

The group API's native cursor does not carry its window, so continuing requires
the explicit `since`/`until` from `next_arguments`. The bridge preserves requested
and effective windows separately; a native policy change that changes the
effective window requires a fresh first page. Native full-page cursor hints may
lead to an **empty terminal page**. `page_complete` means no native continuation
hint, not full error capture. Groups are live mutable data; concurrent updates
can move entries between pages, so pagination is not a snapshot guarantee.
Missing/purged fingerprints keep their native not-found response; the bridge
never switches to a different group, broader window or sample endpoint.

Empty results use `no_returned_groups` or `no_returned_error_requests`; they do
not claim a healthy app. `source_completeness` and `freshness` remain `unknown`,
and the summary's `generated_at` is query time. Native retention/cardinality and
deduplication policies limit what remains. Route labels are redacted before
bounding to 256 bytes and have query/fragment removed; `route_modified` and
`text_truncated` expose changes. Modified labels may collide and must not be
manufactured into exact selectors. Fingerprints and request lookup identifiers
are validated and returned unchanged, never repaired into a new identity.

Explicit projections exclude sample/error messages, stacks, headers, bodies,
raw spans/SQL, instance/node/region and deployment/commit/image payloads, as well
as future native DTO fields. Unknown native error classifications or inconsistent
metadata fail the read. No automatic evidence reads, probes, collection, replay,
model requests, extra environment/log permissions or platform writes are added.
The read-only `gregale_performance_diagnosis` prompt includes this workflow and
retrieves no error data during prompt retrieval.

## Review retained wake observations

Use `gregale_get_wake_history` to inspect retained instance starts:

```json
{"app":"alpha","since":"2026-10-16T10:00:00Z","until":"2026-10-16T11:00:00Z","limit":20}
```

The tool reuses app-wide diagnostic consent across environments, native
ownership/read-surface policies and immutable app checks before and after the
read. The native endpoint requires Hobby or above. Native plan and authorization
errors remain authoritative. `since` and `until` are RFC3339 timestamps, each
bounded to 64 bytes. Omitted `until` resolves once to now; omitted `since` is
that end minus 24h. Explicit end times must be at or before now and start must
precede end. Normalize to UTC and PostgreSQL microsecond precision.

The bridge requests native `pagination=keyset_v1`. Validated modern responses
report `pagination_status: keyset_started_at_instance_id_v1`. They filter app
and inclusive original `since`/`until` before the page limit, seek in descending
start-time/instance-ID order, preserve PostgreSQL microseconds and exclude
missing start times. Instance IDs remain inside opaque cursors.

Explicitly continue with `next_arguments`, preserving the exact app, original
normalized bounds, limit and opaque MCP cursor. The envelope pins immutable app
identity and preserves the native cursor unchanged. Each read rechecks consent
and identity. `window_verification: native_window_echo` verifies the selection,
not retention coverage. Modern row membership is `verified_native_window` with
`observed_to_microsecond_precision` timestamps. No automatic paging occurs.

`source_status: read_succeeded` means this database read succeeded. Backend and
malformed-source failures return errors rather than successful empty or partial
history. Boot/completion metadata is app-scoped; absent metadata remains unknown.
Counters and summaries describe only this returned page, with
`count_basis: returned_page_instance_records` and
`at_capacity_denominator: returned_page_instances_with_meta`. They count instance
records, not unique wakes or requests. Shared wake metadata is not exact instance
attribution. `page_complete` marks the end of this live retained read, not capture
completeness, freshness or a snapshot. Late inserts, retention, state and metadata
can change independent pages; empty terminal pages remain valid.

Older APIs may omit native page metadata. Such initial reads retain
`pagination_status: latest_candidates_not_pageable` with no `next_arguments`
and the compatibility limits below. A resumed downgrade returns
`native_upgrade_required` without repeating or broadening the legacy query.

For legacy compatibility reads, the native API selects the **latest 100 instance candidates before filtering**
their start times. It provides no pagination or truncation flag. Historical
selections can omit older records even when fewer than 100 rows are returned.
Report `native_candidate_limit: 100`, `native_candidate_truncation: not_reported`,
`native_selected_instance_count` and `native_returned_row_count`. These are
selected instance records, not unique wake attempts or request counts. The
misleading native `wake_count_24h` name is projected as the selected instance
count for the actual requested range.

`requested_since` and `requested_until` describe the submitted range;
`window_verification: not_echoed_by_native` records that the endpoint does not
return an effective window or retention clamp. Source filtering is inclusive
for known start times. Native row timestamps lose fractional precision, so
`window_membership: compatible_at_native_second_precision` establishes only
that the timestamp's second overlaps the range. Legacy missing start times can
bypass the native filter: those rows report
`window_membership: unverified_missing_started_at`. Do not claim a verified
effective range or reconstruct missing timestamps.

Each `wakes` entry contains an unchanged safe `wake_id` when available, native
`kind`, current retained instance `state`, start time, bounded trigger/class,
boot or restore `method` and `tier`. Labels remain untrusted display data and
can collide after redaction/truncation; `labels_modified` records changes.
Missing wake IDs remain `wake_id_status: not_reported`. These observations are
not a full wake-event timeline or a live outcome assessment. The bridge fetches
no events, request evidence or extra ranges automatically.

Positive `ready_in_ms` is an observed duration. Native -1/zero is omitted with
`readiness_status: not_reported`; neither proves an unfinished/failed wake or
zero latency. Positive `queued_count` and `concurrency_at_admit` are preserved.
Both originate from the admission concurrency ledger, reported as
`admission_count_basis: native_admission_concurrency_ledger`; the former's native
name does not establish a waiting queue length or a queue waiting duration.
Their native scalar zero values lose presence, so omit them with
`zero_or_not_reported` status instead of inventing observed zero. A present
false capacity flag is retained as observed false. Without a presence flag,
capacity remains unknown even when the raw native boolean is true.

`at_capacity_pct` uses `native_instances_with_meta` as its native denominator.
Report `at_capacity_present_count` and `at_capacity_unknown_in_meta_count`:
metadata rows can lack a capacity flag and still contribute to that denominator.
Such a percentage has status `native_denominator_includes_unknown_capacity` and
is not an all-wake saturation rate. With no metadata, omit the percentage and
report `no_meta_rows`. Summaries describe all native rows, including rows omitted
by the output prefix.

For legacy reads, the newest-first output prefix defaults to 20 rows, maximum 50. It does not
alter the native query. Preserve `returned_row_count`, `rows_truncated`,
`text_truncated` and the native ordering at whole-second precision. All native
rows are validated before projection, including omitted rows. Unknown capacity,
missing readiness and zero-presence limits must remain explicit.

The legacy endpoint can swallow backend read failures and return empty or partial data
without a source flag. Therefore `source_status: not_reported`,
`source_completeness: unknown` and `freshness: unknown` apply to every response.
Native `as_of` is query time. `status: no_returned_wakes` does not prove zero
wakes, complete history or healthy operation. App URLs, histograms, full events,
infrastructure and deployment payloads are excluded. Do not infer restoration
failure, resource changes or savings, add wake readiness to request latency, or
perform probes, replay, diagnostic collection or writes. Performance prompt
guidance includes these limits and fetches no wake evidence during retrieval.

## Inspect one wake's stages

Use an unchanged `wake_id` from a request's validated `wake` association, wake
history or an exact ID supplied by the user:

```json
{"app":"alpha","wake_id":"wake-1","limit":50}
```

`gregale_get_wake_timeline` reads one native response. History never fetches
stage evidence automatically, and event retention is independent of instance
history. An ID visible in history can have no retained timeline. The tool reuses
app-wide diagnostic consent across environments, native ownership, Hobby+
entitlement and MFA/read-surface policies, and final immutable app checks.
Native errors are returned without fallback reads or wider queries.

Omitting `since` starts at the beginning of retained history, with no implicit
24-hour cutoff. Optional `since` accepts a nonzero RFC3339 timestamp up to 64
bytes, normalized to UTC and truncated to native PostgreSQL microseconds. The
source lower bound is **exclusive**. The original normalized lower bound stays
unchanged across pages; there is no historical `until` argument.

The bridge requests the native API's opt-in `pagination=keyset_v1` mode. It
scopes source rows to the app, selects the canonical boot marker, and then seeks
in timestamp/event-ID order before limiting. Equal-timestamp events therefore
continue on the next page. Native event IDs remain inside opaque cursors and
are not exported as event fields. `pagination_status: keyset_at_id_v1` and
`window_verification: native_since_echo` identify validated keyset support.
The native event limit defaults to 50 and is capped at 100 for MCP.

To continue, explicitly call `gregale_get_wake_timeline` with `next_arguments`:
repeat the exact app, wake ID, normalized original `since` and page size, plus
its opaque `cursor`. `next_cursor` is a bounded MCP context envelope containing
an unchanged native cursor and the pinned immutable app ID. Context changes,
unsafe cursors, app replacement, inconsistent native metadata and nonprogressing
native continuations fail without retries or wider queries. Each page rechecks
consent, live ownership and app identity. The envelope is data, not authorization.

`page_complete: true` means the current native read found no further retained
row. It does not establish complete capture, freshness or a snapshot. Pages are
live: inserts behind the seek position, retention and a changed canonical boot
observation can affect the visible set. Preserve returned/native event counts,
`native_more_events_hint`, repeated kinds and tied timestamps. Stage summaries
still describe each returned page only. Empty terminal pages remain valid if
retained rows disappear between independent reads. The bridge never auto-pages.

Older APIs may ignore the new mode and omit native `page` metadata. A first read
then remains a bounded compatibility read with
`pagination_status: timestamp_only_not_lossless`,
`window_verification: not_echoed_by_native` and no `next_arguments`. Its optional
`native_cursor_at` is only a diagnostic timestamp hint: the legacy source filters
with `at > since`, so using it as a continuation can skip tied rows. Never invent
a cursor or advance time by epsilon. A continuation against an API that no longer
reports keyset support returns `native_upgrade_required` instead of repeating
or broadening a legacy read. A missing legacy hint does not prove complete capture.

Each `events` entry exposes recorded `at`, a recognized `kind`/`stage` and
selected `details`. Queue acceptance, admission, boot start/observation/completion/
failure, restore/cold-boot summaries, readiness, first byte/wake page and park
start/completion/failure are recognized. Unknown kinds retain their timestamp
with `kind: unrecognized_event` and `details.status: not_exposed_for_kind`.
Arbitrary kind/actor strings and raw payloads are not forwarded. Tied times and
repeated kinds remain separate events; one wake can serve multiple requests.

Boot details include bounded method, tier, trigger and trigger class labels,
with `labels_modified` and `text_truncated`. Labels remain untrusted display
data. The closed restore fallback category `after_restore_failed` is exposed
when recorded; unfamiliar values report `restore_fallback_status: unrecognized`
without returning free text. Boot/park failure kinds record observations, not
current app health or the outcome of every attempt.

Selected timings retain distinct boundaries:

| Field | Meaning |
|---|---|
| `at` | Native event recorded time; `event_time_basis: native_event_recorded_at`. |
| `requested_at`, `observed_at`, `started_at`, `completed_at`, `failed_at` | Selected producer-supplied phase timestamps when present and nonzero. |
| `readiness_elapsed_ms`, `probe_count` | Native readiness-loop measurement and attempt count. |
| `acceptance_to_first_byte_ms` | Native gateway acceptance-to-first-byte observation. |
| `post_wake_proxy_ms` | Separate native post-wake proxy timing. |
| `native_call_total_ms`, `wait_ready_ms` | Selected native restore/cold-boot call summary with explicit `native_call_basis`; wait-ready can overlap the call total. |

Do not derive queue waiting time from recorded-event differences or add/subtract
these overlapping measurements to reconstruct a causal critical path. Queue
acceptance has no `queue_wait_ms` payload. The tool computes no durations,
percentiles, failure verdicts or resource/cost recommendations.

Raw selected event keys preserve presence: numeric zero and capacity false are
observed values; absent/null remains omitted and unknown. This differs from
the flattened wake-history DTO, which loses queue/concurrency zero presence.
`queued_count` and `concurrency_at_admit` use
`admission_count_basis: native_admission_concurrency_ledger` and do not measure
waiting queue length. Missing values cannot be inferred from a trigger, known
capacity flag or a different event. Native selected measurements must be finite,
nonnegative whole numbers within JSON's exact integer range.

`stage_observations` counts queue, admission, boot, readiness, proxy and parking
events on this returned page. `not_returned` means the page supplies no such
event. It does not establish failure, an unfinished wake or a missing lifecycle
transition. `status: no_returned_events` can describe an empty filtered read;
unknown unfiltered IDs retain the native 404. Source completeness and freshness
remain unknown; no native `as_of` metadata is available.

Request/instance/node/deployment/snapshot/account IDs, actor labels, healthcheck
paths, raw failure messages, CPU/billing data, nested gateway phases and artifact
maps, and future native fields are excluded. All event envelopes are validated
for matching app/wake identity, order and selection before projection. Selected
payload types/timestamps/measurements are also validated. This read grants no
extra permissions and performs no probes, replay, collection, mutations or
automatic request/event enrichment. Performance prompt guidance includes the
workflow and fetches no timeline evidence during retrieval.

## Inspect retained sampled CPU

Use `gregale_get_profiles` with an exact deployment UUID, required native runtime
and explicit RFC3339 capture window:

```json
{"app":"alpha","deployment_id":"11111111-1111-4111-8111-111111111111","runtime":"go","start":"2026-10-16T10:00:00Z","end":"2026-10-16T11:00:00Z","route":"GET /orders","limit":10}
```

The deployment must belong to the app. Its ownership/scope and current app
identity/diagnostic consent are checked before release. Native profiling plan,
retention and backend checks still apply; errors are returned without retrying a
broader query. There is no implicit latest deployment or default runtime. Runtime
is at most 64 bytes of lowercase letters, digits, hyphens or underscores. Bounds
are at most 64 bytes each; start precedes end, which must be at or before now.
UTC normalization preserves nanoseconds. Optional `route` is the exact native
`METHOD /static-pattern` or `[unattributed]` label, at most 256 bytes. Omit it to
include all routes in that deployment/runtime/window. Do not pass a URL, query,
function name or modified display label.

The response validates the native query echo and reports
`window_verification: native_query_echo`. `sampled_cpu_seconds` and each
function's self/inclusive total CPU describe sampled instrumented processes for
the selected route/window. They do not measure VM CPU, billed usage, request
latency or establish a causal bottleneck. `native_nonzero_sample_entries` counts
nonzero native sample entries, not requests, unique stacks or profile receipts.
Inclusive function totals overlap across callers; never sum them.

Functions retain native descending self-CPU order. `limit` defaults to 10 and
accepts 1–50, bounding only the returned prefix. Preserve `native_function_count`,
`returned_function_count` and `functions_truncated`. There is no cursor or
automatic follow-up. Native groups use name/file/line identity; the projection
excludes file/line/source details, so duplicate names remain separate. Labels are
untrusted display data capped at 256 UTF-8 bytes after known-credential redaction,
with `labels_modified` and `text_truncated` flags. They are never selectors.
Observed function zeros remain zero. `status: no_selected_samples` returns an
empty function array and omits sampled CPU; it does not prove zero CPU, health,
profiling disablement or absence of activity.

Both `coverage` and `attribution` explicitly describe the whole
`deployment_runtime_window`, including for a route-filtered or empty-route read.
They do not describe just the returned function prefix. Coverage reports profile
receipts, union of recorded capture intervals, gaps, optional ingestion time and
recorded failed-upload lower bound. Capture intervals do not measure the fraction
of VM CPU instrumented. `last_received_at` may fall outside the capture window
and proves no freshness. Failure recording is rate limited; even a recorded zero
is a lower bound. Preserve `failures_complete`.

Attribution reports whole-window labeled/unlabeled CPU shares and native
attribution diagnostics completeness. Closed reason codes are `unlabeled`,
`invalid_label`, `route_not_admitted`, `encoding_limit`, `unknown` and
`unrecognized`; native arbitrary reasons are excluded. Neither attribution nor
`native_diagnostics_complete` proves end-to-end capture or instrumentation
completeness. Missing/unavailable quality metadata has status `not_reported` or
`unavailable` and omits measurements while preserving valid function evidence.
`source_completeness` and `freshness` remain `unknown`.

Flamegraphs, raw stacks/samples, paths, lines, source links, repository/commit
details, route/request tables, collector/host identities and native prose are
excluded. This tool reads existing retained profiles; it never enables profiling,
collects, uploads, probes, replays or writes. Retrieving
`gregale_performance_diagnosis` explains these limits without fetching profiles.
See [ADR-1639](adr/1639-management-mcp-cpu-profiles.md).

## Review performance evidence

Call `gregale_get_debug_coverage`, `gregale_get_dependency_latency` and
`gregale_get_critical_paths` with an
`app` and optional `since`, for example:

```json
{"app":"alpha","since":"1h"}
```

These tools reuse per-app diagnostic consent across all environments and preserve
native ownership, entitlement and retention checks. They accept positive Go
durations or whole-day aliases such as `3d`, bounded to 64 bytes. Omission keeps
the native 24-hour default. Native aggregate queries select only app/lookback;
route, status, deployment and latency filtering belongs to request discovery.
The critical-path tool also accepts an output pattern `limit`, described below.
App identity and the native response's app ID must match, and current consent
and identity are checked again immediately before releasing the projection.

Coverage returns retained telemetry rows, represented request counts, error
counts and trace/span/wake/guest evidence counts/percentages. The denominator is
**retained represented requests**, with collapsed-row weighting; it cannot count
requests dropped before storage. `coverage_basis: retained_telemetry_only` and
`collection_completeness: unknown` make that limitation explicit.
`status: no_retained_telemetry` identifies an empty read; zero percentages do not
establish full capture or health. Optional oldest/latest observations remain
native timestamps, with no invented observation when absent.

Each result retains its own effective `since`, `window_start`, `window_end` and
`retention_clamped`. Dependency/path clamp information comes from the native response
(`retention_clamped_basis: native_response`). The coverage API has no clamp flag,
so its bridge indicator compares the requested/default lookback with effective
lookback (`retention_clamped_basis: requested_and_effective_lookback`). The
reads have independent clocks/windows; they are not an atomic snapshot.

Dependency output includes at most 20 groups in native order: type/kind/name,
calls/errors, p50/p95/p99 and exclusive percentiles, baseline/current p95 and
error rates, and native latency/failure regression signals. Those comparisons
split the dependency window into two halves; they do not prove a deployment
regression or root cause. Span evidence remains sampled and weighted by retained
request counts. `telemetry_rows`, `represented_requests` and `span_samples`
preserve that basis. `status: no_retained_spans` identifies an empty span sample.

`source_complete` and `source_truncated` preserve the native bounded-read state.
`dependencies_truncated` reports bridge group omission, `text_truncated` reports
strings bounded to 256 UTF-8 bytes after known-credential redaction, and `complete`
is true only when the source and dependency/edge/exemplar/text projections are
complete. `source_completeness: unknown` retains uncertainty about full capture. Returned/native
group counts describe the native response, not a count of every dependency in
all traffic. Complete bounded evidence never establishes full traffic capture.
SQL/query fingerprints, raw spans, attributes, payloads, headers, separate trace IDs
and deployment-comparison payloads are excluded. Remaining application names and
error classes are untrusted data and can still be private.

Dependency `edges` preserve the native ordered parent-to-child relationship
prefix, capped at 20 records. `from`/`to` contain bounded type/kind/name display
labels; `labels_modified` reports redaction, cleaning or truncation. Labels can
collide and are not unique graph IDs, routes or tool selectors. A native
`application`/`request` parent can be synthetic when the actual parent was absent
or unretained. These links do not establish a complete service topology or a
causal critical path, and an empty `edges: []` supplies no proof of absent
dependencies or healthy traffic.

Edge metrics describe weighted **child span samples**: calls/errors, latency and
exclusive percentiles, baseline/current percentiles and error rates, signed
deltas and the native latency regression flag. Multiple child calls per request
can make edge calls exceed represented request counts. Do not count them as
unique requests or sum overlapping edge timings. Trends split this dependency
window in half; they do not compare deployments. Native edges provide no
per-side call counts or failure-regression flag, and MCP does not infer them
from dependency nodes. `side_sample_availability: not_reported` preserves that
uncertainty: zero baseline/current metric scalars can mean absent side samples
or observed zero. An exemplar establishes only its selected sample, not complete
side coverage or a healthy baseline.

Each edge returns up to three `exemplars` in native order, selected from current,
baseline and failed samples with native selector deduplication. A representative
is an aggregate sample, not a random independent request. `lookup_identifier`
is the unchanged selector accepted by `gregale_get_request` and
`gregale_get_request_evidence` for this same app; it can itself be a native trace
alias. No separate trace ID or inferred deployment is exposed. Retrieval fetches
no exemplar automatically, and follow-up request reads recheck consent,
deployment ownership/scope and app identity independently.

Preserve the exemplar's `window`, `received_at`, `count` and
`duration_basis: child_span_sample`. Its `duration_ms` measures the selected child
span, not full request latency, and zero duration with a positive count remains
observed. `error` describes the child span independently of `http_status`.
Timestamps must fall inside the labeled native half-window (inclusive start,
exclusive end, midpoint in the current half). Invalid/rewritten selectors,
malformed timestamps, contradictory counts or metrics return `invalid_response`
without leaking partial results. Duplicate selectors within an exposed edge are
rejected; reuse across edges remains possible and does not make independent
evidence.

`native_returned_edge_count`, `returned_edge_count` and `edges_truncated` describe
this projection. Each edge has native/returned exemplar counts and
`exemplars_truncated`; the result's `exemplars_truncated` combines the returned
edge prefixes. These flags remain separate from native `source_complete` and
`source_truncated`. Limits are applied after the same native app/lookback query;
they add no query, argument, endpoint or permission. The shared 128-KiB response
bound still applies. These independent reads do not form an atomic snapshot.

Critical-path output preserves native order and projects up to `limit` patterns
(default 5, maximum 10), 16 segments per pattern and 3 representative requests
per pattern. This is a bridge output limit, not a native query/sample limit;
it is never forwarded to the API. Reduce it if a result exceeds the shared
128-KiB output bound. `returned_path_count` and `native_returned_path_count`
describe this response, not every path in all traffic. `paths_truncated`, each
pattern's `segments_truncated`/`exemplars_truncated`, and `text_truncated` report
projection loss separately from native `source_complete`/`source_truncated`.
`complete` requires both source and projection completeness. A bounded complete
read still cannot establish full traffic capture.

The native patterns come from retained span relationships; their duration is
reconstructed path wall time, not full request latency. `path_samples` counts
unweighted retained rows with a reconstructed path; calls and percentiles use
represented request weights. Ordered segments, p50/p95/p99, error rates and
baseline/current calls/p95 preserve the native values. Regressions compare the
two halves of this path window and do not prove deployment causality.
Aggregate `dominant_segment_exclusive_ms` is the **maximum observed sample**,
not a percentile or typical bottleneck. Do not sum dependency percentiles to
manufacture a path. Signatures and normalized segment names are bounded display
labels, not guaranteed unique keys, routes or supported filters.

Each exemplar exposes a safe `lookup_identifier`, baseline/current half,
timestamp, path duration, HTTP status, error signal, represented `count` and
optional dominant segment. Use that identifier with `gregale_get_request` and
`gregale_get_request_evidence` for the same app. It can be a native trace alias
or telemetry-row ID; no separate `trace_id`, raw spans/attributes or SQL fields
are exported. Examples are selected aggregate representatives, not independent
individual requests. Preserve weights and selection limits when citing them.
Empty `critical_paths` are reported as `status: no_returned_paths`, including
retained rows with no reconstructable path. Missing/empty paths do not prove
healthy performance. Exemplar identities remain unchanged; unsafe or
credential-shaped identities fail the read instead of becoming altered IDs.

Use `gregale_performance_diagnosis` with `app` and optional string `since` for a
read-only recipe: review route analytics, hourly trends, retained coverage and
wake observations, inspect dependency and ordered-path trends, then drill into
representative requests or discover relevant examples and inspect exact evidence.
Wake history takes explicit RFC3339 timestamps rather than a lookback alias.
It calls no performance API
during prompt retrieval. App completion suggests currently consented identities;
lookback values are not enumerated. Native errors are returned without broadening
the query. No SQL, replay, probes, collection, model requests or platform writes
are added by the tools or prompt.

## Compare deployment performance

Use `gregale_compare_deployments` when two exact deployment IDs are available:

```json
{"app":"alpha","source_deployment_id":"11111111-1111-4111-8111-111111111111","target_deployment_id":"22222222-2222-4222-8222-222222222222","since":"1h","until":"2026-10-16T11:00:00Z","limit":20}
```

The IDs must be different nonzero UUIDs belonging to the selected app. They are
comparison labels; the tool does not infer release order. Optional `route` is
an exact path/template without query/fragment or credentials (maximum 256 bytes).
`since` accepts a positive duration or whole-day alias (maximum 64 bytes).
Comparison and regression detection default to **1h**, while coverage,
dependency and critical-path tools default to 24h.
`until` is an optional RFC3339 timestamp at/before now (maximum 64 bytes); omission
uses the native server clock. Use an explicit lookback when aligning evidence.
`limit` bounds projected routes (default 20, maximum 50) in native order and
does not limit the native query. No continuation or automatic widening occurs.

The tool reuses app-wide diagnostic consent across environments. Each exact
deployment's app ownership/scope is checked before the native query and again
before releasing output, with current app consent/identity checked last.
`source_scope` and `target_scope` identify the native deployment scopes. This
permission grants no additional environment/log reads or writes to other tools.

The existing native POST `/v1/apps/{slug}/debug/compare` is a read-only retained
telemetry query. Its `mirror` side is exposed as `target` by MCP; the query does
not execute a mirror request. Native ownership, read scope, MFA and plan gating
remain authoritative. Effective `since`, `window_start`, `window_end`,
`retention_clamped` and the exact route filter come from the API response. The
start is clamped against current plan retention even for a historical `until`.
A window ending outside retention, an invalid/nonpositive lookback, or an
invalid/future window end returns a native validation error. The two native
reads use the same reported window and occur sequentially; they are not an
atomic snapshot. The bridge checks returned IDs/filter/app/window and an
explicit window end. Older APIs lacking metadata return
`comparison_metadata_required`; upgrade the API before using this tool.

Each route exposes `source`/`target` counts as `represented_requests` and
`p50_ms`/`p95_ms`/`p99_ms` only for a positive count. Observed zero latency is
valid. A missing side is `evidence: no_retained_samples` with zero count and
absent percentiles, and `comparable` is false. Empty route unions remain `[]`
with `status: no_retained_route_samples`. None of these states proves health.
`source_completeness: unknown` preserves capture/read uncertainty.

`returned_route_count`, `native_returned_route_count`, `routes_truncated` and
`text_truncated` describe this projection. Known credentials are redacted before
256-byte text bounds, and route queries/fragments are removed. Display labels
can be shortened; all remaining text is private, untrusted application data.
Counts are weighted by collapsed telemetry representation, not independent
individual observations. Traffic mix, sample sizes and route availability can
explain differences; the tool performs no significance test or causal regression
decision. It never triggers replay, collection, rollback or traffic/configuration
changes. Native errors are returned without retries or fallback comparisons.

The performance prompt includes this workflow when the user supplies both IDs.
It does not silently choose latest/previous releases, and fetching the prompt
does not fetch comparison evidence.

## Review the regression inbox

Use `gregale_list_regressions` to prioritize native detector observations:

```json
{"app":"alpha","since":"1h","limit":20}
```

The tool reuses app-wide diagnostic consent. Native app ownership, read scope,
MFA, plan and retention checks remain authoritative. Each projected deployment's
UUID, app ownership and scope are checked, with ownership/scope and current app
consent/identity rechecked before output. Local stdio uses native credential
authority. Diagnostic consent adds no environment/log or write permission.

`since` accepts a positive duration or whole-day alias up to 64 bytes and defaults
to **1h**. It filters detection recency (`lookback_basis: last_detected_at`),
not request sampling time; `first_detected_at` can predate that lookback. The
effective native `since` is preserved and cannot widen the request.
`retention_clamped` is derived with
`retention_clamped_basis: requested_and_effective_lookback`. The API supplies no
exact window boundaries or completeness metadata. App identity comes from the
selected app lookup and is checked against projected deployments.

Rows remain in native order: error-bearing observations first, then factor and
last detection time. Native visibility excludes resolved and currently
time-dismissed observations; expired dismissals can return as active. The bridge
does not reinterpret visibility using its own clock. Triage state describes
workflow, independently of health. The read-only inbox exposes no acknowledge,
dismiss, resolve or reopen actions.

Each row preserves `kind`, p95/baseline latency, the decimal string
`regression_factor`, affected/error counts and error rates, detection timestamps,
triage state/timestamps and deployment scope. These are detector observations,
not a newly sampled comparison. For errors-only signals, `affected_count` counts
failed requests. `suspected_dependency.basis: hypothesis` preserves uncertainty;
an empty legacy reason means latency. SQL/query text is omitted by construction.
The observation supplies no baseline deployment ID or proof of root cause.

`limit` bounds projected rows in native order (default 20, maximum 50); it does
not limit the native query and provides no pagination. The output records
`returned_regression_count`, `native_returned_regression_count`,
`regressions_truncated` and `text_truncated`, with
`source_completeness: unknown`. Empty results remain `regressions: []` and
`status: no_visible_regressions`, which does not establish healthy traffic or
complete capture. Native errors return without retry, fallback or wider reads.

Known credentials are redacted before UTF-8-safe 256-byte text bounds. Route
queries/fragments are stripped; `route_modified` flags any changed display label.
Do not copy a modified label into an exact route filter. Remaining app text is
private and untrusted, and redaction cannot recognize every arbitrary secret.
The performance prompt guides request discovery/evidence followup and deployment
comparison only when two exact deployment IDs are supplied. These independent
reads do not form an atomic snapshot. Prompt retrieval fetches no regression
evidence, and neither the prompt nor tool triggers replay, collection, rollback
or other writes.

## Review regressions across selected apps

Use `gregale_list_fleet_regressions` for a bounded overview of explicitly
selected apps:

```json
{"apps":["alpha","beta"],"since":"1h","limit_per_app":5}
```

`apps` must contain one to five distinct existing slugs. The entire selection,
lookback and output limit are validated before API reads. No app discovery or
future-app inclusion occurs. Remote access requires diagnostic consent for each
app; account-wide/future-app inventory access alone does not grant diagnostics.
Local stdio uses current native credential authority.

Results preserve app selection order. Each `apps` entry has `status: ok` and a
`result` containing the existing safe regression projection, or `status: error`
and an `error` with code, optional HTTP status and retry delay. Error entries have
no diagnostic result or native error prose. Each successful app keeps its own
effective lookback, retention clamping, deployment scopes, native observation
order, counts, visibility and unknown completeness. `requested_since` records
the shared requested lookback (default **1h**); it is not an effective fleet
window. No cross-app metric aggregation, ranking or health verdict is inferred.

`limit_per_app` defaults to **5** and accepts **1–10**. A separate **16 KiB** safe
result bound can further shorten each native prefix, setting
`response_size_truncated` and `result.regressions_truncated`. Native counts and
the actual returned count remain visible. These are projection bounds without
native query limits, pagination, fallback reads or automatic follow-up.

`successful_app_count`, `failed_app_count`, `partial` and envelope `status`
describe selected app reads. Status is `ok` when every app succeeds, `partial`
when some succeed, and `unavailable` when none succeed. `partial` is true for
any failed read, including all-app unavailability. It does not measure source
completeness; failed or empty evidence does not prove health. Truncation is
reported independently in each app result.

Reads are sequential. Each app read and final guard receives at most **5s** and
its share of the tool deadline, with half the request budget reserved for final
checks. Per-app timeouts preserve other valid results; global timeout or
cancellation fails the call. Immediately before output, every collected result
is checked again for current diagnostic authority, app identity and deployment
ownership/scope. An app whose check fails becomes an error entry. Independent
reads and checks are not an atomic snapshot. Existing reviewed triage tools
remain explicit follow-ups; this tool starts no watch, subscription or write.

## Review regression triage

Use `gregale_plan_regression_action` with `app`, exact `deployment_id` and
unmodified `route`, `action` (`acknowledge`, `dismiss`, `resolve`, `reopen`), and a
reviewable `reason`. Select `project` and `environment` for a named deployment.
Planning reads an exact workflow observation, including dismissed or resolved
rows that the normal inbox hides. It pins immutable app/deployment/environment
identities and an opaque native observation version for 15 minutes. Route labels
containing query/fragment data, controls or reflected credentials are rejected;
do not use a modified display label as an action key.

For dismissal, optional `dismissed_until` is an absolute RFC3339 timestamp in
the future, at most 30 days away. Omission freezes an expiry 24 hours from plan
creation. Plans use PostgreSQL microsecond precision. Retries never extend the
reviewed expiry. A committed dismissal receipt remains replayable after that
dismissal expires, while the MCP plan remains valid. The review shows stored workflow state; an expired dismissal
may appear active in the inbox while its stored state remains dismissed.

Review the plan, then call `gregale_apply_regression_action` with its `plan_id`.
Local connections require `--allow-writes` and ordinary native permissions/MFA.
Remote connections also require diagnostic read consent, a separate **Triage
regressions** selection for the app, and write access to the selected environment.
Existing grants have no triage consent; reconnect to select it. Consent pins
current app IDs and survives encrypted restart and token refresh. Named
write access does not imply default-environment writes or vice versa.

The dedicated native `POST /v1/apps/{slug}/debug/regressions/actions` atomically compares `expected_app_id` and `expected_version`.
Any intervening detector update, legacy action or concurrent triage invalidates
the review and returns `regression_changed`; inspect again and create a new plan.
Older APIs without the exact versioned review endpoint cannot create these plans.
Older nodes also reject the dedicated action endpoint, including during a mixed
version rollout.
No fallback removes the guard. Native audit records retain the action and reason.

After a timeout or lost response, retry the **same plan ID**. The attempt and
stable request are saved before the write; encrypted remote state
recovers the exact request and receipt after restart.
The native observation update, response and reason commit together. Its operation
identity covers the complete checked body and account, so a lost response or
native API crash recovers the committed receipt without repeating the action.
Native receipts use the existing 24-hour replay retention; MCP plans expire in
15 minutes. A receipt persistence failure rolls back the triage update. Replays still check current
consent, account and app/deployment/environment identities. A receipt records a
past triage action, not current observation state or recovery. These actions
change workflow metadata only; resolving an observation does not prove health.

## Explain app residency

Use `gregale_get_running_causes` to inspect why the scheduler observed an app
remaining resident. It accepts `app`, optional `since` (the same positive
duration/day syntax as request discovery), and `limit` from 1 to 20 (default 10).
The existing native `/v1/apps/{slug}/debug/running` API enforces ownership,
debugger plan access and retention. The default lookback is 24 hours. Native
errors are returned without retrying or widening the selection.

This tool reuses the selected app-wide diagnostic consent across environments.
The OAuth **App diagnostics** review explicitly includes request, performance
and scheduler residency evidence. Local stdio uses the selected native
credential. No additional log, environment or write access is granted.

The result includes configuration floors/idle timeout, bounded `history`, and
the native `current` causes with `current_observed_at`. Native `current` checks
that an instance is resident and reuses the newest retained observation; it
does not measure live blockers. `current_observation_age_seconds` is relative
to the native `window_end`, and `freshness` is always `unknown`. `config_basis`
identifies whether configuration came from the native app configuration or
that retained observation. Historical observations retain their own settings.
Report observation times and degraded flags when explaining causes.

`history_truncated` preserves the native flag, including conservative truncation
when the result reaches its requested limit. Each current/history observation
projects at most 16 causes with a separate cause-truncation flag. Text is
credential-redacted before the 256-byte field bound; `text_truncated` reports
shortening. Connection endpoints/ports, flow topology and event/trace/wake/
instance identities are excluded. Counts do not identify an application protocol.
Known-credential redaction does not detect every arbitrary application secret;
all remaining text is private, untrusted data.

A cause's optional `request` is a **nearest retained aggregate**, not proof of
one individual request causing residency. Preserve `count`, `match_delta_ms`
and `basis` when citing it. Use `lookup_identifier` with `gregale_get_request`
and `gregale_get_request_evidence` for the same app. Attributed deployment
ownership/scope is checked before projection and again before output; current
app consent/identity is checked last. Route queries/fragments are removed.

`status: no_retained_observations` preserves empty history. Empty causes,
`no_blocker_observed`, old observations and degraded/truncated evidence do not
prove inactivity, health or scale-to-zero. Workload residency does not prove
queue processing or readiness. This tool never estimates hypothetical savings.

Use `gregale_residency_diagnosis` with `app` and optional `since` for guidance
through causes, configuration basis, observation age and request evidence.
Prompt retrieval fetches no scheduler/request evidence; app completion suggests
only currently consented identities. These are independent reads, not an atomic
snapshot. The tool and prompt add no probes, replay, collection, model requests
or platform writes, and do not plan/apply configuration changes.

## Enable reviewed release actions

Add `--allow-writes` to the agent's subprocess arguments to expose
`gregale_apply_deploy`, `gregale_promote_deployment`, `gregale_apply_rollback`,
`gregale_apply_environment_promotion`, `gregale_apply_environment_rollback`,
`gregale_apply_workload_rollout`, `gregale_apply_service_abort`,
`gregale_apply_route_budgets`, `gregale_apply_regression_action`, and, on
local connections, `gregale_apply_local_deploy` and `gregale_apply_env_change`.
The selected credential still needs ordinary API permissions. A dedicated key
with `apps:read` and `deploy:write` scopes supports management; additional API
policies, including MFA and release checks, remain enforced.

The image release workflow is:

1. Call `gregale_plan_deploy` with the existing app slug, an immutable image
   reference such as `registry.example/team/app@sha256:<64 lowercase hex digits>`,
   and a reviewable `reason`. Review the account, API endpoint, profile, app ID,
   default scope, source, current release, and traffic effect in the plan.
2. Call `gregale_apply_deploy` with its `plan_id`. The server rechecks account,
   app configuration, latest deployment, and current serving release before
   staging a revision at **0% traffic**. This can incur build/runtime costs.
3. Call `gregale_wait_deployment` with the app and returned deployment ID.
   `wait_seconds` defaults to 20 and is capped at 45, within the tool deadline.
   `pending: true` means poll again. A `live` revision at zero traffic is staged;
   it does not establish current production health.
4. Call `gregale_promote_deployment` with the original `plan_id` after reviewing
   the staged revision. Gregale atomically sends 100% traffic to it only if the
   original serving release still owns production traffic. A concurrent release
   returns `traffic_serving_changed`; the adapter never retries without the guard.

For rollback, call `gregale_plan_rollback` with the app, exact
`target_deployment_id`, and reason, then review and apply its `plan_id` through
`gregale_apply_rollback`. Checked rollback verifies the current release, artifact
integrity, and bindings. If the result includes `rollback_operation`, follow its
`id` using `gregale_get_rollback`; acceptance is not completion.

Image and rollout plans expire after 15 minutes and belong to this connection.
Source plans use the thirty-minute window described below. Each action has a
stable idempotency key derived from the plan. Retry with the **same plan ID**,
including after a timeout or ambiguous upstream error. Remote OAuth refresh
preserves the connection's plans. With encrypted remote state configured, a
restart also preserves the grant's unexpired plans and retry keys. A fresh
connection has its own plans. Stdio and ephemeral HTTP restarts lose plans;
inspect deployment history before creating another plan. A connection holds at
most 256 plans. Staging, promotion and checked rollback use API transactional
checks. Reviewed image staging requires an API that implements
`GET /v1/apps/{slug}/release-review`; it never falls back to an unguarded insert.

Deploy tools support immutable OCI images for existing request-mode apps in the
default scope or a selected named project environment. Connected GitHub source
builds use the source workflow below. Local connections create apps and
deploy a project directory with the local tools below; mutable image tags keep
their CLI release workflow.
Image and reviewed source service/worker rollouts use the native workflow below. Apps with multiple serving
releases require explicit traffic-split review in the CLI. Secret values are
never accepted, and other configuration mutations are not exposed.

## Deploy a local project and set variables

On a local stdio connection, the server can deploy a project directory and set
non-secret variables. It runs the same `gregale` binary as a subprocess with
`--non-interactive --json` and the connection's credential, so each step does
exactly what the matching CLI command does (ADR-20261026141841). These four
tools are absent from remote OAuth connections.

1. Call `gregale_plan_local_deploy` with the absolute `path` of the project
   directory, the `app` slug and a `reason`. The plan:
   - shows the app shape the CLI infers (`gregale deploy --app --plan`);
   - records a fingerprint of the files;
   - pins the app's release state.

   The filesystem root, the home directory, and a directory under a linked
   project context (`.gregale/project.json`) are refused. Functions and
   multi-service projects stay in the CLI.
2. Call `gregale_apply_local_deploy` with its `plan_id`. Apply refuses a
   directory that changed since planning. It uploads with the stable
   idempotency key `mcp-local-<plan_id>`, so retry with the same plan ID after a
   timeout. An upload may take up to ten minutes.
   - A new app, or one that serves nothing yet, is created if needed and its
     first release goes live.
   - An app with a serving release is staged at **0% traffic**. Wait with
     `gregale_wait_deployment`, then call `gregale_promote_deployment` with the
     same plan ID. Promotion keeps its atomic guard on the planned serving
     release.
3. To create an app before its first deploy, for example to set variables
   first, plan with `create_only: true`.

To set variables, call `gregale_plan_env_change` with the `app`, up to 50
`values` (keys match `^[A-Z][A-Z0-9_]*$`, values hold no newlines), a
`reason`, and optional `restart`.
- The plan runs `gregale env push --dry-run --secret-scan strict` on the
  default scope and pins the newest config revision.
- A value that looks like a credential refuses the plan. The agent should ask
  the user to run `gregale secrets set` instead and never handle a secret
  value.
- `gregale_apply_env_change` applies the plan only if no other config change
  landed since planning. The config guard judges the change as it does any
  push.

The credential needs `deploy:write` to create and deploy apps. It needs
`secrets:write` to set variables, because `env push` stores them through the
secrets API.

## Reviewed Git source deployments

Call `gregale_plan_source_deploy` with `app`, connected GitHub `repo` as
`owner/repository`, a branch or full lowercase 40-character commit SHA in `ref`,
and `reason`. For a named target, also provide `project` and `environment`.
Planning checks repository access and resolves a branch once; it does not fetch
an archive or enqueue a build. For a tag, supply its exact commit SHA.

Review the returned account, app/environment IDs, `source.commit_sha`, immutable
repository/installation identities, source root, configuration hash and traffic
impact. Source plans last thirty minutes: the normal fifteen-minute review
window plus Gregale's fifteen-minute build budget. Image plans retain their
fifteen-minute lifetime. Connection expiry and consent gates still apply.

Use the same `gregale_apply_deploy` with `plan_id`. Gregale fetches that exact
commit using the connected installation, verifies provider identity and stream
metadata, scans the archive, and atomically stages it at zero traffic with the
reviewed configuration. Moving a branch afterward cannot replace the commit.
The build runs through existing builderd microVMs and normal quotas. Repository
manifest configuration, triggers, workflows, operation definitions and release
commands are not imported by this mode. Current target workflow and operation
declarations are retained from the reviewed serving release. Manage declaration
changes through their native configuration workflows.

For an existing service or worker, explicitly supply `rollout_mode: "service"`
or `rollout_mode: "worker"` to the same source planning tool. It must match the
reviewed target mode. Apply that plan with `gregale_apply_workload_rollout`.
Traffic must remain omitted: services retain their predecessor through native
readiness, gateway acknowledgement and drain; workers become the newest live
generation after the build and replace consumers under the reviewed stop policy.
These plans cannot use `gregale_apply_deploy` or manual traffic promotion.
Protected environments require their approved environment promotion workflow.
The same plan ID retains the commit, build/deployment receipt and retry payload
across bridge restart; never replan an ambiguous write without inspecting history.
Worker residency observations do not confirm queue delivery or acknowledgements.
For service and worker rollback, `gregale_plan_workload_rollout` accepts an exact
`retained_deployment_id` for an image or a GitHub source release. Source releases
need canonical `github://owner/repository@<40-character-commit>` provenance and
a matching commit field. The build root must still match. Planning reviews the
retained commit through the currently connected installation and pins current
target settings; apply rebuilds it as a new generation through builderd.
This rebuild does not restore the original artifact: mutable build dependencies
can produce different binaries. Uploads, branch-only provenance and inaccessible
repositories are rejected. Protected targets retain environment approval gates.

`gregale_wait_deployment` returns bounded build lifecycle data as `build`, along
with deployment state. Build success is not serving readiness: wait for the
zero-traffic deployment to become live before `gregale_promote_deployment`.
Protected targets retain the human-approved environment-promotion workflow.
Retry apply using the original plan ID after an ambiguous response; encrypted
bridge state preserves the exact source and payload across restart. Admission
fails closed if native configuration or repository identity changed. Inspect
existing deployments before replanning after a partial enqueue failure; retries
must not create duplicate work.

## Native service and worker image rollouts

Call `gregale_plan_workload_rollout` with `app`, digest-pinned `image`, and
`reason`. For a named target, provide `project` and `environment`. Review the
execution mode, per-replica resources, replica policy, readiness path, stop signal/grace period,
configuration hash, exact release baseline and `traffic_effect`. Plans last
fifteen minutes and require the same app/environment consent as other releases.
An active deployment must finish or be recovered before another generation is
planned. Protected environments require approved environment promotion.

Apply through `gregale_apply_workload_rollout` with the same `plan_id`. This
**activates a native rollout** and can change serving traffic or consumers
without another promotion call. The native API checks the review under its
admission locks, freezes named workload settings, and retains normal image,
signature, billing, quota and binding checks. Retry the same plan ID after an
ambiguous response; remote encrypted state retains the original key and body.

For services, the candidate begins at zero traffic with the native `rolling_out`
marker. Gregale waits for desired ready replicas, checks bindings, switches
routing, waits for gateway acknowledgements and drains previous requests.
`gregale_get_rollout` exposes the handoff phase, generation and binding blockers.
`gregale_wait_deployment` keeps a live service image pending while its handoff is
still rolling out. MCP does not force service traffic or bypass readiness.

To recover an active service rollout, call `gregale_plan_service_abort` with
`app`, exact `deployment_id`, exact `predecessor_deployment_id`, and `reason`
(and the named project/environment when applicable), then
`gregale_apply_service_abort`. The native transaction checks the configuration
and exact generation pair again. Its `service_recovery.status: accepted` receipt
confirms reverse-handoff intent; observe the rollout until the native barriers
finish. A completed service rollout can be recovered by redeploying a retained
image as described below, or through native checked rollback.

Workers select the newest live generation per environment regardless of HTTP
traffic weights. A live image can therefore start consumer replacement
immediately. Gregale stops retired consumers using its native stop policy and
reconciles queue-driven replicas, including retirement lease holds.
`gregale_get_rollout` includes a native `worker_observation` with the selected
live generation, running/starting/draining counts and retired resident count.
The native reader takes one database snapshot, including other deployments only
within the selected scope. `completion_basis: resident_generation_snapshot` and
`consumer_switch_confirmed` describe **generation residency at `checked_at`**.
Convergence requires a running selected generation, no starting/draining/other
resident rows on it and no residents from retired generations. An idle pool is
not reported as confirmed. `queue_processing_confirmed` remains false: these
observations do not prove replica policy satisfaction, delivery acknowledgement,
external consumer shutdown or queue processing. Use scoped health and native
queue evidence for those facts.

For service or worker rollback, use `retained_deployment_id` instead of
`image` in `gregale_plan_workload_rollout`. It selects the exact retained image or
canonical GitHub commit
in the same app/environment and creates a **new generation with current
reviewed configuration**. It does not restore historical settings or undo
external queue effects. GitHub source releases rebuild through the reviewed
source workflow described above. Upload provenance and retained image digests
without a full immutable OCI reference require the native CLI release workflow. Worker
rollback through HTTP traffic weights is unavailable in MCP.

## Advisory route budgets per environment

Use `gregale_get_route_budgets` with `app` and optional `project`/`environment`.
Without a named selection it reads the explicit default scope. Every scope inherits
the app policy until an override is saved. Customer grouping and production recovery
actions are excluded from scoped evaluation.

Call `gregale_plan_route_budgets` with the same selectors, `reason`, `inherit: false`,
`enabled`, and `routes`. Each route selects an exact normalized `method`/`path`,
`max_5xx_rate_bps` (0–10000) and/or `max_p95_ms`. Error thresholds are basis points:
100 is 1%. Omit a signal to disable it. Supply an empty routes array with
`inherit: true` and `enabled: false` to restore app-budget inheritance.

The 15-minute plan shows the current effective policy and exact proposed update.
Apply its `plan_id` with `gregale_apply_route_budgets`; writes must be enabled and
separately consented for the selected environment. Native admission atomically
checks immutable IDs and both policy revisions. Changes to either policy reject
stale plans. Retry the same plan after an uncertain response; an encrypted bridge
journal and a durable native receipt preserve the exact proposal and original result.

Saving requires fresh completed observation windows, including restoring inheritance.
The update is advisory: it changes neither traffic nor canary/release gates, and does
not enable incidents or automatic rollback. Production's existing app monitor keeps
its own configuration and recovery behavior. Environment budget overrides remain
bound to their registered identity; release promotion does not copy them.

## Release between named environments

Registered project environments use Gregale's guarded promotion workflow. These
tools reuse existing immutable source releases. Direct image staging also accepts
an explicit project/environment. `gregale_preflight` accepts the same app and
optional project/environment selection. It reports each read compatibility and consent
check independently; unknown results do not grant write permission. It creates no
plans or source reviews. Store status describes an open encrypted journal, not a
restart qualification or a guarantee that future writes will succeed.
The tool catalog has 49 read/planning tools, or
58 with writes.

To prepare a new staging release, call `gregale_plan_deploy` with `app`, `image`,
`reason`, `project`, and `environment: "staging"`. Review the immutable registry
IDs, `configuration_hash`, workload revision/hash and serving release in the plan.
Apply its `plan_id` through `gregale_apply_deploy`; the API atomically freezes the
reviewed target configuration and inserts a zero-traffic candidate. The first
reviewed deployment also freezes a desired revision when the environment has no
head yet. Configuration or registry changes reject the stale plan.

Call `gregale_wait_deployment` with that same project/environment and deployment
ID. A live zero-traffic candidate is staged; it does not confirm serving health.
For an unprotected staging environment, explicitly call
`gregale_promote_deployment` with the original plan ID to send 100% of **staging**
traffic to it. Promotion checks the committed configuration and original serving
release in the write transaction. Traffic in other scopes remains unchanged.
Protected targets use an approved environment promotion instead of manual traffic
promotion.

Call `gregale_get_health` with `app`, `project`, and `environment` after staging
traffic is exercised. `gregale_get_metrics` accepts the same selection and returns
scoped request counters from the fixed five-minute health window; `requests.known`,
coverage and sample time identify unavailable or stale evidence. Read exact
deployment logs with `gregale_get_logs`. Health reads never issue smoke requests,
wake instances or certify source qualification. Named staging and health require
the updated scoped-review API; default-scope responses are never substituted.

1. Call `gregale_list_environments` to see available project/environment pairs and
   this connection's write access. Call `gregale_get_environment` with `project`
   and `environment` to inspect consented workload release inventory.
2. Call `gregale_plan_environment_promotion` with `project`, `from_environment`,
   `to_environment`, and `reason`. Review the account, project/environment IDs,
   exact release changes, `promotion_hash`, and traffic effect. Target settings
   and secrets are preserved. Gregale's source qualification checks still apply.
3. Call `gregale_apply_environment_promotion` with `plan_id`. For a protected
   target, it returns `status: "approval_pending"`, `review_url`, `expires_at`,
   and `retry_after_seconds: 5`, without changing deployments. Ask the human to
   open that link, sign in to the matching Gregale account, and review the exact
   source/target releases, environment identities, reason, and promotion hash.
   The form requires account-bound CSRF and expires after five minutes. Closing
   the page leaves the request unapproved until expiry.
4. Retry apply with the same `plan_id`, no more frequently than the returned
   interval. While waiting, it returns the same pending review. After human
   approval, the bridge exchanges a separate private polling secret and keeps
   the approval credential out of MCP output. It rechecks consent, registry and
   workload identities, and the preview hash, then uses Gregale's guarded promote
   endpoint. Expired or changed reviews require a new plan. With encrypted bridge
   state configured, pending reviews and approved retries survive restart. Use
   `gregale_get_environment_promotion` with the returned `promotion_id` to follow
   progress; acceptance is not completion. The manual `approval_token` argument
   remains available for existing CLI/API approval workflows. MCP has no tool
   that can approve a review on behalf of the human.
5. To restore a promotion's captured predecessor, call
   `gregale_plan_environment_rollback` with that project, target environment,
   exact promotion ID, and reason; review and call
   `gregale_apply_environment_rollback` with its `plan_id`. Gregale stops rollback
   if a target release changed after promotion.

These plans expire after 15 minutes and use the same connection-owned retry rules
as default releases. Retry the same plan ID after an ambiguous error. The encrypted
journal preserves environment plans, original approval credentials and request
keys. Whole-project promotion/status/rollback requires consent for every current
workload in both source and target; a partially selected project can still inspect
its consented workload inventory.

## Connect a remote agent with OAuth

Streamable HTTP includes a self-hosted OAuth authorization bridge, independent
of the API's existing OIDC token-exchange issuer. Browser onboarding is the
default: start sign-in on the bridge, authorize in Gregale's signed-in dashboard,
and return to review the resolved account, agent, callback, and apps. Gregale
issues a dedicated eight-hour `mcp-login` credential with `apps:read` and optional
`deploy:write`; it cannot administer keys, read secrets or change account settings.
The existing dashboard session chooses the account. To connect another account,
switch accounts in Gregale before authorizing.

Choose all apps (including future apps) or only selected apps. Selection binds
slugs to immutable app IDs; recreated slugs do not inherit permission. Inspection
covers the selected apps. Default inspection is included; default writes and each
named environment's read/write access require separate checked consent. Named
grants pin project, environment and workload IDs; future environments/workloads
require reconnecting to review access. Write scope also requires checked consent
and `--allow-writes` on the bridge. The bridge
never uses the host's saved CLI login.

Remote deployment and instance lists filter environment access, and ID-only reads
cannot bypass consent. Remote logs require an exact consented deployment ID.
Remote metrics use attributed request evidence from the selected scope and accept
only the fixed `5m` window. Routes and operational summaries also select an
explicit environment; default reads send `scope=default`. Responses bind app,
project and environment IDs, and old aggregate-only servers fail closed.

Scoped routes use deployment-attributed stored request observations from the
last five minutes. `coverage: deployment_attributed_history` and `source: partial`
identify this limited history; it is not a complete live collector or route
catalog. Missing telemetry support or plan access returns `source: unavailable`.
Unknown deployment identities and neighboring scopes never contribute routes.
No app-wide route counter or discovered-route catalog is used as a fallback.

Scoped operational summaries include scoped health, attributed incident metadata,
and bounded recovery lists filtered **before** their limit. Recovery coverage is
`explicit_scope_only`. Restart handoffs without an explicit scope are excluded,
including app-wide requests; unavailable readers are marked unavailable rather
than treated as an empty successful queue. The latest handoff is selected before
scope/state filtering so a completed or moved operation cannot revive an older
pending record.

Scoped monitoring evaluates the saved **app route budgets** against one exact
serving deployment in the selected environment. `monitoring.route_budgets`
includes the scope, immutable environment identities, `budget_source: app_policy` or `environment_policy`,
and a route report with error rates, weighted p95, sample counts and reasons.
Scopes inherit app thresholds by default. An environment override uses its own
thresholds and enabled state; `gregale_get_route_budgets` reports the effective
configuration, policy source, override revision and parent app revision.
The two completed one-minute windows retain the native ingestion allowance and
minimum samples (20 requests for errors, 100 for latency, and at least two
observed errors before confirming an error-budget violation). Both windows must
settle after the deployment and budget observation anchor. Sparse, ambiguous,
unsettled or old observations remain unknown. Historical pooled windows and
customer cohorts are excluded. MCP rejects reports older than the existing
two-minute health evidence age limit or with inconsistent check timestamps. Named releases without a binding to the current
environment identity cannot establish monitored health.

App-wide stored reports are not reused as scoped reports; unavailable readers
retain `environment_monitor_unavailable`. Scoped evaluation is read-only and
does not persist incidents or trigger automatic rollback. Reads do not wake
workloads, probe customer endpoints or mutate recovery work.

Browser onboarding requires the updated API device-code flow. Operators can
explicitly select `--oauth-onboarding api-key` for dedicated-key onboarding. That
fallback still requires account/app review; API keys are never MCP access tokens.

Run one bridge process behind HTTPS ingress that preserves the public Host header:

```sh
gregale mcp serve --transport streamable-http \
  --listen 127.0.0.1:8787 \
  --resource-url https://management.example.com/mcp \
  --oauth-redirect-uri https://client.example/oauth/callback \
  --oauth-redirect-uri 'http://127.0.0.1/callback/*'
```

Replace the public resource and callback addresses with those used by your
client. HTTPS callbacks must match the operator's allowlist exactly. A loopback
HTTP entry without a port permits the client's ephemeral port on that same
hostname, path, and query. The registered redirect is then bound exactly through
authorization and code exchange. Codex adds a random final path component; the
operator can allow `http://127.0.0.1/callback/*` for that single component and
ephemeral port. This rule accepts only letters, digits, hyphens and underscores
under `/callback/`, with no nested path or query. It cannot be used for HTTPS or
non-loopback addresses. Configure the client with the public `/mcp` URL
and use its OAuth connect/login flow. Codex's remote MCP configuration uses
`url = "https://management.example.com/mcp"` in `[mcp_servers.gregale]`.

Clients discover OAuth metadata from the 401 challenge, register an allowed
callback, and use authorization-code **S256 PKCE** with the exact resource URL.
Read scope is `gregale:read`; optional write scope is `gregale:write`. API keys
are accepted only by browser account review, never as MCP access tokens. Gregale
API scopes remain authoritative for every tool call.
The default challenge requests read access. To expose remote write tools, request
both scopes in the client's OAuth settings as well as enabling bridge writes.

Each grant has its own SDK server, API credential, and plan store. Every remote
request rechecks its account credential through the API. Access tokens expire
after 15 minutes; refresh rotates both tokens, and reusing a spent refresh token
revokes the grant. Grants expire after eight hours. `POST /oauth/revoke` disconnects
a grant using its token and registered client ID. Token/code lookups use hashes.
Revoking the backing `mcp-login` credential in Gregale also disables the connection
on its next request. Without a state file, restarting disconnects clients. Durable
state supports one bridge process; shared multi-replica grants remain separate work.

Ingress must route `/mcp`, `/oauth/*`,
`/.well-known/oauth-protected-resource` and its `/mcp` variant, and
`/.well-known/oauth-authorization-server` to the bridge, terminate TLS, and avoid
logging request bodies, Authorization headers, or token response bodies. Apply
ingress rate limits to registration and authorization. The bridge enforces Host
and Origin checks, browser-bound consent cookies, CSRF, request-size bounds, and
bounded authorization stores. These tools use stateless request/response HTTP;
standalone SSE subscriptions are unnecessary. On Gregale control-plane hosts,
use existing public ingress; the bridge listener defaults to loopback.


### Retain connections across restarts

Create a private state directory and an independent 32-byte encryption key once:

```sh
install -d -m 700 ./mcp-state
umask 077
openssl rand -hex 32 > ./mcp-state/key
```

Add `--state-file ./mcp-state/state --state-key-file ./mcp-state/key` to the HTTP
serve command. Keep the same key across restarts and store backups separately
from the key. The journal seals account credentials, OAuth registrations/grants,
refresh replay history, browser flows and release plans with AES-256-GCM. Files
must be private regular files. Atomic replacement and fsync precede successful
OAuth responses; mutation attempts are committed before the API request.

One process holds an exclusive state lease. A second writer, modified journal,
wrong key, endpoint mismatch or unsafe permissions fails startup. Storage failure
returns 503 and disables further access until storage is repaired and the server
restarts. This supports process restart, not multiple replicas or rollback to
older backups. Restoring an old backup can restore old token state; use a fresh
state file and reconnect clients after disaster recovery. CLI stderr emits JSON
authorization and mutation audit events containing identifiers, never credentials
or tool arguments. Apply ingress rate limits as described above.

### Validate a real agent client

With Codex installed and a built Gregale binary, run the actual Codex MCP client
against a local, isolated API fixture:

```sh
python3 scripts/ops/management-mcp-client-acceptance.py --fixture
```

The MCP contract workflow runs management race tests, private metrics/listener
checks, SIGTERM/drain/state-reopening checks, and both stdio and remote HTTPS/OAuth
real-client fixtures automatically for relevant changes. Metrics and drain checks
run independently after a successful build, and their JSON reports join the
`management-mcp-client-acceptance` artifact even when another check fails. Its Codex version is pinned to
`0.159.0-alpha.3` with npm lockfile integrity. To run the same client locally:

```sh
npm ci --prefix scripts/ci/management-mcp-client --ignore-scripts --no-audit --no-fund
python3 scripts/ops/management-mcp-client-acceptance.py --fixture \
  --codex-binary scripts/ci/management-mcp-client/node_modules/.bin/codex
```

Remote acceptance runs the real Codex login, dynamic client registration,
PKCE callback, tool discovery and reads, reconnect after bridge and client
restart, token refresh, and grant revocation. It uses a temporary certificate
chain trusted only by the child client and an isolated API/profile; expiry is
advanced only inside fixture state and the disposable client credential file.
Run it locally with the pinned client:

```sh
GREGALE_MCP_CODEX_BINARY="$PWD/scripts/ci/management-mcp-client/node_modules/.bin/codex" \
  go test -race ./pkg/mcpmanagement -run TestCodexRemoteOAuthAcceptance -count=1 -v
```

The published CI report contains discovery/read results and tool count. Client
diagnostics, keys, and live account data are not uploaded.

To use an existing test account, run with `--profile staging` instead of
`--fixture`. The check discovers tools and calls context/app listing without a
model request or deployment mutation. For a remote bridge, first log in with
Codex's OAuth flow and run with `--remote-url https://management.example.com/mcp`
and `--codex-config-dir` pointing to that writable Codex profile. By default the
check uses a disposable client profile and does not change the current chat's
configuration. Alternatively provide an existing access token through
`GREGALE_MCP_ACCESS_TOKEN`; tokens are never put in command arguments or reports.
Claude Desktop acceptance uses the stdio configuration above and still needs a
machine with that client installed. Passing fixture acceptance is protocol
compatibility evidence, not a live-account or production deployment certificate.

The hosting commands below manage your own MCP applications.

## Host an MCP server on Gregale

The MCP hosting profile is preview for stateless servers that advertise at least
one MCP catalog: tools, resources (including resource templates), or prompts.
It uses ordinary HTTP applications and stateless Streamable HTTP, with optional
stateless legacy compatibility. `gregale mcp init` defaults to the Node starter;
`--language go` and `--language python` create compact starters using the
official SDKs. All three starters share the `/mcp` and `/healthz` hosting
contract, reject browser origins unless explicitly allowed, and start with
public open access. Go and Python support external OAuth validation and
per-entry scopes for tools, resources, resource templates and prompts. All three
support modern durable Tasks and legacy protocol compatibility over Streamable
HTTP. Node remains the full Tasks protocol reference. MCP hosting-enabled
builds provide the commands below; check `gregale mcp --help`.
The profile requires a streaming-enabled Hobby, Pro, or Scale account and a
gateway with streaming enabled.

```sh
gregale mcp init --path ./my-mcp
cd my-mcp
npm ci
npm test
npm start
```

`--runtime` selects `node` (the default), `go`, or `python`. All three starters
implement stateless tools, resources (including URI templates), and prompts,
and support external OAuth with per-entry scopes. Python uses PyJWT for token
validation. The Go and Python starters include `greet`, `add`, `stream_demo`,
`build_report`, a welcome resource, a customer-record resource template, and a
summarize prompt. All three starters provide bounded prompt and resource
completions and streamed progress.
Go and Python also support durable `build_report` Tasks, split worker roles,
and queue metrics. Node remains the full Tasks protocol reference; see
[MCP Tasks](#mcp-tasks).

```sh
gregale mcp init --path ./my-mcp --runtime go
cd my-mcp && go run .
```

```sh
gregale mcp init --path ./my-mcp --runtime python
cd my-mcp && python -m pip install -r requirements.txt && python server.py
```

Choose another starter language with `gregale mcp init --language go --path
./my-mcp-go` or `gregale mcp init --language python --path ./my-mcp-python`.
Both templates use the same zero-traffic `gregale mcp deploy` verification and
promotion flow.

In another terminal:

```sh
gregale mcp doctor --url http://127.0.0.1:8080/mcp --legacy --stream-tool stream_demo
gregale mcp call --url http://127.0.0.1:8080/mcp --tool add --arguments '{"a":7,"b":5}'
gregale mcp deploy --path . --name my-mcp --size small
gregale mcp tools --app my-mcp
gregale mcp resources --app my-mcp
gregale mcp resource-read --app my-mcp --uri 'customer://records/example-1'
gregale mcp prompts --app my-mcp
gregale mcp prompt-get --app my-mcp --prompt summarize --arguments '{"text":"weekly report"}'
gregale mcp complete --app my-mcp --prompt summarize --argument style --value exec
gregale mcp config --app my-mcp --name my-mcp
gregale mcp watch --app my-mcp --baseline gregale-mcp.lock.json
```

`mcp resources` and `mcp prompts` return definitions only. Resource reads and
prompt rendering require the explicit `resource-read --uri` and `prompt-get --prompt`
commands. Returned resource contents and rendered prompt messages may
contain private data; review them before saving or forwarding the output.

`mcp complete` requests server-side suggestions for one prompt argument or URI
template variable. Select exactly one of `--prompt` or `--resource-template`, and
provide `--argument` and the current `--value` (an empty value requests the first
suggestions). Pass other resolved string arguments with `--context` or
`--context-file` when suggestions depend on them:

```sh
gregale mcp complete --app my-mcp --prompt summarize --argument style --value exec
gregale mcp complete --app my-mcp \
  --resource-template 'customer://records/{recordId}' --argument recordId --value example-
```

Completions help MCP hosts guide users while filling prompt forms or resource
templates. The CLI caps accepted responses at 100 suggestions. Keep returned
values bounded and caller-authorized; completion results can reveal information
just like resource reads.
All three starters register bounded sample prompt and resource-template
completion handlers.

## Private onboarding and durable stacks

`mcp init` defaults to public access for local examples. Configure private access
while creating any starter instead of editing the JSON by hand:

```sh
gregale mcp init --path ./private-mcp --runtime python \
  --auth external-oauth \
  --issuer https://identity.example.com \
  --jwks-url https://identity.example.com/jwks \
  --resource https://mcp.example.com/mcp \
  --scopes mcp:tools,mcp:read
```

The issuer, signing-key URL, canonical endpoint audience and nonempty scopes
are validated before the destination is created. The generated configuration
preserves the sample catalog allowlists. Required endpoint scopes apply to all
requests; edit entry policies for different roles. Handlers must still enforce
business-record ownership. This configures an OAuth resource server; your
external provider supplies login, consent, PKCE, issuance and refresh.

Obtain a bearer token with the configured audience and scopes and use
`gregale mcp deploy --path ./private-mcp --name private-mcp --token-env MCP_ACCESS_TOKEN`.
The canonical resource URL must be the eventual MCP endpoint. Client tokens
remain in environment bindings and are not written to the scaffold.

Add `--tasks --name reports` to generate separate `web`, `worker` and `observer`
source projects. For example:

```sh
gregale mcp init --path ./reports --runtime go --tasks --name reports
```

All three roles have Tasks enabled. Each `runtime.env` contains only the role,
shared namespace and, for workers/observers, metric destination. The root README
provides application reservation, `mcp tasks configure`, credential binding,
schema preparation, deployment and observer verification steps. Credentials
are never generated or placed in source. Dependency installation and credential
binding remain explicit operations.

`mcp tasks configure` previews known non-secret role settings and uses `--apply`
to write them before first deployment. It refuses conflicting sealed bindings
and changes to live applications; use a reviewed rollout for those. Writes are
per key, preserve unrelated settings, and can be retried after a partial failure.
The worker starts at minimum one replica. Enable zero only after the separate
always-on observer has verified readiness and a fresh heartbeat; the database
and observer remain running while workers are at zero.

Use `gregale mcp capabilities --json` or `--runtime node|go|python` to inspect the
built-in starter contract. The [generated capability matrix](mcp-capabilities.md)
records runtime differences and the separate live qualification requirements.

## Unified operations overview

Inspect serving replicas, durable workers, the observer and held promotions in
one report:

```sh
gregale mcp overview --app reports-web --worker-app reports-worker --json
gregale mcp overview --app reports-web --worker-app reports-worker \
  --observer-app reports-observer --token-env MCP_ACCESS_TOKEN
```

The command reuses the exact-replica health and MCP catalog/protocol checks from
`mcp status`. `--release-policy` adds reviewed caller-specific catalog baselines;
choose it or `--token-env`. Without a baseline, catalog checks establish replica
consistency and readiness, not compatibility with an earlier release. Discovery
never invokes tools, reads resource contents or renders prompts. It may wake the
serving application, just like other endpoint diagnostics.

With `--worker-app`, the report joins backlog, oldest outstanding Task age,
incompatible-handler counts, available running/failure/retry gauges, worker
replicas and handler-version details. Missing or stale gauges are explicitly
unknown. The three shared backlog/age/compatibility metrics are required for
Task health; optional Node telemetry is not required from Go/Python. Failed-Task
counts represent failures retained within TTL, not a cumulative failure rate.

The observer defaults to the worker's scaling link. `--observer-app` can inspect
a separate observer before it is linked. Readiness covers running replicas,
role detection, the shared namespace, metric destination and a fresh heartbeat;
it does not inspect database grants or verify the scope of sealed metric tokens.
Without `--worker-app`, Task inspection is `not_requested`, not disabled or empty.

All pages of web/worker deployment history are checked for promotion holds.
The report includes hold age, recovery attempts, next retry time and explicit
inspection/retry arguments. Retry actions are marked `mutating` in JSON and are
omitted while recovery is in progress. Overview itself never retries, promotes,
cancels, drains or changes configuration.

The JSON document has `version: 1`, observation time, separate `serving`,
`tasks`, `releases` and `diagnostics` sections, and overall health
`healthy`, `unknown` or `degraded`. Exit zero requires healthy evidence for the
requested scope. Missing evidence returns exit one with the available report;
an unavailable worker does not hide serving results. One overall `--timeout`
(default 30 seconds, maximum two minutes) bounds both independent collectors.
These are live snapshots, not an atomic fleet view or native/provider release
qualification. Raw upstream error text, Task payloads and bearer values are
excluded. Actions carry argument arrays for use by automation or a dashboard.

## Runtime catalog management

The Node, Go, and Python starters expose in-process operations to register,
replace, and remove tools, resources, and prompts. Application code can change
catalogs after startup; active `subscriptions/listen` clients receive the
corresponding `list_changed` notification when their authorized catalog view
changes. Mutations are synchronous and process-local. There is no network
endpoint for changing catalogs. In a multi-replica app, register the same
definitions in each serving process and make any runtime mutation on every
replica.

For a serving guard against catalog drift, set the same
`MCP_CATALOG_EXPECTED_SHA256` environment value on every serving replica. Start
one instance without the pin and read `catalog_sha256` from `/healthz`, then
configure that digest across the fleet. The starter health response also
reports the platform-injected `FAAS_INSTANCE_ID`. Gregale's authenticated
per-instance health probe stamps the selected app, deployment, and instance
identity independently of the workload response, so status checks do not rely
on load-balancer sampling or trust an app-supplied instance ID. The digest covers
sorted advertised tool, resource, and prompt definitions, OAuth per-entry allow-lists, and
whether durable Tasks are enabled; it excludes handler code and the local
revision counter. `/healthz` reports `catalog_revision` and `catalog_sha256`.
With a pin configured, a mismatch returns HTTP 503 from `/healthz` and blocks
MCP endpoint requests until the process has the expected catalog. A runtime
catalog mutation therefore needs a coordinated pin update or a restart with
the new pin. Without the variable, the digest is diagnostic only.

On Gregale, use `gregale mcp deploy` to apply this pin to each candidate
deployment. Remove any existing app-wide env or secret with this key first;
those runtime layers take precedence over deployment overrides.

The pin detects missing or changed registrations across replicas when all use
the same expected digest. It does not synchronize runtime definitions or prove
that two handlers with identical advertised definitions run identical code;
deploy the same reviewed application build to every replica.

Each starter applies its existing per-entry OAuth policy to the live catalog.
When external OAuth allow-lists are configured, a new name or resource pattern
must already be in the corresponding `tool_scopes`, `resource_scopes`, or
`prompt_scopes` map to be visible and callable. Handlers remain responsible for
argument validation and application data ownership. See each starter README
for language-specific signatures and examples.

Each starter binds loopback locally and the guest interface on Gregale.
`gregale.yaml` controls start, port and health. `gregale-mcp.json` controls the
MCP endpoint, stateless mode, legacy compatibility, trusted browser origins and
client authentication. The default Node starter explicitly allows public access
to four harmless tools, a welcome resource, a customer-record URI template and a
summarize prompt. The Go and Python starters allow public access to four tools,
the welcome and customer-record resources, and the summarize prompt.
All three reject browser origins by default and accept explicitly allowlisted
origins. All three starters implement CORS preflight and expose protocol response headers.
Allowlisting an origin does not bypass bearer authorization.

`mcp deploy` stages the current worktree with zero production traffic and waits
for readiness. When the candidate exposes the catalog-health contract, the CLI
reads its catalog digest and stages a second candidate with
`MCP_CATALOG_EXPECTED_SHA256` as a deployment-scoped override. This pins only
that revision; it does not change app-wide environment values. Remove any
revision then goes through the release checks and is promoted only if the
previous serving revision still owns 100% of traffic. The checks include catalog
discovery, missing and malformed bearer rejection, Origin rejection and declared
legacy compatibility. First promotion requires no serving sibling. Failed checks
leave the candidate at zero traffic and preserve the serving revision. A
deployment preview URL is required. On a new app, the command enables streaming
and MCP client ingress; an existing serving app must already have those settings.
App-wide secret bindings, schema migrations and gateway policy updates are
separate operations. Verification does not execute tools.

This currently performs two source builds for a catalog-aware deployment: one
to discover the digest and one to verify the pinned revision. Both start with
zero production traffic; only the verified pinned candidate is promoted.

When a candidate exposes the starter catalog-health contract, the deployment
receipt records per-replica `catalog_revision` and `catalog_sha256` values under
`catalog_integrity.health`; `catalog_integrity.pin` contains the discovery
deployment ID and pinned SHA-256. Promotion probes every running candidate
replica directly; the platform validates and stamps each instance identity,
then checks the returned catalog digest. Missing, unhealthy, or inconsistent
replicas block the release. Apps without catalog health fields remain
deployable, but cannot receive this integrity pin.

Use `gregale mcp status --app my-mcp` to inspect every running replica for each
serving deployment. Besides the bounded `/healthz` integrity response, the
command sends MCP `initialize`, `server/discover`, and the advertised catalog
list requests to each exact replica. It never calls tools, reads resource
contents, renders prompts, or accesses task data. The platform stamps the
selected instance identity on each response and the command confirms the
replica set stayed stable during the check. A missing replica, changing
inventory, identity mismatch, initialization failure, catalog error, or
cross-replica catalog drift fails qualification. It also checks the public
endpoint for each selected role, so direct replica success cannot hide a
front-door authentication or routing failure.

For open MCP endpoints, status needs no workload token. For external OAuth,
provide `--token-env MCP_READER_TOKEN` or `--release-policy release.json` with
role-specific tokens and catalog baselines. The release policy checks the
caller-visible catalog through the public endpoint and checks that it matches
the catalog observed on every exact replica.
Use `--endpoint /custom/path` when the app config uses a path other than `/mcp`.
Tokens are passed in a dedicated request header and are not included in status
reports.

Run `gregale mcp status --app my-mcp --watch` to keep polling the serving fleet.
It emits an initial snapshot and then reports changes to replica health,
protocol qualification, catalog revision or digest, and serving replica
membership. Set `--interval` to change the 30-second polling interval. With
`--json`, output is NDJSON: each line is a `snapshot` or `change` event with a
UTC timestamp, the changed categories, and the same catalog status report.
Ctrl+C stops the watcher; unsuccessful checks remain visible and later checks
can report recovery.


client authentication. The Node starter explicitly allows public access to four
harmless tools, a welcome resource, a customer-record URI template and a
summarize prompt. Go and Python expose two sample tools plus the same resource
and prompt examples. An empty allowed-origins list rejects all browser origins;
add exact origins for trusted browser clients.

`mcp deploy` stages the current worktree with zero production traffic, waits for
readiness and verifies the exact deployment preview. It checks catalog discovery,
missing and malformed bearer rejection, Origin rejection and declared legacy
compatibility, then promotes only if the previous serving revision still owns
100% of traffic. First promotion requires no serving sibling. Failed checks leave
the candidate at zero traffic and preserve the serving revision. A deployment
preview URL is required. On a new app, the command enables streaming and MCP
client ingress; an existing serving app must already have those settings.
App-wide secret bindings, schema migrations and gateway policy updates are
separate operations. Verification does not execute tools.

Use `--release-policy release.json` to gate each role's reviewed catalog before
promotion. Tokens come only from environment variables. Full format-2 catalog baselines resolve within
the policy directory, preserve their modern/legacy protocol, and use strict
catalog comparison. Any breaking change or change requiring review blocks
promotion, including newly visible tools. Prepare and review fresh baselines for
intentional changes. For example:

```json
{"version":1,"roles":[
  {"name":"reader","token_env":"MCP_READER_TOKEN","baseline":"reader.lock.json"},
  {"name":"writer","token_env":"MCP_WRITER_TOKEN","baseline":"writer.lock.json"}
]}
```

```sh
gregale mcp deploy --path . --name my-mcp --token-env MCP_CLIENT_TOKEN --release-policy release.json --json
```

Doctor reports the real response's gateway streaming classification, wake tier
and client-observed request duration. Duration includes network and application
time. `--stream-tool` explicitly runs the named tool and checks spaced progress
events; use a harmless probe tool. Discovery alone cannot establish unbuffered
delivery. MCP JSON-RPC errors, truncated streams and `isError` tool results fail
the appropriate command even when HTTP returns 200. Calls are never retried.

For HTTP failures, discovery, contract capture and calls retain the upstream
status in the JSON Problem on stderr: 401 exits 2, 5xx exits 3, and other HTTP
failures exit 1. Numeric `Retry-After` is exposed as `retry_after_seconds`;
this is retry advice, not an automatic retry. Remote error bodies are excluded
from diagnostics so HTML and credentials cannot enter the error receipt.
Tools with invalid parameter-header annotations are excluded from discovery;
`tools` and `doctor` report their names and rejection reasons in `rejected_tools`.
Other valid tools remain available. A malformed response or duplicate tool name
still fails discovery.

For modern servers that return a stateless `input_required` form request,
`mcp call` can resume the same tool invocation after you explicitly enable input
handling. `--interactive` displays the server's form schema and accepts a JSON
object, `decline`, or `cancel`; it requires terminal stdin. Scripts can use
`--input-responses-file` with a JSON object keyed by form request ID:

```sh
gregale mcp call --url https://mcp.example.com/mcp --tool report_preview \
  --arguments '{"report":"sales"}' --interactive
```

```json
{
  "report_filters": {
    "action": "accept",
    "content": {"from": "2026-06-01", "to": "2026-06-30"}
  }
}
```

```sh
gregale mcp call --url https://mcp.example.com/mcp --tool report_preview \
  --arguments '{"report":"sales"}' --input-responses-file responses.json
```

The CLI sends form responses only after an explicit opt-in and bounds the
number and size of requests it handles. Calls without either input option keep
the existing single-request behavior.

## MCP Tasks

Gregale's Go client supports the `io.modelcontextprotocol/tasks` extension, and
the Node, Go, and Python starters can optionally persist and run task-enabled
tools. Tasks use the modern `2026-07-28` protocol. Each starter includes
`build_report`, a safe example that returns a task handle for opted-in clients
when durable tasks are enabled. Other calls stay synchronous. The server
returns the handle only after PostgreSQL can read the new row.

The Go and Python starters support durable enqueue, worker execution, task
status reads, cancellation, encrypted client input through `tasks/update`, the
caller-owned task resource, split web and worker roles, task queue metrics, and
task status subscriptions. Their workers can process version `1` rows created
by older starters and version `2` `build_report` rows created by current
starters.

Tasks are disabled by default. Enable them in `gregale-mcp.json` and bind a
PostgreSQL runtime binding. Use a separate migration account to create and alter
the task and owner-cursor tables, indexes, claim-order sequence, functions and
triggers with `npm run tasks:migrate -- apply` before starting the Node runtime.
Go and Python use
`go run . tasks-migrate apply` and `python task_schema.py apply`, respectively,
with `MCP_TASK_MIGRATION_DATABASE_URL` bound only to the operator process.
Run this once before upgrading existing Go/Python queues; runtime startup now
validates the installed schema without DDL. Grant service roles SELECT on
`gregale_mcp_task_starter_schema` alongside their existing Task data grants.
Keep a stable
32-byte-or-longer secret in `MCP_TASK_OWNER_KEY`:

```json
"tasks": {
  "enabled": true,
  "database_url_env": "DATABASE_URL",
  "owner_key_env": "MCP_TASK_OWNER_KEY",
  "owner_key_id": "initial",
  "payload_encryption_version": 1,
  "previous_owner_keys": [],
  "namespace_env": "FAAS_APP_ID",
  "ttl_seconds": 86400,
  "poll_interval_ms": 2000,
  "worker_concurrency": 1
}
```

For local runs, set `MCP_TASK_NAMESPACE` when `FAAS_APP_ID` is absent. The key
derives both the per-caller owner hash and AES-256-GCM payload keys. Task
payload format `1` is the rolling-deploy-safe default. Format `2` tags each
payload with its immutable `owner_key_id`; assign a new ID whenever the secret
changes and never reuse an ID for a different secret. The starter rewraps existing live
payloads in bounded background batches without changing their JSON bytes.

Rotate keys in stages: deploy the rotation-capable starter everywhere with the
current key and format `1`; distribute the new key as a `previous_owner_keys`
entry while the current key remains active; switch the active ID and secret,
retaining the old key as previous and keeping format `1`; after that rollout
completes, set `payload_encryption_version` to `2`. For example, the key
distribution stage keeps the current active key and adds the next key:

```json
"owner_key_id": "initial",
"owner_key_env": "MCP_TASK_OWNER_KEY_INITIAL",
"payload_encryption_version": 1,
"previous_owner_keys": [
  { "id": "next", "env": "MCP_TASK_OWNER_KEY_NEXT" }
]
```

The activation stage changes the active ID/env to `next` and lists `initial` as
the previous key. Keep that old key configured for at least 30 days after the
new-key rollout completes. Payloads are rewrapped, but existing `owner_hash`
values cannot be recomputed, so the previous key is needed to authorize their
callers until those tasks expire. The task table is scoped by
app namespace, stores no bearer tokens or plain caller subjects, and deletes

For local runs, set `MCP_TASK_NAMESPACE` when `FAAS_APP_ID` is absent. Keep the
owner key stable for the namespace; it derives the per-caller owner hash and
the legacy payload key. Use a separate versioned payload key ring for rotation.
The task table is scoped by app namespace, stores no bearer tokens or plain caller subjects, and deletes
expired rows. TTL is configurable from 60 seconds to 30 days. An explicit
`MCP_TASK_NAMESPACE` overrides `FAAS_APP_ID`; set the same stable value in every
app that shares the task database.

By default, each starter claims tasks in the HTTP process. All three starters
can instead run a separate Gregale worker app. For Node, set
`MCP_TASKS_ROLE=web` on the HTTP app and use
`npm run start:tasks-worker` for the worker app. For Go and Python, use the
same starter command for both apps and set `MCP_TASKS_ROLE=web` on the HTTP app
and `MCP_TASKS_ROLE=worker` on the worker app. The Go/Python worker role starts
the task loop without opening an HTTP listener. Bind the same PostgreSQL
database and the same active and previous task keys to both apps, and set the
same explicit `MCP_TASK_NAMESPACE` on both because their Gregale app IDs
differ. For example,
the Node worker app's `gregale.yaml` can declare:

```yaml
hosting:
  start: npm run start:tasks-worker
worker:
  drain_timeout: 45s
  stop_signal: SIGTERM
  scale:
    min: 1
    max: 10
    metric: custom
    name: mcp_tasks_outstanding
    target: 4
```

Go and Python worker apps can keep their generated `hosting.start`; the role
environment variable selects the worker process.

The Node worker has no HTTP listener. Go and Python worker roles also run
without an HTTP listener. Set `MCP_TASKS_SCALING_APP_SLUG` to the worker app's
Gregale slug and `MCP_TASKS_SCALING_TOKEN` to an API key restricted to
`metrics:write`; the optional `GREGALE_API_URL` selects a non-production API.
Each worker publishes the namespace's aggregate outstanding-task count,
oldest task age, and claimable tasks without a live compatible handler as
`mcp_tasks_outstanding`, `mcp_tasks_oldest_age_seconds`, and
`mcp_tasks_without_compatible_worker`. Compatibility uses the exact tool name
and handler version and a worker heartbeat fresh within 45 seconds. The count
includes queued tasks and running tasks whose lease expired, with exhausted
retries excluded. These gauges include no task or caller identifiers. The
custom target scales to `ceil(outstanding / target)`, so use
the task worker concurrency as the target. Keep `min: 1` while using the
in-process worker reporter: at zero workers no process can refresh the metric.
Custom metrics require Hobby or higher. If the pool is empty, Gregale starts its
configured minimum so a publisher can send its first reading; afterward,
missing or stale metrics preserve the current pool rather than interpreting an
unavailable database signal as an empty queue. On `SIGTERM`, the worker stops
claiming new tasks and waits for active
handlers to finish within the configured drain window. If a worker is killed
early, another worker can reclaim the task after its lease expires. Task
cancellation is cooperative. A handler may run again after a lease expires,
including after a crash following an external side effect, so handlers that
cause such effects must be idempotent by task ID. The sample `build_report`
handler is pure and safe to replay. The portable suite
tests the wire using memory storage and checks SQL parameters through a fake pool.
A PostgreSQL 16 integration job verifies DDL, encrypted payloads, caller and app
isolation, concurrent worker claims, lease recovery, cancellation, retry
exhaustion, expiry cleanup, encrypted task input requests, partial
`tasks/update` responses, and replacement-runtime resume after a worker
restart. Run it locally against a disposable database with
`MCP_TASKS_TEST_DATABASE_URL` and `npm run test:postgres`; it creates and removes
its own schema. Before enabling Tasks on a managed database, also confirm the
migration account owns the schema/objects and has DDL privileges and that
provider connection and failover behavior meet the app-local worker contract.

For Go and Python, `MCP_TASKS_ROLE=combined` is the default. `web` keeps the
HTTP server and task API but does not claim work. `worker` processes tasks and
publishes queue metrics without serving HTTP. The optional `observer` role also
has no HTTP listener; run it as a separate always-on process with a PostgreSQL
credential that has `SELECT` access to `gregale_mcp_tasks` and
`gregale_mcp_task_worker_handlers`, and no table writes,
`CREATE` on the database, or `CREATE` on the task table's schema. It does not
need `MCP_TASK_OWNER_KEY`. The observer publishes the same aggregate gauges and
`mcp_tasks_observer_heartbeat`; its API token is still restricted to
`metrics:write`. An observer can keep the metric fresh while the worker pool
scales to zero. Set `MCP_TASK_NAMESPACE` explicitly for every non-combined
role, and use the same value across the web, worker, and observer apps.

Custom Node task handlers can request client input with
`requestInput(key, request)` or `requestInputs({key: request})` in the execution
context. The Go and Python starter's `build_report` tool demonstrates the same
flow with `request_approval: true`.
The task moves to `input_required`, and `tasks/get` returns outstanding
`inputRequests`. The client answers with `tasks/update` and `inputResponses`;
the worker then replays the handler from its beginning with the saved responses
available under the same keys. Request and response payloads are encrypted
with the task key, and request keys cannot be reused for a different prompt.
The original client must have declared support for each embedded request type.
Keep handlers idempotent because worker recovery can also replay them after a
crash.

Modern clients can open `subscriptions/listen` with `notifications.taskIds` for
task snapshots and `notifications.resourceSubscriptions` for registered,
caller-readable resource URIs. Resource changes emit
`notifications/resources/updated`; task resources additionally require task
ownership and permission for the originating tool. Clients can also request
tool, resource, and prompt list-change notifications. Task ID subscriptions
require the client Tasks extension. Reopening the stream sends fresh task
snapshots, while `tasks/get` polling remains available as a fallback. Node, Go,
and Python fan task changes out over PostgreSQL `LISTEN`/`NOTIFY`. Runtime
catalog mutations publish local change hints; each active stream compares the
caller's authorized catalog before sending a list-change notification. The Go
and Python starters also compare catalogs on a bounded poll, so a missed hint
is recovered within five seconds (or the configured task poll interval when
longer). A snapshot poll also recovers missed task notifications and listener
reconnects. Active streams and watched task IDs are capped per process.

Each starter exposes a resource update publisher for application-owned changes:
Node returns `notifyResourceUpdated(uri)` from `createApp`, Go provides
`NotifyResourceUpdated(uri)`, and Python provides `notify_resource_updated(uri)`.
The default adapter is process-local. Supply a shared adapter with `publish(uri)`
and `subscribe(uri, listener)` when application resources can change on another
web replica. Task updates continue to use the shared PostgreSQL listener.

Use `mcp call --tasks` to request task support from the server. If it returns a
handle, the CLI prints it so you can inspect, resume or cancel it:

```sh
gregale mcp call --url https://mcp.example.com/mcp --tool build_report \
  --arguments '{"report":"weekly","steps":8}' --tasks
gregale mcp task-get --url https://mcp.example.com/mcp --task-id TASK_ID
gregale mcp task-wait --url https://mcp.example.com/mcp --task-id TASK_ID
gregale mcp task-cancel --url https://mcp.example.com/mcp --task-id TASK_ID
gregale mcp resource-read --url https://mcp.example.com/mcp --uri task://tasks/TASK_ID
gregale mcp resource-watch --url https://mcp.example.com/mcp --uri task://tasks/TASK_ID
```

With Tasks enabled, `task://tasks/{taskId}` exposes the caller-visible task
status and result as a resource in all three starters. Reading it requires both
ownership of the task and permission for its originating tool, plus the
resource's configured scope. `resource-watch` receives resource updates as the
task moves or completes. Application-owned resources can publish the same update
notification after their content changes; use a shared adapter to reach streams
on other web replicas.

`mcp call --wait` also opts in and returns the final tool result. It listens for
task status notifications when the server supports subscriptions, reopening a
dropped stream from a fresh snapshot. It falls back to `tasks/get` polling when
subscriptions are unavailable or keep disconnecting. It reports status changes
on stderr. If the task requests form input, use `--interactive` or
`--input-responses-file`; the CLI sends those replies through `tasks/update`.
When either wait command reaches its timeout, it makes a best-effort cooperative
`tasks/cancel` request. Tasks require the modern `2026-07-28` protocol; legacy
calls keep their existing behavior. Task IDs are opaque and must be saved by
the caller if they need to resume waiting after a CLI restart.
`task-wait` resumes waiting on a saved handle after a restart; it accepts the same
`--interactive` or `--input-responses-file` options for supported form requests.

## MCP catalog snapshots

Capture caller-visible definitions before changing a server:

```sh
gregale mcp lock --app my-mcp --out baseline.json
gregale mcp lock --url https://candidate.example.com/mcp --out candidate.json
gregale mcp diff --before baseline.json --after candidate.json --check --json
```

`lock` discovers advertised definitions without executing tools, reading resource
contents or rendering prompts. Its default destination is
`gregale-mcp.lock.json`; `--legacy` captures the stateless 2025-11-25 interface.
Snapshots contain the protocol version, catalog capabilities, advertised
extension identifiers, tool definitions, resource and resource template metadata,
and prompt arguments. Extension additions are informational by default and need
review with `--strict-catalog`; extension removals are breaking. They omit resource contents, rendered
prompt messages, endpoint URLs, timestamps and client credentials. Catalog names
and JSON object keys are sorted, and schema numbers keep their precision. Contract
format 2 records capabilities and adds resources, templates and prompts; existing
format 1 tool-only locks remain readable and compare as advertising tools.
Discovery that rejects any tool cannot produce a complete snapshot and fails
without writing a file. Existing snapshots require a new `--out` or explicit
`--force`; writes are atomic with private file permissions and reject symlinks.

`mcp watch` compares the live caller-visible catalog to a saved snapshot and
prints only drift findings. It listens for tool, resource and prompt list-change
notifications where supported, then re-discovers only the changed definition
lists. It also reconciles against a fresh snapshot at `--interval` (30 seconds
by default), so changes are still found if a notification is missed; servers
without a working subscription use polling. Pass `--tools`, `--resources` or
`--prompts` to limit subscribed and refreshed lists; with none, all three are
watched. The initial snapshot is compared in full. `--json` emits one NDJSON
drift event per change, including an event when drift clears back to the
baseline. The command lists definitions only: it does not call tools, read
resource contents or render prompts. Ctrl+C stops the watch.

## MCP resource updates

Watch one explicitly selected resource URI and receive its current contents
immediately, then again only when they change:

```sh
gregale mcp resource-watch --app my-mcp --uri 'task://tasks/TASK_ID'
```

On the modern protocol, the client subscribes to that URI and re-reads it when
the server sends `notifications/resources/updated`. It also re-reads every
`--interval` (30 seconds by default) to cover missed notifications and falls
back to polling when the server does not acknowledge the URI subscription. The
watch uses the same endpoint and client token for every read. `--json` emits
one NDJSON event for the initial snapshot and each changed result; ordinary
output prints the same event as readable JSON. Resource contents can be
sensitive, so select a URI the current caller is authorized to read and treat
the output accordingly. Ctrl+C stops the watch.

All three starters publish per-resource updates for `task://tasks/{taskId}`
when Tasks are enabled. For application-owned resources, call the publisher
after changing a resource so clients can re-read it. With the default local
adapter, publisher events reach streams on that web process; multi-replica apps
should connect a shared event adapter. Notification streams still reconcile on
`--interval`, and servers without URI subscriptions use polling.

Use the same authorization context for both captures: a caller's scopes can change
which definitions are visible. Review catalog metadata before committing it; it is supplied
by the server and may contain private information. Snapshots do not record the
identity or permissions used to capture them.

`diff` reads two local files without making network requests. It reports removed
tools/resources/templates/prompts, new required tool or prompt inputs, narrowed
input types/enums, weakened output guarantees, metadata changes and changes
needing review. Required-field, type-set
and enum ordering is ignored. Protocol and annotation changes need review;
annotations never grant execution permission. Other changed schema keywords,
including constraints, references and combinators, need review. References are
preserved without fetching them. Changed subtrees beyond 64 property/item levels
also need review; snapshots retain their full contents. The comparison does not prove arbitrary
JSON Schema compatibility or unchanged tool behavior. Property removal is treated
conservatively as breaking even where JSON Schema would still permit that key.

Without `--check`, a successful comparison exits zero and prints its findings.
With `--check`, breaking changes **or** changes needing review exit one; unchanged
contracts and informational changes exit zero. Invalid snapshots fail either mode.
Newly visible definitions are informational by default. Add `--strict-catalog`
to mark each addition as `needs_review`, so `--strict-catalog --check` rejects
catalog expansion. The receipt includes `strict_catalog: true` when enabled.
This applies even when the baseline catalog is empty; removals and loss of an
advertised capability remain breaking. Capability additions need review in strict
mode. Descriptions and other informational changes retain their existing severity.
This is an explicit local CI check; it does not switch traffic, enforce platform
promotion policy or invoke tools.

### CI checks for each caller

Capture and review one baseline per permission set. Keep reader and writer tokens
separate, and use the same issuer, audience, endpoint scopes and tool scopes for
each role's future captures. For example:

```sh
gregale mcp lock --url https://baseline.example.com/mcp --token-env MCP_READER_TOKEN --out contracts/reader.lock.json
gregale mcp lock --url https://baseline.example.com/mcp --token-env MCP_WRITER_TOKEN --out contracts/writer.lock.json
```

The reusable [catalog workflow](../.github/workflows/mcp-catalog-check.yml) builds
Gregale at a reviewed full commit SHA, checks out the caller repository's baseline
and runs only `mcp lock` and `mcp diff --check`. Strict catalog comparison defaults
to enabled. It saves the baseline, candidate and JSON receipts for seven days,
including when comparison fails. It does not execute code from the caller's
repository or invoke tools. The candidate endpoint must already be running.

Replace `REVIEWED_GREGALE_SHA` below with a full commit SHA containing this workflow
and `--strict-catalog`. Pin both the workflow and `gregale-ref` to that SHA:

```yaml
name: MCP caller catalogs
on: workflow_dispatch
permissions:
  contents: read
jobs:
  catalog:
    strategy:
      fail-fast: false
      matrix:
        include:
          - role: reader
            token_secret: MCP_READER_TOKEN
          - role: writer
            token_secret: MCP_WRITER_TOKEN
    uses: poyrazK/faas/.github/workflows/mcp-catalog-check.yml@REVIEWED_GREGALE_SHA
    with:
      gregale-ref: REVIEWED_GREGALE_SHA
      endpoint-url: https://candidate.example.com/mcp
      baseline: contracts/${{ matrix.role }}.lock.json
      receipt-name: mcp-catalog-${{ matrix.role }}
    secrets:
      endpoint-token: ${{ secrets[matrix.token_secret] }}
```

By default the baseline comes from the PR's base commit, or the caller commit for
other events. `baseline-ref` can pin another full commit SHA. Baselines must be
regular files inside that checkout. Review intentional additions before updating
a baseline; the default PR base prevents a candidate capture from replacing the
baseline used by that check. Set `legacy: true` with a separately captured 2025-11-25 baseline when that
protocol is part of the release contract. Omit `endpoint-token` for public servers.
Use explicit named secrets on trusted workflow runs; never pass production tokens
to an unreviewed Gregale pin or an untrusted candidate endpoint.

The repository's portable fixture compares the official SDK against
[reader](../testdata/mcp-catalog/reader.lock.json) and
[writer](../testdata/mcp-catalog/writer.lock.json) baselines. It verifies both
protocols, deterministic captures, unexpected reader catalog expansion, unchanged
writer visibility and writer tool removal, with zero tool calls. Catalog checks
detect changes in visibility and structure; they do not prove execution denial or
object-level authorization. Per-tool execution guards are tested separately;
object ownership remains the application's responsibility.

## External OAuth

Before adding sensitive catalog entries, change `auth` in `gregale-mcp.json`:

```json
{
  "mode": "external-oauth",
  "issuer": "https://identity.example.com",
  "jwks_url": "https://identity.example.com/.well-known/jwks.json",
  "resource": "https://my-mcp.gregale.dev/mcp",
  "scopes": ["mcp:tools"],
  "tool_scopes": {
    "greet": [],
    "add": ["math:read"]
  }
}
```

Add `resource_scopes` and `prompt_scopes` to the `auth` object to control access
to those catalogs in any starter:

```json
{

  },
  "resource_scopes": {
    "greeting://welcome": [],
    "customer://records/{recordId}": ["records:read"]
  },
  "prompt_scopes": {
    "summarize": ["reports:read"]
  }
}
```

Configure the provider for OAuth 2.1, discovery, PKCE and this canonical resource
audience. All three starters serve RFC 9728 protected-resource metadata and a
bearer challenge. Each validates signed RS256/ES256 JWT access tokens for issuer,
audience, expiry, subject and all configured scopes. The provider owns login,
consent, client registration and token issuance. Opaque tokens require an introspection
adapter. `auth.scopes` are required for every request and are advertised with
catalog scopes in protected-resource metadata. `tool_scopes` adds
application-owned permissions for tools in all three starters.
`resource_scopes` and `prompt_scopes` apply to resource URIs (including URI
templates) and prompt names in all three starters. Go and Python support simple
URI-template variables that each match one path segment. Every listed scope is
required in addition to the endpoint scopes. An explicit empty scope array permits any
authenticated endpoint caller to use that entry. A configured map denies
entries missing from it; `{}` hides and denies every entry of that catalog
type. Null maps and scope arrays fail configuration validation. Open mode
accepts only empty scope arrays. The generated public starter lists its
harmless entries explicitly.

All three starters filter `tools/list`, `resources/list`,
`resources/templates/list` and `prompts/list` using verified JWT scopes for each
request. They check `tools/call`, `resources/read`, `prompts/get` and
`completion/complete` references before dispatch. The Node starter also checks
verified request context again inside each registered callback.

catalog scopes in protected-resource metadata. In the generated starters,
`tool_scopes`, `resource_scopes` and `prompt_scopes` add application-owned
permissions for tools, resource URIs (including URI templates) and prompt names.
Every listed scope is required in addition to the endpoint scopes. An explicit
empty scope array permits any authenticated endpoint caller to use that entry. A
configured map denies entries missing from it; `{}` hides and denies every entry
of that catalog type. Null maps and scope arrays fail configuration validation.
Open mode accepts only empty scope arrays. Each generated starter lists its
harmless entries explicitly.

The Node, Go and Python starters filter `tools/list`, `resources/list`,
`resources/templates/list` and `prompts/list` using verified JWT scopes for each
request. They check `tools/call`, `resources/read` and `prompts/get` before
dispatch and check verified request context again inside each registered
callback or SDK request middleware.
Missing entry scopes return HTTP 403 with an `insufficient_scope` bearer
challenge naming the endpoint and entry scopes; an unlisted entry returns a
catalog-specific `*_access_denied` error. In the Node starter, resource policy
keys are exact resource URIs or URI-template patterns, such as
`customer://records/{recordId}`. Headers, arguments and catalog annotations
cannot grant permission. The JSON-RPC method and body parameters control
authorization; `Mcp-Method` and `Mcp-Name` headers do not.

Omitting an individual policy map preserves endpoint-only authorization for that
catalog type. This manifest describes application policy: deploying an arbitrary
server with these fields does not install a gateway enforcement layer. Use
`mcp policy` to install the explicit gateway execution gate; keep application
catalog filtering and ownership checks. Scope checks do not replace
object/tenant ownership checks inside callbacks; use the verified subject and
your own data lookup to enforce those. Compare contract snapshots under the same
identity and scopes, including separate baselines for different roles.

Pass an MCP client access token with `--token-env MCP_TOKEN` for authenticated
deploy verification, doctor or calls. Keep secrets out of shell history and use
`--arguments-file` for sensitive tool arguments. CLI account credentials are used
only against the Gregale control plane. Connection JSON contains no credentials;
it uses the common `mcpServers` HTTP shape, which may need adaptation for your client.
Provider/client interoperability needs qualification for your chosen provider;
this profile does not host an authorization server.

## Runtime and qualification

The starter uses the official SDK, supports MCP 2026-07-28 and optional stateless
2025-11-25 compatibility, and stops streaming work on disconnect. It logs validated
tool callback name, duration and outcome without arguments, results or tokens.
Use normal app logs to inspect those events. Stateful protocol sessions, old
HTTP+SSE and direct stdio hosting are outside this profile. For those workloads,
use an explicit adapter with durable session storage and its own acceptance tests.

Preserve dependency lockfiles. The source scanner recognizes valid npm integrity
digests without suppressing provider credential checks. Older deployed apid builds
may still reject those hashes; upgrade the scanner before qualifying reproducible
deployments. Do not disable secret scanning to bypass that issue.

Keep durable task records in the bound PostgreSQL database. The default combined
process may pause when its app scales to zero; a dedicated worker deployment can
scale on the task table's aggregate backlog through Gregale custom metrics.
The gateway tool policy, redacted execution metrics and tool-contract rollout
checks are available in this preview. Strict per-customer task scheduling
fairness and customer-managed task-key rotation remain follow-on capabilities.

Workers rotate claims among active task-owner partitions in an app namespace,
choosing each owner's oldest eligible task. The cursor is persisted in
PostgreSQL, so worker restarts retain the rotation order and concurrent replicas
skip a cursor that another claimant currently holds. Open-mode callers share one
owner partition; this is caller-level fairness, not account-level scheduling.
The gateway tool policy, redacted execution metrics and tool-contract rollout
checks are available in this preview. Versioned payload-key rotation is available
in the starter; the ownership secret remains fixed for its namespace.

Protocol references: [Streamable HTTP](https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/streamable-http),
[Tasks extension](https://tasks.extensions.modelcontextprotocol.io/specification/2026-07-28/tasks)
and [authorization](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization).
Design: [ADR-426](adr/426-mcp-hosting-contract.md).

## Gateway OAuth resource policy

`gregale mcp policy --path . --name my-mcp` installs or updates an MCP resource
policy on the existing JWT edge rule. This uses the normal JWT edge-rule plan
and quota gates. New rules are staged disabled and their persisted policy is
checked before activation, so an older API cannot silently discard MCP fields.
Deploy matching apid and gateway builds before using this policy. Configure `external-oauth` first, with a canonical resource and
provider JWKS. The command refuses unrelated or competing enabled JWT rules;
resolve those explicitly with `edge-rules`. This is a separate app-wide change,
not part of candidate traffic promotion. Reapply after intentional policy changes.

The gateway serves protected-resource metadata without waking the guest,
validates issuer, audience, asymmetric signatures, subject and unexpired JWTs,
and enforces endpoint/tool/resource/prompt execution scopes using exact JSON-RPC
body fields. Forged MCP hint headers grant no access. Missing credentials receive
401 with a resource-metadata challenge; missing scopes receive 403. Streams have
a token-expiry deadline. The provider retains discovery, PKCE, login, consent,
registration and issuance. Opaque tokens need a provider-specific adapter.

An omitted scope map permits endpoint-authorized access for that catalog type;
an explicit empty map denies every entry. Every overlapping resource policy
must pass. Gateway resource templates support simple `{variable}` expressions;
advanced RFC 6570 expressions remain application policy. Requests are bounded to
1 MiB. Keep the starter's catalog filters, owner checks and domain authorization:
the gateway does not filter discovery responses or look up task owners. Keep
canonical resource rules covering all paths/methods without header selectors,
and avoid adding JWT rules that shadow the MCP rule. The router applies the same canonical policy to deployment previews, named
environment aliases and custom domains. JWT policy load failures reject the
request with 503. Keep application OAuth validation as an additional gate.

## Task admission and worker releases

`tasks.max_outstanding` and `tasks.max_outstanding_per_owner` default to 1000 and
100. The store locks namespace admission and counts unexpired queued, running
and input-required tasks in the insert transaction. Completed, cancelled, failed
or expired tasks release capacity. Open-mode callers share one owner partition.
These are customer-configurable application safeguards, not Gregale plan quotas.

Workers select only registered `(tool, handler version)` pairs. Keep an old
worker running until its queue drains, or retain the old implementation:

```js
const handlers = {
  build_report: { version: '2', execute: executeV2, previousVersions: { '1': executeV1 } },
};
```

Keep old implementations behaviorally compatible with saved input requests.
Unsupported versions stay queued until a compatible worker claims them or their
TTL expires. Work remains at least once: use `taskId` to deduplicate external
side effects. Database connection and statement timeouts bound queue operations.
A metrics publication failure logs `mcp_task_metrics_publish_failed` without
credentials, identifiers or task payloads; alert on recurring failures.

For worker scale from zero, deploy `npm run start:tasks-observer` for Node or
set `MCP_TASKS_ROLE=observer` for Go/Python in a separate always-running
app/process. Set the same `MCP_TASK_NAMESPACE`, a read-only database URL, and
the worker app's scoped `MCP_TASKS_SCALING_APP_SLUG` and
`MCP_TASKS_SCALING_TOKEN`. The app slug must match the `--app` worker slug.
Enable Tasks in its config. Initialize the schema first through a web/worker
process running the matching starter version. The observer only reads aggregate
queue metrics, never migrates schema or claims tasks, and needs no
`MCP_TASK_OWNER_KEY`. Every 15 seconds it publishes queue gauges, its dedicated
`mcp_tasks_observer_heartbeat`, and a bounded report of waiting tasks and live
compatible workers grouped by tool and handler version. The report contains at most 256
pairs and no task IDs, owners, or payloads; Gregale assigns its observation
timestamp and marks reports stale after 45 seconds. The command
`gregale mcp tasks status --details` reads it through the API, so operators do not need direct task
database credentials. Worker processes publish only the queue gauges. Configure
the worker's custom scaling target and `worker.scale.min: 0`; keep the observer
at a minimum of one running process. An in-process worker publisher still requires a
worker minimum of one. At startup and before every queue read, the observer
checks that its effective database role lacks write/truncate grants on both
task tables,
CREATE on the task table's schema, and database-level CREATE for new schemas. A
failed check withholds the heartbeat and emits the redacted
`mcp_task_observer_health_check_failed` event; monitor it and metric freshness.

The CLI stores the observer app slug in the worker's scaling policy. The
observer must be a distinct, active worker app with a live deployment, at least
one configured worker replica, and at least one running instance. `setup`
checks those conditions and a fresh observer heartbeat before applying a
min=0 policy; the API also rejects a min=0 MCP task policy without a linked
observer slug.
If `gregale.yaml` owns worker scaling, keep the same link under
`worker.scale.observer_app_slug` so project apply preserves the observer.

For worker scale from zero, deploy `npm run start:tasks-observer` in a separate
always-running app/process. Set the same `MCP_TASK_NAMESPACE`, a read-only
`DATABASE_URL`, and the worker app's scoped `MCP_TASKS_SCALING_APP_SLUG` and
`MCP_TASKS_SCALING_TOKEN`. Enable Tasks in its config. Run the explicit Task
migration before starting the observer. The observer only reads aggregate queue metrics,
never migrates schema or claims tasks, and needs no `MCP_TASK_OWNER_KEY`. It
publishes the existing custom metrics every 15 seconds. Configure the worker's
custom scaling target and `worker.scale.min: 0`; keep the observer at a minimum of
one running process. An in-process worker publisher still requires a worker
minimum of one. Monitor observer health and metric freshness.

The CLI can configure and inspect the worker policy:

```sh
gregale mcp tasks setup --app mcp-worker
gregale mcp tasks status --app mcp-worker
```

Use the observer onboarding commands before linking the worker:

```sh
gregale mcp tasks observer setup --app mcp-observer --worker-app mcp-worker --config ./gregale-mcp.json
gregale mcp tasks observer setup --app mcp-observer --worker-app mcp-worker --config ./gregale-mcp.json --apply
gregale mcp tasks observer status --app mcp-observer --worker-app mcp-worker --config ./gregale-mcp.json
```

`observer setup` checks that Tasks are enabled, the observer entrypoint names
the Node observer command or the app explicitly sets
`MCP_TASKS_ROLE=observer`, both apps use the same explicit
`MCP_TASK_NAMESPACE`, both metrics publishers target the worker app, and the
database and metrics credentials are configured as existing secrets. The
worker's owner key must also be secret-backed. `--apply` only raises the
observer worker minimum to one replica; app creation, deployment, secret
provisioning, and database grants remain operator steps. The CLI reads secret
metadata only and never displays values.

`observer status` checks the running observer and the fresh heartbeat on the
worker app. Confirm separately that the database role is read-only and that
`MCP_TASKS_SCALING_TOKEN` is an API key restricted to `metrics:write`; those
permissions cannot be derived from secret metadata. The observer verifies its
effective PostgreSQL grants at runtime and withholds its heartbeat if the role
can write task data or create schemas. Once status is healthy,
link it by applying `--observer-app mcp-observer --min 0` on
`gregale mcp tasks setup --app mcp-worker`.

After the observer status is healthy, apply the scale-to-zero policy and verify
it:

```sh
gregale mcp tasks setup --app mcp-worker --observer-app mcp-observer --min 0 --apply
gregale mcp tasks status --app mcp-worker --require-fresh
```

`mcp tasks setup` previews changes by default; pass `--apply` to save the policy. It keeps
the current replica bounds and scaling target when present, otherwise it uses a
minimum of one, maximum of ten and target of four outstanding tasks per worker.
Choose `--min 0` only after the separate observer is deployed, configured with
at least one worker replica, running, and publishing a fresh heartbeat. The
worker retains the observer slug as an explicit link, so status and
qualification verify the same app and its heartbeat.
`status` reads the worker's `mcp_tasks_outstanding`,
`mcp_tasks_oldest_age_seconds`, and
`mcp_tasks_without_compatible_worker` custom metrics, including
server-reported freshness. The last gauge shows how many claimable tasks have
no live worker registered for their exact tool and handler version. For a
configured min=0 worker, status also reports the dedicated
observer app readiness and dedicated heartbeat. `--require-fresh` fails if the
observer app is missing, aliases the worker, is not live, has no running
replica, or stops publishing its heartbeat.
The scheduler holds the worker fleet at its current size when the linked
observer app or heartbeat is unavailable, so a zero backlog gauge from a
worker cannot scale the entire pool down without the separate observer.

For a monitor or scheduled check, set an age limit and ask it to fail only if
the violation persists:

```sh
gregale mcp tasks status --app mcp-worker --max-oldest-age 5m \
  --require-fresh --sustain-for 2m --poll-interval 15s --json
```

`--max-oldest-age` also treats a missing or stale oldest-age metric as a
violation. `--require-fresh` requires all three task gauges to be present and
fresh; for a min=0 worker, it also requires a fresh observer heartbeat with
value 1. Use `--max-without-compatible-worker 0` to fail when any claimable
task has no live compatible worker, or choose a positive limit during a
controlled handler-version rollout. Worker capability heartbeats refresh
every ten seconds and are removed on graceful shutdown; unclean exits are
treated as offline after 45 seconds.
With `min: 0`, a queued task temporarily has no live worker while Gregale
starts one; use `--sustain-for` with a threshold to allow for that cold start.

For a tool/version breakdown, ask the control plane for the observer's latest
aggregate report:

```sh
gregale mcp tasks status --app mcp-worker --details
```

The observer publishes at most 256 tool/version pairs with unexpired,
nonterminal tasks every 15 seconds. The count includes queued, running, and
input-required tasks because a worker may need to resume any of them after a
deployment. Gregale stores only their task and live-compatible-worker counts,
including per-deployment worker counts, with a server timestamp; it receives no
task IDs, owners, or payloads.
The observer uses the existing scoped metrics token and its read-only task
database role. The CLI reports whether the latest snapshot is unpublished,
fresh, or stale (freshness window: 45 seconds), and marks when the 256-pair
limit omits additional entries. JSON output nests these fields in
`handler_compatibility`.

Starter workers include Gregale's deployment ID in their capability heartbeat.
At stable deployment cutover and terminal canary promotion, Gregale keeps the
old deployment live while any reported task version lacks a worker on the
candidate. Stale, truncated, or deployment-unattributed reports also hold the
cutover. Upgrade at least one worker to a starter version that records
`deployment_id`; an older read-only observer reports a truncated snapshot until
a worker migrates the table. If a report has no worker attributed to the
candidate deployment for an outstanding handler version, the cutover is held.
Use `gregale mcp tasks status --details` to see which deployment has each
compatible handler. If you accept losing the old handler version before its
tasks drain, `gregale deploy --mcp-task-migration-override` grants this
deployment a one-use cutover override. The same flag is available on
`gregale mcp deploy`.
Without a published report, Gregale cannot know whether task versions are
outstanding and allows cutover to proceed. Start the observer and wait for its
first fresh report before relying on this protection.

To list only pairs that currently lack a compatible worker, run this SQL with
the same read-only credential and namespace:

```sql
SELECT queued.tool_name, queued.handler_version, COUNT(*) AS waiting_tasks
  FROM gregale_mcp_tasks AS queued
 WHERE queued.namespace = $1
   AND queued.expires_at > clock_timestamp()
   AND (queued.status = 'queued'
        OR (queued.status = 'running' AND queued.lease_expires_at <= clock_timestamp()))
   AND (queued.attempt_count < 3
        OR (queued.status = 'queued' AND queued.resume_pending AND queued.attempt_count = 3))
   AND NOT EXISTS (
     SELECT 1
       FROM gregale_mcp_task_worker_handlers AS handler
      WHERE handler.namespace = queued.namespace
        AND handler.tool_name = queued.tool_name
        AND handler.handler_version = queued.handler_version
        AND handler.heartbeat_at > clock_timestamp() - interval '45 seconds'
   )
 GROUP BY queued.tool_name, queued.handler_version
 ORDER BY waiting_tasks DESC, queued.tool_name, queued.handler_version;
```

The query returns handler identities and counts only; it does not read task
arguments, results, owners, or task IDs.
The command returns exit code 1 for a current
violation; with `--sustain-for`, it polls while a violation is active and only
fails if it remains through that interval. A recovered queue returns healthy.
Use the exit code or JSON `health`/`violations` fields to notify through your
existing monitor.

Gregale's native alert evaluator can also notify on the oldest task age. The
rule reads the latest `mcp_tasks_oldest_age_seconds` gauge in seconds. Missing
or older-than-five-minute data marks the rule degraded instead of treating a
dead observer as a healthy zero or alerting on a frozen value. The required
`--window-spec` is retained for the common alert-rule shape but does not
aggregate this gauge. Create a webhook-only rule with a threshold of 300
seconds (five minutes):

```sh
printf '%s\n' "$WEBHOOK_SECRET" | gregale alerts add \
  --app mcp-worker --name mcp-oldest-task-age \
  --metric mcp_tasks_oldest_age_seconds --comparison gt --threshold 300 \
  --window-spec 5m --webhook-url https://hooks.example.com/gregale \
  --webhook-secret-stdin
```

Alert evaluation runs through Gregale's existing alert pipeline and cooldown.
This metric supports webhook notification only; deployment actions are rejected.

To notify when a required publisher signal goes missing, create a second rule
on `mcp_tasks_metrics_stale`. It reports `1` when
`mcp_tasks_outstanding`, `mcp_tasks_oldest_age_seconds`, or
`mcp_tasks_without_compatible_worker` is missing, older than the five-minute
custom-metric freshness window, or invalid. For a min=0
worker, a missing, stale, or non-1 `mcp_tasks_observer_heartbeat` also reports
`1`. It reports `0` when every required signal is fresh. Use `gt 0`. Unlike an
age rule that becomes degraded when its source disappears, this binary signal
can send a webhook for the missing/stale publisher itself:

```sh
printf '%s\n' "$WEBHOOK_SECRET" | gregale alerts add \
  --app mcp-worker --name mcp-task-metrics-stale \
  --metric mcp_tasks_metrics_stale --comparison gt --threshold 0 \
  --window-spec 5m --webhook-url https://hooks.example.com/gregale \
  --webhook-secret-stdin
```

This alert is webhook-only. A backend read failure still leaves the rule
degraded because Gregale cannot distinguish it from an application signal.

Use `scripts/ops/mcp-qualification.py` to create a pending ten-row evidence
workspace, run the automated native-wake, OAuth and official SDK interop probes,
record hashes for the remaining redacted receipts, and run the same fail-closed

gregale mcp tasks setup --app mcp-worker --min 0 --apply
gregale mcp tasks status --app mcp-worker
```

`setup` previews changes by default; pass `--apply` to save the policy. It keeps
the current replica bounds and scaling target when present, otherwise it uses a
minimum of one, maximum of ten and target of four outstanding tasks per worker.
Choose `--min 0` only after the separate observer is deployed and publishing.
`status` reads the worker's `mcp_tasks_outstanding` and
`mcp_tasks_oldest_age_seconds` custom metrics, including server-reported
freshness. A fresh backlog metric does not prove that an always-on observer is
deployed, so verify the observer app's health separately.

Use `scripts/ops/mcp-qualification.py` to create a pending eight-row evidence
workspace, record hashes for redacted receipts, and run the same fail-closed
checker used by release CI. For example:

```sh
python3 scripts/ops/mcp-qualification.py init --dir ./mcp-evidence --commit <reviewed-sha>
python3 scripts/ops/mcp-qualification.py sdk-interop --dir ./mcp-evidence \
  --target '<reviewed commit / Node, Go and Python starters / official SDK 2.3.1>' \
  --node-url http://127.0.0.1:18808/mcp \
  --go-url http://127.0.0.1:18809/mcp \
  --python-url http://127.0.0.1:18810/mcp
python3 scripts/ops/mcp-qualification.py status --dir ./mcp-evidence
```

Repeat `record` for every manual row. The runner automates the provider's
authorization-code + PKCE flow when given a registered staging client; actual
ChatGPT/Codex client logins remain live observations. It can also execute the
two native wake probes against a disposable staging app:

```sh
python3 scripts/ops/mcp-qualification.py probe --dir ./mcp-evidence \
  --name native_cold_boot --app mcp-staging --confirm-staging-app mcp-staging \
  --target 'KVM host/build / mcp-staging' --stream-tool stream_demo
```

The probe parks the app, checks the wake tier and harmless live streaming
progress through `gregale mcp doctor`, then parks it again and verifies no
active instances remain. It requires an exact staging-app confirmation because
parking interrupts the app. Worker restart and scale-from-zero remain operator
observations. The release gate stays incomplete
until all ten rows pass and their artifact hashes match. Keep tokens, subjects
and customer payloads out of the evidence directory.

python3 scripts/ops/mcp-qualification.py record --dir ./mcp-evidence \
  --name official_sdk_interop --target 'official SDK 2.x / test app' \
  --artifact official-sdk.json --status passed
python3 scripts/ops/mcp-qualification.py status --dir ./mcp-evidence
```

Repeat `record` for every required row. Create and redact each receipt from the
real native host, provider/client or SDK observation; the runner does not
perform those live checks. The release gate stays incomplete until all eight
rows pass and their artifact hashes match. Keep tokens, subjects and customer
payloads out of the evidence directory.

See [release qualification](ops/mcp-release-qualification.md) for native and
real-provider/client checks required before promoting the preview to GA.

When automatic activation is blocked by task compatibility, deployment status
includes `promotion_hold` with its code, actionable detail, and latest observation
time. Use `gregale deployment <id>` to inspect the reason. The candidate stays
eligible for durable recovery retries; a hold does not mark it failed. The hold is
omitted after successful activation or a terminal transition. CLI deployment
polling stops with exit code 3 when it observes a hold, rather than reporting success.

Compatibility holds now retain their original snapshot/readiness evidence in a
durable recovery queue. Imaged polls due work at startup and every two seconds,
with node ownership, two-minute leases, and generation-fenced completion. A
changed fresh observer report makes a retry eligible within five seconds; an
unchanged heartbeat preserves backoff. Unavailable, stale, truncated, or
incompatible reports defer activation with exponential backoff from five seconds
to five minutes. A report arriving during an attempt also schedules a follow-up.

Recovery repeats the original activation checks, including current readiness,
public smoke, contract checks, and the transactional task compatibility gate.
Cancelled, failed, superseded, or already live candidates are not reactivated;
a newer same-scope revision supersedes a held candidate at cutover. Intentional
holds are excluded from stale pipeline cancellation. Operators can cancel a held
candidate explicitly.

The metrics `imaged_mcp_task_promotion_recovery_attempts_total` (outcomes
`recovered`, `held`, `retry`, `inactive`) and
`imaged_mcp_task_promotion_hold_duration_seconds` record recovery attempts and
hold duration when recovery activates a candidate. These metrics do not label
individual apps or deployments.

Deployment `promotion_hold` status includes `held_at`, `recovery_attempts`,
`next_retry_at`, `recovery_in_progress`, `last_recovery_outcome`,
`last_recovery_at`, and a bounded `last_recovery_error_code`. Next retry time is
an eligibility time; while an attempt runs it reflects lease expiry. Infrastructure
error details and original activation payloads are not exposed in this status.

Use `gregale deployment retry-hold <id|vN> [--app SLUG]` or
`POST /v1/deployments/{id}/mcp-task-hold/retry` to schedule another attempt.
The endpoint requires deployment write scope and the same MFA policy as deploys.
It returns 202 for scheduled or already due work, and 409 when no active queued
hold exists. A running attempt retains its lease and schedules a follow-up.
Repeated requests for already due work or an already queued follow-up do not
create extra attempts or audit events. All compatibility and activation checks
remain enforced.

The deployment audit timeline records `deploy.mcp_task_held`,
`deploy.mcp_task_retry_requested`, `deploy.mcp_task_retry_started`,
`deploy.mcp_task_retry_completed`, `deploy.mcp_task_recovered`, and
`deploy.mcp_task_hold_cancelled`. Queue changes and audit entries commit together;
expired leases also reconcile completion after an activation-time crash.

Configure a webhook alert using metric `mcp_task_promotion_hold_age_seconds`,
comparison `gt`, and a threshold such as `900` to notify when the oldest active
hold exceeds 15 minutes. This metric reads current deployment state directly,
returns zero with no holds, and respects account/app ownership. Its measurement
is current hold age, independent of the rule's window; normal webhook cooldown
settings apply. Hold alerts support webhook action only.

### Obsolete promotion holds

The recovery loop also checks held candidates independently of observer reports
and retry backoff. When a newer, nondeleted revision is live and serving traffic
in the same app and scope, the older candidate becomes superseded. Its queued
recovery is cleared and existing claims are fenced out. The deployment change
notification uses normal scheduler reconciliation to release candidate workers;
snapshots and layers follow the existing superseded deployment retention window.

The audit timeline records `deploy.mcp_task_retry_completed` with outcome
`inactive`, reason `newer_serving_revision`, and `superseded_by` pointing to the
serving deployment. Newer pending, failed, cancelled, or zero-traffic deployments
do not trigger this cleanup. Reviewed environment candidates, active environment
promotion candidates, and active checked rollback targets remain owned by their
coordinators.

### Graceful worker and session draining

The Node, Go, and Python starters enter drain mode on SIGTERM or SIGINT. They
stop claiming tasks, withdraw their worker capabilities, and reject new HTTP
work with 503 while accepted requests finish. Web connections have a bounded
shutdown window; task subscription streams reconnect through the existing
client recovery path. Use SIGTERM as the worker's stop signal.

`MCP_DRAIN_TIMEOUT_SECONDS` sets the application drain deadline (default 10,
integer range 1–300). Choose a value below the worker's `drain_timeout` in
`gregale.yaml`, leaving at least 10 seconds for database cleanup and process
exit, and keep that outer timeout within your plan's allowed grace period.
For example, use `MCP_DRAIN_TIMEOUT_SECONDS=30` with `worker.drain_timeout: 45s`
on a plan that permits that stop grace.

Active task heartbeats continue during the grace window. At its deadline the
runtime requests handler cancellation, clears each unfinished task's current
lease token, and requeues it atomically unless cancellation was requested (then
it becomes cancelled). Input-required tasks remain parked awaiting input.
Returning work preserves its attempt budget and handler version. Late results,
input writes, and heartbeats carry the old lease token and cannot modify work
owned by a replacement worker. If PostgreSQL is unavailable or the process is
forcibly killed before cleanup, ordinary lease expiry remains the recovery path.

Handlers should observe their Go context, Node AbortSignal, or Python
cancellation event. External side effects still need idempotency using the task
ID: lease fencing protects queue state, but cannot undo an external operation
that completed before interruption. Existing deployed servers must adopt the
updated runtime code to get this behavior.

Deployment detail includes optional `worker_drain` counts for active, completed,
timed-out, failed, and overdue instance shutdowns, plus the earliest active
shutdown deadline. These count worker **instances**, not application tasks.
A completed shutdown can include tasks returned to the queue. An overdue span
means the scheduler has not recorded an outcome; it does not assert that the
worker exited. `gregale deployment <id>` displays the same summary.
The audit timeline records `deploy.worker_drain_started` and
`deploy.worker_drain_completed`; a forced kill records outcome `timed_out` and
lifecycle reason `drain_timeout`. Runtime logs provide unfinished task counts.


### Running Task capacity

`tasks.max_running` (default 16) limits live execution leases across all worker
replicas in a namespace. `tasks.max_running_per_owner` (default 4) limits each
owner within that namespace and must not exceed the namespace limit. These are
separate from `worker_concurrency`, which limits each worker process, and from
outstanding Task admission limits. PostgreSQL serializes capacity checks and
claims, while owner rotation skips owners whose execution capacity is full.

Use the same limits on every worker and metrics observer in a namespace, and
upgrade every worker before relying on enforcement. Completion, failure, and
input pauses release execution capacity. Running cancellation releases capacity
when acknowledged or when its lease expires. Expired Task TTLs and leases stop
counting automatically; expired leases cannot be renewed. Lease expiration does
not guarantee that an old handler has physically stopped, so handlers must still
make external side effects idempotent.

The `mcp_tasks_running` gauge counts unexpired live leases.
`mcp_tasks_capacity_waiting` counts unexpired queued Tasks and expired-lease
running Tasks blocked by namespace or owner capacity, regardless of handler
availability or retry eligibility. A capacity limit can explain backlog even
when more replicas would not help. Both gauges use the configured observer limits
and contain no customer identifiers.


### Task retry policy and failure metrics

Handler errors remain terminal by default. Opt into retries only for failures
that are safe to repeat:

```js
import { RetryableMcpTaskError } from './tasks.js';
// Inside a Task handler, after classifying a transient provider failure:
throw new RetryableMcpTaskError('Provider temporarily unavailable');
```

Configure `tasks.max_attempts` (default 3, range 1–10, including the initial
execution), `tasks.retry_base_delay_ms` (default 1000), and
`tasks.retry_max_delay_ms` (default 60000). Delay bounds must be integer
milliseconds between 100 and 86400000, and the maximum must be at least the
base. Each retry waits a random delay between half and all of
`min(maximum, base * 2^(attempt - 1))`. PostgreSQL persists the due time; retries
survive restarts, retain the same Task ID and encrypted arguments, and release
running capacity during the wait. Delayed retries still count against outstanding
admission limits. Cancellation wins over retry scheduling. Exhausted attempts or
a delay that would reach the Task TTL produce a terminal failure. Retries never
extend TTL. Input pauses do not consume another execution attempt.

Keep retry settings consistent across every worker in a namespace. Upgrade all
workers before enabling retryable handler errors, since older workers do not
respect delayed retry scheduling. Handlers still need idempotent external side
effects keyed by Task ID; retry classification does not provide exactly-once
execution. Worker crashes continue to recover via lease expiration. Stored and
client-visible execution errors use a generic message, without handler exception
messages or provider details.

`mcp_tasks_retry_waiting` counts queued Tasks whose persisted retry time is still
in the future. `mcp_tasks_failed` counts terminal failed Tasks retained within
TTL; it is a current gauge, not a cumulative failure counter or rate. Expired
Tasks are excluded. Delayed retries are excluded from
`mcp_tasks_capacity_waiting`, but remain in `mcp_tasks_outstanding`. Interpret
these together when diagnosing backlog and autoscaling.


### Task operational diagnostics

`gregale mcp tasks status --app <worker-app>` shows running Tasks, capacity waits,
delayed retries, retained failures, active worker registrations, unsupported
handler versions, and observer heartbeat freshness. JSON output adds stable
`diagnostics` entries with `code` and `message`, plus `worker_heartbeats_fresh`
and `observer_heartbeat_fresh`. Missing or stale metrics produce an unknown-health
explanation instead of being interpreted as zero work. Queue explanations include
`execution_capacity`, `retry_delay`, `failed_tasks_retained`,
`unsupported_handler`, `no_active_workers`, and `idle_queue`.

Workers register their supported tool/handler-version pairs in
`gregale_mcp_task_workers`. Heartbeats refresh at a bounded cadence (20–30 seconds,
depending on polling), expire after 90 seconds using database time, and are
withdrawn when a worker stops accepting work. Worker registrations describe
availability to claim work; they are not a guarantee that a handler succeeds.
Expired registrations do not participate in inventory and are pruned by workers.
The observer reads this table without writing registrations or migrating schema.
Run `npm run tasks:migrate -- apply` with migration credentials before
starting an upgraded observer. Include read access to the worker registry in any
observer database grants, and upgrade every worker so the inventory is complete.

`mcp_tasks_active_workers` counts unexpired registrations.
`mcp_tasks_unsupported_handler_tasks` counts unexpired queued Tasks and
expired-lease running Tasks with no matching version among registered workers.
Future retries and live executions are excluded; retry-attempt eligibility is
separate from handler compatibility. When there are no registered
workers the unsupported count is zero and compatibility is unknown; status reports
worker absence rather than claiming that all handlers are compatible.

Only the observer role publishes `mcp_tasks_observer_heartbeat` (value 1), after
its database read and aggregate metric publication succeed. Worker publishers do
not overwrite it. The CLI uses server-reported metric freshness; a fresh observer
heartbeat indicates a recent successful reporting cycle, not proof of continuous
uptime. Worker freshness means that the latest fresh aggregate reported a live
registration; allow for registry TTL and metric publication delays. Scale-to-zero
status reports an unknown observer when that heartbeat is absent or stale.
Telemetry contains aggregate counts and declared handler inventory, without Task
arguments, results, bearer tokens, caller identities, or exception messages.


### Task payload encryption key rotation

Keep `MCP_TASK_OWNER_KEY` stable for the namespace. It identifies callers and
supports legacy payloads; rotating payload encryption does not change ownership.
The store persists an irreversible ownership fingerprint and immutable key-ID
fingerprints, never the secrets themselves. Use a new namespace when intentionally
changing caller identity. Existing legacy payloads are authenticated before first
binding the ownership fingerprint.

Set `tasks.encryption_keys_env` to a distinct secret environment variable, such
as `MCP_TASK_PAYLOAD_KEYS`. Its value is JSON:

```json
{"activeKeyId":"2026_10","keys":{"2026_09":"<old secret>","2026_10":"<new secret>"}}
```

Each secret must contain at least 32 UTF-8 bytes; generate independent random
secrets rather than using the example placeholders. IDs contain 1–64 ASCII
letters, digits, underscores or hyphens. The ring supports at most 16 explicit
keys. `legacy` is reserved for the original owner-derived payload key and is
always available. Omit `encryption_keys_env` to retain legacy writes, or use
`activeKeyId: "legacy"` with a populated ring while preparing a rollout.

New encrypted fields carry an authenticated key ID. Reads select the matching
key; completing or resuming an older Task can use the new active key without
rewriting its arguments. Startup checks key usage across arguments, input state,
results, and errors for every unexpired Task, including terminal Tasks, and
rejects missing keys. Reusing a key ID with a different secret is rejected even
when old Tasks have expired. Payloads remain bound to namespace, Task ID and field.

Rotate in phases: run the explicit migration with the complete union of keys
to register their fingerprints, then upgrade all web and worker processes with the complete union
of old and new keys first; then activate the new key. Retain old keys until every
Task field using them has expired. Stop or reconfigure every writer still using
the old active key before removing it from configuration. Startup validation is
a snapshot and cannot prevent another already-running process with an obsolete
configuration from writing afterward; all participating processes must follow
the rollout order. This feature does not re-encrypt historical payloads or rotate
the ownership secret. Observers remain read-only and require no encryption secrets.

### Hosting readiness gate

The protocol doctor remains available as `gregale mcp doctor --url <endpoint>`.
For durable Task hosting, run:

```sh
gregale --json mcp doctor --hosting --app <worker-app> --preflight-path <generated-starter>
```

The hosting doctor uses server-reported metric freshness, verifies the scaling
policy and requires an observer heartbeat for scale-to-zero. Missing or stale
signals are `unknown`, and unknown or failed checks return exit code 1. Capacity
waiting, retry delays and retained failures are operational diagnostics rather
than automatic deployment failures.

`--preflight-path` executes `node task-doctor.js` in the specified starter with
the current environment. Install the starter dependencies first and provide its
actual deployment bindings, including `MCP_TASK_NAMESPACE`. The standalone
`npm run doctor:tasks` prints a JSON report and returns the same exit-code
convention. It checks an already initialized database without creating schema,
claiming Tasks, or exposing payloads or secrets. Run `npm run tasks:migrate -- apply` before running this gate. It verifies runtime DML and
sequence privileges, not privileges to perform future schema migrations.

Without local preflight, database/key readiness stays unknown and the command
fails the gate. The CLI cannot confirm that local bindings match the remote
app: supply the deployment environment for the selected worker. Run preflight
on every writer during rotation; one successful process cannot prove that
other replicas use the same keys or have stopped writing with retired keys.
Handler coverage describes currently eligible queued work, not every future
handler invocation. Zero live workers leaves compatibility unknown, including
an intentionally idle scale-to-zero deployment. A recent observer heartbeat
is evidence of a successful publication, not a guarantee of future uptime.

### Draining workers during rollout

On shutdown, Task workers stop claiming new work and mark their live registration
as draining. `mcp_tasks_active_workers` counts workers available to claim;
`mcp_tasks_draining_workers` counts workers finishing active work. Draining
registrations do not provide handler coverage for pending Tasks.

Active handlers continue lease renewal until they finish or the configured
`tasks.shutdown_timeout_ms` deadline expires (default 30000; range 1000–300000).
At the deadline the runtime aborts handler signals and stops lease renewal;
unfinished Tasks remain running until lease expiry makes them recoverable.
Late handler results and errors are not persisted after abandonment. Concurrent
stop calls share the same shutdown operation. Dedicated worker processes exit
when shutdown finishes, including when a handler ignores its abort signal.

Configure the platform termination grace period above the Task deadline, allowing
additional time for database and metrics cleanup. A process kill can interrupt
any drain. In-flight database operations can finish after the deadline; no lease
is cleared early. Handlers must respect abort signals and use Task IDs for
idempotent external effects because recovery remains at least once.

Run the explicit migration before upgrading workers or read-only observers: the
worker registry now includes a `draining` column. Older workers do not report
draining state, so complete the rollout across workers before relying on this
metric. Run replacement workers before draining the last compatible worker.

### Retiring Task handler versions

Run the candidate worker's `npm run check:task-compatibility` before replacing
workers or removing `previousVersions`. Provide the Task PostgreSQL binding and
`MCP_TASK_NAMESPACE`; this read-only command needs no owner or encryption secret.
It uses the candidate `mcpTaskHandlers` registry, including retained previous
implementations, rather than the currently deployed workers' heartbeat inventory.
Exit code 1 blocks the gate when coverage is missing or cannot be checked.

The JSON `gaps` array reports tool/version, affected Task count, queued/running/
input-required counts, and latest expiry. The query covers every unexpired
nonterminal Task: delayed retries, paused client input, and both live and expired
leases. Terminal and expired Tasks do not need a handler and are excluded. Task
IDs, caller identities, arguments and results are not reported.

The full Task doctor also runs this check; `gregale mcp doctor --hosting
--app <worker-app> --preflight-path <candidate-starter>` includes the compatibility
report in its JSON output and fails when retained work lacks candidate coverage.
Keep the missing versions in `previousVersions` until the affected Tasks finish
or expire, then rerun the gate.

This is a database snapshot, not admission fencing. Stop or upgrade producers
that can enqueue retired versions before taking the final snapshot and replacing
the last compatible worker. Run the check against the actual deployment namespace
and candidate code. A passing snapshot cannot prevent an older producer from
creating incompatible work afterward. Deliberately partitioned workers must use
an aggregate candidate registry covering the replacement fleet when gating a
fleet-wide retirement.

### Enforcing handler admission and retirement

The explicit Task migration installs a database admission trigger on Task
inserts and registers each current handler version. New current versions start
allowed; existing disabled or retired entries are preserved. During the first
namespace upgrade, versions used by retained Tasks and live worker registrations are seeded
so older producers can keep using known versions. Versions without a registry
entry are rejected in activated namespaces. Each namespace activates atomically
with its first explicit migration; other namespaces sharing the database keep their
previous admission behavior until upgraded. Check `status.enforced` before
relying on the gate. Initialize the namespace before retiring any version;
older producers are then fenced by the trigger even without application changes.

Run these commands in the starter using the queue database and namespace bindings:

```sh
npm run tasks:admission -- status
npm run tasks:admission -- disable build_report 1
# Keep compatible workers until retained work completes or expires.
npm run tasks:admission -- status
npm run tasks:admission -- retire build_report 1
npm run tasks:admission -- audit
```

`disable` commits before returning, waiting for transactions that already passed
admission. Later Task inserts, including inserts waiting behind disablement,
are rejected. The check and insert share the same database transaction and hold
a registry row lock, so stale replicas cannot slip work past a committed disable.
Existing work, retries and input resumes remain executable. `status` reports
allowed/draining/retired state, retained Task count, latest expiry and `canRetire`.
`retire` requires disabled admission and zero unexpired nonterminal Tasks under
the registry lock. Keep the registry tombstone after removing handler code.
Restarting the runtime cannot reopen it. `allow <tool> <version>` explicitly
re-enables admission for rollback; use it only while compatible handlers exist.

Every registration, disablement, retirement and re-enable is recorded atomically
in `gregale_mcp_task_admission_audit`, with timestamp and database role. `audit`
returns the latest 100 namespace events without Task IDs, caller identities or payloads.
Repeated no-op operations do not add state-change events. The operator command
prints JSON and exits nonzero on failure; it does not initialize schema itself.
The hosting doctor checks current-version admission and the enabled trigger and
includes registry status in its JSON report.

Runtime Task insertion uses a schema-pinned admission trigger running with the
migration owner's permissions. Runtime accounts need policy SELECT, and cannot
edit the policy or key registry. Operator accounts need policy SELECT/INSERT/UPDATE
and audit SELECT; the audit trigger writes events with its owner's permissions
while preserving the originating database role. The migration account owns the
schema and objects. Keep the
trigger enabled, registry tombstones intact, and registry administration limited
to trusted operators. These are application coordination guarantees: privileged
SQL that disables triggers, deletes policy rows, or rewrites Task version fields
can bypass them. Audit retention is operator-managed; startup never deletes
policy tombstones, namespace activation records or audit history. Initialize the namespace with its existing current producer versions before
introducing version changes in a split web/worker fleet. Explicitly allow idle
producer versions absent from retained work or live worker inventory before
routing traffic to them. The migration command registers the candidate worker
current versions; a still-older idle producer with no known registry
entry may be rejected until its version is explicitly allowed.

For activated namespaces, disable admission before taking the final compatibility
snapshot. The enforced fence lets stale producers continue running while that
version drains; attempts to enqueue it are rejected. Namespace activation is
encoded in trigger definitions, so a transaction with an older snapshot cannot
skip the check. Admission status reports `enforced=false` if the namespace
trigger is absent or disabled, and policy changes fail until enforcement is
available.

### Explicit Task schema migrations and database roles

Run migrations before starting updated web, worker or observer processes:

```sh
# Set MCP_TASK_NAMESPACE, the stable owner secret and the candidate key ring.
# MCP_TASK_MIGRATION_DATABASE_URL uses the schema owner's account.
npm run tasks:migrate -- apply
npm run tasks:migrate -- status
```

`apply` requires the separate migration binding, acquires a database advisory
lock, and applies the versioned baseline migration transactionally. It upgrades
previous starter-owned schema, prepares the namespace and registers current
handler versions and immutable encryption-key fingerprints. Repeated runs skip
already applied global schema changes and preserve disabled/retired versions.
A future schema version is rejected; downgrade is unsupported. Namespace
activation and legacy-version seeding occur under a Task-table lock so older
writers cannot slip an unregistered version between the seed and enforcement.

Startup schema checks use a read-only transaction and shared migration lock to
check schema version,
namespace enforcement, required privileges, registered handler versions and
payload keys. It never creates or alters database objects, registers handlers,
or writes key fingerprints. Run migration again when adding a handler version,
a namespace or a new payload key before deploying code that requires it.
Workers can start with disabled current versions to finish retained work;
admission remains disabled. The observer checks schema and SELECT privileges
without needing payload secrets. The hosting doctor reports schema version and
runtime privileges; standalone `npm run doctor:tasks -- --role observer` or
`--role operator` checks those role profiles without decrypting payloads.

Use a dedicated schema owned by the migration account and configure the same
`search_path` in every database binding. Migration and grant commands target
`current_schema()`. Runtime and operator
accounts must not own that schema or its objects or have CREATE privileges there.
The schema must not grant CREATE to PUBLIC. Admission and audit trigger functions
run with the migration owner's permissions and pin their search path to this
trusted schema. This permits Task insertion without policy UPDATE privileges,
and operator policy changes without direct audit INSERT privileges.

Create database accounts through your provider, then inspect or apply grants:

```sh
npm run tasks:roles -- plan runtime gregale_runtime
npm run tasks:roles -- grant runtime gregale_runtime
npm run tasks:roles -- grant observer gregale_observer
npm run tasks:roles -- grant operator gregale_operator
```

`grant` uses migration credentials and applies the plan transactionally. It adds
schema USAGE and profile-specific table, function and sequence grants; it does
not create accounts, manage passwords, change ownership or revoke inherited
privileges. Role profiles apply across the selected schema, so use separate
schemas/accounts for separate trust boundaries. Provision accounts with no
broader existing grants and use their bindings for the corresponding processes.

| Account | Responsibilities |
| --- | --- |
| Migration | Own schema/objects; apply DDL, prepare namespaces, register handlers and keys, grant profiles |
| Runtime | Read configuration metadata; create/execute/update/prune Tasks; manage fairness and worker heartbeats |
| Observer | Read schema metadata, queue and worker inventory |
| Operator | Read retained work and audit history; allow, disable and retire handler versions |

Existing deployments must run `tasks:migrate apply` successfully before starting
this runtime version. For an older installation, the migration account must own
its existing tables, sequences and functions. Keep elevated migration credentials
out of runtime, observer and operator environments.

The initial migration can take table locks and validate retained rows. Schedule
it before serving traffic or within a controlled release window. Upgrade all
replicas that previously performed startup DDL before removing their elevated
credentials; older binaries must not restart with schema-owner credentials and
rewrite the migrated trigger definitions.

#### Automated Task release gate

Generated Node starters provide `npm run tasks:release -- release-plan.json`.
A plan supplies executable argument arrays `start` and `drain`, and an optional
`timeoutMs` (1–600 seconds per hook or wait). The candidate runtime binding and
`MCP_TASK_MIGRATION_DATABASE_URL` must target the same namespace/database/schema.
See the generated README's **Gated Task releases** section for the adapter contract.

The gate runs explicit migrations, checks retained payload keys and handler
compatibility, and requires coverage of all admission versions still allowed to
create work. It verifies fresh heartbeats for the replacement IDs returned by
the start adapter before signaling previous workers through the drain adapter.
Old registrations must disappear and replacements remain ready before promotion.
Worker startup logs include `workerID` for deployment adapters. Failed stages
return nonzero without exposing hook output, payloads, or database credentials.

This is a deployment-adapter workflow: it does not infer image identity, deploy
applications automatically, or roll back migrations. Independently verify web
readiness and observer health in the start adapter. Namespace release locks
serialize cooperating gate invocations; coordinate independent policy changes
and all key-writing processes during rollout. No automatic cleanup follows a
failed release; inspect the reported stage and reconcile candidate processes.

#### Native deployment adapter and recovery

Use `gregale mcp tasks release --plan release.json --state release-state.json`
to run the Task gate through Gregale's deployment APIs without custom hooks.
The plan is JSON; source and policy paths are relative to the plan file:

```json
{
  "web_app": "my-mcp",
  "web_path": "./web",
  "worker_app": "mcp-worker-next",
  "worker_path": "./worker",
  "previous_worker_apps": ["mcp-worker-current"],
  "observer_app": "mcp-observer",
  "observer_metric_app": "mcp-worker-current",
  "timeout_seconds": 300
}
```

Provision the named apps and their runtime bindings first. The candidate worker
app must have no instances or live deployment, and a worker replica minimum of
at least one during rollout. The observer must also have a minimum of one and
publish `mcp_tasks_observer_heartbeat` to `observer_metric_app` using its
`MCP_TASKS_SCALING_APP_SLUG` binding. Web, observer, candidate and previous worker
apps must be distinct. The worker source must start `npm run start:tasks-worker`
and use a worker manifest; the web source must use the web-only Task role.
All apps and local migration/runtime bindings must share the same Task namespace,
database schema, ownership secret and required key ring. Previous worker stop
grace must exceed the Task shutdown deadline (30 seconds by default).

The adapter deploys candidate workers into a separate app because an in-place
worker deployment can retire the previous generation before readiness is checked.
It deploys the web candidate with zero production traffic, checks MCP discovery,
Origin rejection and optional role catalog baselines (`release_policy`), and
requires a running observer plus a fresh server-reported observer heartbeat.
Worker IDs come from deployment-filtered runtime startup logs for every running
candidate instance; their database heartbeats are independently checked by the
Task gate. Old namespace worker IDs must belong to the listed previous apps.
Promotion compares the captured serving revision before switching traffic; the
adapter rechecks the canonical MCP endpoint after promotion, then parks the
previous worker apps and the gate verifies registrations
have disappeared. The adapter never parks the web or observer apps.

Keep the journal and its `.gate` checkpoint outside both source directories.
They contain deployment/worker IDs and progress, never credentials. Use
`gregale mcp tasks release status --plan release.json --state release-state.json`
to inspect progress; rerun the same release command with the same source files,
plan and journal to resume. Known candidate deployment IDs are reused; a
promotion committed before its response was lost is reconciled against the
actual serving revision. New submissions persist a random release ID before upload and carry a role-specific
idempotency key and deployment reason annotation. If a response is lost before
its deployment ID reaches the journal, resume searches all pages of the target
app's deployment history for that exact annotation and records the unique match.
Reconciliation only reads the API; it never resubmits an uncertain deployment.
Missing or ambiguous history stops resume safely; retry after the deployment
becomes visible, or investigate the history. Legacy pending journals without a
release ID still require manual reconciliation. Independently verify their
submission before recording its ID and clearing the pending field in the stopped
journal; never substitute another artifact. Changed source or policy
content and namespace/API environment changes block resume. Do not edit or remove
the journals while a release process is running.

Worker parking sends `expected_deployment_id`; the API compares the latest
deployment under the same app lock used by deployment creation before committing
the park transition. A changed generation returns conflict, including on drain
retries, and leaves the release journal resumable. There is no automatic
rollback; a failure after promotion may leave the new web revision serving while
worker draining is incomplete. Check both journals and the recorded deployments,
restore health, and resume. Log retention must include worker startup records;
missing records fail readiness. No production deployment is performed by local
verification tests.

### Recovering a failed native Task rollout

`gregale mcp tasks release recover --plan release.json --state release-state.json`
reads the actual serving revision, candidate endpoint and worker health, observer
freshness, and previous worker readiness. Its JSON includes a `checks` list with
individual `passed`/`failed` status and a detail for serving generation, candidate
workers, observer, endpoint and previous workers; the existing readiness booleans
remain available to scripts. A failed `--resume` names the blocking checks and
reasons directly. Previous worker readiness remains diagnostic during forward
resume; candidate, observer, endpoint and serving-generation checks gate it. The
command makes no control-plane changes.

Add `--resume` to continue the original release through its namespace-locked gate
when candidate health and the serving-generation checks pass.

For an incomplete rollout that already promoted its candidate web revision, use
`gregale mcp tasks release restore --plan release.json --state release-state.json`.
Restoration holds the same PostgreSQL namespace lock as deployment, verifies
retained and admitted handlers, checks fresh previous workers, probes the exact
previous web artifact against the pinned configuration and role policy, and
restores traffic with a compare-and-swap expecting the candidate revision. It
then verifies the canonical endpoint. A lost traffic response is reconciled
against the actual serving revision; rerun `restore` after repairing health.

Restoration is deliberately conservative: every original worker app must still
have its captured generation running, must not have been parked, and must cover
**every handler version in the candidate inventory**, including previous versions.
Missing registrations, stale heartbeats, changed traffic or worker generations,
incompatible admission, and failed endpoint checks stop recovery. Restoration
cannot recover capacity after old workers have been parked; use a separate reviewed recovery/deployment plan once that capacity has been
retired.

Restoration preserves candidate workers and Tasks, changes no admission policy,
and performs no schema rollback. Its journal stages are `restore_pending` and
`web_restored`; either blocks the original rollout's `run`/`recover --resume`
path. After successful restoration, inspect capacity and make a new release plan
and journal for future deployment. Do not restart the old release or reuse its
`.gate` checkpoint. If a post-switch canonical check fails, `restore_pending`
remains recorded so restoration can be retried without repeating the traffic
switch. Recovery requires the updated starter's restore and readiness scripts;
keep the sources pinned and do not modify a running release's journal or sources.

### Quarantining and retiring recovered candidate workers

After restoring web traffic, run `gregale mcp tasks release quarantine --plan release.json --state release-state.json`
to stop the journal's candidate worker IDs from acquiring new Task leases. This
requires the restored web revision, healthy observer, and fresh, compatible
previous worker generations. It holds the release namespace lock and fences
claims under the same PostgreSQL transaction lock used for running capacity.
Heartbeats and expired-registration cleanup preserve the fence until confirmed
shutdown or retirement removes it. Existing active handlers retain their leases and
may finish; quarantine does not cancel Tasks or revoke leases during execution.

Use `gregale mcp tasks release retire --plan release.json --state release-state.json`
to fence those workers, recheck replacement capacity and exact candidate
generation/worker IDs, and park only the candidate worker app. Its stop grace
must exceed the configured Task shutdown deadline. Graceful shutdown
finishes or aborts its handlers using the normal worker drain deadline. Abandoned
leases become reclaimable through their normal expiry, preserving lease-token
protection against late writes. Completion waits for candidate registrations to
stop or expire. `retirement_pending` preserves an uncertain park outcome; rerun
`retire` to reconcile an already parked app without deploying another generation.
`worker_retired` is recorded only after the gate completes. Neither quarantine
nor retirement changes web traffic, observer capacity, admission, or Task data.

These fences require updated worker runtimes that pass their worker ID on claims.
The CLI requires their per-instance startup logs to attest `claimFence: true`;
legacy or missing startup records stop quarantine instead of silently allowing
claims.
They apply to the captured worker IDs, not to arbitrary future processes. A
restarted candidate with different worker IDs or a changed app generation stops
retirement; investigate it rather than substituting IDs in the journal. The
conditional park API also rejects a generation change between the health checks
and parking. Quarantine remains in place if a later check fails. All recovery
and retirement stages block resuming the original rollout; use a new plan/journal
for a subsequent release. Runtime database credentials supply the existing
worker-table UPDATE privilege; observer credentials remain read-only.

Guarded worker draining and retirement use `POST /v1/apps/{slug}/park/conditional`,
which requires `expected_deployment_id`. Older control planes reject this route;
the release client stops and never falls back to unconditional parking. Upgrade
the control plane before retrying the pending release or retirement.

`GET /v1/capabilities` advertises `conditional_parking` only when the serving
control plane has an atomic parking backend. Missing or false support blocks
Task releases and candidate quarantine/retirement before their Node gates start.
The adapter repeats the check before deployment, promotion/draining, and
quarantine checks. `gregale mcp doctor --hosting` includes a `conditional_parking`
check; failed discovery is unknown readiness and missing support includes upgrade
guidance. Upgrade all control-plane instances before retrying. Capability reports
are preflight signals: mixed-version fleets still rely on the dedicated conditional
parking endpoint and never fall back to ordinary parking.

Native release journals include a monotonic `revision`. Mutating commands
(`run`, `recover --resume`, `restore`, `quarantine`, and `retire`) exclusively own
the journal before reading it. A second command fails with ownership guidance;
`status` and read-only `recover` remain available. Gate adapters inherit a private
owner token and serialize their operations, so an orphaned or stale adapter
cannot join a replacement command. An active adapter prevents takeover if its
parent exits. OS locks release when processes exit; leave the `.owner.lock`,
`.adapter.lock`, and `.write.lock` files in place rather than deleting them.

Each save checks the loaded revision under a write lock, syncs the temporary
file, atomically replaces the journal, and syncs its directory on Unix. Stale
writes stop instead of overwriting newer stages. If persistence fails, reload
the journal before retrying: replacement may already have happened. Existing
version-1 journals without a revision remain readable and receive a revision on
their next save. Temporary `.mcp-release-*` files left by an interrupted process
are ignored; the journal remains the recovery source of truth.

For native management deployment acceptance, see the
[nonproduction KVM harness](ops/management-mcp-native-acceptance.md).

## Watch one rollout

`gregale_watch_rollout` accepts `app`, `deployment_id`, optional `project` and
`environment`, and `wait_seconds` (1–45; default 20, shortened by the connection's
per-tool deadline). It polls passive API observations and returns the last valid
snapshot with `watch_timed_out: true` when the observation budget expires. It
never probes customer endpoints, changes traffic or invokes recovery.

The response separates `accepted` (the exact deployment exists), `live`
(artifact lifecycle), `healthy` (fresh healthy observations attributed solely to
that serving deployment), and `completed`. Current request releases require
100% traffic, completed native rollout state and fresh health. Services also
require a completed promote handoff with no missing gateway acknowledgements;
an absent handoff cannot establish completion. Workers require the selected
running generation with no starting, draining or retired resident rows;
`completion_basis` explicitly limits this to residency, not queue processing,
delivery acknowledgements or desired replica counts. A queued/running build
cannot establish completion. Historical releases are not reported completed.

`basis: sequential_passive_reads` means these independently collected facts are
not an atomic snapshot. `blockers` contains fixed identifiers and `next_read`
names a read-only tool. Build progress and native handoff details stay in their
own fields; acceptance is not successful serving readiness. Malformed or
cross-environment responses fail closed, rather than falling back to aggregates.

For progress-capable clients, add `_meta: {"progressToken": "watch-123"}` to
`tools/call` parameters alongside `name` and `arguments`. The token is a finite
number or nonempty UTF-8 string of at most 128 bytes. Progress is opt-in; ordinary
calls keep the same bounded response.

The server sends standard `notifications/progress` for the initial validated state
and subsequent changes, with strictly increasing update numbers and no total.
These numbers count observations that changed, not percent completion. The JSON
message summarizes build status, readiness counts, service handoff/binding state
and worker residency. It contains no logs, raw errors or gateway/resource names.
Counts are bounded at one million. Unchanged states emit no heartbeat; a watch
sends at most 46 updates and starts no background subscription. The final tool
result remains authoritative for timeout and native completion semantics.

Cancel the MCP call to stop its observation and in-flight API reads. HTTP watches
use MCP 2026-07-28 or later for request-disconnect cancellation; legacy stateless
HTTP clients can stop waiting, while server observation ends at the bounded watch
deadline. Stdio cancellation uses its existing request session. This leaves
the underlying deployment/build running; stopping a service rollout remains a
separate reviewed action. Consent is rechecked before each emitted update, and
notification or consent failures end the watch. Each notification has a bounded
send context. Client display varies: the pinned Codex app-server accepts progress
metadata on direct calls, while visible progress may depend on its calling interface.

## Management resources and review prompts

The management server advertises MCP resources and prompts as well as its tool
catalog. These features are read-only even on a write-enabled connection.

| Resource URI | Content |
| --- | --- |
| `gregale://context` | Consented connection/account metadata and capabilities; no credentials. |
| `gregale://default/inventory` | First bounded page of consented apps; requires default read access. Use tools for pagination. |
| `gregale://default/apps/{app}/release` | Safe app projection and current release review identities/hashes. |
| `gregale://default/apps/{app}/health` | Scoped passive health observations. |
| `gregale://default/apps/{app}/budgets` | Scoped operational summary, including inherited or overridden route-budget findings and bounded recovery metadata. |
| `gregale://apps/{app}/regressions` | App-wide native-visible regression snapshot across environments; requires separate per-app diagnostic consent. Fixed 1h detection lookback, subject to native retention, and at most 20 projected observations. |
| `gregale://projects/{project}/environments/{environment}/inventory` | Consented workload/release inventory in one registered environment. |
| `gregale://projects/{project}/environments/{environment}/apps/{app}/{view}` | Named versions of `release`, `health`, and `budgets`. |

Resource templates advertise generic selectors, not unconsented app names.
Reads validate consent and immutable identities before and after collection,
reuse existing safe projections/redaction and bound the serialized response.
Canonical `gregale://` URIs cannot contain credentials, query strings, fragments,
encoded path segments or arbitrary external endpoints. Resource writes are not
exposed; subscription polling requires an explicit request as described below.
Resource contents and API descriptions
remain untrusted application data, not instructions.

The regression resource reuses `gregale_list_regressions` projections and
immutable app/deployment guards, including detection/triage timestamps, scope,
redaction, native ordering and truncation/unknown-completeness metadata. It has
no default/named-environment aliases. Use the tool for a different lookback or
projection bound. `snapshot_observed_at` records the bridge collection time;
it does not imply an atomic native snapshot or complete source coverage.
`update_semantics` explains that disappearing observations can reflect dismissal,
resolution, lookback expiry or projection ordering/truncation, and do not prove
recovery. Reads, completion and discovery do not start a watch or change state.

`gregale_deployment_review`, `gregale_rollout_diagnosis`, and
`gregale_rollback_review` return scoped review recipes with exact resource URIs
and existing guarded tool guidance. All require `app`; named selectors also
require `project` and `environment`. Diagnosis/rollback require an exact
`deployment_id` belonging to that app and scope. Unknown arguments are rejected.
Retrieving a prompt reads identity context but creates no release plan, applies
no change and grants no additional permission. Protected promotions still need
native human browser approval. A source rebuild is not an identical artifact
restore; worker residency is not queue completion.

The pinned Codex app-server API supports resource reads; prompt retrieval is
verified through the standard MCP SDK in-memory and over OAuth HTTP for both
supported protocol versions. Client UI exposure of prompts may vary.

### Subscribe to scoped resource changes

MCP 2026-07-28 clients can explicitly request `subscriptions/listen` with
`notifications.resourceSubscriptions` containing canonical app `release`, `health`,
`budgets` or app-wide `regressions` URIs from the table above. The server
acknowledges the accepted set and reports `_meta.gregale_subscription_expires_at`.
On a meaningful change, it
sends `notifications/resources/updated` with the URI and subscription metadata;
clients then reread that resource. Notifications contain no resource payloads.

Limits are four distinct resources per stream, sixteen total reserved resources and
eight streams per connection/grant, and four streams per SDK session. Each stream
expires after at most two minutes; the OAuth access token, grant or credential may
shorten it. Polling occurs every five seconds after the previous poll completes.
Unchanged observations, observation clocks and moving-window timestamps stay quiet;
health freshness expiry remains a meaningful change. Multiple publishers coalesce
per recipient. These sampled updates can miss intermediate transitions and provide
no durable event cursor.

Regression listens require current diagnostic consent at admission, polling and
delivery, and pin the app identity before collecting further diagnostics. The
snapshot collection clock alone does not notify; projected detection/triage
timestamps, state, metrics, native ordering, counts and visibility remain
meaningful changes. Changes exclusively outside the bounded projection may not
notify. A URI invalidation is not a confirmed incident/recovery event. Reread
the snapshot after invalidation and after reconnecting, then explicitly resubscribe;
there is no event replay or automatic triage.

Unsubscribe, cancel or disconnect to stop the listen. A recreated app/environment,
consent failure, API failure or revoked grant closes the affected stream and stops
its reads. No subscription survives a bridge restart. Renew explicitly after expiry;
with the Go SDK, call `Unsubscribe` before `Subscribe` again for the same URI.
Context and inventory resources are not subscribable, and discovery alone starts
no polling. Watches use the same customer API permissions and never wake VMs.

Legacy clients retain resource reads and do not advertise subscription support.
The pinned Codex app-server offers resource reads but has no resource-subscribe
method; its event-stream API is separate. Resource subscription delivery is verified
with modern SDK clients over session and stateless HTTP transports.

### Complete prompt and resource arguments

Clients that support MCP `completion/complete` can request prefix suggestions for
Gregale's three review prompts and eight resource templates. Regression resource
app suggestions require diagnostic consent. Other suggestions cover
`app`, `project` and `environment`; diagnosis and rollback prompts also complete
`deployment_id`. Resource references use the exact URI **template** returned by
`resources/templates/list`, including `{app}` placeholders, rather than a resolved URI.

Send previously selected values in `context.arguments`. Named-resource app suggestions
require both project and environment. Deployment suggestions require an app and,
for named scope, both project and environment. Project and environment suggestions
revalidate the connection's consented identities; app suggestions exclude workloads
outside consent. Completion is read-only and grants no additional access.

Results are sorted, deduplicated, case-sensitive literal prefix matches with at most
100 values. Deployment completion examines one recent history page and filters it
to the selected scope. `hasMore` indicates additional values or unexamined history;
`total` is omitted when the result is incomplete. Use deployment-history tools to
search older pages. Empty suggestions can mean the selector is incomplete or the
prefix has no matches. Unknown references and malformed arguments are rejected.
Client interfaces may not expose completion even when ordinary MCP reads work.

### Concurrent requests and overload

Each stdio management connection or remote OAuth grant allows eight concurrent
short requests and two concurrent `gregale_watch_rollout` calls. All HTTP requests
and SDK sessions for the same grant share these limits. Separate grants have
independent capacity. These bridge limits do not change Gregale plan quotas.

Short requests include ordinary tools, resource reads, prompt retrieval and
argument completion. Subscription baseline reads, polls and notification-recipient
checks use the same short capacity; idle subscription streams do not. A busy
subscription poll is skipped, and pending changes can notify after capacity returns.
The existing subscription lifetime and stream limits still apply.

Admission has no queue. Saturation returns `mcp_busy`, `status: 429` and
`retry_after_seconds: 1` before any customer API call. Tools return `isError`;
other methods return an RPC error with sanitized diagnostics. The diagnostic
status does not imply an HTTP 429 response. Retry after at least one second with
jitter. For mutations, reuse the same plan ID to preserve retry safety.

Completion, failure, deadlines and cancellation release capacity when the handler
exits. Modern HTTP clients propagate disconnection to active short reads, watches
and listens; legacy clients may hold capacity until the bounded handler deadline.
Discovery, initialization, ping and control notifications remain available during
overload, including cancellation and unsubscribe. Capacity is per grant/process;
these limits do not replace gateway controls for aggregate traffic.

### Observe management MCP operations

Enable a private Prometheus listener for either transport:

```sh
gregale mcp serve --transport stdio --metrics-listen 127.0.0.1:8788
curl http://127.0.0.1:8788/metrics
```

`--metrics-listen` is optional and disabled by default. It accepts a literal
loopback IP and port, including `[::1]:8788`; wildcard addresses, public IPs and
hostnames are rejected. The metrics listener is separate from the OAuth/MCP
endpoint, has bounded HTTP timeouts, and closes when the serving command exits.
Startup diagnostics go to stderr, preserving stdio protocol output.

| Metric | Meaning |
|---|---|
| `gregale_management_mcp_requests_total` | Completed requests/snapshots by fixed `operation`, registered `tool` (or `none`/`unknown`), and `outcome`. |
| `gregale_management_mcp_request_duration_seconds` | Handler-duration histogram by operation/tool, including validation and customer API reads. |
| `gregale_management_mcp_active` | Aggregate reservations by `kind`: `short_requests`, `rollout_watches`, `listen_streams`, `resource_slots`. |
| `gregale_management_mcp_resource_updates_total` | Invalidation notifications successfully sent to individual recipients. |

Outcomes are `success`, `error`, `busy`, `cancelled` and `timeout`. Tool results
with `isError` count as failures even when their RPC completed successfully.
Operations cover tools, resources, prompts, completions, listen RPCs and
`subscription/read` snapshots. Snapshots include baselines, polls and recipient
checks; busy skips are counted. Listen duration includes its lifetime, and listen
outcomes follow the SDK RPC result rather than asserting a termination cause.

Metrics aggregate all grants in this process and reset on restart. They contain
no app/project/environment/account/grant identifiers, URIs, arguments, logs,
credentials, arbitrary error codes or resource contents. No global Go/process
collectors are registered. Gauges include setup/cleanup reservations; sent
notifications do not prove client processing or rereads.

For overload, inspect the rate of requests with `outcome="busy"` alongside active
short requests and watches. For slow operations, compare histogram latency by
operation/tool; watch and listen lifetimes are expected to exceed ordinary reads.
Scrape this listener locally. Embedders can share `mcpmanagement.NewTelemetry()`
through server options and mount its `Handler()` on their own private listener.

To verify the CLI listener against an isolated local API fixture without customer
writes, run `python3 scripts/ops/management-mcp-metrics-acceptance.py --gregale-binary /path/to/gregale`.
It checks bind validation, protocol framing, private labels and listener cleanup.

### Restart and drain the management bridge

SIGTERM and SIGINT stop new MCP work, cancel read-only observations, rollout
watches and subscriptions, and give accepted mutations five seconds to finish.
Remaining work is then canceled, with up to five more seconds for cleanup.
HTTP listener shutdown runs concurrently; stalled connections are force-closed
at the grace deadline. Stdio keeps the transport alive through handler drain.
Client disconnection can still cancel its own request earlier.

New MCP work on an existing session returns `mcp_draining` with status 503 and
retry guidance. New HTTP requests receive HTTP 503 and `Retry-After: 1`.
Drain cancels subscription polling workers and waits for handlers and the final
HTTP journal commit before releasing the encrypted state's single-writer lease.
If cleanup cannot finish, the command reports failure and retains that lease
until process exit. Successful cleanup leaves active-capacity gauges at zero;
the metrics listener remains available during drain and closes on exit.

Cancellation does not undo native operations already accepted by Gregale. For an
ambiguous remote mutation, restart with the same state/key files and retry the
same plan ID. Plans and grants retain their original expiry and consent checks.
Ephemeral stdio plans do not survive restart. Subscriptions always require explicit
resubscription after reconnect. Shutdown does not revoke the account's native keys.

Library embedders can share `mcpmanagement.NewLifecycle()` through `Options` and
`HTTPOptions`, call `Shutdown(graceContext)`, and close their state only after
successful cleanup. Use a fresh coordinator when restarting; drained instances
never admit new work again.

Run `python3 scripts/ops/management-mcp-drain-acceptance.py --gregale-binary /path/to/gregale`
for isolated CLI SIGTERM, native-read cancellation, metrics cleanup and encrypted
state reopening checks. SDK tests additionally cover mutation grace, lost replies,
durable retry keys and journal commits during drain.

### Inspect and revoke agent connections locally

For an HTTP management bridge, enable `--admin-socket /path/to/private/control.sock`.
Its parent directory must already exist, be owned by the bridge user with mode
0700, and contain no symlink components. The bridge creates a mode-0600 Unix socket
and refuses any existing path. The flag is disabled by default and requires HTTP
transport; the public OAuth/MCP listener has no connection-management routes.

Run these commands as the bridge owner; no customer API key or agent token is needed:

```sh
gregale mcp connections list --socket /path/to/private/control.sock
gregale mcp connections list --socket /path/to/private/control.sock --limit 10 --offset 10
gregale mcp connections revoke --socket /path/to/private/control.sock --grant-id gmcp_grant_example
```

Commands print JSON. Listing shows active grant/client/account IDs, consent scopes,
expiry, default-write access, selected apps, and named/writable environments. Lists
are sorted snapshots with `next_offset` when applicable. Selector arrays include at
most twenty entries, plus full counts and truncation flags. The page size defaults
to twenty and accepts 1–100; overly large metadata pages require a smaller limit.
Keys, tokens, hashes, browser codes and resource contents are excluded.

Revocation stops the selected grant's active requests, watches and subscriptions,
including requests authenticated before revocation. Other grants remain usable.
A success response includes `durable: true` only after the encrypted OAuth snapshot
is committed. Without state files, it returns `durable: false`. Unknown or already
revoked grant IDs return 404. Storage failure returns 503 and disables further
bridge access until repaired; failed revocation is not reported as durable success.

Revocation does not revoke native account API keys or undo operations Gregale has
already accepted. Inspect native deployment state before issuing replacement plans
for an ambiguous operation. A revoked agent must reconnect and review new consent.
Both HTTP listeners participate in bounded draining, and normal exit removes the
owned socket. After an abnormal exit, verify a remaining socket is stale before
removing it; the bridge never overwrites it automatically.

Library embedders can use `NewHTTPService` for separate `Handler()` and
`AdminHandler()` endpoints and `ListenAdminSocket` for the protected listener.
The admin handler belongs on a private owner-controlled endpoint. CI runs
`management-mcp-admin-acceptance.py` for socket permissions, CLI access, public
isolation, collision protection and cleanup; SDK tests cover scoped revocation and
restart persistence.

### Authorization expiry during a request

Remote MCP requests are bounded by the earliest expiry of their presented access
token, OAuth grant and native credential. The bridge applies that bound during
account verification and throughout tools, resources, prompts, completions,
rollout watches and subscriptions. It rechecks token membership, revocation and
expiry after account verification before dispatching work.

This applies to legacy and current MCP HTTP clients. Expiry cancels active native
reads and handlers and releases capacity during cleanup, without waiting for
another request to trigger pruning. Existing request/watch timeouts can end an
observation earlier. Local stdio keeps its existing timeout behavior.

Refresh authorizes subsequent requests; it cannot extend one already running under
an older token. A rejected or expired verification gets the OAuth 401 challenge.
Work that expires after dispatch follows existing timeout/cancellation diagnostics.
Access-token expiry alone does not revoke an otherwise refreshable grant.

Expiry does not undo native operations already accepted by Gregale. After an
ambiguous durable mutation, refresh authorization and retry the same plan ID while
its grant and plan are still valid. Expired grants or native credentials require
renewal and consent checks. The bridge may finish journal cleanup after request
expiry to retain its attempt/retry key; that cleanup grants no new API access.
SDK checks cover all read views in legacy/current HTTP, verification races,
refresh isolation, grant/credential expiry and a lost mutation reply with one native
write after restart and retry.
### Compatibility when upgrading existing Task deployments

Keep the Task owner key stable when using Node's versioned admission schema.
Use `tasks.encryption_keys_env` for independent payload-key rotation; Go and
Python understand the same `activeKeyId`/`keys` configuration and authenticated
v2 envelopes, while retaining legacy payload readers for existing queues.
Run Node's matching explicit schema migration before combining Node runtimes
with queues initialized by a Go or Python starter. The Node migration also
creates the deployment-attributed worker-handler table used by the observer.
Grant its read-only observer access to that table together with the existing
Task and worker tables. This does not grant the observer task ownership or
schema mutation rights.

### Stack setup doctor

Use the read-only stack doctor to join serving/catalog evidence, Task scaling,
worker and observer readiness, namespace/process settings and promotion holds:

```sh
gregale mcp doctor --stack --app reports-web \
  --worker-app reports-worker --observer-app reports-observer \
  --path ./reports/web --token-env MCP_READER_TOKEN --json
```

`--path` loads the reviewed `gregale-mcp.json` with the deployment configuration
validator. Its endpoint must match `--endpoint` (default `/mcp`). For private
access, the doctor checks the public unauthorized/malformed-token challenge,
canonical resource audience, advertised issuer and scopes. It also checks OIDC
or OAuth authorization-server metadata against the configured issuer/JWKS URL,
and checks the configured JWKS for a usable public RS256 or ES256 signing key.
Provider requests never carry client or platform credentials, follow redirects,
or follow URLs returned in provider metadata; responses are capped at 64 KiB.
All requested checks share `--timeout` (default 30s, maximum 2m).

The versioned JSON report contains `ok`, `checks` and the complete `overview`.
Checks use `passed`, `failed` or `unknown`; either failed or unknown checks exit
with status 1. Omit `--worker-app` to leave Tasks outside the requested scope.
Without `--path`, OAuth expectations remain unknown. An open source configuration
makes provider checks inapplicable; it does not prove deployed authentication
matches that source. The report includes suggested command argument arrays and
configuration guidance but never executes repairs, retries, tools or workloads.
Public MCP discovery may wake the serving app. The snapshot is not atomic.

Passing public discovery/key checks does not verify token issuance, user login,
client integration, token claims/scope enforcement, database permissions,
encryption keys, or native cold-boot/restore behavior. Continue to run provider,
Task preflight and native acceptance checks before production qualification.
Existing `mcp doctor` protocol checks and `mcp doctor --hosting` Task preflight
remain available; their flags cannot be combined with `--stack`.

### Reviewed authorization release gates

A version-1 release role may opt into read-only authorization checks with an
`authorization` object. Existing policies that omit it keep their catalog-only
behavior. The role's format-2 baseline remains the exact reviewed catalog of
allowed tool, resource and prompt definitions. Explicit exclusions provide an
additional assertion that sensitive entries stay hidden:

```json
{
  "version": 1,
  "roles": [{
    "name": "reader",
    "token_env": "MCP_READER_TOKEN",
    "baseline": "reader.lock.json",
    "authorization": {
      "denied_tools": ["write_report"],
      "denied_resources": ["file:///reports/private"],
      "denied_resource_templates": ["file:///private/{id}"],
      "denied_prompts": ["admin_summary"],
      "insufficient_scope_token_env": "MCP_INSUFFICIENT_SCOPE_TOKEN"
    }
  }]
}
```

The positive role token must be available. Exclusions must be unique, non-empty
identifiers: tool/prompt names, resource URIs and resource-template URI templates.
A baseline that includes an excluded entry is rejected before deployment.
Tokens are loaded from environment references; inline credentials are rejected.
The insufficient-scope token must differ from the positive role token. Supply a
valid provider-issued token lacking the endpoint's required scopes: the probe
expects HTTP 403, so an expired, malformed or wrong-audience token returning 401
fails. Omit that environment reference when only per-entry scopes are enforced;
the endpoint may legitimately permit discovery for a token with fewer entry
scopes. HTTP 403 alone does not independently prove the token's validity or the
provider's claim validation.

Every opted-in role requires HTTP 401 for both missing and malformed bearer
credentials on initialization and the tools/resources/templates/prompts list
methods, plus modern `server/discover`. The optional insufficient-scope token
requires HTTP 403 on those same methods. Timeouts, redirects, 5xx responses and
unexpected statuses fail the gate. Successful protocol responses still require
the expected version and stateless behavior; authorization rejection responses
need not advertise a protocol version.

`mcp deploy --release-policy` checks every running candidate replica through the
existing exact-instance probe API, confirms the replica set, and checks the
candidate endpoint before conditional promotion. Task-stack releases with an
opted-in policy also require exact-replica qualification before promotion or
recovery verification. `mcp status`, `mcp overview` and `mcp doctor --stack` reuse
the same role checks with `--release-policy`. Reports include per-role
`authorization` checks with stable names, statuses and sanitized details. No
credential values or upstream rejection bodies are included.

The probes never execute business tools, read resource contents, render prompts,
or mutate Tasks. Hidden catalog entries do not prove that direct operations on
those entries are denied; those operations require separate, explicitly
approved authorization tests. Provider login, real token issuance and native
cold-boot/restore acceptance remain separate production qualification evidence.

### Access policy drift audit

Successful access audits also require fresh gateway policy convergence. The CLI
waits up to 10 seconds, within the overall operation deadline, for every registered
serving gateway to observe the account edge-rule revision. Account coverage includes wildcard
policies owned by other apps in that account. Known mutations in another
account do not raise the desired revision. Unknown pre-upgrade ownership
provides a conservative fleet revision floor until gateways have replayed it. Missing gateways, stale observations, an unavailable status
API, or no observed policy revision produce an unknown check and block promotion.
The policy fingerprint is re-read after convergence; a changed policy fails the
audit. Deploy, task-stack promotion, and recovery use this same audit path when
access auditing is configured. Candidates remain at 0% on a rejected promotion;
recovery does not restore traffic until its destination policy passes.

`GET /v1/apps/{slug}/policy/status?edge_rules_scope=account` exposes the account
edge-rule revision and fresh replay counts; the default remains app scoped.
The optional `legacy_fleet_revision` records the unknown-history floor. Ledger
pruning preserves that floor and each affected account's latest revision,
including rule transfers and deletions. `edge_rules_scope=fleet` remains available
for fleet diagnostics.
This requires an updated control plane. Replay confirms cache invalidation and
policy observation, not successful JWKS resolution or valid provider tokens.
`runtime_verified` remains false. The atomic stored-policy promotion fence still
rejects later configuration changes, but this check does not freeze gateway
membership or attest gateway health after verification.

Inspect reviewed source policy against stored gateway configuration without
calling the MCP endpoint or resolving any role bearer environment variables:

```sh
gregale mcp access audit --app my-mcp --path ./my-mcp \
  --auth-placement gateway --release-policy release.json --json
```

`--auth-placement` is required. `gateway` expects one enabled, non-expiring,
app-bound JWT rule on the canonical host and `/**`, with no method, header or
match-expression conditions. `guest-only` expects no active gateway JWT rule
overlapping the MCP endpoint or protected-resource metadata paths; the reviewed
source must still declare `external-oauth`. This is a configuration declaration,
not verification that a deployed guest actually enforces authentication.

The audit reads account-wide edge rules, so earlier or equal-priority wildcard
JWT rules attached to another app cannot hide behind an app-only listing. A rule
with conditions can shadow some requests and is treated conservatively. Later
rules cannot override an unconditional managed rule. Disabled, expired and
log-only rules do not establish enforcement. An expiring managed policy cannot
match the source's permanent policy intent. Account-wide read permission is
required; inaccessible rules remain `unknown`, not an empty healthy list.

Gateway comparison covers issuer, canonical audience/resource, JWKS URL,
signature algorithms, endpoint scopes, allowed origins and tool/resource/prompt
scope maps. Scope/order-only changes are normalized; unrestricted (`null` or
omitted) and explicit deny-all (`{}`) maps stay distinct. Additional required
claim or platform-tenant bindings are flagged for review; stronger expiration
claim enforcement is permitted. Gateway algorithms default to `RS256,ES256`;
use `--algorithms RS256` to review a deliberately narrower gateway policy.
Algorithm options apply only to gateway placement.

Optional role baselines are loaded within the policy directory with the same
format-2 and exclusion validation used by releases, but bearer values are never
read. Baseline entries must be admitted by source allowlists. These structural
checks do not infer a token's scopes, evaluate claims, or prove that every entry
missing from a baseline is forbidden. Scope-sensitive role visibility and
rejection remain the responsibility of live release qualification.

The version-1 report contains `ok`, `checks`, `roles_requested`,
`auth_placement`, the selected `rule_id` when available, a successful audit's
`policy_expectation` (resource and SHA-256 fingerprint), and
`runtime_verified: false`. Failed or unknown checks exit 1; consistent stored
policy with fresh gateway replay evidence exits 0. Details expose field names,
not raw JWT actions, claim values,
credentials or error bodies. Suggestions inspect rules; no repairs are applied.
The observations are not atomic with traffic changes and do not freeze gateway
membership. They do not establish DNS/custom-domain behavior, cross-account
rules, provider login, JWKS availability or live guest configuration. Continue
to use doctor, role qualification and provider/client
acceptance checks for runtime evidence.

Enable an optional check immediately before conditional deployment promotion:

```sh
gregale mcp deploy --path ./my-mcp --name my-mcp \
  --token-env MCP_CLIENT_TOKEN --release-policy release.json \
  --access-audit gateway --access-audit-algorithms RS256 --json
```

Drift or missing evidence leaves the candidate at zero traffic, with the
`access_audit` report in its receipt. Source/baseline inconsistencies fail local
preflight. Omit `--access-audit` to retain existing deployment behavior. A successful audit supplies an account-wide fingerprint of potentially overlapping
JWT rules, including other-app wildcard rules, selectors, conditions, priority,
actions, enablement, mode and expiry. The traffic patch checks this fingerprint
and the serving revision in its database transaction. A changed policy returns
`409 mcp_access_policy_changed` without changing traffic; audit again before
retrying. Unrelated hosts and descriptive metadata do not invalidate the gate.

Edge-rule commits serialize with the guarded transaction through an account
advisory lock and a deferred database trigger, covering API edits, manifests,
rule-set rollback and other transactional writes. The lock ends at commit;
subsequent policy changes remain allowed. This fences stored intent only and
does not wait for gateway cache convergence. The control plane must include the
promotion-fence migration and API support; an older control plane rejects the
new request instead of silently omitting the guard.

Task-stack release plans support the same optional gate:

```json
"access_audit": {"auth_placement": "gateway", "algorithms": ["RS256"]}
```

For guest-only placement, use `{"auth_placement":"guest-only"}` without
algorithms. The plan checks access policy during preflight and endpoint release
or recovery verification. Promotion and restoration use the newly verified
policy expectation; it is not loaded from an old journal. Audit options enter the release fingerprint; changing
them cannot silently resume an existing journal under a different reviewed
policy. Plans that omit this object retain existing behavior.

Traffic promotion and recovery now send the successful audit's account edge-rule
revision as `expected_mcp_access_policy.account_revision`. The traffic API checks
that revision and fresh replay from every currently registered serving gateway in
one database snapshot inside the traffic transaction. It returns HTTP 409 with
`mcp_account_revision_changed`, `mcp_gateway_evidence_unavailable`,
`mcp_gateway_evidence_stale`, or `mcp_gateway_replay_pending` when the condition
fails, without changing traffic. Re-run the access audit after convergence before
retrying. Fingerprint-only API requests remain compatible. This admission check
does not lease fleet membership or certify live token enforcement; gateway
membership and reports may change after the snapshot.

MCP traffic decisions are retained as `deploy.mcp_admission` events in the
existing deployment audit timeline. Inspect a promotion or recovery target with:

```sh
gregale mcp access history --deployment DEPLOYMENT_ID --json
```

History pages contain up to 50 MCP receipts, with a continuation cursor.
`mcp status` includes MCP-only evidence for serving deployments. `mcp overview`
also inspects up to five recent web deployments, including blocked candidates.
Both show the latest accepted and rejected receipts separately, using filtered
queries so an older outcome remains visible beyond the first page. Each outcome
is `available`, `empty` (no matching retained receipts), or `unavailable`
(the read failed or returned invalid evidence). Available receipts include an
exact API path and a `gregale mcp access history --evidence UUID` command.
These historical records do not change the live protocol-health verdict.

SDK callers can use `GET /v1/deployments/{id}/audit` and decode
`data.mcp_admission` using the `MCPAdmissionEvidence` model. Records contain
expected and observed policy digests, account revisions, gateway boot IDs,
replay positions, observation times and the admission outcome. They do not
contain JWT actions, token values, claims, resource URLs or raw failure messages.
Accepted records commit with traffic; rejected records are saved after rollback.
`mcp_admission_evidence_unavailable` means audit storage failed: inspect traffic
and retry after storage recovers. Records use existing deployment audit retention.
Only requests reaching the authorized MCP policy check produce these records.

For ongoing account policy monitoring after release, enable the existing alert
system's `mcp_policy_replay_pending` and `mcp_policy_unverified` presets for the
HTTP MCP app. Configure your webhook and secret using the usual
`gregale alerts preset enable --app APP --webhook-url URL --webhook-secret-stdin PRESET`
flow. No monitoring rule is enabled automatically. Existing plan quotas, delivery
claims, cooldowns and retention apply; these alerts support webhook action only.

`mcp_policy_unconverged_gateways` counts each stale, missing, future-dated or behind
gateway once. `mcp_policy_unverified` detects an active HTTP app with no fleet or
no established account edge-rule revision. Apps without an active HTTP deployment
are paused. Failed reads are degraded; an unverified revision cannot count as a
healthy replay observation. Metrics are instantaneous and run at the configured
alert evaluator cadence; the `5m` window label does not impose a five-minute hold.

`mcp status` and `mcp overview` show current account policy convergence and enabled
alert states. Overview degrades for pending replay and returns to healthy after
fresh convergence when its other checks pass. The account policy status API
includes `mcp_convergence`, with a timestamp and admission-evidence links when a
retained accepted record exists. Fired alert payloads include that same observation
metadata. Recovery uses the existing alert rule state and `alert.resolved` audit.
These signals attest replay freshness, not live token enforcement.

### Search retained admission receipts

Use `gregale mcp access history --deployment UUID --limit 50` to page MCP-only receipts. Filter with `--outcome accepted` or `--outcome rejected`; pass the returned `next_cursor` as `--cursor` for the next page. `gregale mcp access history --evidence UUID` reads an exact receipt, including receipts outside the recent generic audit window. These commands are read-only and account-authorized. Retention and deployment deletion can remove receipts; this is not permanent archival storage. Status and overview show the first MCP-only page and independently query the latest accepted and rejected receipts.

### Explain an admission decision

```sh
gregale mcp access explain --evidence EVIDENCE_UUID --json
gregale mcp access explain --evidence EVIDENCE_UUID --app APP_SLUG
```

The explanation compares expected and recorded policy fingerprints and account
revisions, translates the recorded rejection reason, and identifies each
gateway's missing, stale, future-dated, or behind report. Freshness uses the
receipt's captured time and the admission freshness limit, not the time you run
the command. Fingerprint-only receipts explicitly indicate that gateway
convergence was not requested. An accepted receipt records a committed traffic
transaction; it does not establish current health.

The optional `--app` reads current account convergence only after verifying that
the selected app owns the receipt's deployment. This observation has its own
timestamp and never rewrites the historical explanation. Missing, incompatible,
or older-than-receipt current snapshots remain unavailable. The command returns
nonzero when a receipt cannot be read or validated, or when a requested current
observation is unavailable; valid historical findings are still printed if the
current read fails. A rejected receipt is a successfully explained decision and
does not itself cause a nonzero exit.

Suggested commands inspect deployment traffic, retained history, and current
hosting state. Before retrying a release, review its source and access policy,
obtain a fresh audit, and use the normal guarded release flow. Explanation is
read-only: it sends no application requests, loads no MCP client tokens, and changes
no traffic or policies.

### Continuous catalog drift monitoring

Configure a reviewed complete baseline, then run the observer as a supervised
process (for example in your monitoring environment or an always-on worker):

```sh
gregale mcp monitor setup --app APP --baseline gregale-mcp.lock.json --freshness-seconds 90
gregale mcp monitor run --app APP --token-env MCP_MONITOR_TOKEN --interval 30s --timeout 10s --json
gregale mcp monitor status --app APP --json
gregale mcp monitor disable --app APP
```

The platform credential needs read and metrics-write scopes for the reporting
loop; baseline configuration requires deploy-write. These are separate from the
MCP role token in `MCP_MONITOR_TOKEN`. Use one observer per enabled role and the same reviewed
role view as that role’s baseline. Setup requires an HTTP app and a plan allowing custom
metrics. Local setup saves configuration; start its process with `run`.
Use `--managed` for platform scheduling as described below. Alert rules require
explicit opt-in.

You can configure up to 16 independent roles per app, including the legacy
`default` role. Each role has its own baseline, revision, freshness limit and last
32 receipts. Role names use lowercase letters, digits, underscores or hyphens,
start with a letter, and contain at most 32 characters. Omitting `--role` selects
`default`; existing single-role commands remain supported.

```sh
gregale mcp monitor setup --app APP --role reader --baseline reader.lock.json --token-env MCP_READER_TOKEN
gregale mcp monitor setup --app APP --role admin --baseline admin.lock.json --token-env MCP_ADMIN_TOKEN
gregale mcp monitor run --app APP --role reader --json
gregale mcp monitor run --app APP --role admin --json
gregale mcp monitor status --app APP --all-roles --json
gregale mcp monitor disable --app APP --role admin
```

Run the two observers as separate supervised processes. `setup --token-env` stores
only the environment variable name; the observer loads its value locally. Named
roles with no reference use anonymous discovery. A run-time override must match
the stored reference; reconfigure the role to change it. The legacy default role
without a stored reference continues to accept `run --token-env`.

All enabled roles contribute to app status. Any unavailable role makes the app
summary unavailable, even if another role is healthy or drifted. Known drift in a
fresh role still triggers drift alerts while another role is unavailable; alerts
include separate affected-role lists and per-role evidence links. The unavailable
metric counts unavailable enabled roles; drift counts changes in fresh drifted
roles. Drift cannot resolve as healthy while an enabled role remains unavailable.
Disabling a role excludes it from aggregation and preserves its history.

Read one role with `GET /v1/apps/APP/mcp-catalog-monitor?role=reader`, or the app
summary with `?all_roles=true`. Exact named-role evidence uses both
`?role=reader&evidence=UUID`. Baseline updates, disabling and observations affect
only the selected role. A revision from another role is rejected.

Each check initializes and lists the advertised tools, resources/templates and
prompts. It executes no tools, reads no resource contents, and renders no prompts.
Apid computes the comparison and retains the last 32 state/catalog transitions,
including recovery. `status` includes those receipts; exact retained evidence is
available through `GET /v1/apps/APP/mcp-catalog-monitor?evidence=UUID`. Repeated
unchanged checks refresh the heartbeat without adding history rows. Differences
may be truncated while preserving the full change count. These observations
cover the configured roles’ visible catalogs, not unconfigured roles or all replicas.

Configure webhook alert rules for `mcp_catalog_drift > 0` and
`mcp_catalog_monitor_unavailable > 0`, or use their presets. Existing cooldown and
recovery handling applies. Missing, failed, stale, and future-dated observations
cannot clear drift as healthy. Drift alerts include exact retained evidence links;
raw errors and client tokens are not retained or included in alert payloads.
Metrics are instantaneous, even when a rule carries a window specification.

Source failures are reported as unavailable and checked again on the next tick.
A failed report or changed baseline stops the loop; supervise and restart the
process to reload configuration. Keep interval plus twice the timeout below the
freshness limit. Monitoring requests can wake a scale-to-zero app and incur
runtime usage. Retained history is bounded and disappears with app/account
deletion; export needed receipts before they age out of the 32-transition history.


### Platform-managed catalog observers

Apid can schedule catalog checks without a customer-maintained CLI process.
Configure each role with `--managed` and its reviewed complete baseline:

```sh
gregale mcp monitor setup --app APP --role reader --managed --baseline reader.lock.json --secret-ref secret:MCP_READER_TOKEN --interval 30s --timeout 10s --freshness-seconds 90
gregale mcp monitor status --app APP --all-roles --json
gregale mcp monitor pause --app APP --role reader
gregale mcp monitor resume --app APP --role reader
gregale mcp monitor restart --app APP --role reader
gregale mcp monitor stop --app APP --role reader
```

`secret:NAME` selects an existing customer secret in the app's `default` scope.
Only the reference is stored in observer configuration; apid decrypts the current
sealed value for each check. Rotation takes effect on the next check without
restarting the observer. A missing, empty or undecryptable secret produces an
unavailable observation, never an anonymous fallback. Omit the reference for an
explicitly anonymous observer. Platform-managed database and object credentials
cannot serve as MCP token references. Starting or resuming a credential-bound
observer requires deploy-write and secrets-read scopes (or admin). Pausing and
stopping require deploy-write. Local `token_env` and managed secret references
cannot be combined.

Targets are the app's canonical public HTTPS origin and a relative endpoint path.
Custom URLs, internal-only apps and redirects are excluded. Requests use the
platform's egress guard, including dial-time DNS/IP checks. Each check initializes
and discovers catalogs only. It does not invoke tools, read resource contents or
render prompts. Like local polling, these requests can wake a scale-to-zero app
and incur normal runtime usage.

Pausing disables the role's collection and excludes it from required app health.
Resume/restart issues a new revision and schedules a fresh check; previous receipts
remain in history. Stop disables the managed observer and role. A local reporting
loop cannot publish observations for an enabled managed observer.

Role leases, receipt writes and next-check times persist across apid restarts.
Expired leases are reclaimed with a new fencing token. Pause, stop, baseline or
credential-reference changes invalidate in-flight reports. Four checks run per
apid process at most. Intervals use positive jitter up to 10%; failures use bounded
exponential backoff, never polling more often than the requested cadence. Scheduling
is best effort under load; overdue checks and stale catalog evidence are visible
and cannot manufacture healthy evidence. Configured interval, jitter, five-second
poll and 45-second lease budgets must fit below the role's freshness limit.

`observer_status` reports last attempt, last successful check, next check,
consecutive failures and a categorical reason. Current scheduling and catalog worker status resets on
configuration changes; historical transition receipts remain available. Raw
source errors, response bodies and credential values are not retained in worker
status or logs. As before, observations are trusted telemetry for the configured
role view, not an attestation of every replica or a live authorization lease.

### OAuth renewal for managed observers

A managed role can use an OAuth refresh grant instead of `--secret-ref`. Obtain
an initial refresh grant from your provider and store it as an app environment-scoped
secret. Give each role its own grant and refresh secret; a rotating grant must
not be shared between independently scheduled roles. Gregale rejects reuse of a
refresh-secret reference by another role in the same app. Distinct references
containing the same provider grant are still the customer’s responsibility.

```sh
gregale mcp monitor setup --app APP --role reader --managed \
  --baseline reviewed-reader.json --freshness-seconds 90 \
  --oauth-token-endpoint https://identity.example.com/oauth/token \
  --oauth-client-id reader-client \
  --oauth-refresh-secret-ref secret:READER_REFRESH
```

For a confidential client add `--oauth-client-secret-ref secret:READER_CLIENT_SECRET`.
Confidential clients default to `client_secret_basic`; select `client_secret_post`
when required. Public clients send `client_id`.
Credential references require `secrets:read` or admin in addition to the normal
configuration permissions. OAuth and a static bearer `--secret-ref` cannot mix.
Only explicit public HTTPS endpoints are accepted. Private egress, environment
proxies and redirects are blocked; no discovery URL selects the refresh target.

Renewal and catalog discovery share the configured observer timeout and durable
lease. The observer refreshes when its access token has 60 seconds or less remaining.
It accepts Bearer responses with `expires_in` greater than 60 seconds and at most
one year, and retains an existing refresh token when the provider omits a new
one. Access and rotated refresh tokens are sealed in the role's control-plane
connection; they never become guest environment variables or public API fields.
The initial refresh secret remains required. Its deletion fails closed; updating
it resets the connection for reauthorization. Updating a client secret uses the
new value on the next renewal and retains the rotated refresh token.

`gregale mcp monitor status --app APP --role reader` shows connection state,
expiry and last successful renewal. `invalid_grant` sets
`reauthorization_required`, marks monitoring unavailable and suppresses repeated
refresh attempts. Obtain a new grant and update the referenced refresh secret to
recover. Other provider failures use the existing observer retry backoff.

Pause, resume, restart, stop and baseline review retain the connection when its
OAuth configuration is identical. Removing or changing OAuth configuration
clears the retained credentials. Stale jobs cannot commit renewed tokens after
reconfiguration or lease expiry. A crash after provider-side rotation but before
the fenced database commit may still require reauthorization: these two systems
cannot share an atomic transaction. Normal observer wake and runtime costs apply.

### Connect a managed observer through browser authorization

Configure a reviewed role with explicit OAuth endpoints and a client registered
for IPv4 loopback redirects with dynamic ports and S256 PKCE:

```sh
gregale mcp monitor setup --app APP --role reader --managed \
  --baseline reviewed-reader.json --freshness-seconds 90 --oauth-guided \
  --oauth-authorization-endpoint https://identity.example.com/oauth/authorize \
  --oauth-token-endpoint https://identity.example.com/oauth/token \
  --oauth-client-id reader-client --oauth-scopes 'read'
gregale mcp monitor connect --app APP --role reader
```

Add `--oauth-client-secret-ref secret:READER_CLIENT_SECRET` for confidential
clients using `client_secret_basic`. The provider must issue a refresh token; request
its required scopes (such as `offline_access`) and configure refresh grants there.
Responses without a refresh token are rejected. Guided connections cannot use an initial
`--oauth-refresh-secret-ref`. Both endpoints must be explicit HTTPS URLs; the
control plane refuses private egress and token endpoint redirects.

The CLI opens the browser and listens on `127.0.0.1` until authorization completes,
the five-minute deadline expires or you interrupt it. Browser callbacks must reach
the machine running the CLI. When browser launch fails, the CLI prints the
consent URL. The callback checks state and the control plane consumes it exactly
once before exchanging the code with its sealed PKCE verifier. Tokens are sealed
directly into the role connection and are not added to guest environment secrets.
Failed or expired attempts require a new connect/reconnect attempt.

```sh
gregale mcp monitor status --app APP --role reader
gregale mcp monitor reconnect --app APP --role reader
gregale mcp monitor disconnect --app APP --role reader
```

Status shows granted scopes, token expiry and renewal health. Providers may narrow
requested scopes; broader responses are rejected. With no requested scopes,
provider defaults are accepted and recorded. Connect and reconnect require
`secrets:read` or admin plus normal deploy intent and MFA. Disconnect requires
deploy intent and MFA and clears tokens and pending authorization, invalidating
in-flight observer writes. Disconnect removes Gregale’s local connection; provider-side
revocation must be performed at the provider. An enabled disconnected role becomes unavailable;
use `pause` if it should stop contributing to required app health.

Pause/restart retain the connection for identical OAuth configuration. Changing
OAuth endpoints, client binding or requested scopes clears it and requires a new
connection. A new connection also retires catalog evidence so the observer must
capture a fresh catalog under its new revision.

### OAuth provider profiles and compatibility checks

Guided setup accepts `--oauth-profile generic|google|oidc`. The default generic
profile preserves existing behavior. Google adds `access_type=offline` and
`prompt=consent`, uses POST client authentication for confidential clients by
default and recognizes Google's canonical email/profile scope aliases. OIDC adds
`openid` and `offline_access` to requested scopes and asks for consent. These
profile scopes count toward the 16-scope limit. Endpoints remain explicit and are
never replaced by a profile or metadata response.

Use `--oauth-token-auth-method none|client_secret_basic|client_secret_post` when a
provider needs an explicit authentication method. Public clients must not bind a
client secret; both confidential methods require `--oauth-client-secret-ref`.
The selected method applies to code exchange and renewal. Missing credentials
fail closed. Request the scopes and enable the provider settings needed to issue
refresh tokens; a profile does not establish live provider support.

For providers requiring an exact redirect URI, configure
`--oauth-callback-port 34567` during setup and register
`http://127.0.0.1:34567/callback`. Connect uses that port and fails before opening
the browser if it is occupied. `connect --callback-port PORT` can choose a port
for a dynamic configuration; it cannot override a different configured fixed
port. The callback host remains IPv4 loopback and its path remains `/callback`.

Set `--oauth-resource https://APP.DOMAIN/mcp` to send the reviewed MCP resource
indicator in authorization, token exchange and renewal requests. The resource
must be recognized by your provider and validated by your app; it is not the
observer's probe target. Leave it empty for providers without resource support.

Pair `--oauth-issuer https://identity.example.com` with
`--oauth-discovery-url https://identity.example.com/.well-known/oauth-authorization-server`
to enable metadata checks. Supply the correct explicit OAuth or OIDC metadata URL
for your provider. Then run:

```sh
gregale mcp monitor provider-check --app APP --role reader
gregale mcp monitor connect --app APP --role reader
```

Checks cover the reviewed issuer/endpoints, S256 PKCE, authentication method,
code response, authorization-code/refresh grants and scopes. Configured discovery
also runs before a new browser flow. Mismatched endpoints, declared incompatible
capabilities and unavailable metadata block consent without creating state.
Incomplete declarations and unadvertised scopes are reported as `unverified` and
permit the actual flow; metadata may omit valid provider API scopes. Checks use
public guarded requests without credentials, proxies or redirects. Output uses
categorical reasons, never source error bodies. Metadata checks retain the
connection API's deploy intent, MFA and secret-read permissions.

Changing profile, authentication, resource, discovery or callback configuration
retires the previous binding and requires reconnecting. Metadata checks cannot
prove real token issuance, refresh, revocation or resource enforcement. Live
provider acceptance remains a separate verification step.

### OAuth disconnect and provider retirement

Configure `--oauth-revocation-endpoint https://identity.example.com/revoke` during
managed OAuth setup to enable RFC 7009 retirement on guided disconnect. Optional
`--oauth-revocation-auth-method none|client_secret_basic|client_secret_post` selects
provider authentication; the default matches token authentication except Google
uses none. Confidential methods require a client secret reference.

`gregale mcp monitor disconnect --app APP --role reader` clears the local connection
and queues provider retirement atomically. Its output reports local disconnection
and provider revocation separately. `gregale mcp monitor status --app APP --role reader`
shows `pending`, `provider_acknowledged` or `manual_required`, attempt counts and
recent OAuth lifecycle events. Provider acknowledgement includes tokens already
invalid; it does not independently verify all provider grants have been removed.

Jobs survive pause, stop, plan changes and observer configuration replacement.
The worker persists refresh and access token acknowledgements separately, retries
failures up to eight times with backoff capped at one hour, and discards sealed
credentials at completion or exhaustion. Missing revocation configuration requires
manual provider retirement immediately. Reconnect is blocked while a job is pending.
Wait for acknowledgement, or revoke manually at the provider, before deleting an
app: app deletion removes the job. Disconnect before changing OAuth configuration
when the previous grant must be retired; configuration replacement does not revoke
old provider grants automatically.

Status flags reauthorization when the provider rejects the refresh grant, or when
credential failures occur within fifteen minutes of token expiry. Healthy renewal
starts before the next observation interval plus clock skew. Reminders are status
indicators; existing credential-unavailable monitoring can drive opt-in alert rules.
No alert rule or external notification is enabled automatically. Audit history
contains at most 64 metadata events per role and never includes credential values,
provider response bodies or sealed queue data.

### Durable OAuth retirement and safe replacement

OAuth retirement now survives app deletion. Configuration changes retain the old
sealed grant for retirement; reconnect installs the new grant and queues the old
grant in the same transaction. Deleting an app captures its active grants before
clearing local connection and callback state. Restoring that app requires reconnecting.

The worker holds retirement when a matching provider/client binding has an active
connection or an in-flight exchange. Some providers revoke the entire grant, so
retiring old tokens can invalidate a replacement. Paused connections also count.
Use administrator-only account history to inspect records, including deleted apps:

```sh
gregale mcp retirements list
gregale mcp retirements list --offset 50
gregale mcp retirements retry --id RETIREMENT_UUID
```

To proceed despite an active connection after reviewing provider behavior:

```sh
gregale mcp retirements retry --id RETIREMENT_UUID --allow-active-grant-revocation
```

For provider-console revocation, record your manual acknowledgement:

```sh
gregale mcp retirements acknowledge-manual --id RETIREMENT_UUID
```

History distinguishes `awaiting_issuance`, `pending`, `held`, `installed`,
`manual_required`, `provider_acknowledged`, `manually_acknowledged`, and `expired`.
A code exchange reserves durable capacity before issuance and persists observed
tokens before validating scopes or activating the connection. Failed activation
leaves those tokens for retirement. Lost provider responses or failures before
persisting token values become `issuance_unknown`; the platform cannot prove cleanup
without those token values, and provider-side investigation may be necessary.

Failed jobs retain sealed data for at most seven days. Retry preserves completed
refresh/access acknowledgements, resets the attempt budget, and requires retained
credentials plus a configured endpoint. Terminal acknowledgements and retention
expiry clear sealed data. Metadata remains for thirty days. Each account can have
64 outstanding records; capacity exhaustion prevents new issuance or credential
removal until records are resolved. No API or CLI response includes token values,
sealed data, lease tokens or provider error bodies.

These controls replace the earlier requirement to wait for acknowledgement before
app deletion. They also replace discarding old grants on OAuth configuration changes.

### Durable OAuth renewal recovery

Observer refreshes now persist renewal intent before contacting the provider,
then seal and save the returned grant before installing it. After a restart or
lease change, a saved result is installed without replaying the refresh request.
An expired saved access token is renewed using its saved rotated refresh token.

Monitor status exposes `oauth_renewal.state` and `started_at`; CLI status prints
`OAuth renewal recovery`. States are `pending`, `saved`, `installed`, `failed`,
and `reauthorization_required`. Private operation IDs, source digests, lease IDs
and sealed results are omitted from responses.

If the provider response is lost, malformed, or the process crashes before saving
it, the next worker requires reauthorization. Gregale does not automatically reuse
a potentially consumed refresh token. Reconnect a guided role, or update the
referenced refresh secret for an externally provisioned grant. A received negative
provider response still uses normal retry backoff; `invalid_grant` requires
reauthorization. Identical OAuth configuration preserves recovery across pause,
resume and restart. Reconnect and disconnect clear the previous journal.

This replaces the previous renewal limitation for results already saved locally.
A crash before result persistence, or a response fenced by reconfiguration or app
deletion, can still require provider-side investigation and reauthorization.

### OAuth credential changes

Replacing a refresh secret now atomically retires the old retained
grant and any saved renewal result. Old worker leases and callbacks are fenced by
a new role revision. The next observer uses the replacement secret; it waits for
pending retirement to become held before beginning another exchange. Retirement
stays held while a replacement is intended, active, or being issued. An
administrator can explicitly permit active-grant revocation through the existing
retirement recovery command; this may invalidate the replacement at the provider.

Client-secret updates preserve installed grants and saved results while fencing
old workers. An unfinished exchange requires reauthorization. Deleting a refresh
or client secret retires its affected grants and reports `reconnect_required`.
Restore the required secret and reconnect guided roles, or let an externally
provisioned refresh grant renew using its new secret generation.

Status includes `oauth_lifecycle.credential_state`: `credential_changed`,
`retirement_held`, or `reconnect_required`. CLI status prints `OAuth credential
recovery`. Successful connection installation clears the local change marker;
held retirement remains visible in account recovery history. Host-key resealing
preserves the secret generation and does not reset the connection. Nondefault
scopes and unrelated secret keys do not affect the connection.

Unfinished renewals retain unknown-issuance retirement metadata after a credential
change. Late responses can supply sealed grants to that record but cannot install
them on the changed role. Missing or lost responses still require provider-side
investigation. Retirement-capacity exhaustion rejects the secret mutation before
old credentials or recovery intent are lost.

### Connection health alerts and fleet recovery

`gregale mcp health` lists actionable OAuth issues across the account, including
retirement records for deleted apps. Use `--app APP` to filter a live owned app
and `--offset N` to read subsequent issue pages. The administrator-only
`GET /v1/mcp-connection-health` endpoint returns counts, issue metadata, recovery
actions and links to existing monitor and retirement controls. Tokens, provider
responses, source digests and private worker identifiers are never included.

Enable the existing app alert-preset workflow with these preset names:

- `mcp_oauth_reauthorization_required`: connect, reconnect or restore credentials.
- `mcp_oauth_renewal_failed`: failed renewal, unavailable credentials or expired
  active access tokens awaiting renewal.
- `mcp_oauth_retirement_expiring`: unresolved cleanup reaches its last 24 hours
  of sealed retention, or that retention has expired.
- `mcp_oauth_issuance_unknown`: an exchange response was lost or its lease expired
  without a saved result; investigate at the provider before reconnecting.

The metrics count current issues in the rule's app or account scope. Presets use
`gt 0` with a fifteen-minute cooldown and support webhook notification only.
Successfully delivered incidents do not repeat while their issue set is unchanged.
Changed issue sets and failed deliveries follow ordinary cooldown behavior.
A complete observation below the configured threshold sends a signed
`alert.resolved` notification with the original `incident_id`, independently of
firing cooldown. Failed recovery notifications retry after cooldown.

Issue pages contain at most fifty entries, while counts cover the observed
inventory. Above 4096 source rows, `complete=false` marks a partial inventory;
filter by app to narrow the view. Incomplete or failed observations degrade alert
sampling and cannot resolve an incident. Renewal `outcome=issuance_unknown`
distinguishes lost responses from recorded provider responses. Older ambiguous
reauthorization state is reported as an authorization requirement.

### Compatibility preflight

Run a starter locally, then validate its Gregale stateless hosting profile:

```sh
gregale mcp validate --url http://localhost:3000/mcp --json
gregale mcp deploy --path . --name my-mcp --validate-url http://localhost:3000/mcp
```

Use `--token-env MCP_ACCESS_TOKEN` for private endpoints. `--timeout 30s` bounds
validation. Initialization, version negotiation, session behavior, advertised
catalogs, tool schemas and protocol response headers must pass. Failures exit 1;
reports contain actionable checks without remote error bodies.

To inspect every running replica through the existing account-scoped protocol API:

```sh
gregale mcp validate --app my-mcp --deployment DEPLOYMENT_ID --url https://my-mcp.example.com/mcp --endpoint-path /mcp
```

The deployment preflight checks an independently running endpoint before source
upload; it does not prove that process matches the source being uploaded. Existing
candidate qualification still checks the deployed revision before promotion.
Validation does not execute tools, read resource contents or render prompts. Those
application behaviors need explicit fixtures. Authorization rejection and role
visibility remain covered by release-policy expectations.

### Application smoke fixtures

Save reviewed application assertions in `gregale-mcp.tests.json`:

```json
{
  "version": 1,
  "cases": [
    {"name": "addition", "kind": "tool", "target": "add", "arguments": {"a": 1, "b": 2}, "expected_result": {"content": [{"type": "text", "text": "3"}]}},
    {"name": "greeting resource", "kind": "resource", "target": "greeting://hello", "mime_type": "text/plain"},
    {"name": "greeting prompt", "kind": "prompt", "target": "greeting", "prompt_arguments": {"name": "Ada"}, "message_roles": ["user"]}
  ]
}
```

```sh
gregale mcp test --url http://localhost:3000/mcp --fixtures gregale-mcp.tests.json --allow-tool-execution --json
gregale mcp deploy --path . --name my-mcp --fixtures gregale-mcp.tests.json --allow-tool-execution
```

Tool fixtures require explicit `--allow-tool-execution` even if a tool advertises
`readOnlyHint`. Each fixture executes once without automatic retries. Use dedicated
test data: tools, resource reads and prompt rendering can contact downstream
systems. Explicitly rerunning a suite or deployment repeats its operations.
`--timeout 30s` sets the standalone suite deadline; deployment suites have a thirty
second deadline. Suite files must contain 1–64 cases and fit within one MiB.

`expected_result` compares the whole RPC result as decoded JSON, ignoring object
key order and preserving numeric spelling and precision. It is required for tools
and optional for resources/prompts. Resource fixtures require nonempty contents
with the requested URI, expected MIME type and text or blob. Prompt fixtures
check the ordered message roles and content structure. Reports omit returned
application data and fixture arguments.

For private endpoints, add `--token-env MCP_ACCESS_TOKEN` and authentication cases:

```json
{"name": "reject missing bearer", "kind": "auth", "credential": "missing", "expected_status": 401}
```

Use `"credential": "malformed"` to check a known invalid bearer. Fixtures contain
no credentials. Insufficient-scope and role-visibility expectations remain in the
release policy. Deployment suites run on the candidate preview before promotion;
a failure leaves its production traffic at zero. Preview tests do not prove that
every replica was exercised.

### Production request diagnostics

```sh
gregale mcp diagnostics --app my-mcp --route /mcp --since 1h --json
```

The command reads at most 200 retained request rows, counts aggregate rows by their
represented request count, and groups HTTP outcomes. It returns failure request
IDs, trace IDs and links to the existing request evidence API. Follow a request with
`gregale debug requests inspect my-mcp REQUEST_ID` for execution evidence and log
lookup guidance. Use `--cursor` to continue an incomplete page. Counts describe the
page, not an unobserved whole window. Plan retention clamps are reported.

`--route` is the retained route label; manifest route patterns may differ from a
literal endpoint path. Apps without specific retained route labels may use
`__other__`, which can include unrelated traffic. The existing request debugger's
plan, account ownership, scope and MFA requirements apply. Empty telemetry is not
proof that the endpoint is healthy.

Operator Prometheus metrics on gatewayd-internal:

- `gateway_mcp_requests_total{app,method,outcome}`: completed transport outcomes.
- `gateway_mcp_request_duration_seconds{app,method}`: ingress through completion,
  including wake time and streamed response lifetime.
- `gateway_mcp_active_forwarding_streams{app,method}`: active MCP-header requests
  using the streaming forwarder, including JSON responses on that path.

Method labels are bounded hints from `MCP-Method`. Requests carrying only
`MCP-Protocol-Version` use `other`; requests without either header are excluded.
Headers are caller supplied and do not prove which JSON-RPC method was executed.
Failure logs include existing request and trace IDs, with no arguments, bearer
tokens or returned application content. `interrupted_stream` records an aborted
streaming forwarder; timeout and cancellation have separate outcomes.

HTTP 200 can still contain a JSON-RPC or tool error. These metrics do not parse
response bodies. Retained CLI diagnostics classify HTTP status and do not expose
method hints or all post-header stream failures; correlate operator metrics/logs
with retained request evidence when investigating those cases.

### Starter protocol telemetry

Node, Go and Python starters emit one structured `mcp_protocol_request` log event
for each observed RPC request. These events classify protocol results rather than
HTTP status: a tool returning `isError: true` produces `tool_error`, even when
HTTP returns 200. RPC error objects produce `rpc_error`; normal results produce
`success`, and Task creation results produce `task_created` (queue admission, not
completion of background work). Observed interruption or deadline expiry before
a response produces `cancelled` or `timeout`. Go also identifies post-parse
entry authorization rejection as `authorization_error`. A request with no
observed RPC result or explicit termination remains `unobserved`.

`method` uses a fixed vocabulary for supported MCP methods, plus `other`.
`tool` uses the four built-in tool names, `other` for application-added/unknown
tools, and `none` for non-tool requests. Arguments, result contents, resource
URIs, prompt names, error messages, customer identity and JSON-RPC IDs are never
included. Do not use request/trace IDs as counter or histogram labels.

`execution_duration_ms` measures parsed request dispatch through construction of
its RPC response. For subscriptions it ends at the subscription acknowledgment,
so a long-lived subscription does not inflate setup latency. It is omitted when
that completion was not observed. `stream_duration_ms` measures an SSE response
from response headers through completion or interruption and is omitted for
non-SSE responses. These durations can overlap; do not add them together.
`stream_outcome` reports completion, observed interruption or internal delivery
failure independently of
an already constructed RPC outcome, preserving a tool error or successful result
when delivery later fails. Typed context/transport timeouts are distinguished;
telemetry does not impose a new tool execution deadline.

Validated `X-Faas-Request-Id` UUIDs and nonzero version-00 W3C `traceparent` IDs
appear as `request_id` and `trace_id`. Duplicate or malformed headers are omitted.
These correlation values are not authorization evidence and may originate from
the caller. Use them with `gregale debug requests inspect APP REQUEST_ID`, or
correlate the trace with existing request evidence and guest logs.

Aggregate only `mcp_protocol_request` events into counters and duration
histograms. Node's existing `mcp_tool_call` events remain callback diagnostics;
counting both event families would double-count tool calls. Pre-parse failures,
authentication rejected before dispatch, notifications without an RPC ID and
worker-side Task execution are outside this event's coverage. Telemetry is
process-local structured logging, not a public metrics endpoint or a retained
protocol-outcome database. Existing request retention and log access policies
still apply; `mcp diagnostics` continues to report transport evidence.

### MCP operations dashboard

Open `/dashboard/mcp` in an authenticated dashboard session. The page combines
connection-health inventory, per-role catalog freshness, gateway policy replay
and admission receipts. Use the application filter to narrow the view. Current
dashboard session and MFA checks apply. Recovery forms also require an account-bound CSRF token.

Connection health shows observation time, issue counts, incomplete-inventory
warnings and next-page navigation independently from the application inventory.
Deleted applications with retained OAuth retirement issues remain visible.
Unavailable sources are labeled unavailable and do not become zero/healthy
results. Catalog state uses the existing role-aware freshness evaluation;
disabled or unconfigured monitoring is distinct from a healthy observation.

Application pages contain at most 25 rows. Each application's newest deployment
shows its latest retained accepted and rejected admission separately. These are
not an account-wide admission history or proof of current fleet health. Follow
an admission link for its outcome, captured time, traffic percentage and rejection
reason. Older deployments remain accessible through application deployment
history. Receipt reads enforce account ownership, including direct links.

Request investigation, application logs and access-rule links lead to the existing
protected dashboard pages. Browser reauthorization and guarded renewal recovery are described below; roles
without browser configuration can still use CLI recovery.
The account-wide grant retirement ledger offers a queued revocation retry and
manual resolution after explicit provider confirmation. These actions reuse the
API ownership, retention, terminal-state and live-lease checks. Active-grant
revocation is never enabled by the dashboard. Unknown issuance without retained
tokens cannot be retried; provider confirmation must precede manual resolution.
A successful submission redirects to the ledger showing the resulting state.
The page does not retry consumed token exchanges, promote traffic or execute tools.
Stored OAuth material, catalog bodies/diffs and raw source errors are excluded
from the dashboard view models. Responses use `Cache-Control: no-store`.

### Browser reauthorization and renewal recovery

For a guided OAuth observer role, register this exact redirect with the provider:
`https://<configured-console-origin>/dashboard/mcp/oauth/callback`. Configure it
with `mcp monitor setup --oauth-browser-redirect-uri <URL>` alongside the existing
reviewed baseline, observer settings and guided OAuth client configuration. The
URL must match Gregale's configured public console URL, use HTTPS, and have no
port, query, fragment or encoded path. Existing CLI loopback callbacks remain
supported and retain their separate validation.

Open `/dashboard/mcp`, expand **Recover OAuth** for the role and select
**Reauthorize**. The flow uses PKCE, expires after five minutes and is bound to
that account, browser session, app, role and configuration revision. Starting
another flow replaces the browser's pending callback cookie. Active browser
flows pause observer claims for that role until completion or expiry. A denied
request exchanges no code; successful, denied, expired, failed and uncertain
outcomes return to the dashboard without provider error details or code values.

If issuance is uncertain, resolve the grant at the provider and record manual
resolution in the retirement ledger before reconnecting. Confirm provider
resolution for an uncertain renewal before starting new authorization. An
in-flight or consumed flow can refuse a new attempt until its five-minute expiry.
If an uncertain grant record has already expired, its sealed credentials cannot
be recovered; resolve it at the provider and check the confirmation before
starting fresh authorization. The expired record remains visible as historical
evidence. Authorization code exchanges are single use and are never automatically retried.

After correcting provider credentials, **Queue renewal recovery** schedules the
observer immediately when a retry is safe, or lets it install an already recorded
response. Pending/unknown exchanges, active leases and flows, paused monitoring,
and credentials requiring reauthorization refuse this action. API clients can
submit `action: retry_renewal` with the role and current revision to the existing
OAuth endpoint. A successful submission means recovery was queued; monitor
connection health to confirm its outcome.

### Protocol performance charts

Open **MCP operations**, select an application's **Protocol performance** link,
and review the last hour's call traffic, failures, execution latency, and stream
latency. The charts aggregate starter `mcp_protocol_request` events at host log
intake, independently of log viewers. Execution p50/p95/p99 and stream p95 need
at least 20 measurements; missing samples stay missing. The displayed last-event
time shows observation recency. An idle app may have no recent events. Long-lived
streams report their measurements when delivery ends.

Only reviewed starter tool names receive individual labels; other custom tools
share `other`, and other RPC methods share `none`. Arguments and results never
enter these metrics. These are app-attributed, starter-reported observations,
not an exact request ledger or authorization evidence. Background tasks,
notifications, and pre-dispatch rejects are outside their coverage.

Platform operator alerts cover sustained error ratios above 10%, execution p95
above five seconds, and aggregation capacity drops. Traffic alerts require at
least 20 observations per five-minute window and ten minutes of sustained
failure. These defaults use existing Alertmanager routing; customer alert-rule
configuration is not extended by this feature. See
[the performance runbook](runbooks/FaasMCPProtocolPerformance.md) and
[ADR 20261017191506](adr/20261017191506-mcp-protocol-performance.md).

### Customer performance alerts

On **MCP operations → Protocol performance**, open **Add performance alert**.
Choose RPC/tool error rate (%) or execution p95 (ms), enter a threshold, select a
5m/15m/1h rolling window, and configure minimum observations and sustained minutes.
Defaults are 20 observations and 10 sustained minutes. Latency counts measured
executions; error rate counts parsed RPC outcomes. Background task execution,
notifications and pre-dispatch rejects remain outside coverage.

Provide a webhook URL and signing secret. Existing alert plan limits, quotas,
webhook signatures, retry handling and cooldowns apply. The page can edit thresholds
and traffic settings or enable/disable a rule. Open the rule to inspect deliveries.
These starter-reported signals support webhook notifications only.

Each 30-second historical check must have enough measurements for the full
sustained period. Sparse or missing data and incomplete sustained history show as
`unknown`; query failures show as `degraded`. Neither means healthy zero traffic,
and neither sends an idle-app alert. Observation recency is shown separately in
the charts. Newly created rules wait for their configured duration; editing or
re-enabling evaluates the new settings against retained history.

The alert API accepts `mcp_protocol_error_rate_pct` and
`mcp_protocol_execution_p95_ms`, with optional `mcp_min_calls` (20..1000000) and
`mcp_for_minutes` (1..60). Omit these fields for other metrics. MCP rules require
app scope, webhook action, gt/gte comparison and a supported window. Error-rate
thresholds are 0..100; latency thresholds are 0..300000 ms. The Node and Python
SDKs expose the same fields. See [ADR 20261017191507](adr/20261017191507-customer-mcp-performance-alerts.md).

### Independent telemetry coverage

Updated Go, Python and Node web starters emit a minimal heartbeat every 30 seconds,
including while RPC traffic is idle. The MCP operations dashboard compares
host-attributed reports with scheduler inventory, showing reporting, partially
reporting, starting, stale, unavailable or idle coverage. Paused/frozen/warm
instances do not require a heartbeat. Heartbeats are process telemetry, not MCP
readiness or evidence of successful tools. Existing deployments need the updated
starter module and startup wiring.

Set `MCP_TELEMETRY_LOSS_ALERT=true` to opt into operator telemetry-loss alerts
through the platform's Alertmanager. This is separate from customer performance
webhooks. Startup/resume has a three-minute grace period; heartbeats older than
two minutes must remain missing for five minutes before alerting. See
[the performance runbook](runbooks/FaasMCPProtocolPerformance.md#telemetry-heartbeat-loss)
for missing host scrapes and coverage limits.

### Public deployment readiness

Check a zero-traffic candidate or serving deployment through its public preview
endpoint without moving traffic:

```sh
gregale mcp readiness --app my-mcp --deployment <deployment-uuid> \
  --token-env MCP_PROBE_TOKEN --baseline gregale-mcp.lock.json --json > readiness.json
```

Omit `--token-env` for public apps. For private apps, use a dedicated MCP client
credential with only the scopes needed to initialize, ping and list the intended
catalog. The CLI's platform API key is used only for deployment lookup. Probe
tokens stay in the environment and are excluded from reports. `--legacy` selects
the legacy protocol; `--endpoint` defaults to `/mcp`; `--timeout` is at most 30s.

The probe validates initialization, protocol response headers, statelessness,
ping and catalog metadata (including resource/prompt-only servers). It refuses redirects, applies bounded response and
pagination limits, and exits nonzero on failure. `--baseline` is optional; when
supplied it requires an exact catalog match, including additions. Format-2 locks
also check other advertised catalog metadata. No tool is invoked, resource
content fetched or prompt rendered. The report contains fixed failure categories
and a hash of the observed contract, without private definitions or response bodies.

`mcp deploy` includes these fresh public readiness results for each release role
in its deployment receipt and blocks promotion if any fails. It also retains its
existing exact-replica and guarded traffic checks. A saved standalone report is
point-in-time evidence and cannot authorize promotion. Local results are CLI/CI artifacts. Use the platform recording mode below for
durable dashboard evidence and server-side promotion enforcement.

### Enforce readiness on platform traffic changes

An optional app policy makes readiness mandatory across API traffic changes,
automated deployment activation, canaries and rollbacks. The platform runs the
probe itself; uploading a local success report cannot satisfy the gate.

1. Review a role's catalog lock and use local `mcp readiness --baseline` to obtain
   its canonical `catalog_format` and `catalog_sha256`.
2. Read `GET /v1/apps/{slug}/mcp-readiness-policy`, then configure policy with
   `PUT` using its `revision` as `expected_revision`. Initial revision is empty.
3. Deploy a zero-traffic candidate and record each role's platform probe:

```sh
gregale mcp readiness --app my-mcp --deployment <uuid> --record \
  --role reader --token-env MCP_READER_PROBE_TOKEN --json
```

Example policy request (replace the digest with the reviewed canonical hash):

```json
{
  "expected_revision": "",
  "enabled": true,
  "endpoint": "/mcp",
  "max_age_seconds": 300,
  "roles": [{
    "name": "reader",
    "protocol_version": "2026-07-28",
    "catalog_format": 2,
    "catalog_sha256": "<reviewed-64-character-sha256>"
  }],
  "reason": "Require reviewed reader readiness before release"
}
```

Policy changes and emergency overrides require admin scope and applicable MFA.
Probes require deployment-write scope and the separate MCP credential. Platform
policy determines the endpoint/protocol/baseline with `--record`; omit local
`--baseline`, `--endpoint` and `--legacy` flags. `mcp deploy` automatically records
platform evidence when policy is enabled; release-role names must cover all
configured roles. Missing, failed, stale or configuration-mismatched evidence
blocks traffic increases with `mcp_readiness_required`. Existing traffic remains
serving. A row can lose traffic without readiness, but any compensating increase
for another revision still requires evidence or an override. Ordinary deploy APIs must create zero-traffic
candidates before activation when this gate is enabled.

`GET /v1/deployments/{id}/mcp-readiness` and the selected app's MCP dashboard show
retained role results and override audit. A recorded `passed` value describes
that observation; it is not a reusable promotion authorization. The gate also
checks age, current policy, artifact and runtime/secret versions at admission.
Changing policy invalidates all earlier results. A newer failed probe replaces
an earlier success; an older in-flight probe cannot overwrite it.

For emergency recovery, an admin can POST
`/v1/deployments/{id}/mcp-readiness-overrides` with `expected_revision` and a
nonempty `reason`. The platform binds the override to the current deployment
configuration for five minutes and consumes it with one traffic increase.
The account, reason, timestamps and consumption remain in the audit. Other
admission guards still apply. A new policy/configuration invalidates the override.

### Continuous readiness monitoring

Add `monitor` to each role in the existing readiness policy (`PUT /v1/apps/{slug}/mcp-readiness-policy`, admin scope and MFA). Preserve `expected_revision` and all existing policy fields when updating:

```json
{
  "name": "reader",
  "protocol_version": "2026-07-28",
  "catalog_format": 1,
  "catalog_sha256": "<reviewed 64-character SHA-256>",
  "monitor": {
    "enabled": true,
    "interval_seconds": 60,
    "failure_threshold": 3,
    "secret_ref": "secret:MCP_READINESS_TOKEN"
  }
}
```

Monitoring checks live deployments receiving traffic. Store the token as an app secret in the deployment's environment scope; the worker reads its latest version on each probe. Omit `secret_ref` for an anonymous role. Plaintext credentials and arbitrary target URLs are rejected. Checks perform initialization, ping, and catalog discovery without calling tools, reading resources, or rendering prompts.

Intervals range from 60 to 3600 seconds and thresholds from one to ten failures. With admission enabled, choose an interval no greater than half `max_age_seconds`. Monitoring can also run with admission disabled. Candidates at zero traffic still use explicit readiness probes before promotion.

The deployment readiness response includes `monitoring`: last success, next probe, failure count, state, and the last 20 checks. The MCP dashboard displays these fields and recent history. Policy or deployment-configuration changes reset the monitoring history for that subject. A missing credential reports `credential_unavailable`; server authentication rejection reports `protocol_failure`; expected-catalog mismatches report `catalog_drift`.

Create an app-scoped webhook alert rule with metric `mcp_readiness_unhealthy`, comparison `gt`, and threshold `0`, or select the **MCP readiness monitor unhealthy** preset in the alert catalog. The metric counts roles that reached their configured failure threshold or whose next probe is over 90 seconds overdue. Existing alert cooldown and recovery handling apply. Observation histories and tokens are excluded from webhook payloads.

Readiness alert rules can select an environment with `mcp_readiness_scope`. Omit it or use an empty string to monitor all environments (the behavior of existing rules and the preset); use `default` to monitor only the default environment. Other valid environment names match exactly. There is no fallback to another environment. Create separate rules for production and staging to configure independent thresholds, cooldowns, signing secrets, and webhook destinations. The selector is immutable: create a new rule to change environments, preserving the old rule's incident and delivery history.

For example, `gregale alerts add --app APP --name "Production readiness" --metric mcp_readiness_unhealthy --comparison gt --threshold 0 --window-spec 5m --mcp-readiness-scope default --webhook-url https://example.com/hook --webhook-secret-stdin` creates a default-environment rule (provide the signing secret on stdin). The MCP dashboard offers a readiness alert form for the selected environment and shows applicable all-environment rules. The API accepts the same selector on alert creation; PATCH may repeat the existing selector but cannot change or clear it. Other alert metrics reject nonempty selectors.

Readiness webhooks include `app_id`, the selected `mcp_readiness_scope` when present, and up to 50 matching `mcp_readiness` status summaries identifying environment (`scope`), deployment, role, and policy revision. A backend failure degrades evaluation without declaring recovery; environments with no monitored roles pause evaluation. These alerts send notifications and never move serving traffic.


Every failed scheduled probe immediately replaces the deployment's admission evidence. Consecutive-failure thresholds delay alerts, not admission enforcement. Monitoring never changes serving traffic automatically.

### Managed OAuth credentials for readiness

Readiness probes can use an existing managed OAuth connection instead of a static app-secret token. Connect the role through the MCP dashboard or existing catalog OAuth API, then use its current role and revision in the readiness policy:

```json
{
  "enabled": true,
  "interval_seconds": 60,
  "failure_threshold": 3,
  "oauth_connection": {
    "role": "reader",
    "revision": "<current managed connection revision UUID>"
  }
}
```

This object is the role's `monitor` configuration. It replaces `secret_ref`; the two credential sources cannot be combined. The connection must belong to the same app, be enabled and unpaused, and use the same endpoint path. The connection scope must match the deployment environment. Use `oauth_connections` for policies covering multiple environments; see the example below.

Scheduled checks refresh expiring tokens under the existing connection lease and persist rotated refresh tokens before probing. Catalog observation cadence is preserved. A revoked grant or uncertain exchange uses the existing reconnect/recovery flow instead of repeatedly exchanging a potentially consumed refresh token.

Explicit platform probes also use the managed connection:

```sh
gregale mcp readiness --app my-mcp --deployment <uuid> --role reader --record --json
```

Omit `--token-env` for an OAuth-bound role. Supplying a probe token header is rejected. The platform never exports the managed token. Local checks in a release policy continue to use their configured local credentials; platform evidence recording selects the managed connection automatically.

Monitoring and alert summaries identify the connection and return a `recovery_action`: `reconnect`, `review_binding`, or `review_connection`. The MCP dashboard links to connection health and OAuth controls. Reconnecting or changing connection intent creates a new revision; update the readiness policy's connection reference after reviewing it.

Connection changes invalidate readiness evidence, including results still being recorded. Ordinary token renewal under the same grant preserves the binding. Existing `mcp_readiness_unhealthy` alerts cover failures and stale monitoring, and serving traffic is never moved automatically.

### Separate OAuth grants for staging and production

Set a connection's environment with `gregale mcp monitor setup --scope staging
--role staging-reader ...` (alongside the existing baseline/provider flags).
Store its refresh source and confidential client secret in the same scope.
Connection role names are app-wide and their scope is immutable; create separate
roles for each environment. Existing roles stay in `default` when scope is omitted.
Pause, reconnect and disconnect continue to address the connection by role.

A readiness role can select a reviewed grant for each deployment environment:

```json
{
  "enabled": true,
  "interval_seconds": 60,
  "failure_threshold": 2,
  "oauth_connections": {
    "default": {"role": "production-reader", "revision": "<connection revision UUID>"},
    "staging": {"role": "staging-reader", "revision": "<connection revision UUID>"}
  }
}
```

Use this map instead of `oauth_connection` or `secret_ref`. Missing environments
fail readiness; they never inherit the production grant. Each referenced role
must already be enabled, use the policy endpoint and belong to the map's scope.
Reconnect changes the connection revision; review and update the pinned reference.
Readiness policy edits affect only the selected environment; see environment readiness policies below.
Nondefault catalog observers require an available deployment preview in that
exact environment. Dashboard connection rows identify the environment.

### Environment readiness policies

Readiness policies now have independent revisions per environment. Existing
policies remain in `default`. Updating staging's catalog expectations or pinned
OAuth connection requires new staging evidence; production evidence stays valid.

```sh
gregale mcp readiness policy get --app my-mcp --scope staging
gregale mcp readiness policy set --app my-mcp --scope staging --file staging-readiness.json
```

The file contains the existing policy fields, `expected_revision` from the selected
scope, and an audit `reason`. Its optional `scope` must match `--scope`. Each scope
can use a different endpoint, roles, catalog hashes and monitor configuration.
A single `oauth_connection` with the matching scope is sufficient; existing
`oauth_connections` maps remain readable and select only this policy's scope.

The policy API accepts `GET /v1/apps/{slug}/mcp-readiness-policy?scope=staging` and
PUT with `"scope":"staging"` in the request body. An optional query scope on PUT
must match the body. Deployment readiness and overrides always infer scope from
the deployment ID. The dashboard readiness environment filter selects the policy,
evidence, overrides and monitor statuses together.

For an app with readiness policies, new traffic in an unconfigured nondefault
environment is blocked. Save an environment policy explicitly, including a disabled
policy if that environment should opt out. Apps without readiness policies retain
existing behavior. Existing serving traffic and traffic decreases continue.

### Readiness policy history, diff and restore

Inspect policy revisions separately for each environment:

```sh
gregale mcp readiness policy history --app my-mcp --scope staging --limit 20
gregale mcp readiness policy history --app my-mcp --scope staging --before-revision <cursor>
gregale mcp readiness policy diff --app my-mcp --scope staging --from-revision <old-revision>
gregale mcp readiness policy diff --app my-mcp --scope staging --from-revision <old-revision> --to-revision <new-revision>
gregale mcp readiness policy restore --app my-mcp --scope staging --revision <old-revision> --expected-revision <current-revision> --reason 'Restore reviewed catalog expectations'
```

History contains author, time, audit reason and restore provenance. Diffs compare
configuration, including role catalogs and OAuth connection references, and ignore
role ordering and audit metadata. Omitting `--to-revision` compares against the
current policy and returns its captured revision for a subsequent CAS.

Restore writes a new revision and revalidates current OAuth bindings. It requires
admin access, applicable MFA, the expected current revision and a new reason.
Run fresh platform probes before increasing traffic: previous evidence and
emergency overrides do not qualify the restored revision. Restoring a disabled
configuration explicitly disables that environment's readiness gate.

The API provides `GET .../mcp-readiness-policy/history`,
`GET .../history/{revision}`, `GET .../mcp-readiness-policy/diff` and
`POST .../history/{revision}/restore`. Reads take `scope`; history supports
`limit` (1–50) and `before_revision`; diff takes `from_revision` and optional
`to_revision`. Restore takes `scope`, `expected_revision` and `reason` in JSON.
Dashboard readiness shows the latest 20 revisions for the selected environment.

### Readiness incidents and recovery notifications

Readiness rules now send paired, signed `alert.fired` and `alert.resolved` events.
Each incident has a stable `incident_id`; each logical event has its own
`event_id`, also used as the signed webhook ID. Delivery retries retain those
IDs and the payload's `occurred_at`. The transport signing timestamp is fresh
on every attempt. Receivers should deduplicate by event ID: transport delivery
is at least once, including recovery after a worker loses an acknowledgement.

Incident and event records are independent of webhook delivery. If a destination
is unavailable during failure, recovery, and another failure, all three logical
events are retained immediately. Each event is enqueued atomically with its
incident transition. Older or duplicate observations cannot rewrite newer state.
Successfully delivered failures do not repeat during a continuous incident outside
maintenance reconciliation; a
new breach after recovery receives a new incident ID.

The delivery queue retries after the rule's cooldown, including when monitoring
is unavailable. Notifications remain ordered per rule, so a failed earlier event
delays later notifications without delaying incident history. A two-minute lease
allows another worker to reclaim an abandoned attempt. Each dispatch has a
one-minute deadline; completion requires the current unexpired lease token, so
a late worker cannot acknowledge a newer attempt. Disabling a rule pauses future
claims and observation writes; re-enabling resumes its retained queue. Deleting
the rule deletes its incident history and queued events.

Recovery requires a complete, nonpending observation below the rule's threshold
with the same environment and role coverage as the failure. Empty results,
unavailable monitoring, and disappearing roles cannot establish recovery.
Replacement deployments or policy revisions can recover after their current
monitors report healthy; both event payloads retain their respective deployment
and policy context. Changed environment/role coverage leaves recovery unverified
until matching coverage is observable again. Existing pre-incident delivery rows
remain readable through ordinary alert deliveries; paired events start with the
next observed breach.

Inspect events and delivery attempts for one environment:

```sh
gregale mcp readiness incidents --app my-mcp --scope staging --limit 20
gregale mcp readiness incidents --app my-mcp --scope staging --before '<next_before>'
```

The read-only `GET /v1/apps/{slug}/mcp-readiness-incidents` endpoint accepts
`scope`, `before`, and `limit` (1–50, default 20). Omitting scope returns all
environments; the CLI defaults to `default` and accepts `--scope ''` for all.
Newest logical events come first, with an opaque `next_before` cursor. Retries
update delivery metadata without adding timeline rows or moving existing rows
between pages. `incident_status` (`open` or `resolved`) is independent of
`delivery_status` (`pending`, `delivering`, `failed`, `delivered`, or `suppressed`). The response
also includes `attempt_count`, optional `attempted_at`, `next_attempt_at` (the
event's earliest retry eligibility; earlier queued events can delay it), and
`last_delivery_id` for the ordinary delivery-attempt journal. A resolved incident
can therefore have a failed notification. The CLI returns these same fields;
the dashboard displays incident and notification status separately. Existing
lifecycle deliveries are imported with their stable IDs and latest attempt
status. Test deliveries are excluded. Results
contain at most 50 subject summaries per event, without observation histories or
credentials; environment filtering uses all observed scopes, including those
outside the bounded summaries. The MCP dashboard provides the same scoped
timeline and pagination.

### Readiness maintenance windows and temporary silences

Mute readiness notifications during planned work without disabling the rule.
Monitoring, readiness enforcement, and incident recording continue. A window
selects one exact environment; `default` means the default environment. A rule
spanning multiple environments is muted only when every observed environment is
covered by active windows. Use separate environment rules for independent
notification control.

Start a temporary silence immediately or schedule a future window:

```sh
gregale mcp readiness maintenance create --app my-mcp --scope staging --duration 30m --reason 'Upgrade staging database'
gregale mcp readiness maintenance create --app my-mcp --scope staging --start 2026-10-17T01:00:00Z --end 2026-10-17T02:00:00Z --reason 'Planned maintenance'
gregale mcp readiness maintenance list --app my-mcp --scope staging --limit 20
gregale mcp readiness maintenance cancel --app my-mcp --id '<window UUID>' --reason 'Work finished early'
```

Times use RFC3339 with a timezone. An omitted start begins now; the end must be
in the future and within 720 hours of the start. Past starts older than one
minute are rejected. Reasons must be nonblank UTF-8, at most 500 bytes. Windows
cover `[starts_at, ends_at)`; cancellation shortens that interval. Overlapping
windows remain effective until the last covering window ends or is cancelled.
Cancelling one window never cancels another. Cancellation retries retain the
original cancellation time, account, and reason; an expired window returns 409.

Muted logical events remain visible with `delivery_status: suppressed`,
`suppression_reason`, and `maintenance_window_ids`. They have no next retry time.
Pending and failed notifications covered by maintenance become suppressed;
notifications already claimed may finish. Suppressed events never replay after
maintenance. Failure, recovery, and a second failure during the window remain
separate incident facts. If a muted incident recovers just after maintenance
without any attempted failure notification, its recovery is also suppressed to
avoid sending an orphan recovery.

The first fresh unhealthy observation after maintenance sends one `alert.fired`
event on the existing incident, with a new stable event ID and
`notification_reason: maintenance_ended`. The following healthy observation can
send its paired recovery. Ordinary cooldown retries keep the new event ID.
Unavailable monitoring cannot create this notification or manufacture recovery.
Observation cursors and durable window history also reconcile windows crossed
while the evaluator was stopped, without replaying the muted backlog.

`GET /v1/apps/{slug}/mcp-readiness-maintenance` lists history for `scope`
(default `default`), with `before` and `limit` (1–50). The page includes expired
and cancelled windows, newest creation first. `POST` on that path creates a
window; `POST .../{id}/cancel` takes a cancellation reason. Changes require admin
scope and MFA, reads use the read surface scopes and MFA. Created/cancelled audit
events accompany the retained creator/cancellation attribution. The MCP
operations dashboard offers the same controls and shows the latest 20 windows
for its selected environment. Node, Python, and Go clients expose these APIs.

For rollout, apply the migration and upgrade every readiness evaluator before
creating maintenance windows. Older evaluator binaries do not recognize the
suppressed delivery state.

### Readiness notification queue health and recovery

Inspect delivery separately from MCP readiness:

```sh
gregale mcp readiness notifications list --app demo --scope production
```

The API is `GET /v1/apps/{slug}/mcp-readiness-notifications`, with optional
`scope`, `after` (rule UUID), and `limit` (1–50, default 20). It reports outstanding
and blocked counts, oldest age, the oldest event, retry time, attempt count, safe
failure category and `idle`, `ready`, `delayed`, `delivering`, `paused` or
`maintenance` state. An environment filter includes all-environment rules with
their **whole** queue. The `destinations` entries show independent receiver heads. Read scope and
MFA are required. Reads do not mutate events; maintenance state can describe
pending suppression until the evaluator next reconciles it.

After fixing a receiver, use the head's current `event_id` and `delivery_version`:

```sh
gregale mcp readiness notifications retry --app demo \
  --event EVENT_UUID --expected-version 2 --request-id REQUEST_UUID \
  --reason 'Receiver credentials repaired'
```

Supply a new UUID for a new decision. Reuse the same request UUID **and identical
arguments** when retrying an uncertain API response. The durable receipt records
the original account, time, reason and resulting delivery version. Retry makes
the head eligible for the next evaluator pass; it does not send inline or change
the event ID. Receivers still deduplicate the stable signed webhook event ID.

If a notification must never be retried, `notifications abandon` accepts the
same flags and permanently removes that event from delivery eligibility. This
allows the next queued event through, which may be a recovery whose failure was
abandoned. Incident facts and attempts remain in the timeline, with the last
operator action and reason. There is no automatic abandonment threshold.

`POST /v1/apps/{slug}/mcp-readiness-notifications/actions` accepts `request_id`,
`event_id`, `expected_version`, `action` (`retry` or `abandon`), and a nonblank
`reason` of at most 500 UTF-8 bytes. Admin scope and MFA are required. Actions
reject stale versions, non-head events, disabled rules, active leases, terminal
events and maintenance-covered work with 409. Retry additionally requires a
previous attempt that failed or whose lease expired. The API never revives
suppressed or abandoned notifications. Duplicate request keys with changed
payloads conflict. Refresh queue health before a new decision.

The MCP dashboard shows these queues, recovery forms and receipt attribution.
The Go, Node and Python clients expose the same contracts. Apply the migration
and upgrade **all readiness evaluators before using recovery controls**; older
binaries do not understand abandoned events or delivery-version fencing.

### Incident acknowledgement and finite reminders

Open incidents have separate acknowledgement and reminder state. Acknowledging
records who is handling an incident without changing readiness, moving traffic or
resolving it. Initial failure, recovery and maintenance-end notifications continue;
only reminders pause. An in-flight reminder may finish.

```sh
gregale mcp readiness attention list --app demo --scope production
gregale mcp readiness attention acknowledge --app demo --incident '<incident UUID>' \
  --expected-version 0 --request-id '<stable decision UUID>' \
  --reason 'Investigating provider outage' --expires-at 2026-10-18T12:00:00+03:00
gregale mcp readiness attention release --app demo --incident '<incident UUID>' \
  --expected-version 1 --request-id '<new stable decision UUID>' --reason 'Handing back to on-call'
gregale mcp readiness attention configure --app demo --incident '<incident UUID>' \
  --expected-version 2 --request-id '<new stable decision UUID>' --reason 'Remind during investigation' \
  --interval 15m --max-reminders 3
gregale mcp readiness attention history --app demo --incident '<incident UUID>' --limit 20
```

Use the current `version` from attention inspection for each new decision. Versions
also advance when a reminder is created; refresh after a 409. Reuse the identical
body and request UUID when retrying a decision after a lost response. Its immutable
receipt remains replayable after expiry, disablement or recovery. A different body
with the same app-scoped UUID conflicts. Reasons require 1–500 nonblank UTF-8 bytes.
Omit `--expires-at` for indefinite acknowledgement, or use an absolute future time
within 30 days. History retains the original account, time, reason and result.

Reminders are **off until configured**. New incidents capture their rule defaults;
existing incidents retain their captured policy or explicit override. Enable an interval from 1 minute
to 24 hours (whole seconds) and a lifetime creation limit of 1–10. Set both
`--interval 0s --max-reminders 0` to disable. Changing policy, releasing an
acknowledgement or disabling/re-enabling reminders never resets the used budget.
Suppressed and abandoned reminders consume it too. A new incident starts with a
new budget. Disabled rules can be acknowledged/configured but cannot dispatch.

Only fresh evaluator observations of the same incident coverage can create a
reminder. The incident must remain unhealthy, unacknowledged, outside maintenance,
past its next eligibility time, below its creation limit, with no outstanding notification for that incident.
Each pass creates at most one; downtime never creates a burst of missed reminders.
A reminder is a signed `alert.fired` event with `notification_reason: reminder` and
`reminder_number`. Retries keep the same logical event UUID and payload. The budget
limits logical reminders, not HTTP attempts. Normal delivery retry behavior applies.

Queued reminders become terminal `suppressed` when acknowledged, resolved, disabled
by policy, over a reduced limit, or lacking matching unhealthy evidence within the
last two minutes. Reasons are `acknowledged`, `incident_resolved`,
`reminders_disabled` or `reminder_obsolete`; maintenance uses its existing reasons.
Live leases may finish; expired tokens cannot complete a suppressed reminder.
After acknowledgement expiry, fresh unhealthy evidence is still required. Release
and policy changes start the interval from their decision time.

The MCP dashboard shows open incidents, actions and their latest five decisions.
The API provides `GET /v1/apps/{slug}/mcp-readiness-attention` (optional `scope`,
`after`, `limit` 1–50, `include_resolved` default false), and GET/POST
`/v1/apps/{slug}/mcp-readiness-incidents/{id}/attention-actions`. History supports
`before` and `limit`; inspection includes all-environment incidents alongside the
selected environment. Reads require read scope and MFA; changes require admin
scope and MFA. Browser changes additionally require the named MCP-alert CSRF token.

Deploy the migration and upgrade **all readiness evaluators before enabling
reminders**. Older binaries do not enforce acknowledgement or stale reminder
suppression. Rollback refuses to discard attention decisions or reminder history.
Native PagerDuty delivery and fallback webhook routing are described below.

### Readiness fallback webhook delivery

A readiness webhook rule can use one separately signed fallback receiver when its
primary is unavailable. Fallback is disabled by default. Read the policy with read
scope and MFA; replacing it requires admin scope and MFA:

```sh
gregale mcp readiness fallback get --app demo --rule '<rule UUID>'
# Set MCP_FALLBACK_SECRET through your secret manager before this command.
gregale mcp readiness fallback set --app demo --rule '<rule UUID>' \
  --expected-version 0 --webhook-url https://fallback.example.com/alerts \
  --secret-env MCP_FALLBACK_SECRET --after-failures 3
gregale mcp readiness fallback disable --app demo --rule '<rule UUID>' \
  --expected-version 1
```

The corresponding API is `GET/PUT /v1/apps/{slug}/alerts/{id}/mcp-readiness-fallback`. PUT requires `expected_version`
and `enabled`. Enable additionally requires `webhook_url`, `webhook_secret`, and
`after_failures` (1–10). Disable omits all three. A stale version returns 409;
refresh before replacing it. Both URLs pass the normal webhook egress controls,
and fallback must differ from primary. Signing secrets are independently sealed;
reads and write responses return only a mask. The MCP dashboard has equivalent
forms under the readiness alert, with session, MFA and CSRF protection.

The threshold counts completed unsuccessful primary **dispatch claims per event**,
not internal HTTP retries or elapsed outage time. A crashed lease does not count
as a completed failure. Once the threshold is met, the next evaluator pass may
send the same immutable event to fallback immediately. Failed fallback delivery
uses the existing cooldown and durable retries. There is one fallback and one
switch per incident. The signed event ID and payload remain stable; receivers
must deduplicate by event ID independently at each destination.

At the incident's first delivery claim, Gregale captures the policy, threshold and
receiver URLs. Changes apply to incidents not yet attempted. An incident first
attempted without fallback keeps the existing live primary behavior. With fallback
enabled, its URLs remain pinned; new credentials for the same configured URL are
used on subsequent claims. Replacing a URL retains the incident's last used sealed
credential for that old address. Disabling fallback affects new incidents; disable
the alert rule to pause existing delivery, or explicitly abandon the queue head.

After a switch, subsequent fired notifications and recovery use fallback. If the
primary previously accepted a fired notification, recovery also goes to primary:
preferring fallback first while allowing independent primary recovery. An expired primary lease
is conservatively treated as potentially accepted. Successful destinations are not
resent within the same logical recovery. A recovery may therefore remain at the
primary queue head after fallback succeeds while primary is still unavailable.
Healthy fallback delivery continues through later incidents. Queue inspection
and receiver-specific recovery controls remain available. Fallback can receive a
standalone recovery if failover first happens during recovery delivery.

Incident timelines and notification queue heads expose `delivery_routing`, including
policy version, destination claim counts, completed primary failures and delivery
success per receiver. Physical alert delivery history includes `readiness_event_id`,
`readiness_destination` and `readiness_failover_reason`. Delivery progress is separate
from MCP readiness and acknowledgement. Existing maintenance and reminder
suppression remain in effect; acknowledgements do not cancel ordinary failure or
recovery delivery.

Apply the fallback migration and upgrade **all readiness evaluators** before enabling
this policy. Older evaluator binaries do not honor pinned routing. Rollback refuses
to discard fallback policies or routing history. See
[ADR 20261017191522](adr/20261017191522-mcp-readiness-fallback-delivery.md) for the routing decisions.

### Independent webhook receiver queues

Readiness notifications now preserve FIFO order separately for primary and
fallback. A recovery still retries every informed receiver, but an unavailable
primary no longer stops a healthy fallback from receiving later incidents.
When an older failed-over incident still owes primary a recovery, a new incident
with fallback enabled starts on fallback (`primary_queue_blocked`) and stays
there for its lifetime. After old primary recoveries finish, new incidents start
at primary again. A rule without fallback continues waiting on primary.

The notification queue API returns `destinations` with each receiver's head,
state, outstanding count and recovery controls. Incident events include
`destination_deliveries` with independent status, attempt count, next retry,
active lease deadline and physical delivery ID. These views omit receiver URLs,
credentials and lease tokens. The original rule summary remains the oldest
outstanding logical event, so inspect receiver queues for actual delivery progress.
`waiting_primary` reserves fallback order until an earlier primary event succeeds
or fails over. `waiting_receiver` prevents reordering when the same pinned URL
appears under different receiver roles after configuration changes.

Use the existing action endpoint with optional `destination: primary|fallback`
to retry or abandon only that receiver's head. Read its current event
`delivery_version`, supply a fresh request UUID and reason, and reuse the complete
body for request retries. The shared event version changes when either receiver
progresses; refresh it after a conflict. Omit `destination` for the existing
whole-event action, which still requires the oldest event and no live lease.
A targeted action preserves other receivers' successful deliveries and leases.
The dashboard exposes these receiver controls, and the Go, Node and Python SDKs
include the same fields.

```sh
gregale mcp readiness notifications retry --app demo \
  --destination primary --event EVENT_UUID --expected-version VERSION \
  --request-id REQUEST_UUID --reason 'Primary receiver repaired'
```

Upgrade: stop readiness evaluators, apply
`20261017181315090_mcp_readiness_destination_queues.sql`, upgrade all evaluators,
then resume. Existing queued events lazily acquire independent state while
preserving retry deadlines, successful receiver flags and legacy lease fences.
Do not mix old and new evaluators. Rollback refuses to discard destination
progress or targeted action history.

See [ADR 20261017191523](adr/20261017191523-mcp-readiness-destination-queues.md) for ordering and upgrade decisions.


### Reminder defaults for new incidents

Readiness webhook rules can define a reminder interval and finite creation budget
for future incidents. The MCP dashboard provides “Save reminder defaults” and
“Disable future reminders” controls beside each readiness webhook alert.

```sh
gregale mcp readiness reminder-defaults get --app demo --rule RULE_UUID
gregale mcp readiness reminder-defaults set --app demo --rule RULE_UUID \
  --expected-version 0 --interval-seconds 300 --max-reminders 3
gregale mcp readiness reminder-defaults disable --app demo --rule RULE_UUID \
  --expected-version 1
```

GET/PUT `/v1/apps/{slug}/alerts/{id}/mcp-readiness-reminder-defaults` exposes
`version` and `reminder_policy: {interval_seconds, max_reminders}`. PUT requires
`expected_version` and `reminder_policy`. Version zero means unconfigured and
disabled. A policy must use 60–86400 seconds and 1–10 reminders, or zero for both
to disable. A stale version returns 409; reload before retrying. Read scope is
required for GET and admin scope for PUT, with MFA on both. Disabled rules can
still be configured. Go, Node and Python SDKs expose these operations.

Defaults are captured atomically **when the incident opens**, including openings
during maintenance. Changing or disabling defaults affects future incidents only;
it never changes an existing incident's acknowledgement, interval, next eligibility
time, or used budget. Use the existing incident `configure_reminders` action to
change or disable reminders for an open incident. That action preserves the
lifetime creation count.

Attention inspection includes `reminder_policy_source` (`rule_default`,
`incident_override`, or `legacy`) and the captured `reminder_defaults_version`
when available, including version zero. An incident override retains its original
defaults version. Older incidents remain `legacy` until overridden and are never
backfilled. Rule defaults versions are independent of incident attention versions;
use the current incident attention version for acknowledgement and override actions.

Apply the migration and upgrade all readiness evaluators before configuring defaults.
Mixed old/new evaluators can ignore defaults or erase inheritance metadata. Existing
fresh-evidence, acknowledgement, maintenance, lifetime budget, and per-incident
notification ordering safeguards continue to apply. Rollback refuses to discard
configured defaults or captured inheritance history, including version zero.


### Native PagerDuty readiness delivery

Existing MCP readiness webhook rules can use PagerDuty as their primary receiver,
with signed webhook or native PagerDuty fallback available (see below). Native delivery
supports the US and EU Events API v2 endpoints. Configure the integration
in the MCP dashboard, or use an environment variable with the CLI:

```sh
export PAGERDUTY_INTEGRATION_KEY=YOUR_INTEGRATION_KEY
gregale mcp readiness pagerduty get --app demo --rule RULE_UUID
gregale mcp readiness pagerduty set --app demo --rule RULE_UUID \
  --expected-version 0 --key-env PAGERDUTY_INTEGRATION_KEY --region eu
gregale mcp readiness pagerduty disable --app demo --rule RULE_UUID \
  --expected-version 1
```

GET/PUT `/v1/apps/{slug}/alerts/{id}/mcp-readiness-pagerduty` provides versioned
configuration. PUT requires `expected_version` and `enabled`; enabling also requires
an `integration_key` of 32 ASCII alphanumeric characters, while disabling omits it.
Optional `region` selects `us` (the default when omitted) or `eu`; disabling omits
region and key. GET always returns effective `region` (`us` when unconfigured or
disabled). Version zero means unconfigured. GET returns version, enabled, region,
and a fixed
`integration_key_masked` value for enabled configurations. Reads require read scope
and MFA; writes require admin scope and MFA. Browser writes also require the named
MCP-alert CSRF token. Go, Node and Python SDKs expose the same operations.

Keys use a dedicated encryption namespace. Their plaintext, ciphertext and private
fingerprint never appear in configuration responses, incident inspection, audit
metadata or provider errors. The selected provider endpoint is fixed:
`https://events.pagerduty.com/v2/enqueue`
for US or `https://events.eu.pagerduty.com/v2/enqueue` for EU. Customer-supplied URLs
and redirects cannot receive a key. Credentials are included only in the provider's
required request body. Provider responses are bounded and never recorded verbatim.

Fired events, reminders and retries share `gregale:mcp:INCIDENT_UUID` as their
PagerDuty deduplication key. Recovery resolves that same key. PagerDuty configuration,
region and encrypted credentials are pinned at an incident's **first delivery attempt**.
Replacing or disabling the configuration affects unattempted incidents; existing
incidents keep their receiver for reminders and recovery. Retain the old integration
key until those recoveries finish. Webhook primary is restored for unattempted
incidents when native delivery is disabled. The existing rule webhook configuration
is retained for that behavior. PagerDuty acknowledgement does not change Gregale
acknowledgement automatically; enable the separate signed acknowledgement policy
below to synchronize it.

Only a valid HTTP 202 response with `status: success` and the matching deduplication
key counts as accepted. Acceptance confirms provider ingestion, not that a person
was paged. Per-receiver inspection includes `provider` and PagerDuty
`provider_status` (`accepted`, `retrying`, or the current queue delivery state).
Existing generic delivery fields retain their compatibility meanings. Recovery is
also owed to a primary whose timeout, invalid acknowledgement, server error or
expired lease could have followed acceptance.

429 and 503 `Retry-After` values support seconds or HTTP dates, bounded to 24 hours.
Completion commits the physical outcome and retry deadline atomically under the
existing lease fence. The next attempt observes both the rule cooldown and the
provider deadline. Existing operator retry controls can explicitly override the
queue deadline. Maintenance, acknowledgement and fresh-evidence reminder guards,
receiver FIFO ordering, fallback failover, and uncertain-delivery recovery remain
active. A separate PagerDuty integration is distinguished by its private receiver
identity, which includes region endpoint and private key fingerprint. The same key
in different regions has distinct receiver identity. Region changes cannot refresh
credentials on an incident pinned to another region.

Stop readiness evaluators, apply the PagerDuty migrations through
`20261017181315128_mcp_readiness_pagerduty_regions.sql`, upgrade every evaluator,
then resume before enabling EU PagerDuty. Existing configurations remain US. Older
workers cannot honor EU receiver claims. Rollback refuses to discard provider configuration
or delivery history; the regional migration also refuses rollback while any EU
configuration or pinned EU incident history exists. Regional routing does not imply
platform-wide data residency. Local fake-provider contracts cover the adapter; real
PagerDuty service/key interoperability and paging outcomes require separate live qualification.

### PagerDuty acknowledgement sync

A separate signed webhook policy lets PagerDuty acknowledgements pause Gregale
reminders. Readiness probes still determine health and recovery. Provider resolve
and unacknowledge events do not change Gregale state; release an acknowledgement in
Gregale to resume eligible reminders. Existing local acknowledgement expiry is
preserved, and already resolved incidents are never reopened.

1. Configure native PagerDuty primary delivery and read its current version. To bind a native fallback instead, follow the fallback-specific version and destination instructions below.
2. Get the acknowledgement settings below. Their `webhook_path` is available even
   before enabling. Add that path to your trusted public API origin to obtain the
   webhook URL; it does not use an application invocation URL.
3. Create a PagerDuty **v3** subscription for `incident.acknowledged`, filtered to
   the service receiving this integration's events. Use that webhook URL and copy
   the subscription signing secret returned by PagerDuty.
4. Save the service ID, signing secret and current native policy version. The
   server captures the integration fingerprint and US/EU region automatically.

```sh
gregale mcp readiness pagerduty acknowledgements get --app demo --rule RULE_UUID
export PAGERDUTY_WEBHOOK_SECRET=YOUR_SUBSCRIPTION_SIGNING_SECRET
gregale mcp readiness pagerduty acknowledgements set --app demo --rule RULE_UUID \
  --expected-version 0 --expected-pagerduty-version 1 --service-id PSVC001 \
  --secret-env PAGERDUTY_WEBHOOK_SECRET
gregale mcp readiness pagerduty acknowledgements events --app demo --rule RULE_UUID \
  --limit 20
gregale mcp readiness pagerduty acknowledgements disable --app demo --rule RULE_UUID \
  --expected-version 1
```

GET/PUT `/v1/apps/{slug}/alerts/{id}/mcp-readiness-pagerduty-acknowledgements`
provides versioned settings. Enabling requires `expected_version`, `enabled: true`,
`expected_pagerduty_version`, `service_id` and `signing_secret`. The signing secret
is the exact 16–1024 printable non-space ASCII bytes supplied by PagerDuty, not an
integration routing key or a signature header. Disabling sends only current
`expected_version` and `enabled: false`. Credentials use a dedicated encryption
namespace, and reads return only a fixed mask. Reads require read scope and MFA;
writes require admin scope and MFA. The dashboard also requires its named CSRF
cookie. Go, Node and Python SDKs expose settings and receipt pagination.

PagerDuty posts to `/v1/webhooks/mcp-readiness/pagerduty/{rule_id}` with
`Content-Type: application/json` and `X-PagerDuty-Signature`. The receiver verifies
HMAC-SHA256 over the unmodified raw body. Comma-separated `v1=HEX` signatures
support provider key rotation; any matching current signature is sufficient.
Bearer tokens or browser sessions do not replace signature authentication.
Valid handled, duplicate or unrelated events receive HTTP 202. Invalid signatures
receive 401; a disabled or missing receiver returns 404; invalid supported events
return 400; oversized bodies return 413; temporarily unavailable decryption/storage
returns 503. Policy changes during authentication require retrying with the current
credential. Conflicting reuse of a provider event ID returns 409.

The authenticated body must contain a v3 `incident.acknowledged` event with
`resource_type: incident`, `data.type: incident`, `data.status: acknowledged`,
`data.incident_key: gregale:mcp:INCIDENT_UUID`, the configured `data.service.id`,
and the matching regional `data.self` incident URL. The integration, region,
application and rule must match the incident's pinned outgoing receiver. Unrelated
incident keys, rules and services are ignored. Configure PagerDuty alert grouping
to preserve the Gregale incident key; grouped incidents without that key cannot be
correlated safely. No provider URLs are fetched.

Event IDs are scoped to the rule; opt-in releases additionally track delivery destination
(see below). Duplicate events replay their immutable receipt
without changing attention state. Events older than the incident or no newer than
its latest attention decision are retained as stale for acknowledgement-only bindings;
opt-in ownership uses the per-destination ordering described below. Preserve local acknowledgement
expiry; an expired local acknowledgement is replaced only by a later provider event.
A newer provider acknowledgement after a local release can acknowledge again.
Times use the signed provider event timestamp; the receiver permits at most five
minutes of future clock skew and seven days of delivery age. Keep clocks synchronized.
An in-flight reminder may finish; queued reminders are suppressed atomically with
an applied acknowledgement. Initial failure and recovery delivery remain intact.

Bodies are limited to 64 KiB, signature headers to 1024 bytes/eight signatures, JSON
to 32 levels, and provider identifiers to 128 ASCII letters/digits/underscores/
hyphens. Retain at most 128 unique authenticated events per incident. At that bound,
further new IDs receive 202 without changing attention or adding receipts; local
attention actions remain available. Raw payloads, signatures, actor identities,
plaintext/ciphertext secrets and receiver fingerprints are never exposed in receipt
history. Minimal receipts and the attention decision commit in one transaction.

GET `/v1/apps/{slug}/alerts/{id}/mcp-readiness-pagerduty-acknowledgements/events`
returns newest-first receipts, `limit` 1–50 (default 20), and an optional
`next_before` cursor. Receipts identify the service, region, provider/Gregale incident
IDs, provider event/time, receipt time, captured policy version and attention
version. Outcomes are `acknowledged`, `already_acknowledged`, `stale` or `resolved`.
Attention history and dashboard acknowledgements include `source: pagerduty` and
provider identifiers, making the owner-authorized integration distinct from a local
account action.

Only the current webhook signing secret is accepted. Saving a fresh signing secret
or disabling settings revokes the previous secret immediately; resubmitting the
same secret does not rotate it. Recreate/rotate the PagerDuty subscription and update
Gregale together;
disable the remote subscription when retiring it. Outgoing key/region changes do
not retarget this binding. Rebind explicitly to the new integration, after which
old pinned integrations cannot use that binding. Disabling outgoing delivery alone
leaves signed acknowledgement sync available for matching old incidents; disable
acknowledgement sync explicitly to revoke it. Previously applied acknowledgements
remain in place; release them in Gregale to resume reminders. Repeated invalid
signatures use the shared failed-auth IP limiter and may receive HTTP 429.

Apply `20261017181315142_mcp_readiness_pagerduty_acknowledgements.sql` and upgrade
apid and evaluators before enabling. Rollback preserves configuration and provider
history, including disabled policies. Local signed-webhook and memory/PostgreSQL
contracts cover the implementation; live subscription delivery and PagerDuty
service/key interoperability require separate qualification.

### Native PagerDuty readiness fallback

A readiness alert can now escalate from a failing webhook directly to PagerDuty.
The existing fallback policy selects `provider: webhook` (the default when omitted)
or `provider: pagerduty`. The completed-primary-failure threshold remains 1–10;
existing per-receiver queues, maintenance, reminder and acknowledgement guards apply.
A blocked primary queue can activate fallback with `primary_queue_blocked` as before.

```sh
# Set the integration key through your secret manager first.
gregale mcp readiness fallback set --app demo --rule RULE_UUID \
  --expected-version 0 --provider pagerduty --region eu \
  --key-env PAGERDUTY_FALLBACK_KEY --after-failures 3
gregale mcp readiness fallback get --app demo --rule RULE_UUID
```

GET/PUT `/v1/apps/{slug}/alerts/{id}/mcp-readiness-fallback` accepts the same
versioned policy. A PagerDuty replacement sends `expected_version`, `enabled: true`,
`provider: pagerduty`, `integration_key`, `after_failures`, and optional `region`
(`us` by default, or `eu`). Omit `webhook_url` and `webhook_secret`. A webhook
replacement omits all PagerDuty fields and uses its existing URL and signing secret.
Disable with only current `expected_version` and `enabled: false`. Reads return
provider and a fixed key mask, with region for native PagerDuty; fingerprints and
plaintext/ciphertext credentials are never exposed. The dashboard has separate
webhook and PagerDuty replacement forms. CLI, Go, Node and Python support both.

The native fallback key is separately configured and encrypted in the PagerDuty
namespace. API destinations are fixed by region. Primary and fallback cannot use
the same PagerDuty integration in the same region, including when replacing the
primary after configuring fallback. Separate services/integrations within a shared
provider do not guarantee delivery during a provider-wide outage.

At first dispatch, fallback provider, region, integration identity, encrypted key,
threshold and policy version stay pinned to that incident. Later replacements or
disabling affect incidents not yet attempted. Re-encrypting the same integration
key refreshes credentials for its pinned receiver. A different key denotes another
integration and does not retarget active incidents. Keep old keys valid until all
pinned incident recoveries finish. `delivery_routing.policy_version` identifies
the captured fallback version; `pagerduty_version` continues to identify native
primary configuration. Per-destination progress reports `provider: pagerduty`,
acceptance/retry status and next-attempt time. HTTP 202 acceptance is not proof
that an engineer was paged.

A stable incident dedup key is used for trigger, reminders, retry and resolve.
Recovery goes to the activated fallback and any previously informed or uncertain
primary, with independent retry queues. Restarting evaluators, changing region,
or disabling future fallback cannot silently discard an outstanding recovery.

Signed acknowledgement sync can select the native fallback:

```sh
gregale mcp readiness pagerduty acknowledgements get --app demo --rule RULE_UUID --destination fallback
# Create the v3 subscription using webhook_path as described above.
gregale mcp readiness pagerduty acknowledgements set --app demo --rule RULE_UUID \
  --expected-version 0 --destination fallback --expected-fallback-version 1 \
  --service-id PSVC001 --secret-env PAGERDUTY_FALLBACK_WEBHOOK_SECRET
```

Use the fallback configuration's actual current version in
`expected_fallback_version`; omit `expected_pagerduty_version`. The API accepts the
same optional `destination: fallback` selector. Omitted destination remains primary
and continues requiring `expected_pagerduty_version`. Primary and fallback have
independent inbound bindings, signing secrets and policy versions. Saving or
disabling one leaves the other unchanged. Use a fresh secret when rotating a
subscription. Reads include the selected destination and captured
`fallback_version` or `pagerduty_version`.

A fallback callback can acknowledge only after that incident's fallback has
activated and its pinned PagerDuty integration, region, service, application and
rule all match. A signed callback before activation is ignored without a receipt.
Accepted minimal receipts include `destination`; absent destination in older
receipts means primary. Duplicate/stale callbacks cannot override newer local
attention decisions. Disabling outgoing fallback alone leaves the incoming binding
available for matching old incidents; disable acknowledgement sync separately to
revoke it. Readiness probes remain authoritative for health and recovery.

Stop readiness evaluators, apply
`20261017181315159_mcp_readiness_pagerduty_fallback.sql`, upgrade **all readiness
evaluators and apid**, then resume before enabling native fallback. Old evaluators cannot enforce
native fallback routing and credential namespaces. Existing policies remain
webhook, and acknowledgement bindings remain primary. Rollback refuses to discard
native fallback configuration, pinned routing, delivery or acknowledgement history.
See [ADR 20261017191528](adr/20261017191528-mcp-readiness-pagerduty-fallback.md). Memory/PostgreSQL and
local fake-provider tests cover the implementation; real PagerDuty service/key,
paging and subscription delivery still require live qualification.


### Independent PagerDuty acknowledgement subscriptions

Configure both primary and fallback acknowledgement sync on the same readiness
rule. Read each binding explicitly before creating its PagerDuty v3 subscription:

```sh
gregale mcp readiness pagerduty acknowledgements get --app demo --rule RULE_UUID --destination primary
gregale mcp readiness pagerduty acknowledgements get --app demo --rule RULE_UUID --destination fallback
# Use each response's webhook_path with your API origin and a separate signing secret.
gregale mcp readiness pagerduty acknowledgements disable --app demo --rule RULE_UUID \
  --destination fallback --expected-version 1
```

The management API supports `GET ?destination=primary|fallback`. Its existing
`PUT` body uses `destination` for enabling, rotating and disabling. Use the
selected binding's `version` for `expected_version`; configuring the other binding
does not change that value. Enabling still requires the selected outgoing native
integration's version, service ID and signing secret. Disabling omits these
credentials and outgoing versions while retaining the destination's own version.

Explicit reads always return destination-specific callbacks at
`/v1/webhooks/mcp-readiness/pagerduty/RULE_UUID/primary` or `/fallback`. Each accepts
only its own current signing secret. Existing subscriptions using the original
`/v1/webhooks/mcp-readiness/pagerduty/RULE_UUID` continue to authenticate against the
same binding, including previously configured fallback subscriptions. For a new
rule the first saved binding owns that original callback. Adding or disabling
another binding never reassigns that URL. GET and disable without a destination
select its original owner; enable without a destination selects primary. Specify
both destinations explicitly when managing dual subscriptions.

Both callbacks still enforce application, rule, service, region and pinned
integration identity; fallback callbacks have no effect before activation. If the
same PagerDuty acknowledgement event reaches both valid subscriptions, Gregale retains one receipt
and returns it on replay, even after a local attention release. Its destination
identifies the first accepted callback. Different event IDs retain their own
receipts while an existing acknowledgement prevents repeated attention actions.
No raw payloads, actor details or signing secrets enter event history. Readiness
probes continue to determine health and recovery.

Pause acknowledgement configuration writes and incoming subscriptions, apply
`20261017181315175_mcp_readiness_pagerduty_independent_bindings.sql`, upgrade every
apid instance, then resume callbacks and configuration writes. The previous
binary cannot write the new composite-key schema. Existing credentials, versions
and original callback URLs are preserved. Create destination-specific
subscriptions only after every apid instance is upgraded. Switching a binding from primary to fallback now leaves the primary
active: explicitly disable any subscription that should be revoked. Rollback
refuses independent bindings and disabled fallback version tombstones; preserve
these records before a downgrade. Older binaries do not serve destination-specific
callbacks, so pause incoming subscriptions during rollback and reconfigure to the
original callback URL only after preserving the data required by the rollback
guard. See [ADR 20261017191529](adr/20261017191529-mcp-readiness-independent-pagerduty-bindings.md).
Local signature, persistence and migration checks do not replace live PagerDuty
subscription qualification.


### Opt-in PagerDuty acknowledgement release

PagerDuty can release its own tracked acknowledgement when a signed v3
`incident.unacknowledged` event arrives with incident status `triggered`. This is
independently opt-in per primary/fallback subscription. Existing bindings default
to acknowledgement-only; other provider events, including `incident.resolved`,
never change readiness health. Probes remain authoritative for recovery.

```sh
gregale mcp readiness pagerduty acknowledgements set --app demo --rule RULE_UUID \
  --destination primary --expected-version CURRENT_BINDING_VERSION \
  --expected-pagerduty-version CURRENT_PRIMARY_VERSION --service-id PSVC001 \
  --secret-env PAGERDUTY_WEBHOOK_SECRET --sync-unacknowledgements
```

For fallback, select `--destination fallback` and use
`--expected-fallback-version` instead of `--expected-pagerduty-version`. Subscribe
each PagerDuty v3 subscription to both `incident.acknowledged` and
`incident.unacknowledged`, using the destination's returned `webhook_path` and
signing secret. API PUT uses `sync_unacknowledgements: true`; GET returns the
setting. Replacements must explicitly retain it. Omission or false disables
release sync. Disable requests must omit it or set false. Go, Node and Python
SDKs expose the setting, active provider acknowledgements and event actions.

Ownership starts with a fresh acknowledgement received while release sync is
enabled. Existing active acknowledgements are never retroactively adopted;
release locally and wait for a fresh provider acknowledgement to start a tracked
cycle. `provider_acknowledgements` on incident attention lists at most two active
holds, in primary then fallback order, with public provider IDs, timestamps,
policy versions and `release_sync_enabled`. It never exposes integration
fingerprints or credentials. Dashboard shows both settings and active holds.

Each destination has its own hold and event timestamp watermark. A release must
match that hold's provider incident, service, region and captured integration.
Releasing primary leaves fallback acknowledged. A known acknowledgement received
with release sync off remains protected from automatic release after enabling;
a fresh matching acknowledgement after enabling can make it releasable. If
multiple provider incidents acknowledge through one destination, Gregale retains
a conservative hold requiring local release. Manual acknowledgements always
require local release. A local acknowledgement or release clears provider holds;
all local attention decisions advance a timestamp barrier against delayed events.

Older events are recorded as stale. Equal timestamps favor acknowledgement to
keep reminders paused. A release received before its acknowledgement records an
inactive watermark to reject older delayed acknowledgements. Duplicate receipts
are immutable and cannot reapply attention after a manual action. Acknowledgement
event IDs remain deduplicated across both subscriptions, preserving legacy IDs
and digests. Release events have separate primary/fallback receipts so one event
can release both destinations; conflicting semantic payloads under the same event
ID remain conflicts across destinations. The existing 128-receipt lifetime cap
per incident includes both release deliveries; duplicate lookup precedes the cap.

When the final releasable provider hold clears, Gregale schedules the next
reminder after the existing interval. It preserves the reminder count and lifetime
budget, and normal fresh evidence, maintenance and queue guards still apply. An
exhausted or disabled policy stays exhausted or disabled. Provider history adds
`action: release` and outcomes `released`, `retained`, or `not_acknowledged`;
existing acknowledgement, stale and resolved outcomes remain. Ownership-only
updates advance the attention CAS version; canonical attention history records
effective acknowledge/release transitions.

Before enabling, pause acknowledgement configuration writes and incoming
subscriptions, apply `20261017181315189_mcp_readiness_pagerduty_release.sql`, and
upgrade **all apid instances, readiness evaluators and other attention snapshot
writers**. Older writers discard unknown ownership/watermark fields, and older
receipt readers assume one row per event ID. Resume subscriptions only after
all writers are upgraded; enable release sync and begin a fresh acknowledgement
cycle. Rollback refuses enabled release settings, ownership/watermarks or release
receipts rather than discarding ordering and audit history. Disabling the setting
alone does not remove that retained state. See
[ADR 20261017191530](adr/20261017191530-mcp-readiness-pagerduty-release.md). Local tests cover memory,
PostgreSQL restart, signatures, migration replay and client contracts; live
PagerDuty subscription qualification remains pending.
