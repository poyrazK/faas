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
| `gregale_preflight` | Read-only app/environment consent, release-review/health/operations compatibility, schedule activity/recovery prerequisites and durable-state checks. Unknown capabilities require native review/admission. |
| `gregale_context` | Selected profile, API endpoint, account ID, plan, status, and platform capabilities. |
| `gregale_recover_environment_cache_activity_consumer` | Recover a bounded baseline page for one consumer, binding continuations to its revision/generation and returning explicit ACK arguments only on completion. |
| `gregale_get_environment_cache_activity_consumer_health` | Bounded consumer readiness/lag details with summary counts for all saved consumers in the selected scope; no processing cursors or ACK drafts. |
| `gregale_watch_environment_cache_activity_consumer` | Resume a bounded event watch from saved consumer progress, returning an event page and explicit acknowledgment arguments with revision/generation guards. |
| `gregale_get_environment_cache_activity_consumer` | Read a named consumer's saved cursor, revision and expiry status from the encrypted bridge journal; current environment read consent required. |
| `gregale_ack_environment_cache_activity_consumer` | Explicit bridge-local checkpoint acknowledgment with revision guard; available with read consent and durable state, without enabling platform writes. |
| `gregale_list_environment_cache_activity_consumers` | Bounded, name-sorted consumer metadata for one app/environment, with scoped continuations. |
| `gregale_retire_environment_cache_activity_consumer` | Explicitly delete one checkpoint with exact revision/generation checks, freeing capacity and fencing stale acknowledgments. |
| `gregale_list_apps` | Apps sorted by slug; `limit` defaults to 20, maximum 100; continue with `next_offset`. |
| `gregale_get_app` | One app's status, URLs, deployment availability, and resource settings. |
| `gregale_list_deployments` | Account deployment history; bounded `limit`; continue with `next_before`. |
| `gregale_get_deployment` | Build/deployment status, commit and image identifiers, and failure diagnostics. |
| `gregale_get_logs` | Newest entries from a non-following log snapshot; optional deployment, level, literal substring, and RFC3339 timestamp filters. |
| `gregale_get_metrics` | App metrics over the requested `range`; the API's default is `5m`. |
| `gregale_list_app_deployments` | App deployment history with `next_before` pagination. |
| `gregale_list_instances` | Instance pages with optional history; host addresses are excluded. |
| `gregale_get_health` | Current health, readiness checks, freshness, and serving releases. |
| `gregale_get_route_awake` | Scoped route wake attribution, running-time estimates and unattributed time from retained deployment history. |
| `gregale_get_route_advice` | Scoped route advice and deployment-isolated learned cacheability evidence; read-only. |
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

### Management MCP awake diagnosis

Use `gregale_awake_diagnosis` with `app` and optional `project`/`environment`
for a read-only investigation using health, routes and route budgets. The prompt
requires no deployment ID and never applies rules or settings.

`gregale_get_route_awake` and `gregale_get_route_advice` accept `app`, optional
`since` (positive duration such as `7d`, or RFC3339) and `until` (RFC3339).
Advice also accepts `cache_max_age_seconds` within the native cache lifetime
bounds, omitted for the native default. Native plan gating, retention clamping,
window validation and telemetry availability still apply. API errors remain
errors; missing telemetry must not be interpreted as zero wakes or safe caching.

The bridge always sends an explicit environment selector, including
`scope=default` for default reads. Successful reports carry exact app/scope and
named project/environment identities, `coverage: retained_deployment_history`
and `partial: true`; the tool envelope also carries `estimates_only: true`.
App/environment consent and immutable identities are checked before and after
collection. Missing scoped coverage, another environment or an older aggregate
response fails closed; no unscoped retry is made.

Usage joins retained instances to deployments. Request evidence joins retained
deployments, and named deployments must be pinned to a workload spec belonging
to the exact environment ID. History missing that mapping, unknown deployment
rows, deleted instances and telemetry gaps are excluded, not evidence of zero
activity. Unattributed running time **within the selected history** remains in
the report. These partial estimates do not reconcile an environment's bill.

Scoped advice accepts only `deployment_v1:` learned body observations, whose
comparison cache is keyed by exact deployment as well as URL. Older app-wide
comparisons remain unknown. Cacheability coverage is
`deployment_isolated_observations_only`; no response bodies or hashes leave the
gateway. Scoped advice omits app-wide wake/priority evidence and mutation rule
bodies, and includes a bounded `policy_context` plus a `policy` assessment on each
suggestion.
Native CLI/API callers that omit both selectors retain aggregate reports.

Policy observations merge the gateway's host-matched and `@app` rules, keeping
only the selected app/account and active cache, async and throttle kinds.
Independent named cache policy replaces inherited cache rules when present;
async and throttle continue to inherit. Headers/CORS remain outside this
assessment. Default-host rules are never substituted for a named environment
host. Rule actions, header
condition values and structured conditions are not returned.

`policy_context.status` is `observed`, `unavailable` or `changed`. Observed
snapshots carry a scoped revision fingerprint based on rule identity/update
metadata, not private action values or an account-wide counter. It is a review
observation, **not a write guard**. A second read detects changed snapshots;
storage failures, missing readers, missing revisions and more than 100 relevant
rules produce explicit gaps. Disabled drafts, other hostnames, other gateway gates and actual
gateway convergence are not evaluated. Telemetry may span several hostnames;
policy assessments apply only to the reported platform/environment host. Snapshots are optimistic observations,
not a transaction spanning telemetry and policy.

Suggestions distinguish `duplicate`, `conflict`, `no_matching_rule`,
`review_required` and `unavailable`, with at most 10 matching rule IDs.
Configured payload equality yields a duplicate; differing settings are flagged
for review as a conflict, without assuming runtime defaults are equivalent.
Header/structured conditions, log mode, route templates, equal-priority matches
and cross-kind overlaps require review. Matching reuses gateway path semantics,
including protective matching for throttles and cache method intersections.
Neither a matching policy nor its absence proves that a change is safe.
Older native APIs without policy evidence remain readable with explicit
`unavailable` coverage; the bridge strips rule bodies and never invents policy
absence. The awake diagnosis prompt requires reviewing these gaps and revisions.

Reports preserve polling patterns, unattributed time, what-if assumptions,
retention clamping and learned content evidence. Savings are estimates at minute
resolution, not billing guarantees; observed response repetition does not prove
that a route is safe to cache. Application descriptions and suggestions remain
untrusted data. The tools read control-plane telemetry and do not probe or wake
an application.

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
The tool catalog has 56 tools with platform writes disabled: 54 read/planning
tools and two explicit bridge-local consumer mutations (acknowledgment and
retirement). Enabling platform writes adds 17 tools, for 73 total.

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
origins. Node implements CORS preflight and exposes protocol response headers;
Go and Python do not provide the same complete browser CORS integration.

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
PostgreSQL URL whose database role can create and alter the namespaced task
table, indexes, notification function, and trigger for status subscriptions.
Keep a stable 32-byte-or-longer secret in
`MCP_TASK_OWNER_KEY`:

PostgreSQL runtime binding. Use a separate migration account to create and alter
the task and owner-cursor tables, indexes, claim-order sequence, functions and
triggers with `npm run tasks:migrate -- apply` before starting the runtime. Keep a stable
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
| `gregale://projects/{project}/environments/{environment}/inventory` | Consented workload/release inventory in one registered environment. |
| `gregale://projects/{project}/environments/{environment}/apps/{app}/{view}` | Named versions of `release`, `health`, and `budgets`. |
| `gregale://projects/{project}/environments/{environment}/apps/{app}/activity-consumers/{consumer}` | Named consumer readiness, saved revision/generation, update time and lag observations; no cursors or ACK arguments. |

Resource templates advertise generic selectors, not unconsented app names.
Reads validate consent and immutable identities before and after collection,
reuse existing safe projections/redaction and bound the serialized response.
Canonical `gregale://` URIs cannot contain credentials, query strings, fragments,
encoded path segments or arbitrary external endpoints. Resource writes are not
exposed; subscription polling requires an explicit request as described below.
Resource contents and API descriptions
remain untrusted application data, not instructions.

`gregale_deployment_review`, `gregale_rollout_diagnosis`, and
`gregale_rollback_review` return scoped release review recipes with exact resource URIs
and existing guarded tool guidance. All require `app`; named selectors also
require `project` and `environment`. Rollout diagnosis/rollback require an exact
`deployment_id` belonging to that app and scope. Unknown arguments are rejected.
Retrieving a prompt reads identity context but creates no release plan, applies
no change and grants no additional permission. Protected promotions still need
native human browser approval. A source rebuild is not an identical artifact
restore; worker residency is not queue completion. `gregale_awake_diagnosis`
uses the same scoped selectors for route wake and cacheability investigation.

