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

The starter binds loopback locally and the guest interface on Gregale.
`gregale.yaml` controls start, port and health. `gregale-mcp.json` controls the
MCP endpoint, stateless mode, legacy compatibility, trusted browser origins and
client authentication. The default starter explicitly allows public access to
four harmless tools, a welcome resource, a customer-record URI template and a
summarize prompt. An empty allowed-origins list rejects all browser origins; add
exact origins for trusted browser clients.

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
the Node starter can optionally persist and run task-enabled tools. Tasks use
the modern `2026-07-28` protocol. The starter includes `build_report`, a safe
example that returns a task handle for opted-in clients when durable tasks are
enabled. Other calls stay synchronous. The server returns the handle only
after PostgreSQL can read the new row.

Tasks are disabled by default. Enable them in `gregale-mcp.json` and bind a
PostgreSQL URL whose database role can create and alter the task and owner-cursor
tables, indexes, claim-order sequence, functions, and triggers. Keep a stable
32-byte-or-longer secret in `MCP_TASK_OWNER_KEY`:

```json
"tasks": {
  "enabled": true,
  "database_url_env": "DATABASE_URL",
  "owner_key_env": "MCP_TASK_OWNER_KEY",
  "namespace_env": "FAAS_APP_ID",
  "ttl_seconds": 86400,
  "poll_interval_ms": 2000,
  "worker_concurrency": 1
}
```

For local runs, set `MCP_TASK_NAMESPACE` when `FAAS_APP_ID` is absent. Keep the
owner key stable for the namespace; it derives the per-caller owner hash and
the legacy payload key. Use a separate versioned payload key ring for rotation.
The task table is scoped by app namespace, stores no bearer tokens or plain caller subjects, and deletes
expired rows. TTL is configurable from 60 seconds to 30 days. An explicit
`MCP_TASK_NAMESPACE` overrides `FAAS_APP_ID`; set the same stable value in every
app that shares the task database.

By default, the HTTP process also claims tasks for single-process use. The Node
starter can instead run a separate Gregale worker app with the same source. Set
`MCP_TASKS_ROLE=web` on the HTTP app so it serves MCP requests without claiming
work. Deploy a second app from the same source with `hosting.start` set to
`npm run start:tasks-worker`, plus a worker lifecycle block. Bind the same
PostgreSQL database and `MCP_TASK_OWNER_KEY` to both apps, and set the same
explicit `MCP_TASK_NAMESPACE` on both because their Gregale app IDs differ. For
example, the worker app's `gregale.yaml` can declare:

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

The worker has no HTTP listener. Set `MCP_TASKS_SCALING_APP_SLUG` to the worker
app's Gregale slug and `MCP_TASKS_SCALING_TOKEN` to an API key restricted to
`metrics:write`; the optional `GREGALE_API_URL` selects a non-production API.
Each worker publishes the namespace's aggregate outstanding-task count and
oldest task age as `mcp_tasks_outstanding` and
`mcp_tasks_oldest_age_seconds`. These gauges include no task or caller
identifiers. The custom target scales to `ceil(outstanding / target)`, so use
the task worker concurrency as the target. Keep `min: 1` while using this
in-process reporter: at zero workers no process can refresh the metric. Custom
metrics require Hobby or higher. If the pool is empty, Gregale starts its
configured minimum so the in-process publisher can send its first reading;
afterward, missing or stale metrics preserve the current pool rather than
interpreting an unavailable database signal as an empty queue. On `SIGTERM`,
the worker stops claiming new tasks and waits for active
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

Custom task handlers can request client input with `requestInput(key, request)`
or `requestInputs({key: request})` in the execution context.
The task moves to `input_required`, and `tasks/get` returns outstanding
`inputRequests`. The client answers with `tasks/update` and `inputResponses`;
the worker then replays the handler from its beginning with the saved responses
available under the same keys. Request and response payloads are encrypted
with the task key, and request keys cannot be reused for a different prompt.
The original client must have declared support for each embedded request type.
Keep handlers idempotent because worker recovery can also replay them after a
crash.

