# Progressive rollouts

A new revision reaches traffic in stages. Deploy it dark first, optionally mirror live traffic to it, let Gregale move it through canary stages on evidence, and keep a user on one revision while it does.

## Dark deployments

Stage a revision, wait until it is ready, and keep production traffic on the
current release:

```bash
gregale deploy --no-traffic
```

The command prints the new revision's preview URL and a copy-paste promotion
command after readiness succeeds. For example:

```
✓ Staged v44 with 0% production traffic.
  Preview: https://deploy-44-my-api.gregale.dev
  Production remains on v43. https://my-api.gregale.app
  Promote: gregale release traffic promote --app my-api --release v44 --if-serving v43
```

`--no-traffic` is the discoverable spelling for `--traffic-percent 0`. It is
mutually exclusive with `--traffic-percent`, `--safe`, and canary flags. With
`--json`, a waited dark deployment adds `preview_url` (when the platform's
preview zone is enabled) and `promotion_command` to the deployment receipt.
When one revision owns all production traffic, the command includes
`--if-serving` so promotion fails with `traffic_serving_changed` (409) if
another release takes over before you promote. If traffic is split or the
CLI cannot read current traffic, inspect `gregale release traffic status APP` before
promoting; the receipt omits `promotion_command` in that case.
`--no-wait` returns once the dark deployment is queued, before a preview URL
is guaranteed to be live.

## Traffic mirroring

Mirroring copies a share of live production traffic to a new version of the
app (a second revision) and compares its answers with the serving one, without
affecting callers: they always get the serving revision's response, and the
copy's response is discarded after the comparison. It is in preview, on the
Pro and Scale plans.

Stage the candidate dark, then mirror to it:

```bash
gregale deploy --no-traffic                                   # stages v44 beside v43
gregale release mirror create --app my-api --source v43 --mirror v44 --percent 10
gregale release mirror summary --app my-api --id RULE_ID --window 24h
```

The summary counts the requests mirrored and how many answered differently:
status code, response shape (JSON schema) and, with `--include-body`, response
value, plus the mean and p99 latency difference and crashes. Bodies are
compared by hash; raw response bodies are never stored.

- **What is copied:** a 5% sample unless `--percent` (0 to 100) says
  otherwise. Only methods other than POST, PUT, PATCH and DELETE, unless
  `--allow-unsafe-methods`; with it, the copy really runs those requests
  against the candidate, which can write to your database or call other
  services. Requests with bodies over 64 KiB are not copied, and a copy gets
  at most 5 seconds.
- **Headers:** `Authorization`, `Cookie`, `Set-Cookie`, `X-API-Key`,
  `Proxy-Authorization` and `WWW-Authenticate` are always removed from the
  copy. `--redact-header NAME` removes more.
- **Limits:** one mirror rule per app on Pro, three on Scale
  (`422 mirror_rule_quota_exceeded`). Both revisions must be live and
  different revisions of the same app. Other plans get
  `403 plan_mirror_not_allowed`.
- **Cost:** the candidate wakes for the copies and its running time is billed
  like any other instance.

Change or stop a rule:

```bash
gregale release mirror update --app my-api --id RULE_ID --percent 25
gregale release mirror update --app my-api --id RULE_ID --disable
gregale release mirror delete --app my-api --id RULE_ID
```

`gregale release mirror replay --app my-api --id RULE_ID --file corpus.json`
replays up to 100 saved requests (8 MiB in all) against the rule's candidate
revision. POST, PUT, PATCH and DELETE requests need `--allow-unsafe-methods`
on the command line; the flag in the file alone is ignored.

## How a rollout proceeds

An exact application response check can pass as soon as the new deployment
is live, while older revisions still serve part of the traffic. Use the
accepted deployment UUID from the deploy receipt for each milestone.

```bash
gregale release wait DEPLOYMENT_UUID --app my-api
gregale release verify DEPLOYMENT_UUID --app my-api --path /
gregale release wait DEPLOYMENT_UUID --app my-api --rollout --timeout 10m
```