`gregale_activity_consumer_review` requires `app`, `project`, named `environment`
and `consumer`. It reads current consumer state through the existing guarded
inspection path, including encrypted-journal availability, fresh feed access and
revision/generation checks. Its response contains the selector, immutable target
IDs, status, revision/generation and lag observations, followed by a review recipe:

- `uninitialized`: complete and process a baseline before choosing initial
  acknowledged progress.
- `ready`: resume using the consumer-aware watch and process the returned page
  before submitting its acknowledgment arguments.
- `cursor_expired`: rebuild and process the full baseline, then explicitly
  acknowledge with `baseline_rebuilt: true`; removed outcomes cannot be replayed.

The recipe also explains refreshing after revision/generation conflicts,
restarting an overtaken baseline, and replay/idempotency after reconnect. It
returns no saved or recovery cursor, event page, or acknowledgment draft. The
snapshot is point-in-time; refresh state before acting. Retrieving this prompt
is a read-only review: it does not acknowledge, retire, process events, create a
consumer, choose a baseline or grant new permission. Consumer retirement remains
a separate explicit operation. Snapshot fields and API content are untrusted
data. Prompt inspection does not add samples to the get-tool lag metrics.
Design: [ADR-1477](adr/1477-mcp-consumer-review-and-completion.md).

The pinned Codex app-server API supports resource reads; prompt retrieval is
verified through the standard MCP SDK in-memory and over OAuth HTTP for both
supported protocol versions. Client UI exposure of prompts may vary.

The named consumer resource requires encrypted durable state and current read
consent for the complete app/project/environment selector. It shares consumer
inspection with the get tool: uninitialized consumers have no chosen baseline,
ready consumers expose retained backlog, and expired consumers separate retained
backlog from the removed history gap. Reads include the consumer name, immutable
IDs/scope, status, revision/generation, optional saved update time and lag fields.
They omit saved/recovery cursors, recovery metadata, hints, event pages and ACK
drafts. Use the consumer watch or recovery tool to obtain a current processing
page and explicit acknowledgment arguments. Reading or subscribing never creates
a consumer, advances progress, processes events or retires state. Resource reads
and polls do not add samples to get-tool consumer lag metrics.
Design: [ADR-1479](adr/1479-mcp-consumer-resources-and-notifications.md).

### Subscribe to scoped resource changes

MCP 2026-07-28 clients can explicitly request `subscriptions/listen` with
`notifications.resourceSubscriptions` containing canonical app `release`, `health`,
`budgets` or named `activity-consumers/{consumer}` URIs from the table above. The
server acknowledges the accepted set and reports
`_meta.gregale_subscription_expires_at`. On a meaningful change, it
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

Consumer notifications track readiness, saved revision, connection generation,
retained backlog, expired-history gap and retention configuration. Observation
time, increasing ACK age and its clock-skew flag do not cause invalidations.
An uninitialized consumer has no chosen cursor, so new feed events alone do not
invent backlog or invalidate it. An explicit ACK changes saved progress;
retirement rotates the connection generation and invalidates its subscribed
consumer resources, including names that become uninitialized. Other connection
namespaces remain isolated. A concurrent ACK/retirement detected during a poll
or recipient check discards that observation and retries on the next poll;
initial subscription and direct-read conflicts remain errors. Each send rechecks
the recipient's native consent, durable grant, immutable target and saved guards.
These notifications are hints to reread current state, not events, leases,
processing evidence or automatic acknowledgments.

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
Gregale's five review prompts and eight resource templates. Suggestions cover
`app`, `project` and `environment`; diagnosis and rollback prompts also complete
`deployment_id`. `gregale_activity_consumer_review` and the named consumer resource
also complete `consumer` for a fully selected app/project/named environment.
Resource references use the exact URI **template** returned by
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
Consumer completion reads the bounded connection journal (at most 100 records),
filters all endpoint/account/immutable target/scope bindings, and then applies
the literal prefix. It does not stop at the first consumer-list page. Only names
are suggested, including expired consumers. Names matching credential-redaction
rules are omitted rather than replaced with unusable labels. Completion does not
validate each saved cursor or imply readiness. Incomplete consumer selectors return no names;
app completion for the consumer prompt and resource also requires project and
named environment, so it never falls back to default scope. Names are isolated by local profile or OAuth
grant, and fresh native read access plus durable consent are required. Failed
reads return errors rather than cached names. Completion does not create records,
advance progress, or start a watch.

Client interfaces may not expose completion even when ordinary MCP reads work.

### Concurrent requests and overload

Each stdio management connection or remote OAuth grant has three independent
admission pools:

| Pool | Concurrent calls |
|---|---|
| Ordinary tools, resource reads, prompts and completion | 8 |
| `gregale_watch_rollout` | 2 |
| Activity watches: raw cursor and named consumer, combined | 2 |

`gregale_watch_environment_cache_schedule_activity` and
`gregale_watch_environment_cache_activity_consumer` share the activity pool.
Waiting activity watches cannot consume ordinary-request or rollout-watch slots,
so acknowledgments and consumer inspection can continue when activity observation
capacity is full. A full ordinary-request pool does not block activity watches.
All HTTP requests and SDK sessions for the same grant share these limits;
separate grants have independent capacity. These bridge limits do not change
Gregale plan quotas. Bounds live in `pkg/api/limits.go`. Design:
[ADR-1476](adr/1476-isolated-mcp-activity-watch-admission.md).

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
| `gregale_management_mcp_active` | Aggregate reservations by `kind`: `short_requests`, `rollout_watches`, `activity_watches`, `listen_streams`, `resource_slots`. |
| `gregale_management_mcp_admission_rejections_total` | Full-pool admission rejections by fixed `kind`: `short_requests`, `rollout_watches`, `activity_watches`. Includes skipped subscription reads using short capacity. |
| `gregale_management_mcp_resource_updates_total` | Invalidation notifications successfully sent to individual recipients. |
| `gregale_management_mcp_consumer_reads_total` | Successful consumer get observations by fixed `status`: `uninitialized`, `ready`, `cursor_expired`. |
| `gregale_management_mcp_consumer_lag_events` | Histogram of event counts sampled on initialized consumer gets, by fixed `kind`: `retained`, `expired`. |
| `gregale_management_mcp_consumer_ack_age_seconds` | Histogram of time since the last progress-changing acknowledgment, sampled on initialized gets with a usable timestamp. |

Outcomes are `success`, `error`, `busy`, `cancelled`, `timeout`,
`checkpoint_conflict`, `consumer_generation_conflict`, `consumer_capacity`,
`durable_state_required`, `durable_state_unavailable`, `cursor_expired` and
`recovery_restart_required`. Invalid journal records count as
`durable_state_unavailable`; arbitrary upstream error codes remain `error`. Tool results
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

For overload, inspect `rate(gregale_management_mcp_admission_rejections_total[5m])`
by `kind` alongside the matching active gauge. The activity-watch gauge includes
the full handler lifetime, including setup and validation, not just sleeping
polls. The rejection counter is initialized to zero for each pool, aggregates all
grants and resets on restart; it has no tool or identity labels. Existing request
metrics with `outcome="busy"` identify the registered tool that was rejected. For slow operations, compare histogram latency by
operation/tool; watch and listen lifetimes are expected to exceed ordinary reads.
Consumer histograms are read-time samples: repeated gets count again, quiet
consumers are absent, and they do not measure total journal backlog or every
consumer's current health. Uninitialized consumers contribute only a status
observation. A successful get with expired progress counts as request `success`
and consumer status `cursor_expired`; a rejected expired ACK counts as request
outcome `cursor_expired`. No background scan is performed.
Get observations are sampled only after final consumer validation and bounded,
sanitized output serialization succeed. Final authorization, revision/generation,
grant/storage and serialization failures add no consumer lag samples. These are
locally accepted response observations, not transport-delivery receipts.

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
an initial refresh grant from your provider and store it as an app default-scope
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

Replacing a default-scope refresh secret now atomically retires the old retained
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


### Reviewed cache-policy updates

`gregale_plan_cache_rule` reviews replacement cache settings for an existing
`@app` rule. Supply `app`, `rule_id`, `reason` and a complete `cache` object.
The review contains the current selectors and settings, normalized proposed
settings, a native rule-set version and a 15-minute expiry. Planning never
writes. `gregale_apply_cache_rule` accepts only the returned `plan_id` and is
available with `--allow-writes`.

These rules are **shared app policy**, including inheritance by named
environments. This initial implementation requires local unrestricted
environment access. Remote default-only and named environment grants are
rejected, including grants with default writes enabled. Do not pass `project`
or `environment`. Native independent named-environment cache ownership is
required before this tool can offer isolated named writes.

