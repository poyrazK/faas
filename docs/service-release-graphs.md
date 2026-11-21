# Service release graphs

A project's release graph pins which revision of each service a caller reaches. Keep client and service revisions consistent, qualify a staged graph before promotion, and recover or deactivate one when needed.

## Keep client and service revisions consistent

Enable an app's compatibility window with `revision_pin_ttl_seconds` (up to
604800). A successful response includes `X-Gregale-Revision: <deployment-id>`.
Clients that cannot upgrade immediately can send that ID back on later public
requests. A replaced revision receives 0% normal traffic but remains reachable
by its exact pin until the cutover TTL expires. A release set can independently
keep that deployment reachable, even after its direct revision pin expires;
when the set is replaced, its own TTL begins. An expired or foreign pin is
rejected; it never silently lands on newer code. This works for HTTP requests
and WebSocket reconnect handshakes, not already-open connections.
The default CORS policy exposes both pin response headers. If you configure a
custom CORS rule, include `X-Gregale-Revision` and `X-Gregale-Release` in its
exposed headers and permit them as request headers for browser clients.

For a static SPA, the gateway sets a host-only `__Host-gregale_release` cookie
on document navigation when revision pin retention is enabled and an active
release graph was selected. The Node browser adapter reads it at initialization
and adds the release header to configured managed origins, including
cross-origin APIs. Gregale strips this platform cookie before forwarding the
request to the app. If a CDN sits in front of the app, preserve the document
response's `Set-Cookie` header with its body; the cached body and cookie must
describe the same release. The cookie is a routing identifier, not a secret.
It is a browser session cookie, while the graph's server-side TTL controls its
routing eligibility. If the graph expires, requests fail with 410 and are not
retried against the active graph.

Same-host browser WebSocket reconnects also inherit this cookie: the browser's
native `WebSocket` API cannot set a custom release header, so the gateway reads
the cookie only from a WebSocket handshake, validates the graph and TTL, then
strips the cookie before forwarding. The SPA and WebSocket endpoint must use
the same hostname for the host-only cookie to be sent. Already-open sockets
stay on their selected deployment until they disconnect.

For a browser socket on a separate managed API hostname, use the Node SDK's
browser adapter `webSocket(url, protocols)` helper after it has learned the
release or has been seeded from SSR/bootstrap. The helper adds the reserved
`gregale.release.<release-uuid>` WebSocket subprotocol; the gateway consumes it
before the app handshake, preserves application subprotocols, and filters the
reserved token from the guest response. The release is not placed in the URL.
The target origin must be included in `managedOrigins`, and opening a managed
socket before the adapter knows a release fails instead of silently routing to
the active graph. Plain native `WebSocket` calls and non-browser clients need a
same-host bootstrap cookie or another explicit pin mechanism. This carrier is
for the handshake only; an established socket stays on its selected deployment
until it disconnects.

For a multi-workload project, publish a complete release set after all member
deployments are ready. An incompatible new service deployment can be deployed
with an explicit 0% traffic weight first; publishing the new graph activates
it for release-pinned calls without shifting the ordinary weighted route:

```http
POST /v1/projects/shop/environments/production/release-sets
Content-Type: application/json

{"ttl_seconds":3600,"deployments":{"shop-api":"API_DEPLOYMENT_UUID","shop-billing":"BILLING_DEPLOYMENT_UUID"}}
```

The response contains a release UUID. Production project ingress follows the
active set by default; a client can continue an older set with
`X-Gregale-Release: <release-uuid>`. Gregale returns that header and sends it
to the API guest. Propagate it on outbound managed service calls; the Go,
Node, and Python SDKs provide request-context middleware/transports for this.
They strip `X-Gregale-Revision` on those downstream hops because revision IDs
are scoped to the caller app. The service proxy verifies the calling VM's
deployment belongs to the release and picks the matching target deployment;
it cannot be spoofed into selecting a graph from a caller header alone. When
the same API deployment belongs to several unexpired sets, an internal call
without the release header returns 409 rather than guessing. The active set
does not expire; its TTL starts when a new set replaces it. Publish a new
complete set whenever project membership changes.

Release sets pin public HTTP/WebSocket handshakes, managed HTTP service calls,
and durable invocations (async invoke, delayed tasks, queues, inbox messages,
and asynchronous edge routes). For durable work, Gregale captures the active
release or an explicit pin when accepting the row, then checks it again before
delivery. A queued item whose release expires fails rather than switching to
new code; schedule it within the compatibility window. The control-plane
invocation, queue, and task APIs accept the same headers on the HTTP request;
invoke and task JSON envelopes may also carry them in their `headers` object.
Accepted control-plane responses return the captured revision or release header
when a version was selected at enqueue time.
Calls that bypass `*.svc.gregale` still need their own release-context
propagation and are not covered by this guarantee. A guest must forward the
received `X-Gregale-Release` on each managed outbound service call when its
deployment can belong to multiple live release sets.