Modern clients that declared the Tasks extension can open
`subscriptions/listen` with `notifications.taskIds` to receive current task
snapshots and subsequent `notifications/tasks` updates over one SSE stream.
The starter acknowledges only task IDs visible to that caller and allowed by
the tool policy. Reopening the stream sends fresh snapshots, while `tasks/get`
polling remains available as a fallback. PostgreSQL `LISTEN`/`NOTIFY` carries
worker updates to the web process; bounded polling takes over if a database
connection or pooler cannot keep a listener open.

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

With Tasks enabled in the Node starter, `task://tasks/{taskId}` exposes the
caller-visible task status and result as a resource. Reading it requires both
ownership of the task and permission for its originating tool, plus the
resource's configured scope. `resource-watch` receives resource updates as the
task moves or completes; the starter uses the task store's PostgreSQL listener
and bounded polling recovery to fan changes out to web replicas.

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

The Node starter publishes per-resource updates for `task://tasks/{taskId}` when
Tasks are enabled. With Tasks disabled, the starter has no changing sample
resource; watch a URI that your own server actually exposes. Notification
streams still reconcile on `--interval`, and servers without URI subscriptions
use polling.

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
    "add": ["math:read"],
    "stream_demo": ["mcp:stream"]
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
audience. The starter serves RFC 9728 protected-resource metadata and a bearer
challenge. It verifies signed RS256/ES256 JWT access tokens with issuer, audience,
expiry, subject and all configured scopes. The provider owns login, consent,
client registration and token issuance. Opaque tokens require an introspection
adapter. `auth.scopes` are required for every request and are advertised with
catalog scopes in protected-resource metadata. In the Node starter,
`tool_scopes`, `resource_scopes` and `prompt_scopes` add application-owned
permissions for tools, resource URIs (including URI templates) and prompt names.
Every listed scope is required in addition to the endpoint scopes. An explicit
empty scope array permits any authenticated endpoint caller to use that entry. A
configured map denies entries missing from it; `{}` hides and denies every entry
of that catalog type. Null maps and scope arrays fail configuration validation.
Open mode accepts only empty scope arrays. The generated public starter lists its
harmless entries explicitly.

The starter filters `tools/list`, `resources/list`,
`resources/templates/list` and `prompts/list` using verified JWT scopes for each
request. It checks `tools/call`, `resources/read` and `prompts/get` before dispatch
and checks verified request context again inside each registered callback.
Missing entry scopes return HTTP 403 with an `insufficient_scope` bearer
challenge naming the endpoint and entry scopes; an unlisted entry returns a
catalog-specific `*_access_denied` error. Resource policy keys are exact resource
URIs or URI-template patterns, such as
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

For worker scale from zero, deploy `npm run start:tasks-observer` in a separate
always-running app/process. Set the same `MCP_TASK_NAMESPACE`, a read-only
`DATABASE_URL`, and the worker app's scoped `MCP_TASKS_SCALING_APP_SLUG` and
`MCP_TASKS_SCALING_TOKEN`. Enable Tasks in its config. Initialize the schema first
through a web/worker process. The observer only reads aggregate queue metrics,
never migrates schema or claims tasks, and needs no `MCP_TASK_OWNER_KEY`. It
publishes the existing custom metrics every 15 seconds. Configure the worker's
custom scaling target and `worker.scale.min: 0`; keep the observer at a minimum of
one running process. An in-process worker publisher still requires a worker
minimum of one. Monitor observer health and metric freshness.

The CLI can configure and inspect the worker policy:

```sh
gregale mcp tasks setup --app mcp-worker
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
Initialize the updated schema through an upgraded web or worker process before
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

Rotate in phases: upgrade all web and worker processes with the complete union
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
claiming Tasks, or exposing payloads or secrets. Initialize the schema through
normal runtime startup before running this gate. It verifies runtime DML and
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