Only enabled, unconditional, non-expiring, enforce-mode cache rules with
`match_host: "@app"` are supported. Rule creation, selector changes, conditional
rules and other kinds remain outside this operation. Native cache validation
and defaults apply; the review shows the values that will be sent. Historical
snapshots may contain other actions or private conditions; only the selected
safe rule's cache settings and selectors appear in MCP output or saved plans.

Apply rechecks account, app identity and write consent, then sends the exact
reviewed settings with native `If-Match` and a stable idempotency key. A stale
uncommitted plan receives 412. Retry the same plan after an ambiguous reply;
when a native replay receipt exists it returns the original response before
checking the advanced version.

For restart recovery, add `--state-file ./mcp-state/state` and
`--state-key-file ./mcp-state/key` to the stdio command. The directory must be
private (0700); the independent key file must contain 32 random raw bytes or
64 hex characters and be private (0600). Keep the key across restarts and use
one journal per running stdio process. Local startup authenticates the account
once and binds its journal to account,
API endpoint and profile. It retains expiring plans, attempts and successful
receipts. Stdio connections without these options must retain their running
connection. Reconnect to the same account, endpoint and profile, then retry
the original plan ID before it expires.

Receipts describe the desired policy written by that operation, not current
policy after later edits, gateway convergence, cache eviction or measured
savings. If a crash or storage failure prevents saving a native receipt after
commit, the old version guard prevents a repeated mutation; the retry can
return 412 instead of recovering success. Inspect native history before
planning again. Existing entries are not explicitly invalidated by this tool.

### Environment-owned cache policies

`gregale_get_environment_cache_policy`, `gregale_plan_environment_cache_policy`
and write-gated `gregale_apply_environment_cache_policy` manage cache rules on
one immutable named environment host. These tools work with remote grants that
consent to that exact project, environment and app; apply additionally requires
environment write consent. They do not grant shared app-policy authority.

Pass `app`, `project` and `environment`. Planning also requires `reason` and a
complete `rules` array, for example:

```json
{
  "app": "catalogue",
  "project": "shop",
  "environment": "staging",
  "reason": "Reduce staging catalogue wakes",
  "rules": [
    {"path": "/catalog/*", "priority": 10,
     "cache": {"max_age_seconds": 120, "stale_if_error_seconds": 0}}
  ]
}
```

A missing override reports `inherited: true`, `revision: 0`, and no inherited
rule bodies. Saving a list replaces **all cache rules** for that environment
host. `rules: []` explicitly disables caching there. Other environments,
default hosts, shared app rules and other rule kinds are unchanged. Rules are
unconditional, enforced and non-expiring; the platform assigns host and rule
identity. Up to 20 rules with distinct priorities are allowed. Existing native
cache validation and defaults apply and appear in the reviewed proposal.

Apply accepts only the returned `plan_id`. Reviews expire after 15 minutes;
stale revisions and replaced registry identities fail closed. Keep the same
plan on an ambiguous response. Native intent and a request-hashed receipt
commit atomically, so a retry recovers the original result without creating
another policy revision. The receipt may describe an earlier successful write
if someone has edited policy since then. Grant identities and current write
consent are rechecked before any saved MCP receipt is returned.

Gateway policy refresh uses the existing 30-second cache TTL. Revision-specific
cache-rule IDs prevent previous response entries from being reused after the
new policy loads. A save does not confirm fleet convergence or measured savings.
Authentication, cookie, method and origin cacheability checks still apply.
Scoped route advice includes the environment overlay's safe metadata with
`source: environment_policy`; it continues to omit mutation recipes.

Apply the new database migration before deploying policy readers and deploy
compatible gateways before enabling these writes. Default scope,
manifest/GitOps ownership is not provided. Cloned or recreated environments start
with app inheritance until explicitly configured. Remote bridge state options
retain these plans across restarts; local stdio can use its encrypted journal.

### Verify environment cache-policy propagation

After apply, call `gregale_watch_environment_cache_policy` with `app`, `project`,
`environment`, and the exact positive `revision` from the receipt. This is a
read-only tool and works with environment read consent; writes need not be
enabled. `wait_seconds` accepts 1–45 (default 20), bounded by the connection's
tool timeout.

```json
{"app":"catalogue","project":"shop","environment":"staging",
 "revision":2,"wait_seconds":20}
```

The result includes exact immutable identities, the desired revision,
`checked_at`, and aggregate `serving_gateways`, `acknowledged_gateways`,
`pending_gateways` and `stale_gateways` counts. It exposes no gateway names,
addresses or unrelated policy bodies.

- `converged`: every registered serving compute gateway acknowledged this
  exact revision in its current process session, with node and observer
  heartbeats fresh within 90 seconds.
- `degraded`: no registered serving gateways, missing/stale sessions or
  heartbeats, or changed registry endpoints. This does not mean propagation
  succeeded.
- `timed_out`: eligible gateways were still pending when the bounded watch
  ended. `watch_timed_out` is true; poll the same revision again.

Pending is the intermediate native status while fresh sessions have not yet
acknowledged the revision. The native GET endpoint is
`/v1/apps/{slug}/cache-policy/convergence?environment=staging`; it returns pending,
converged or degraded without waiting. Inherited revision zero has no independent
policy to acknowledge and reports degraded. If desired revision changes during
a watch, the MCP tool fails with `policy_superseded`; inspect current policy
before selecting another revision. Registry replacement or withdrawn consent
also fails closed. Cancellation and unavailable APIs remain errors.

Gateway reconciliation runs every five seconds in pages of up to 100
unacknowledged revisions. It explicitly reloads idle hosts and does not depend
on notifications. Acknowledgement follows successful direct compilation and
cache installation, including empty overrides. Failed readers, last-known
fallbacks, policy races and discarded cache generations do not acknowledge.
New process sessions invalidate old acknowledgements; unchanged policies keep
their acknowledgement while the session is live. Endpoint changes require a
new gateway session. Configure `FAAS_NODE_NAME` to match the registered compute
node; unnamed gateways cannot produce verified fleet evidence.

Apply the convergence migration before deploying observer-enabled gateways.
A save receipt still confirms desired intent; the watch provides a later
sample of registered-gateway evidence, not continuous serving behavior,
unregistered-process coverage or measured cache savings. Existing 30-second
policy-cache refresh continues independently of this observation loop.


### Restore app cache inheritance

Use `gregale_plan_environment_cache_inheritance` to review removing the cache
rules or cache-disabled override for one named environment. It accepts `app`,
`project`, `environment` and `reason`; it writes nothing. Apply the returned
`plan_id` with write-gated `gregale_apply_environment_cache_inheritance`.
Both tools enforce the same exact environment identities and consent as the
cache-policy tools. Applying requires environment write consent. The two apply
tools reject each other's plan IDs.

```json
{"app":"catalogue","project":"shop","environment":"staging",
 "reason":"Restore the app cache rules in staging"}
```

Restoration saves `inherited: true`, an empty `rules` list, and a new positive
revision. It retains a small revision record instead of deleting it, so old
reviews cannot become valid again. App-owned cache rules remain unchanged and
are evaluated live on subsequent gateway reloads; their bodies are not returned
or frozen by this review. An inherited environment may therefore have no cache
rules if the app has none. `inherited: false` and `rules: []` continues to mean
cache disabled, which is a different operation. A later override uses the next
revision rather than starting again at one.

Native clients use the existing guarded PUT
`/v1/apps/{slug}/cache-policy?environment=staging` with `inherited: true` and
`rules: []`, plus immutable IDs, `expected_revision` and `request_id`. Ambiguous
replies are retried with the same body and ID. Native intent and the restoration
receipt commit together; replay returns the original receipt even after a
later edit. Remote persisted plans and local encrypted journals retain this
operation across restarts. Receipts record desired intent at that time.

Watch the returned positive revision with
`gregale_watch_environment_cache_policy`. Gateways reconcile restored revisions
without depending on notifications and reload them again after process restart.
Restoration partitions inherited cache-rule response identities so responses
cached before the override cannot be reused. This evidence confirms the
inheritance-mode revision was loaded, not a frozen version of app-owned rules
or a continuous serving guarantee.

Apply the inheritance migration, then update apid and all gateways before
enabling the new MCP tools or native inherited writes. Older gateways treat
the stored row as an empty override, and older APIs do not support the new
request flag. A rollback of the migration fails while inherited revision rows
remain, rather than silently turning them into cache-disabled overrides.


### Copy cache policy between named environments

