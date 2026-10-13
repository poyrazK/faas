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

## Enable reviewed release actions

Add `--allow-writes` to the agent's subprocess arguments to expose
`gregale_apply_deploy`, `gregale_promote_deployment`, `gregale_apply_rollback`,
`gregale_apply_environment_promotion`, `gregale_apply_environment_rollback`,
`gregale_apply_workload_rollout`, and `gregale_apply_service_abort`.
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
builds use the source workflow below. Mutable image tags, local uploads,
and app creation retain their CLI release workflows.
Image and reviewed source service/worker rollouts use the native workflow below. Apps with multiple serving
releases require explicit traffic-split review in the CLI. Secret-value and
arbitrary configuration mutation APIs are not exposed.

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
The tool catalog has 29 read/planning tools, or
37 with writes.

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
per-entry scopes for tools, resources, resource templates and prompts. Durable
Tasks and legacy transport remain Node starter features. MCP hosting-enabled
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

Choose another starter language with `gregale mcp init --language go --path
./my-mcp-go` or `gregale mcp init --language python --path ./my-mcp-python`.
Both templates use the same zero-traffic `gregale mcp deploy` verification and
promotion flow.

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
The Node starter registers sample completions; the Go and Python starters expose
the sample prompt and resource template without completion handlers.

The starter binds loopback locally and the guest interface on Gregale.
`gregale.yaml` controls start, port and health. `gregale-mcp.json` controls the
MCP endpoint, stateless mode, legacy compatibility, trusted browser origins and
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
the Node starter can optionally persist and run task-enabled tools. Tasks use
the modern `2026-07-28` protocol. The starter includes `build_report`, a safe
example that returns a task handle for opted-in clients when durable tasks are
enabled. Other calls stay synchronous. The server returns the handle only
after PostgreSQL can read the new row.

Tasks are disabled by default. Enable them in `gregale-mcp.json` and bind a
PostgreSQL runtime binding. Use a separate migration account to create and alter
the task and owner-cursor tables, indexes, claim-order sequence, functions and
triggers with `npm run tasks:migrate -- apply` before starting the runtime. Keep a stable
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
migration account owns the schema/objects and has DDL privileges and that
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
    "add": ["math:read"]
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
`notifications.resourceSubscriptions` containing canonical app `release`, `health`
or `budgets` URIs from the table above. The server acknowledges the accepted set
and reports `_meta.gregale_subscription_expires_at`. On a meaningful change, it
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
Gregale's three review prompts and seven resource templates. Suggestions cover
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