## Inspect a project's release graph

```sh
gregale environment get shop production
gregale environment get shop production --json
gregale environment release-sets shop production --limit 20
```

`inspect` compares active release-set membership with each workload's ordinary
live deployment, which is what its stable environment URL currently selects.
It reports different selections and project membership gaps explicitly. A bare
`gregale environment get` uses the linked project and selected
environment; supplying both positional arguments overrides that context.

The overview includes the configuration version, last promotion, and managed
PostgreSQL/object-storage binding metadata. It omits configuration and variable
values, secret names, and secret fingerprints in both text and JSON. Service
and queue bindings are outside this first overview's binding coverage. Runtime
health is reported as `not_checked`: a live deployment record alone does not
prove health, and inspection does not wake parked workloads. Failure to read
promotion history appears as an issue while the available environment inventory
remains visible. Failure to read environment or release state fails inspection.

`release-sets` lists active, retired, and expired graphs, newest first. The
page size defaults to 50, with a maximum of 100. Pass `next_before` back through
`--before` to continue. Historical visibility does not extend a graph's routing
eligibility or retain deleted artifacts.

## Qualify a staged release before protected promotion

Protected-target promotion from an active source release set is gated on the
latest passing `health` and `smoke` qualification for that exact release-set
ID, non-secret configuration version/hash, and per-workload secret revision
fingerprints. Receipts expire after 24 hours; publishing a new release set,
changing source configuration, or rotating a source secret requires new
checks. `qualify` snapshots the active graph, configuration identity, and
secret revision metadata,
resolves each member's exact deployment preview URL, and runs both configured
GET probes before recording the receipt. The API rejects the result if either
the release set, configuration, or secret revisions changed while probes were
running. Fingerprints cover secret key/version metadata and managed credential
generations only; they never contain secret values or value hashes. Add
`gregale-qualification.yaml` to the project repository:

```yaml
version: 1
timeout_seconds: 5
workloads:
  api:
    health_path: /healthz
    smoke_path: /ready
  billing:
    health_path: /healthz
    smoke_path: /ready
```

Then run:

```sh
gregale environment qualify shop staging --profile gregale-qualification.yaml
gregale environment preview shop --from staging --to production
gregale environment check shop --from staging --to production \
  --profile gregale-qualification.yaml --json
```

`preflight` runs the source qualification and promotion preview together. It
exits non-zero when probes fail, the qualification is stale/unresolved, or the
server says promotion is blocked. Its JSON output contains the receipt summary
and blockers, but omits secret-revision fingerprints and the opaque promotion
token. The [GitHub Actions environment-preflight Action](../.github/actions/environment-preflight/README.md)
uses a five-minute OIDC bearer with only `project_environments:read` and
`project_environments:qualify`; the later promotion command still rechecks
policy and the latest qualification on the server.

The profile must define exactly the workloads in the active release set. Each
probe is an HTTPS GET with normal TLS verification; redirects and non-2xx
responses fail. The receipt binds each outcome to its deployment ID and records
the tested configuration version and canonical hash, plus opaque per-workload
secret revision fingerprints. It stores only the
workload, status, HTTP status, and a bounded error code—never response bodies,
headers, configuration values, or secrets. A failed or expired receipt, or one
whose configuration or secret revisions are stale, blocks promotion until a
newer passing qualification is recorded. Promotions without an active source
release set retain their existing behavior.

The read API uses the same project/environment scope as publication:

- `GET /v1/projects/{slug}/environments/{environment}/release-sets`
- `GET /v1/projects/{slug}/environments/{environment}/release-sets/active`
- `GET /v1/projects/{slug}/environments/{environment}/release-sets/{release}`

An active lookup returns 404 when no active set exists. The environment-state
response instead returns `release_set_status: none` and `active_release_set:
null`. When a graph exists, state includes `release_set_status: active`, the
complete stored graph, and each workload's `app_id` for joining membership.
Existing workload `release` fields continue to describe ordinary live selection.
All release inventory reads require the owning account's read access.

## Smoke-test a downstream deployment

`gregale app binding verify` checks the private transport and current route
availability without invoking customer code. To exercise a handler on one
exact deployment, use an explicit smoke request from the caller:

```bash
gregale app binding call public-api billing \
  --caller-deployment v12 \
  --target-deployment TARGET_DEPLOYMENT_ID \
  --path /health \
  --expect-status 200
```