`gregale_plan_environment_cache_copy` reviews a one-time copy of an explicit
cache override between two different named environments of the **same app and
project**. It accepts `app`, `project`, `source_environment`, destination
`environment`, and `reason`. Both environments require read consent to inspect
the before/after review. Source write consent is not required. Apply the
returned `plan_id` using write-gated `gregale_apply_environment_cache_copy`;
this additionally requires destination write consent. Source read consent and
both immutable identities are rechecked even for a completed saved receipt.

```json
{"app":"catalogue","project":"shop","source_environment":"staging",
 "environment":"production","reason":"Promote the reviewed staging cache rules"}
```

The review contains `environment_cache_policy.copy_source` (the exact explicit
source snapshot), `before` (destination policy), and `proposed` (destination
replacement with the source pin). Reviews expire after 15 minutes. Apply guards
both source and destination revisions atomically in the native write
transaction. Changing either policy requires a new review before a new copy
can succeed. The original source policy is never mutated. Opposite-direction
copies cannot deadlock on environment lock ordering: writes lock the same app
first, then resolve their environment identities.

An explicit empty source (`inherited: false`, `rules: []`) deliberately disables
destination caching. An inherited source, at revision zero or a restored
positive revision, is rejected; use the inheritance plan/apply tools to restore
live app inheritance. Copies are independent overrides, not ongoing policy
synchronization. Other apps, shared app rules and other rule kinds are unchanged.
New destination rule IDs partition its response cache from the source.

A lost reply is retried with the same plan. The native write and request-hashed
receipt commit together. Receipt recovery returns the original result after
subsequent source or destination edits without overwriting them. Deleted or
recreated source/destination identities cannot recover the old operation. The
source pin participates in the request hash. Remote durable plans and local
encrypted journals retain both snapshots and the exact request across restarts.
The copy, override and inheritance apply tools reject each other's plans.

Native clients use the existing guarded PUT with an optional `copy_from` object:

```json
{"scope":"staging","environment_id":"SOURCE_ENVIRONMENT_UUID","revision":3}
```

The complete destination request still carries immutable destination IDs,
`expected_revision`, `request_id`, and normalized `rules` matching that exact
explicit source. `inherited` must be omitted or false. Native scope and UUID
checks apply even on receipt replay; source revision/content and destination
revision checks apply before a new mutation.

Use the returned destination revision with
`gregale_watch_environment_cache_policy` to verify propagation. A successful
copy receipt alone confirms desired policy. Existing gateway reconciliation,
restart handling and stale-session checks apply to the copied revision.
Upgrade apid before enabling the new tools or native `copy_from` writes. This
feature adds no migration beyond the existing environment cache-policy schema.


### Inspect cache history and roll back a reviewed revision

Use `gregale_list_environment_cache_history` with `app`, `project` and named
`environment`. Optional `limit` is 1–50 (default 20). Pages are newest first;
pass `next_before_revision` as `before_revision` to read older entries. Zero
or omitted `before_revision` starts at newest. Each page includes exact
immutable identities, `current_revision`, and retained policy `entries`.
Use `gregale_get_environment_cache_revision` with the same scope and an exact
`revision` to read one snapshot. Both tools require environment read consent.
They expose historical rules only inside that consented scope and write nothing.

`gregale_plan_environment_cache_rollback` accepts the same scope, the historical
`revision`, and a reviewable `reason`. The review shows `before`, `restore_from`,
and the exact `proposed` replacement under `environment_cache_policy`. Apply
its `plan_id` with write-gated `gregale_apply_environment_cache_rollback`, which
requires current environment write consent. Reviews expire after 15 minutes.

```json
{"app":"catalogue","project":"shop","environment":"staging",
 "revision":2,"reason":"Restore the previous reviewed cache behavior"}
```

Rollback writes a **new** revision using the historical mode and normalized
rules. It never decrements the revision counter. The native transaction guards
the reviewed current revision and compares the requested mode/rules against
the selected historical snapshot. Concurrent edits fail closed. Other apply
tools cannot consume rollback plans. Existing copy provenance is not reapplied:
rolling back a previously copied policy restores its saved destination outcome,
without reading or copying the original source again.

History records explicit rules, cache-disabled overrides (`inherited: false`,
empty rules), and restored inheritance (`inherited: true`, empty rules).
Revision zero is a synthetic initial inherited mode with no timestamp or old
app-rule bodies. Rolling back to zero or another inherited revision follows
**live** app cache rules on reload; it does not restore historical app-owned
rules. Positive revisions unavailable in retained history return not found,
not a guessed snapshot. A later policy may differ while a receipt is recovered.

Intent, history and the request-hashed native receipt commit atomically. A lost
reply is retried with the same plan, including after an encrypted journal or
remote bridge restart. Retry recovers the original successful result without
another history entry or overwriting later policy. Exact identities and current
write consent are checked even before returning a completed saved receipt.
Deleting and recreating an environment cannot recover its old history or plans.

Native reads are GET `/v1/apps/{slug}/cache-policy/history?environment=staging`
and `/v1/apps/{slug}/cache-policy/history/{revision}?environment=staging`.
Native rollback uses the existing guarded PUT with `restore_revision`, matching
`inherited`/`rules`, immutable IDs, `expected_revision`, and stable `request_id`.
`restore_revision` and `copy_from` are mutually exclusive. Watch the returned
**new** revision using `gregale_watch_environment_cache_policy`; an apply receipt
alone is desired intent rather than propagation proof.

Apply the history migration and update every apid writer before relying on
complete new history or enabling these tools. The migration backfills the
current policy and revisions available in existing receipts; it cannot recreate
missing receipts. All subsequent writes through updated apid retain history
until the owning app or environment is deleted. History pagination bounds reads,
not lifetime revision storage. Existing gateway observers need no new protocol.


### Review and verify an environment cache purge

`gregale_plan_environment_cache_purge` takes `app`, `project`, named
`environment`, explicit `path`, and `reason`. Use an exact decoded URL path
such as `/catalog/item` or an explicit empty string to target the entire
immutable environment host. Globs, queries, fragments, percent escapes,
controls and dot segments are rejected. A path purge covers all method,
query, cache-rule, deployment-cohort and vary variants at that path.

```json
{"app":"catalogue","project":"shop","environment":"staging",
 "path":"/catalog/item","reason":"Invalidate the updated catalogue item"}
```

Planning requires exact environment read consent and writes nothing. It shows
current policy for context and a proposed immutable target/request UUID under
`environment_cache_purge`. It does not claim an entry count or inspect private
cached responses. Apply the returned `plan_id` with write-gated
`gregale_apply_environment_cache_purge`, requiring current environment write
consent. Reviews expire after 15 minutes. Other apply tools reject purge plans.
Purge does not modify cache policy or create a policy/history revision.

A stable command row is both the durable replay request and retry receipt.
Its globally commit-ordered `generation` is independent of policy revision.
Retry the same plan after a lost reply, including after encrypted journal or
remote bridge restart. The same request UUID/body recovers the original receipt;
changing its target conflicts. Exact identities and write consent are rechecked
before returning even a completed saved receipt. Deleted/recreated environment
slugs cannot recover old requests.

Read `gregale_get_environment_cache_purge` with `app`, `project`, `environment`
and the returned `request_id`. Environment read consent suffices.

- `completed`: all registered serving compute gateways replayed at least this
  generation in their current process sessions, with node/session heartbeats
  fresh within 90 seconds.
- `pending`: fresh registered sessions have not all published that checkpoint.
- `degraded`: the roster is empty, or sessions, endpoints or heartbeats are
  missing/stale. No vacuous success is reported.

Counts and `checked_at` form a sampled snapshot; node names/addresses are not
returned. A restart or roster change can return a completed request to pending
or degraded until the new process catches up. This is not continuous serving
attestation or a guarantee about unregistered old gateways.

Gateway replay runs alongside environment-policy convergence every five
seconds, with up to 100 commands per pass. New processes replay from zero and
bypass environment caching until caught up; they never trust a prior process's
checkpoint. Notifications are not required. Per-app/environment whole-host and
exact-path generations partition L1 and Redis keys. Late in-flight responses
write old keys and cannot repopulate the current namespace. Redis objects age
out by normal TTL rather than being physically deleted, so no Redis admin
operation is needed for this logical purge. Requests already in flight may
finish; origin/application data freshness is outside cache invalidation proof.
Other immutable environments and default/custom hosts are not targeted.

Native POST `/v1/apps/{slug}/cache/purges?environment=staging` accepts immutable
IDs, `request_id` and explicit `path` (including `""`). Native GET
`/v1/apps/{slug}/cache/purges/{request}?environment=staging` returns status.
Apply the purge migration, then upgrade apid and **all** serving gateways before
enabling these tools. Older gateways do not honor generation keys or publish
these checkpoints. Configure matching `FAAS_NODE_NAME` fleet registration;
unnamed gateways retain bypassed environment caching rather than producing
unverified completion. Commands/receipts live until owner deletion, and replay
work/startup bypass grows with retained command history.

