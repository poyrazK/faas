# MCP servers on Gregale

The MCP hosting profile is preview for stateless servers that advertise at least
one MCP catalog: tools, resources (including resource templates), or prompts.
It uses ordinary HTTP applications and stateless Streamable HTTP, with optional
stateless legacy compatibility. MCP hosting-enabled builds provide the commands
below; check `gregale mcp --help`.
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

In another terminal:

```sh
gregale mcp doctor --url http://127.0.0.1:8080/mcp --legacy --stream-tool stream_demo
gregale mcp call --url http://127.0.0.1:8080/mcp --tool add --arguments '{"a":7,"b":5}'
gregale mcp deploy --path . --name my-mcp --profile small
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
All three reject browser origins by default. Only the Node starter implements
configurable CORS; Go and Python continue rejecting origins even if
`allowed_origins` is set in the manifest.

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
PostgreSQL URL whose database role can create and alter the namespaced task
table, indexes, notification function, and trigger for status subscriptions.
Keep a stable 32-byte-or-longer secret in
`MCP_TASK_OWNER_KEY`:

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
configured role has the required create/alter/index DDL privileges and that
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