Find the target ID with `gregale release traffic status billing`. `--caller-deployment`
selects an exact app-owned live caller deployment, including a candidate at 0%
traffic; `v12` resolves within `public-api`. Omit it to retain automatic caller
selection. `--deployment` remains an alias for `--target-deployment`; conflicting
aliases are rejected. Neither selector changes traffic. This command sends one GET
over the caller's verified `https://billing.internal` binding and accepts any
2xx response by default; `--expect-status 204` requires one exact status. It
does not follow redirects, retain the response body, or include the request
query in its report. This is an active smoke test: a parked selected deployment
is woken before forwarding, so its handler may have application-level side
effects. Use `bindings verify` for a no-wake infrastructure preflight.

The CLI confirms the admitted caller deployment and scope on every task read.
Success requires a complete matching guest report, the expected HTTP status,
and a successful task with exit code zero. Truncated, malformed or mismatched
reports fail. The test exercises the target handler from the caller's task
runtime; it does not execute the caller's own application handler or count as
binding-verification evidence. Ctrl-C requests bounded task cancellation;
`--wait-timeout` bounds CLI reads and waiting, while the task retains its own
90-second execution limit.

The equivalent raw request sends that same target override explicitly:

```bash
curl -H 'Gregale-Target-Deployment: DEPLOYMENT_ID' \
  'https://billing.internal/health'
```

This works for a live deployment at 0% traffic: the service proxy wakes that
exact deployment if needed. The override wins over `Gregale-Version-Key` for
this one service hop, but the version key remains available to the target app. The override header
is removed before forwarding, so it cannot accidentally pin a later call to
another service. Only deployments belonging to the authorized target app are
accepted; malformed IDs return 400 and non-live or wrong-app IDs return 422
instead of silently falling back to weighted routing. Direct public smoke
tests should use the deployment's preview URL instead. Gregale strips this
header from public requests before they reach your app; attach it explicitly
to the service call rather than forwarding it from an end-user request.

## Qualify and activate a project release graph

For projects using enforced binding release policies, prepare a fixed graph,
verify its dependencies, then activate it with an atomic check. Every project
workload must appear in the selection, with a materialized live deployment in
the selected environment and enough revision retention for the release TTL.
Every member scope needs an enforced policy. Preparation and verification keep
the current routes.

Save a selection such as this as `release-selection.json`, using real deployment
UUIDs for each workload:

```json
{
  "ttl_seconds": 1800,
  "deployments": {
    "shop-api": "11111111-1111-4111-8111-111111111111",
    "shop-billing": "22222222-2222-4222-8222-222222222222"
  }
}
```

```bash
gregale environment release-sets prepare shop production \
  --file release-selection.json > release-manifest.json
gregale environment release-sets verify shop production \
  --file release-manifest.json --dry-run --json
gregale environment release-sets verify shop production \
  --file release-manifest.json --json
gregale environment release-sets activate shop production \
  --file release-manifest.json --json
```

If the preview plans storage uploads and cleanup, add
`--object-storage-write` to the verification command after reviewing the plan.
The verifier reuses fresh compatible evidence and waits for compatible pending
probes and required queue readiness. It previews every member before any task.
Internal service edges use HTTPS HEAD probes pinned to the graph's exact target
deployments. These probes require a healthy registered endpoint and never wake
the target or call its handler. External services retain ordinary probe coverage.

The manifest fixes the candidate deployments, current routing, service targets
and policy revisions. Activation rechecks the complete graph, evidence age,
policy, artifacts and service replica readiness in one transaction. A concurrent
release, policy/config change, missing member, stale proof or loss of readiness
blocks the switch. Prepare and qualify again when the intent changes. The first
release also compares the existing weighted routing and requires it to be
unambiguous. Qualification alone does not reserve capacity or guarantee future
readiness.

The API routes are `POST /v1/projects/{slug}/environments/{environment}/release-sets/prepare`
and `POST /v1/projects/{slug}/environments/{environment}/release-sets/activate`.
Exact service evidence can be inspected through the binding inventory's
`service_targets` JSON query parameter together with `deployment_id`.
Ordinary inventory remains separate from evidence for proposed targets.
Generic graph publication and deactivation stay blocked under enforcement;
Checked fallback deactivation is described below.

## Recover a retained project release graph

Select an inactive, unexpired release from the release-set inventory. Save its
UUID and the desired release retention as `rollback-selection.json`:

```json
{"release_id":"33333333-3333-4333-8333-333333333333","ttl_seconds":1800}
```

```bash
gregale environment release-sets prepare-rollback shop production \
  --file rollback-selection.json > rollback-manifest.json
gregale environment release-sets verify shop production \
  --file rollback-manifest.json --dry-run --json
gregale environment release-sets verify shop production \
  --file rollback-manifest.json --json
gregale environment release-sets rollback shop production \
  --file rollback-manifest.json --json
```