### One-time environment cache schedules

`gregale_plan_environment_cache_schedule` reviews one exact policy replacement
(`rules`, or `inherited: true` with `rules: []`) or purge (`purge_path`, with
explicit `""` for the whole immutable environment host), plus an RFC3339
`execute_at` within 30 days. The review shows immutable IDs, current policy,
complete proposed action and UTC time. Policy guards pin the current revision;
copy/rollback sources are unsupported in schedules. Apply within 15 minutes via
`gregale_apply_environment_cache_schedule` with current named-environment write
consent. Retry the same review after an ambiguous response. At most 100 pending
or executing schedules are admitted per app/environment; terminal receipts remain.

Acceptance delegates the action durably to apid. Disconnecting MCP, revoking the
creating token or changing local consent does not cancel that delegation. Use
`gregale_cancel_environment_cache_schedule` with exact schedule UUID and current
write consent to withdraw it before worker claim. Cancellation races atomically
with claiming and is idempotent once cancelled; executing and terminal schedules
reject cancellation. Suspended account owners can still cancel pending actions.

`gregale_get_environment_cache_schedule` requires exact environment read consent
and distinguishes scheduled, executing, executed, failed and cancelled. Executed
means the desired policy/purge has a durable receipt, not that gateways converged.
MCP status includes separate current gateway evidence; superseded policy receipts
report `policy_superseded`. Zero/stale fleets retain degraded evidence. Workers
revalidate live owner/account eligibility and immutable target, and reject stale
policy revisions. The original action UUID makes retries after lost replies or
worker restarts safe. Transient errors retain a 60-second lease before retry;
known rejections become terminal failures with a stable code. Execution occurs
at or after the timestamp, including delayed recovery after outages. No recurring
schedules, precise deadline guarantee or cancellation of in-progress actions.

Roll out migration `20261016153051002`, then updated apid (including its normal
background workers). Policy convergence and purge completion also require the
previously documented gateway observer/purge migrations and upgraded gateways.
Design: [ADR-1465](adr/1465-environment-cache-schedules.md).

### Discover and reschedule cache actions

`gregale_list_environment_cache_schedules` discovers schedules for one read-consented
named environment. It supports an optional status and execution-time range (`from`
inclusive, `until` exclusive), default 20/max 50 rows, and opaque `after` cursors.
Results are ordered by execution timestamp and UUID. Cursors bind immutable target
and filters; changing either requires restarting pagination. Pages are live, so
new replacements or status changes may require restarting from page one. Discovery
returns action receipts, not newly sampled gateway evidence; use exact schedule
status to inspect convergence.

`gregale_plan_environment_cache_reschedule` reviews a pending schedule's unchanged
action and original policy revision guard, original timestamp, and new timestamp
within 30 days. `gregale_apply_environment_cache_reschedule` requires current
named-environment write consent. Atomic replacement cancels the pending original
and inserts one linked successor, preserving its active quota slot. Worker claim,
cancellation, competing replacement and stale policy guards reject the review.
Identical retries recover the original successor after lost replies, including
terminal successors and later replacements. Existing encrypted plan journals retain
review attempts and receipts across MCP restarts; cached receipts still recheck
current consent. UUID collisions roll back without cancelling the original.

The original's `replaced_by_schedule_id` and successor's `replaces_schedule_id`
preserve the chain. Execution time and action bodies are never edited in place.
To withdraw the new delegation, cancel the **successor UUID**; cancelling an already
cancelled original is idempotent and leaves the successor scheduled. Acceptance,
action execution and gateway convergence remain distinct.

Roll out migration `20261016153051010` before updated apid. Prior schedule/gateway
requirements remain applicable. Design: [ADR-1466](adr/1466-cache-schedule-discovery-and-replacement.md).

### Durable cache schedule activity

`gregale_get_environment_cache_schedule_activity` reads compact accepted,
rescheduled, cancelled, executed and failed events for one currently read-consented
named environment. Page size defaults to 20 and caps at 50. Save the returned
`cursor`, including on an empty page, and pass it as `after` after reconnecting.
`has_more` indicates a committed backlog in that read snapshot; it is not a promise
that another event cannot arrive immediately afterward. Cursors bind the account
and immutable app/project/environment IDs. Foreign, malformed and ahead-of-stream
cursors fail rather than silently skipping future activity.

Schedule changes and their events commit atomically. Sequence allocation uses a
transactional counter retained through commit, avoiding missing late commits.
Idempotent create/cancel/reschedule retries and stale completion leases add no
extra events. A reschedule publishes its cancelled original linked to the
successor, followed by the successor's acceptance linked to the original. Claim
and lease retries are not published as outcomes. Failure entries carry stable
codes; execution entries carry only the resulting policy revision or purge
generation. Rule bodies, decoded purge paths, raw errors and gateway identities
are excluded. Execution remains distinct from gateway convergence; use exact
schedule status for fresh fleet evidence.

Events have a 30-day retention window plus bounded maintenance lag, and are also
removed on account/app/project/environment physical deletion. There is no
historical reconstruction of pruned events. Recording begins in the
updated apid writers, with no invented backfill for earlier changes. Install
migration `20261016153051018`, upgrade **all apid writers/workers**, and establish
an initial schedule-list baseline before relying on activity catch-up. Mixed old
writers cannot publish events; do not enable consumers until rollout completes.
Go, Node and Python clients expose the same bounded read endpoint.
Design: [ADR-1467](adr/1467-durable-cache-schedule-activity.md).

### Bounded schedule activity watches

`gregale_watch_environment_cache_schedule_activity` accepts the same `app`,
`project`, `environment`, `after` and `limit` arguments as the durable feed reader,
plus `wait_seconds` (default 20, maximum 45). It returns the first nonempty page
with `watch_status: "events"`, or an empty page with `watch_status: "timed_out"`
and `watch_timed_out: true`. Save the returned cursor after consuming events;
`has_more` means another page is already available.

An empty timeout preserves the supplied cursor exactly. With no supplied cursor,
it returns a scoped initial checkpoint. Reconnect or retry cancelled requests
with the last consumed cursor. Cancellation and earlier request or authorization
lease deadlines return errors rather than a successful timeout. The effective
wait is bounded by all these deadlines. Each internal observation (approximately
once per second) rechecks consent and immutable target identity. Revoked access,
invalid cursors and invalid responses fail the call. No subscription or background
job survives the request. Events arriving after the last observation remain
available for the next call. This uses the existing durable feed and its rollout
requirements; it adds no migration. Execution receipts do not assert gateway
convergence.

### Activity retention and recovery

Migration `20261016153051026` adds a durable pruned-prefix floor. Upgrade all apid
readers/writers before enabling consumers: old readers cannot report expired
checkpoints correctly. Apid cleanup runs at startup and once a minute, locking
at most 20 available environments and removing at most 1000 expired events per
environment in each pass. A recent event stops prefix cleanup even if later
records have older timestamps. Locked writers are skipped. Failed cleanup rolls
back both deletion and floor advancement. Retention is eventual, not an exact
maximum row count; maintenance backlog or contention can extend the window.
Schedule intent and execution receipts follow their existing lifetimes.

Both MCP activity tools return `cursor_expired` (native HTTP 410) rather than
silently jumping over removed events. The error's `activity_recovery` contains
immutable target IDs, `oldest_cursor`, `restart_cursor` and `retention_seconds`.
The oldest cursor points immediately before the retained suffix; the restart
cursor points at the observed feed head. They are scoped continuations, not
permissions or reservations that pin retained data. An omitted cursor starts at
sequence zero and also expires once any prefix was removed.

To recover current state safely:

1. Save the error's **restart cursor first** and verify its target.
2. List all pages of current schedules for that environment. Treat discovery as
   a live baseline, not an atomic snapshot or a reconstruction of lost history.
3. Resume activity reads or watches after the saved restart cursor. Reconcile
   baseline/event overlap by schedule UUID. Use replay events to identify schedules
   to refresh through exact status; do not overwrite a newer baseline status with
   an older intermediate event. Do not repeat actions merely because an event is
   replayed.
4. Persist each page's next cursor only after consuming that page. If the recovery
   cursor expires while discovery is in progress, restart recovery using the new
   metadata; never silently accept a gap.

Changes during baseline discovery remain after the saved checkpoint and are
therefore replayed. Removed historical outcomes remain unavailable. Current MCP
read consent and immutable target identities are checked before recovery metadata
is returned. Execution and gateway convergence remain separate.
Design: [ADR-1469](adr/1469-cache-schedule-activity-retention.md).