The final wait follows the canary to 100% traffic. Direct traffic promotion
is blocked while the canary is active. Use `gregale release traffic status my-api`
to see the current step or repeat the wait with the same UUID after an
interruption.


Instead of moving all traffic at once, shift it in stages and let the platform
promote only while the app stays healthy. `--rollout` picks how a release
reaches traffic:

```bash
gregale deploy --rollout safe        # adaptive 1% -> 10% -> 50% -> 100%, rollback, wait for 100%
gregale deploy --rollout fast        # 5% -> 50% -> 100%, a minute each
gregale deploy --rollout immediate   # all traffic at once
```

Without `--rollout`, the app's release policy decides: `safe` by default,
or whatever `gregale app my-api --release-policy safe|fast|immediate` set.
`safe` is the `adaptive` ladder, which moves on as soon as the evidence is
clean (see [Safe by default](#safe-by-default)). Custom ladders still use
`--canary-preset custom --canary-stages "5@1m,25@5m,100@0s"`; `--canary-preset`,
`--canary-stages` and `--safe` remain for existing scripts but are
deprecated. Preview how a ladder would behave against your app's recent
traffic before deploying:

```bash
gregale release canary simulate my-api --canary-preset balanced
```

### Canary ladders

Each ladder (canary preset) is a list of traffic shares and how long the new
revision holds each one before moving on:

| Preset | Stages | Used by |
|---|---|---|
| `adaptive` | 1% → 10% → 50% → 100%; each stage moves on after 1 minute once the evidence is clean, else after 2 minutes | `--rollout safe`, and every production release by default |
| `aggressive` | 5% → 50% → 100%, 1 minute each | `--rollout fast` |
| `balanced` (also `1-10-50-100`) | 1% → 10% → 50% → 100%, 2 minutes each | |
| `slow` | 1% → 10% → 100%, 5 minutes each | |
| `custom` | the stages you give in `--canary-stages`, as `percent@duration` | |
| `none` | all traffic at once, like `--rollout immediate` | |

An unknown name fails with `invalid_canary_preset`.

Promotion is health-gated. If an alert rule with a `rollback` or `demote`
action is firing, the ladder holds at its current stage rather than advancing.
The `safe` and `fast` rollouts add automatic rollback when the new revision
returns 5xx responses in its first window; `--rollback-on-5xx` adds the same
rollback to any deploy, and `--rollback-on-5xx=false` skips it.

The first window opens when Gregale first sees traffic for the completed
release and lasts 5 minutes. If at least 5 of the release's responses are 5xx,
and those are at least half its requests, Gregale rolls back to the release it
replaced. A 5xx the replaced release also returned in the 24 hours before, for
the same method, route and status, does not count: a route that already failed
cannot roll a release back. The check runs every 15 seconds, and the release
keeps serving until the previous one is ready again. The rolled-back release
records the decision, and `gregale release summary <id>` shows it with or
without a canary, for example `abort_post_release_5xx: first-wake 5xx: 5 of 5
responses failed; rolled back automatically to <previous>`. While a rollout
runs, a plain
`gregale rollback my-api` is refused because traffic is split; to stop the
rollout and return its traffic to the release it replaced, run
`gregale release traffic rollback my-api` (the same as
`gregale rollouts recover my-api --action abort`). For a split you set by
hand with `release traffic set`, the same command gives the release with the
largest share all traffic; `--to v6` names another, and a tie needs it. It
acts on one environment (`--env`, or the only one the app is live in), and
refuses if traffic changes while it runs. If a rollout wedges,
`gregale rollouts recover my-api --action advance|promote|abort` is the manual
escape hatch.

A new release that goes live while an earlier canary is still rolling out
replaces it.
The earlier canary's traffic returns to the serving release, its rollout ends
`aborted` with the reason `superseded by deployment <id>` (and a
`rollout.aborted` webhook), and only the newest release rolls out. A release
deployed with `--rollout immediate` takes all traffic and ends the earlier
canary's rollout the same way.
If the app cannot run the new release beside both the serving release and the
earlier canary (with the default max_concurrency of 1 it cannot), the earlier
canary is replaced just before the new release's health check instead of when
it goes live, so the check can wake it. If that check then fails, the app stays
on the serving release.

### Safe by default

A production release of an app that already serves traffic rolls out on the
`adaptive` ladder with first-wake 5xx rollback even when the deploy names no
rollout policy. This covers
`gregale deploy`, GitHub App pushes, `gregale project apply`, the API, and
Terraform, into the default scope or a project's `production` environment.
It does not apply to an app's first deploy into that scope, PR previews,
other project environments, atomic project releases (which cut every app
over together), or service, worker, and job apps, which keep their own
rollout behavior. A promotion into `production` gets the first-wake 5xx
rollback and the post-release watch but no canary: its protected approval
already stands in for the app's approval policy.

The adaptive ladder sends 1%, then 10%, then 50%, then 100% of traffic to the
new revision. Each stage lasts at least a minute and advances as soon as both
revisions have answered at least 20 requests in that stage (your traffic, or
the probes below) with no regression, no OOM kills, and no liveness restarts. A typical release
reaches 100% in 3 to 5 minutes. When the evidence isn't in after 2 minutes,
the stage follows the rules below.

At a terminal, `gregale deploy` follows the rollout live, printing each stage
and why it advanced, until the release reaches 100% or is aborted. Press
Ctrl-C to stop watching: the rollout keeps running, and
`gregale release wait <id> --rollout` picks it up again. In CI, with
`--json`, or with `--no-wait`, deploy returns once the candidate is live at
1% and prints how to follow it; `--rollout safe` always waits for 100%.

A quiet app may not send enough requests to compare the two revisions, so
while the rollout runs Gregale sends `GET /` twenty times a minute to both the
new and the previous revision. These probes go through your app's normal
routing and auth, are not counted in your request analytics, and wake both
revisions for the rollout, which is billed like any other running time
(about 5 minutes for a quiet app). If the new revision answers the probes
with 5xx more often than the previous one, the rollout is aborted and traffic
returns to the previous revision. If the probes are clean, each stage
advances after its first minute.

To probe the routes that matter instead of `/`, name up to three of them:

```bash
gregale app my-api --release-probe-paths /healthz,/api/orders
gregale app my-api --release-probe-paths ""   # back to /
```

Probes are always `GET`, and the twenty a minute are split across the paths.
Pick cheap, read-only routes your app answers without a login; each path must
be concrete, with no query, wildcard, or `{parameter}`. The setting is
`release_probe_paths` in the app manifest and the API.

A function is not probed unless it sets `release_probe_paths`: every path of
a function is its handler, so a probe of `/` would run a queue or cron
handler on an empty event. Its canary is judged on real traffic and runtime
signals instead.

Probes are ordinary requests to your app: they wake the new release and the
one it replaces and keep both running while the rollout waits for evidence,
and that running time is billed like any other request (about 0.09 GB-h for a
quiet rollout of a 256 MB app). `release_policy: immediate` skips them.

When probes cannot give an answer (for example, the probed path requires a login), a
stage whose only problem is too few samples waits up to 5 more minutes after
its 2 minutes and then advances, provided the new revision returned no 5xx
responses, had no OOM kills, and was never restarted by its liveness probe.
The first-wake 5xx rollback still protects the release once real traffic
arrives. Set `release_policy: immediate` to turn both the rollout and its
probes off.

A new revision that its liveness probe restarts twice (and more than twice as
often as the release it replaces) is treated as crash-looping: the rollout is
aborted and traffic returns to the previous revision, even with no traffic.

For 30 minutes after a release reaches 100%, Gregale keeps comparing it with
the previous release's traffic from before the rollout. A clear 5xx or p95
latency regression rolls it back automatically and records why on the
deployment (`gregale release summary`). When the previous release had too
little traffic to compare, the first-window bar applies instead, again leaving
out 5xx the previous release also returned. This also covers immediate
releases, and anything the canary's share of traffic did not show. Releases
deployed with `--rollback-on-5xx=false` are not watched. The watch ends early
only when a newer release takes traffic; deploying one with `--no-traffic`,
scheduling one, or a build still running leaves the serving release watched.

Opt out when you need an immediate cutover:

```bash
gregale deploy --rollout immediate             # this deploy only
gregale deploy --rollback-on-5xx=false         # skip the automatic rollback
gregale app my-api --release-policy immediate  # every future deploy
```

An explicit `--rollout`, `--traffic-percent`, or `--rollback-on-5xx` (or the
deprecated `--canary-preset`) always wins over the default.

### Why a rollout advanced, waits, or stopped

Gregale records the evidence behind every rollout decision: the request, 5xx, probe,
latency, OOM, and restart counts for both revisions, and which rule decided.
`gregale release wait --rollout` prints why a stage is waiting and why each
stage advanced:

```text
Rollout held at 1% traffic (step 1/4): waiting for enough traffic to compare: this release 3 requests, the current one 41 (20 each needed); probes 4 and 3 so far
Rollout: 10% traffic · step 2/4 · rolling_out · advanced on synthetic probes: this release 0/30 probe 5xx, the current one 0/30
```

`gregale release summary <id>` shows the latest decision and the current
hold. The API returns them as `rollout_decision` and `rollout_hold` on
`GET /v1/deployments/{id}`; every decision is also in the deployment's audit
timeline (`GET /v1/deployments/{id}/audit`). The `rollout.completed` and
`rollout.aborted` webhooks carry the decision that completed or aborted the
rollout as `decision`. An abort by a route monitor, an alert rule, or a profile
gate keeps only its `reason`.

### Keep one user on one revision

Send a stable, opaque user or tenant identifier in `Gregale-Version-Key` when
the same caller must stay in one rollout cohort across requests:

```bash
curl -H 'Gregale-Version-Key: customer-42' https://my-api.gregale.app/account
```

Gregale hashes the app, the active rollout revisions, and the key, then applies
the current traffic weights. The same key therefore selects the same revision
across gateway replicas. When a two-revision canary grows from 1% to 10% to
50%, callers already in the candidate cohort remain there and the cohort only
expands. Session affinity still prefers the same VM, but only inside the
revision selected by the version key.

The key is forwarded to the app and honored by managed service-to-service
calls, so propagate it when one request chain must see a consistent revision.
Response-cache entries are partitioned by the selected revision as well. The
value is trimmed, must be a single non-empty header no longer than 256 bytes,
and cannot contain control characters; it is otherwise opaque. Missing or
invalid values retain the normal weighted request distribution.

This is cohort affinity, not a revision override or an authorization boundary:
clients cannot name a deployment with it, and changing weights still controls
the size of each cohort.

For browser traffic, configure a cookie source on the app so ordinary page,
image, and script requests share a cohort without JavaScript setting a custom
header. For example, `PATCH /v1/apps/my-app` with
`{"version_affinity_cookie":"visitor_id"}` uses the `visitor_id` cookie when
`Gregale-Version-Key` is absent. The cookie should contain a stable, opaque,
non-secret value of at most 256 bytes. Gregale hashes it before exposing the
derived key to the guest or managed service calls. An explicit version-key
header takes precedence, including when invalid; duplicate or invalid cookie
values fall back to normal weighted routing. Set the field to `""` to disable
cookie sourcing. This setting is distinct from `session_affinity`, which
targets a running VM rather than a rollout revision.

If the app has no visitor cookie, set
`{"version_affinity_managed_cookie":true}` instead. Gregale mints a random,
opaque `__Host-gregale_version` cookie before the first rollout decision, so
the initial page and its later assets share a cohort across gateways. It is
host-only, Secure, HttpOnly, SameSite=Lax, and expires after seven days. An
explicit `Gregale-Version-Key` still wins. The edge removes its own cookie
before forwarding the request or evaluating response-cache eligibility;
customer cookies retain the normal cache bypass, and responses setting any
other cookie are never stored. When a first cache miss issues only Gregale's
cookie, the public origin response can populate the cache without storing that
cookie; each later visitor still receives their own cookie. This option and
`version_affinity_cookie` are mutually exclusive. Set it to `false` to disable.
When enabled, guest responses cannot set the reserved
`__Host-gregale_version` cookie; other application cookies are unaffected.

Traffic splitting and canary rollouts are available on every plan. During a
rollout an app runs one instance above its plan's concurrency limit so both
revisions can serve at once; that extra instance lasts only as long as the
rollout and is still subject to available capacity. A release staged with
`--no-traffic` gets the same allowance while its instance runs, so production
still wakes beside it. If a node has no headroom, the rollout holds at its
current stage instead of promoting.

Automated canary progression also waits for the current stage's configured
dwell time. APID rechecks the persisted stage timestamp against the database
clock in the same transaction that changes traffic, so a worker clock skew or
premature request cannot skip the observation window. Customer-requested
manual stage advances remain immediate.

While a canary is pending or rolling out, the generic traffic-split endpoint
returns `409 traffic_change_during_canary`. Advance or recover the rollout
before applying a separate traffic split; `gregale rollouts recover <app>
--action abort` stops it and returns its traffic to the release it replaced.

A saved `gregale deploy` attempt can recover a lost acceptance reply with
`gregale release resume SESSION_ID`. Image, multipart and source-ref submissions
use a durable server receipt; resumable uploads use their committed upload
receipt. Recovery reconnects to the original deployment without source files or
another submission. Missing receipts leave acceptance uncertain; see
[deploy recovery](cli/deploy.md).

Project deploys also defer HTTP cron replacements until each workload is ready
in production with all traffic. A cron-only change queues that workload even
when its source is unchanged. Reapplying identical pending changes reuses the
accepted deployment; failed workloads keep their active schedules while healthy
workloads activate independently. Apply receipts include each workload's
`configuration` outcome, and CLI text shows pending or blocked cron activation.
`--no-triggers`, excluded workloads and preview deployments preserve production
schedules. Command crons remain unchanged by HTTP schedule replacement. Removing
an app still removes its schedules. When reallocating account cron quota across
workloads, retry after the workloads freeing capacity have activated.

Waited project deploys finish only after workload readiness, full traffic and
rollout completion, and activation of server-managed HTTP cron changes. A blocked
or discarded configuration fails the command (exit 1) and reports its reason and
recovery hint. A timeout (exit 3) names the unfinished phase; live workloads retain
their live status and configuration diagnostic. The server continues processing,
and checking deployment status never submits another build. `--no-wait` still
returns immediately with pending configuration in its receipt. Older servers
without configuration diagnostics retain their readiness-based wait behavior.

Accepted project deploys now save a private recovery session before waiting,
including when using `--no-wait`. The CLI prints a copyable
`gregale release resume SESSION_ID` command. Project JSON receipts include
`session_id`, `resume_command` and `recovery_saved`. Resume checks the original
workload deployment IDs through readiness, rollout and cron activation without
source files, manifests or secrets, and never creates another build. Successful
workloads remain successful; workloads that were not accepted stay visible and
require an explicit new deployment attempt. Use the session ID for projects;
`--app` continues to select individual app deploy sessions.

If saving fails, the command returns nonzero and retains accepted workload IDs
in its receipt with `recovery_saved:false`. Inspect those IDs before retrying.
Lost-response recovery requires the updated receipt endpoint described below. Use the original endpoint, profile and
credential when resuming; sessions are isolated by that connection.

Project deploy recovery also covers a lost apply response. The CLI saves an
attempt before submission and prints its session/resume command on uncertain
outcomes. `gregale release resume SESSION_ID` discovers accepted workloads through
`GET /v1/project-submissions/{attempt_id}` and checks their original deployment
IDs. It never repeats apply or uploads source. Interrupted submissions expose
partial acceptance; unconfirmed workloads require inspection before an explicit
new attempt. A missing receipt remains uncertain. Default deploy invocations use
fresh attempts; reuse an explicit idempotency key only for the same request.
Changing request bytes requires a new key. Existing sessions with a received
acceptance receipt remain compatible. Deploy the receipt migration and updated
APID before relying on lost-response recovery with the updated CLI.