Add `--object-storage-write` when the reviewed verification plan requires it.
The graph must match current project membership and every member must satisfy
current enforced policy, artifact eligibility and service readiness. Verification
uses exact historical service targets. Preparation and verification do not switch
routes or wake targets. Expired graph history cannot be recovered even when its
deployments remain live or pinned independently. The historical source and the
expected current release are rechecked atomically, with expiry rechecked at
PostgreSQL commit. A failure leaves the current graph intact.

Recovery creates a new release. Release inventory exposes
`rollback_source_release_id` and `rollback_previous_release_id` as its durable
recovery record. The API uses `POST .../release-sets/prepare-rollback` and
`POST .../release-sets/rollback`; use an `Idempotency-Key` for API mutation retries.
A stale unkeyed retry fails rather than publishing a second recovery. This does
not undo application data migrations or lift GitOps ownership restrictions.

## Deactivate a project graph with checked fallback routing

Observe the active graph and its weighted fallbacks, qualify them, then explicitly
deactivate. Every current workload must have exactly one eligible materialized
live 100% fallback in the environment and an enforced binding policy. Missing
fallbacks and traffic splits block preparation.

```bash
gregale environment release-sets deactivation prepare shop production \
  --json > deactivation-manifest.json
gregale environment release-sets verify shop production \
  --file deactivation-manifest.json --dry-run --json
gregale environment release-sets verify shop production \
  --file deactivation-manifest.json --json
gregale environment release-sets deactivate shop production \
  --file deactivation-manifest.json --json
```

Supply `--object-storage-write` when the reviewed verification plan needs a
storage canary. Preparation and verification keep current routing. Internal
service probes qualify the exact fallback deployments; they do not wake targets.
Deactivation rechecks the current graph, complete fallback routing, current
policies, fresh evidence, artifacts and service readiness atomically. Changed
routing, stale proof or missing readiness keeps the active graph intact.

The manifest has `deactivate: true` and `ttl_seconds: 0`. No new deployment
retention setting is required for already-serving fallbacks. Retirement preserves
the graph's existing TTL and retained member access for requests carrying its ID.
New requests use the qualified weighted routes. The retired release's
`deactivation` field records the receipt ID, release ID, completion time and every
fallback app/deployment pair. The API routes are `POST .../release-sets/prepare-deactivation`
and `POST .../release-sets/deactivate`; use an `Idempotency-Key` for mutation retries.
The same key replays the completed receipt; stale unkeyed requests fail. Existing
unchecked deactivation and GitOps-managed workloads remain blocked under enforcement.

## Stage candidate services before graph qualification

A prepared forward or rollback graph may select a dark service deployment with
no ready replicas. Service probes do not wake targets. Use a temporary staging
lease to bring those exact service deployments up through the normal
admission and readiness gates, while keeping current weighted and graph routes.
Selected deployments must already be live and materialized; use the existing
dark-deployment preparation workflow first. GitOps-managed workloads, active
service rollouts, mirror recipients and deactivation manifests are excluded.

```bash
gregale environment release-sets stage create shop production \
  --file manifest.json --ttl-seconds 600 --json > stage.json
stage_id=$(jq -r .id stage.json)
gregale environment release-sets stage status shop production \
  --stage "$stage_id" --json
# Once status is ready, qualify exact targets and explicitly publish the graph.
gregale environment release-sets verify shop production --file manifest.json
gregale environment release-sets activate shop production --file manifest.json
# Use rollback instead of activate for a prepared rollback manifest.
```

`stage-status` reports `waiting`, `ready`, `blocked`, `expired` or `cancelled`,
with per-service ready/desired counts and a reason for blockers. Capacity, startup
or dependency failures and changed manifests are observable. Readiness is an
observation, not binding evidence: activation still independently checks current
policies, exact service evidence, membership, artifacts and replica readiness.
Staging gives function and worker members no residency lease.

The capacity is billable and uses normal concurrency and residency limits.
Staging preserves serving allocations and provides no rollout overlap allowance.
A full plan can block the candidate until spare capacity is available. The lease
lasts 1–1800 seconds; repeating the same manifest reuses the original stage ID
and expiry. Another manifest conflicts while that scope has an unexpired,
uncancelled stage. Cancel before staging a different selection:

```bash
gregale environment release-sets stage cancel shop production \
  --stage "$stage_id" --json
```

Cancellation and expiry remove only the temporary allocation. An in-progress
boot may complete and is cleaned up by normal reconciliation. Current traffic
and active graph allocations remain authoritative, so cancellation does not tear
down a service serving the active graph. Active graph members retain their desired
service allocation after activation invalidates the staging manifest.

The API routes are `POST .../release-sets/staging` with
`{ "manifest": <prepared manifest>, "ttl_seconds": 600 }`, and
`GET` / `DELETE .../release-sets/staging/{stage}`. They enforce existing account,
project and environment scope, MFA and deploy/read permissions. Mutation retries
support `Idempotency-Key`. Release-set staging is not enabled for every account
yet.