### Guided schedule activity recovery

`gregale_recover_environment_cache_schedule_activity` rebuilds a current schedule
baseline in bounded pages. Supply `app`, `project`, `environment` and optionally
`limit` (default 20, maximum 50). Omit `recovery_token` to start a new recovery.
The tool captures the current feed head before discovering schedules, without
scanning the retained backlog. Each call returns one page of `entries` and:

- `complete: false` with a `recovery_token` to pass unchanged to the next call.
- `complete: true` with `activity_cursor` to pass as `after` to activity reads or
  watches after storing the complete baseline. No continuation token is returned.

Tokens are bounded to 8192 bytes, versioned, and bound to the API endpoint,
account and immutable app/project/environment identities. They contain pagination
metadata, no credentials, action bodies or authorization grants. They are not
signed attestations; do not decode, modify or combine their constituent cursors.
Current consent, target identity, schedule ordering and checkpoint validity are
checked on every call. Reconnects and MCP server restarts can resume using the
same token with the same endpoint/account and current consent. Page size may
change between calls; reduce it if the response exceeds the MCP output bound.
There is no background job or server-side recovery session. On cancellation or
transient failure retry with the same input token; no progress is committed by
the server. When the first call fails, start again without a token.

The activity checkpoint is fixed throughout discovery. New schedules can sort
before an already returned baseline page; their activity remains after the saved
checkpoint, so catch-up still finds them. The tool checks retention before and
after each page without advancing that checkpoint. If cleanup overtakes recovery,
it returns `recovery_restart_required` with HTTP status metadata 410 and validated
`activity_recovery` metadata. No page or replacement continuation is returned.
Discard the partial baseline and call again **without** `recovery_token` to begin
a fresh recovery. Permission loss and invalid responses remain errors rather
than successful partial pages.

Completion means live pagination finished, not that discovery was an atomic
snapshot. Baseline and replay can overlap; refresh exact schedule status for
replayed identities rather than overwriting newer state with an older event.
Pruned history cannot be reconstructed, and baseline completion does not mean
scheduled actions executed or gateways converged.

The native helper is
`GET /v1/apps/{slug}/cache/schedules/activity/checkpoint?environment=NAME`, exposed
by Go, Node and Python clients. Roll out updated apid readers before the MCP
helper, and ensure all writers already publish durable activity. No new migration is added; the durable feed and retention migrations
remain prerequisites. Design: [ADR-1470](adr/1470-guided-cache-schedule-activity-recovery.md).

### Schedule activity compatibility preflight

Before recovery, call `gregale_preflight` with the same `app`, `project` and
named `environment`. Its checks include `schedule_listing`, `schedule_activity`,
`schedule_checkpoint`, `schedule_retention` and `schedule_recovery`. Preflight
reads explicit support from `GET /v1/capabilities` and validates one bounded
schedule page, a scoped checkpoint, and one activity page after that checkpoint.
It does not scan the backlog or change saved cursors. Default-scope calls report
`select_named_environment` for these checks.

- `passed`: advertised support and the necessary scoped reads were validated.
- `unsupported`: this backend explicitly reports a missing prerequisite.
- `unknown`: capabilities were omitted, an endpoint or target was unavailable,
  a response was incompatible, or another prerequisite could not be verified.
- `blocked`: a required read was denied. Check current credentials and read
  consent; upgrading the backend does not grant permission.

The additive `environment_cache_schedules` capability object advertises `listing`,
`activity`, `checkpoint`, and `retention_seconds` (2592000 for the current contract).
`gregale_context` and `gregale://context` expose the same capability object.
It describes the serving API/backend, not plan entitlements or selected-target
authorization. Old APIs can omit it; omission does not prove feature absence.
`schedule_activity_guidance` explains the required reader, migration and writer
upgrade order. It contains no upstream error bodies or private diagnostics.

Guided recovery verifies advertised support on every page, including after
reconnect. Missing/incompatible signals return `backend_upgrade_required`; a
missing capability endpoint returns `backend_capabilities_unavailable`.
Permission and transient failures remain errors. Recovery never emulates a
checkpoint by scanning the feed or substitutes a newer checkpoint after an
endpoint failure. A failed call returns no successful recovery page or cursor.

Roll out updated apid readers before these MCP helpers. Apply the existing
schedule/feed/retention migrations and upgrade all schedule writers before
relying on feed completeness. Capabilities and preflight are sequential evidence
about responding replicas; they do not establish migration health, maintenance
health, fleet-wide compatibility or native execution qualification. Every tool
still validates authorization and response identity. Tool discovery remains
static and makes no API requests. Design:
[ADR-1471](adr/1471-schedule-workflow-compatibility-preflight.md).

### Durable named activity consumers

`gregale_get_environment_cache_activity_consumer` reads acknowledged progress for
an `app`, `project`, named `environment`, and `consumer`. Names start with a
lowercase letter and contain only lowercase letters, digits, underscores or
hyphens, up to 64 bytes. A new consumer returns `status: "uninitialized"`,
`revision: 0` and no cursor. Reading does not create a record or choose a head.

Finish and process the current baseline with
`gregale_recover_environment_cache_schedule_activity`, or process activity from
the beginning if still retained. Then explicitly save its cursor:

```json
{
  "app": "alpha",
  "project": "shop",
  "environment": "staging",
  "consumer": "maintenance-agent",
  "cursor": "<completed baseline activity_cursor or processed page cursor>",
  "expected_revision": 0,
  "expected_generation": "<generation from the latest consumer get/list>"
}
```

Send these arguments to `gregale_ack_environment_cache_activity_consumer`. It
returns `status: "ready"`, the saved cursor, update time and a new `revision`.
Use that cursor as `after` in activity reads or watches, process the returned
page, then acknowledge its cursor with the last saved revision. Neither reads
nor watches advance the checkpoint. Store baseline contents and downstream work
in your own application; this journal saves progress metadata only.
Pass `generation` from the latest get/list response as `expected_generation` on
each acknowledgment. It starts as an empty string for existing journals and
changes whenever any consumer in the same connection is retired.

Concurrent different acknowledgments using the same revision have one winner;
others return `checkpoint_conflict`. Read the consumer again before deciding
what to process or acknowledge. Backward updates return `checkpoint_regression`.
Retry the exact cursor and expected revision after a lost response: the last
successful acknowledgment can be returned without incrementing its revision.
Acknowledging the unchanged current cursor is also a no-op. Crashes before an
acknowledgment can replay events; acknowledgments declare processing and do not
prove external effects occurred or provide exactly-once processing.

A saved cursor below retention returns `status: "cursor_expired"` from get,
with its unchanged revision/cursor and validated `activity_recovery` metadata.
Complete and process a fresh baseline, then acknowledge its activity cursor with
that revision and `baseline_rebuilt: true`. Without this explicit confirmation,
acknowledgment over an expired checkpoint fails. An already expired replacement
also fails. The tools never jump to the head automatically or restore pruned
historical outcomes. Validation is point-in-time; retention can continue moving.

Both tools require the existing encrypted `--state-file` and independent
`--state-key-file`. Without configured durable state they return
`durable_state_required`; unhealthy storage returns `durable_state_unavailable`.
`gregale_preflight` reports `activity_consumer_state` separately from backend
workflow compatibility. Restore storage before retrying an uncertain commit;
retain the original encryption key. Committed progress survives reconnect and
bridge restart using the same namespace and key.

Consumers are isolated by local profile or remote OAuth grant, API endpoint,
account, immutable app/project/environment and consumer name. Fresh read consent,
target identity and bounded feed access are checked on every call. Revocation
removes the remote grant's consumer namespace and prevents in-flight saves from
recreating it. A newly consented connection has its own namespace. Each connection
can retain at most 100 consumers across all targets. Consumers also share the
journal's byte budget; `consumer_capacity` leaves existing checkpoints readable
and does not disable the journal. Managers using one StateStore coordinate
revisions; separate state files are independent, and
the existing single-writer file lease remains required.

Acknowledgment is a bridge-local metadata mutation available with read consent,
including when platform writes are disabled. It advertises `readOnlyHint: false`,
`destructiveHint: false`, and `idempotentHint: true`; it gets mutation drain grace
and sends no native mutation requests. No plan or platform write approval is
created. Design: [ADR-1472](adr/1472-durable-mcp-activity-consumers.md).

### Final validation of consumer reads

Consumer get, watch, baseline recovery, list and health responses are checked after
the shared tool wrapper's final native authorization reads and before output
serialization. This catches consumer or grant changes during those additional
reads, after the individual handler has already collected its result. Private
guard context is never returned and the existing response/token formats remain
unchanged.

Get, watch and recovery recheck the exact consumer revision and connection
generation, including uninitialized/expired states, empty watch timeouts, partial
recovery pages and completed ACK drafts. List and health validate the complete
selected scoped record set, so an out-of-page change during the request also
withholds its page and continuation. A detected scoped change returns
`checkpoint_conflict`; any retirement in the connection returns
`consumer_generation_conflict`. Grant/storage failure, permission/identity loss,
cancellation and drain remain errors. Failed final validation exposes no stale
progress, pages, tokens, lag observations, recovery guidance or ACK drafts.

List pagination remains live between requests: an ACK between completed list
calls does not invalidate its generation-bound continuation. Health continuations
retain their stronger scoped-record fingerprint. Exact no-op ACK retries and
changes in another scope do not cause false conflicts; single-consumer reads also
permit another consumer's ACK. No journal locks span native reads and final guards
add no native requests, writes, subscriptions or persistent state.

Only successful exact consumer gets sample lag metrics, after final validation
and output serialization. Watches, recovery, list, health, prompts and resource
reads remain quiet. These read guards do not retroactively reject committed ACK
or retirement receipts on later progress changes. Observations and ACK drafts
remain point-in-time data rather than processing or retention leases; ACK still
validates current consent, generation/revision and cursor retention separately.
Design: [ADR-1481](adr/1481-final-consumer-read-validation.md).

### Recover a baseline for a named consumer

Call `gregale_recover_environment_cache_activity_consumer` with `app`, `project`,
named `environment`, `consumer`, and optional `limit` (default 20, maximum 50).
Omit `recovery_token` to start a new baseline. Use it to initialize a new consumer,
rebuild expired progress, or deliberately rebuild a current consumer's baseline.
The tool requires encrypted durable state and current read consent; it performs
no native or journal writes.

Each response contains the consumer name, captured `revision` and `generation`,
and one bounded schedule page:

- `complete: false`: process and retain the page, then pass its opaque
  `recovery_token` unchanged to the next call. There are no acknowledgment
  arguments or `activity_cursor` yet.
- `complete: true`: finish processing the entire accumulated baseline, then
  explicitly submit the returned `acknowledgment` object to
  `gregale_ack_environment_cache_activity_consumer`. Its cursor is the feed
  checkpoint captured before the first baseline page, and its guards are the
  original consumer revision/generation. No recovery token is returned.

For an existing consumer, the draft includes `baseline_rebuilt: true` even if its
old checkpoint was initially ready: retention may expire that old checkpoint
while the newer recovery checkpoint remains valid. For a new consumer the draft
uses revision zero and does not claim prior progress. Merely completing recovery
never saves either draft or cursor.

Continuations are strictly decoded, bounded to the existing 8192-byte recovery
limit, and scoped to the connection namespace, consumer, endpoint, account and
immutable app/project/environment. Consumer tokens and raw schedule-recovery
tokens are different formats; do not interchange them. They are untrusted
pagination metadata, not authorization or proof that pages were processed.
Filtering or editing them cannot grant access; the ACK tool still checks current
consent, identity, revision/generation, cursor validity and forward progress.

The tool checks consumer state before and after native recovery, including failed
reads. If another acknowledgment changes its revision, it returns
`checkpoint_conflict`. Any retirement in the connection invalidates the captured
generation and returns `consumer_generation_conflict`. Both failures withhold the
page, continuation and ACK draft. Discard partial results and restart without
`recovery_token` after inspecting current consumer state.

If retention overtakes the captured recovery checkpoint, the response is
`recovery_restart_required` with validated scoped recovery guidance, without a
page, replacement token or ACK draft. Restart the whole baseline without the old
token. Permission loss, revocation, storage failure and cancellation remain
errors. Reconnect or restart with the same journal/key and namespace to continue
an otherwise valid token; no server-side recovery session is stored.

The baseline remains a live paginated view, not a frozen snapshot. After the
explicit final ACK, watch by consumer name and reconcile activity overlapping
baseline rows by schedule identity. Earlier-sorting concurrent changes are
replayed from the original checkpoint. Consumer guards are point-in-time checks,
not leases; a delivered ACK draft can still become stale or expire. Keep
processing idempotent and never infer downstream completion from an ACK receipt.
Recovery uses ordinary short-request admission, not activity-watch capacity.
Design: [ADR-1478](adr/1478-consumer-aware-baseline-recovery.md).

### Watch activity using saved consumer progress

Call `gregale_watch_environment_cache_activity_consumer` with `app`, `project`,
`environment`, `consumer`, and optional `limit` and `wait_seconds`. It resumes
from that consumer's durable checkpoint; no caller-supplied `after` is needed.
Limits match activity watches: 20 events by default, maximum 50, with a default
20-second observation wait and maximum 45 seconds. The serving command's request
deadline can end the call earlier; use `--timeout 1m` for a 45-second watch.
Polling uses the existing one-second interval and the separate activity-watch pool
(two watches per connection/grant, shared with raw activity watches).
There is no background subscription or journal write.

The response's top-level `cursor`, `revision` and `generation` describe saved
progress. They stay unchanged while watching. Interpret `watch_status` as:

- `events`: `page` contains one bounded activity page, including its own `cursor`
  and `has_more`. Process all events, then pass the returned `acknowledgment`
  object as arguments to `gregale_ack_environment_cache_activity_consumer`.
  This object contains the candidate cursor and exact revision/generation.
- `timed_out`: `watch_timed_out: true` and an empty `page` whose cursor preserves
  saved progress. No acknowledgment arguments are returned.
- `recovery_required`: status is `uninitialized` or `cursor_expired`. No page or
  acknowledgment arguments are returned. Complete and process baseline recovery,
  then explicitly acknowledge its cursor with the returned revision/generation;
  expired consumers also require `baseline_rebuilt: true`. Watching never chooses
  an initial baseline or silently skips expired history.

For example:

```json
{
  "app": "alpha",
  "project": "shop",
  "environment": "staging",
  "consumer": "maintenance-agent",
  "limit": 20,
  "wait_seconds": 20
}
```

The watch pins saved revision and connection generation across each poll and
before returning, including empty timeouts and recovery guidance. A detected
concurrent acknowledgment returns `checkpoint_conflict`; retirement returns
`consumer_generation_conflict`. Refresh current consumer state and start another
watch after deciding how to proceed. These errors withhold the old page and
acknowledgment draft. Current native read consent, immutable target identity and
durable grant/storage checks still apply; failures and parent cancellation are
errors rather than successful empty timeouts. Only the watch's own observation
deadline can yield `timed_out`, after at least one successful empty observation.

Reconnect or restart the bridge with the same journal/key and watch by consumer
name again. Unacknowledged pages replay; reading a page does not reserve it.
Concurrent workers can observe the same events and acknowledgment conflicts do
not undo external side effects. Use downstream idempotency when needed. A draft
acknowledgment may become stale or expire after delivery; the ACK tool revalidates
it. Lag metrics remain sampled by exact consumer gets; watches do not add lag
samples. Design:
[ADR-1475](adr/1475-mcp-consumer-aware-activity-watch.md).

### Consumer lag diagnostics

Consumer get responses include `lag` for the returned revision and generation:

- `observed_at` and `retention_seconds` describe when the feed bounds were
  observed and the configured history window (30 days).
- `retained_pending_events` counts retained events after the saved cursor.
- `expired_unacknowledged_events` counts the removed prefix after the saved
  cursor, separately from retained pending events. These outcomes cannot be
  replayed; rebuild the complete baseline when status is `cursor_expired`.
- `seconds_since_ack` measures time since the last progress-changing
  acknowledgment. Exact retries and unchanged-cursor acknowledgments preserve
  that timestamp. Clock skew that puts it in the future omits the age and sets
  `ack_time_in_future: true`.

Uninitialized consumers omit both event counts and acknowledgment age because
no baseline has been chosen. Zero counts are emitted for initialized consumers
that have caught up. An expired consumer can have zero retained events while
still having a nonzero lost history gap. For example, saved sequence 5, retention
floor 8 and head 12 produce 4 retained pending and 3 expired events.

Counts use a validated checkpoint captured after reading the saved record. A
later validated expiry response supplies newer bounds if retention moves during
validation. If another acknowledgment changes the revision during inspection,
get returns `checkpoint_conflict`; refresh it. Retirement returns
`consumer_generation_conflict`. Permission, identity or storage failures return
errors without lag observations. Diagnostics are point-in-time observations,
not a frozen feed or a retention lease. ACK age is not event age and cannot
predict when history will expire or prove downstream work succeeded. ACK/list
responses do not include lag; request an exact consumer get to observe it.

No native endpoint, migration, event scan, journal format change or background
poller is added. Design:
[ADR-1474](adr/1474-mcp-consumer-lag-observability.md).

### Review consumer health across an environment

Call `gregale_get_environment_cache_activity_consumer_health` with `app`,
`project`, named `environment`, and optional `limit` (default 20, maximum 50).
The report covers only saved consumers in the current connection and exact
endpoint/account/immutable app/project/environment/scope binding. Unsaved names
are not invented as uninitialized consumers. It requires encrypted durable state,
advertised native schedule recovery support and current scoped read access.

`entries` is one name-sorted page. Each entry contains the consumer name, saved
revision/update time, `ready` or `cursor_expired` status, and the same lag fields
as individual inspection: retained pending events, expired-history gap, ACK age
or its future-time flag, observation time and retention. The report includes the
immutable target IDs/scope, current connection `generation`, `observed_at` and
`retention_seconds`. Saved or recovery activity cursors, recovery metadata, event
pages and ACK drafts are omitted.

`summary` covers **all matching saved consumers**, including those outside the
current page:

- `total`: number of matching saved consumers.
- `ready` and `cursor_expired`: mutually exclusive readiness counts.
- `with_retained_backlog`: consumers with retained pending events, including
  expired consumers that also have retained events.
- `needs_attention`: consumers with retained pending events or an expired-history
  gap, counted once each. ACK age alone does not classify a consumer as stalled.

All entries and summary counts in one call use the same validated feed bounds,
captured after the bounded saved-record snapshot. Native checkpoint/feed reads
are shared across consumers rather than repeated for each name. If retention
overtakes the head probe, validated scoped expiry metadata supplies newer bounds
without exposing a recovery cursor. Invalid bounds or a saved cursor ahead of
the observed head fail the entire report, including entries outside the page.

Pass `next_cursor` unchanged as `after` to continue. Health continuations use a
distinct strict format, bounded by the existing 2048-byte consumer-list cursor
limit. They bind the connection/target, connection generation and a fingerprint
of all matching saved records. A scoped create or progress-changing ACK returns
`checkpoint_conflict`; any retirement in the connection returns
`consumer_generation_conflict`. Restart pagination without `after`. An exact
no-op ACK retry or another scope's ACK does not invalidate the record set. These
tokens are untrusted pagination metadata, not authorization or processing proof.

Feed events and retention may change between pages without changing saved
records. Every call obtains fresh feed bounds and repeats its own full-scope
summary; do not add those summaries together or treat the pages as a frozen feed
snapshot. Reconnect/restart can reuse a valid token with the same journal/key and
namespace. No health snapshot or pagination session is persisted.

The tool rechecks durable grant/storage health, generation and every matching
saved record after native reads and again after the shared tool wrapper's final
authorization reads. Concurrent changes withhold the whole report and its
continuation, including changes to consumers outside the returned page. Native
permission/identity loss, revoked consent, unavailable storage, cancellation and
drain remain errors without partial summaries. No journal lock spans native I/O.

This is an ordinary short read. It creates no consumer, saves no progress,
starts no polling, samples no get-tool lag metrics and performs no native writes.
Use individual watch/recovery tools to obtain current processing pages, then
explicitly acknowledge only after processing. Removed history remains unavailable
and point-in-time health observations are not retention leases or proof of
downstream completion. Design:
[ADR-1480](adr/1480-scoped-mcp-consumer-health-overview.md).

### Consumer discovery and retirement

Call `gregale_list_environment_cache_activity_consumers` with `app`, `project`,
named `environment`, and optional `limit` (default 20, maximum 50). It returns
`entries` containing consumer names, revisions and update times, the current
connection `generation`, and optional `next_cursor`. Pass that cursor unchanged
as `after` to continue. Results expose only this account/endpoint/connection and
immutable app/project/environment; activity cursors are excluded. Get an exact
consumer to validate its cursor and current expiry status. Live name-sorted pages
can miss names created before the continuation; they are not a frozen snapshot.

To reclaim capacity, call `gregale_retire_environment_cache_activity_consumer`
with the exact `consumer`, `expected_revision`, and `expected_generation` from
get/list, plus the same app/project/environment. Retirement deletes that saved
checkpoint and returns `status: "retired"`, `retired_revision`, and a newly rotated
`generation`. It can retire expired checkpoints without reconstructing pruned
history. Current target read consent and feed access are still required.

Retirement rotates the generation for the **whole connection**, including other
targets. Old acknowledgments, even initial revision-zero requests, fail with
`consumer_generation_conflict`. Get fresh consumer state before acknowledging
again; restart old list continuations without `after`. `checkpoint_conflict`
means the saved revision changed. Deliberate recreation requires a new initial
acknowledgment using revision zero and the fresh generation; it does not resume
retired progress automatically. An exact retry of the last retirement returns
its original receipt while the consumer remains absent. If the name has been
recreated or another retirement replaced that receipt, inspect current state
instead of retrying with old inputs. An old retirement cannot delete a recreated
consumer with the same revision and cursor.

Checkpoint removal and generation rotation commit together in the encrypted
journal. Only one generation and last-retirement receipt remain per connection,
so repeated retirement/recreation does not grow tombstones without bound.
Retirement frees the checkpoint's capacity; it does not cancel schedules or undo
external work. It advertises `readOnlyHint: false`, `destructiveHint: true` and
`idempotentHint: true`, requires explicit invocation and read consent, and uses
mutation drain grace without enabling platform writes. Remote grant revocation
also removes its generation state; OAuth updates preserve local profile records.

Existing journals remain readable with an empty generation until first
retirement. First retirement atomically upgrades encrypted journal authentication
to v2; upgraded bridges read both versions and preserve v2 afterward. Older
generation-unaware bridges fail to open v2 journals, preventing unsafe downgrade
writes. Keep an upgraded bridge for journals where retirement has been used.
No native migration is added. Design:
[ADR-1473](adr/1473-mcp-consumer-discovery-and-retirement.md).

`TestCodexActivityConsumerAcceptance` drives the pinned real Codex client through
the complete lifecycle using loopback fixtures and an actual encrypted journal
reopened after a drained bridge restart. It checks discovery/pagination,
acknowledgment/retry, retirement, stale requests, reconnect, recreation, lag
diagnostics, consumer watch arrival/replay/timeout/recovery, consumer baseline
recovery/restart/conflicts, resource reads, scoped health pagination/summary and
saved-record conflicts, expiry retirement and denied access. Bridge metadata mutations are intentional; native
mutation requests are rejected and counted. No model turn or live account is
used. CI runs it with the pinned Codex binary and writes the sanitized
`management-mcp-consumers.json` artifact; set `GREGALE_MCP_CONSUMER_REPORT` to retain
the same report locally. Without `GREGALE_MCP_CODEX_BINARY`, this test skips
explicitly and provides no real-client evidence.

### Real-client schedule workflow acceptance

`TestCodexScheduleActivityAcceptance` drives the pinned real Codex client through
Streamable HTTP against an isolated, authenticated MCP fixture. It composes
baseline recovery, concurrent earlier-sorting creation and rescheduling, bounded
activity catch-up, arrival during a wait, timeout checkpoint preservation,
process disconnect/reconnect with offline activity, expired-cursor recovery,
retention overtaking a partial baseline, and upstream read-access revocation
during a watch. Revocation must also prevent restarting recovery.

The Python driver, `scripts/ops/management-mcp-schedule-acceptance.py`, accepts only
the loopback fixture supplied by the Go test. Fixture controls mutate an isolated
MemStore and simulate retention aging without waiting 30 days. They never enter
production handlers. Native mutation requests are rejected and counted. No model
turn or deployment operation is requested. This exercises a real MCP client and
protocol composition; Postgres correctness and native cluster qualification have
separate checks. Remote OAuth login and grant revocation retain their separate
`TestCodexRemoteOAuthAcceptance` coverage.

Run from the repository root after installing the pinned client:

```sh
GREGALE_MCP_CODEX_BINARY="$PWD/scripts/ci/management-mcp-client/node_modules/.bin/codex" \
GREGALE_MCP_SCHEDULE_REPORT=/tmp/management-mcp-schedules.json \
  go test -race -p=1 ./pkg/mcpmanagement -run '^TestCodexScheduleActivityAcceptance$' -count=1 -v
```

CI runs this acceptance and uploads the sanitized JSON report alongside existing
MCP client evidence. Reports include scenario outcomes and zero model/MCP write
counts, without credentials, cursors, action bodies or application data. Without
`GREGALE_MCP_CODEX_BINARY`, the Go test skips explicitly; a skipped local run is
not real-client acceptance evidence. No tool surface or production behavior is
changed by this test.
