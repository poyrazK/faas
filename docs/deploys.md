# Deploys

The normal deploy path is source-first and wait-by-default:

```bash
gregale deploy
gregale deploy --path services/api
gregale deploy --tarball build/source.tar.gz
gregale deploy --image ghcr.io/acme/api@sha256:...
```

Use `--no-wait` when a CI job only needs the queued deployment ID. Use
`--timeout 900` to bound a wait, and `--idempotency-key KEY` when a retry must
represent the same logical deploy. Without a key, the CLI derives one from the
source digest and flags, so repeating an identical deploy returns the
deployment already in progress or live. A deployment that failed, was
cancelled, or was superseded is never replayed: re-running the same deploy
starts a new one.
`--reason`, `--tag`, and `--deployed-by` annotate deployment history.

Every successful wait ends with readiness plus a platform-side smoke request;
a queued build is not reported as live. The final output includes the app URL,
a release summary against the previous deployment, and a copy-paste rollback
command when a previous release is available:

```
✓ Deployed. https://my-app.gregale.dev
Release summary:
  Changes since d0:
    commit_sha         "old" -> "new"
  Rollback: gregale rollback my-app --to d0
```

With `--json`, the receipt keeps the existing deployment/provenance fields and
adds `release_summary` with `previous_deployment_id`, `changes`,
`rollback_target_id`, and `rollback_command`. A queued `--no-wait` deploy has
no release summary yet because the new release is not live.

## GitHub Actions

Print a workflow starter with:

```bash
gregale deploy --github
```

The generated workflow uses an immutable deploy Action SHA from the CLI
release, and Dependabot can update it from the accompanying `# v0` comment.
Pass `--pin-action` to resolve the current `v0` tag during generation. Connect
a repository with `gregale connect` when pushes should deploy automatically.

GitHub Actions OIDC deployments are tied to the repository connected through
the GitHub App installation. Gregale records GitHub's owner and repository IDs
when the repository is bound. If an existing connection predates this identity
check and the workflow uses GitHub's immutable subject format, rebind the
repository through the GitHub installation before deploying. A transfer to a
different owner also requires a rebind; a repository rename keeps the same
identity.

## Safe changes

Preview a change with `gregale deploy --diff` or `--dry-run`. For a bad live
release, use `gregale rollback APP`; rollback reuses the previous live
artifact instead of rebuilding it. Without `--to`, rollback (and automatic
rollback) returns to the deployment that most recently stopped serving
traffic, not the most recently created one, so a rollback after an earlier
rollback or a completed canary restores the release that was just replaced.
A rollback that fails its readiness or hosting check leaves the current
release serving. The target returns to `superseded` with the error recorded,
so it remains available for another rollback.

`gregale rollback APP` waits until the previous release actually serves
again and reports how long it took:

```
✓ Rolled back: 3add53b1-… serves again after 8.3 s
```

If the rollback cannot restore the previous release, the command exits
non-zero with the reason. Every rollback, including an automatic 5xx
rollback, has a receipt you can read later; `--no-wait` returns as soon as
the rollback is requested and prints its id:

```bash
gregale rollback status my-api --operation <id> --wait
```

See [`gregale deployments`](cli-reference.md#deployments) for deployment
history and annotations.

## Releases that change the database

A release command (`release:` in a Procfile, or `release.command` in the
manifest), typically a migration, runs before the new version serves. The
release it replaces keeps serving while the command runs and through the
rollout, so the old code always runs against the new schema for a while.
Write migrations that the previous release can live with: add columns and
tables first, and remove what the old code uses only in a later release.

A rollback restores code, never the database. Rolling back to the release
that was serving when the migration ran is the usual case and needs nothing
extra: that code already ran against the migrated schema. Rolling back
further is different. If a release command ran after the target last served,
the rollback is refused with `rollback_crosses_release`
([ADR-1448](adr/1448-releases-that-change-the-database.md)):

```text
This rollback crosses release commands that ran after its target last served:
  v42  succeeded  started 2026-10-15T10:02:13Z
A rollback does not undo them; the target would run against their changes.
To undo database changes first, restore each database to a new one and attach it:
  gregale postgres restore 4b1e… --name orders-before-v42 --point-in-time 2026-10-15T10:02:13Z
Pass --acknowledge-releases to roll back anyway.
```

At a terminal, `gregale rollback` then asks you to type the app's slug to go
ahead. Scripts pass `--acknowledge-releases`, and the API takes
`"acknowledge_releases": true`. A failed release counts too: it may have
committed part of its changes. Automatic rollbacks (the first-wake 5xx
rollback and alert-rule rollback actions) never acknowledge, so they leave
the current release serving instead.

For a managed PostgreSQL database that the app's release commands can migrate
(attached with `--access migration`), the refusal names the moment before the
earliest crossed release. `gregale postgres restore` creates a new database
from that point without touching the current one; attach it when you have
checked it. The point must be inside the database's restore window.
`gregale deployment summary <id>` and `GET /v1/deployments/{id}` show a
deployment's release run as `release`: its status, start and finish times,
and the databases it can migrate.

## Release gates

Gregale already stops a release whose 5xx rate, latency, crashes or memory
regress. Release gates add the signals only your app knows about
([ADR-20261019071734](adr/20261019071734-release-gates-on-customer-signals.md)):

```bash
# Abort or roll back when the release burns the checkout SLO 10x too fast.
gregale release-gates add --name checkout-slo --slo checkout --max-burn-rate 10
# ...when it introduces a new error group with 3 or more events.
gregale release-gates add --name no-new-errors --new-errors --min-events 3
# ...when orders per minute average below 5.
gregale release-gates add --name orders --metric orders_per_minute --below 5

gregale release-gates list
gregale release-gates rm orders
```

The canary judges every gate on each tick, over the traffic since the rollout
started. The post-release watch judges them again for 30 minutes after the
release reaches 100%. When a gate fails, the canary is aborted (rule
`abort_release_gate`) or the release is rolled back (rule
`abort_post_release_gate`). The rollout decision names the gate and what it
measured.

How each gate judges:
- **`slo` gates** compare the release's bad requests with the SLO's error
  budget. 5xx count as bad for availability SLOs; requests slower than the
  threshold count for latency SLOs. A gate fails only when the release is
  also worse than the revision it replaces.
- **`new_errors` gates** count error groups that the release produced first.
- **`custom_metric` gates** bound the metric's average. Custom metrics are
  reported per app, not per revision, so these gates are most useful after
  the release takes all the traffic.

A gate that has too few requests or values (`--min-samples`, default 20) is
not judged. A gate never holds a rollout on missing evidence.

## Release approvals

Require people to approve each production release before it takes more than
its first canary stage
([ADR-20261020085706](adr/20261020085706-release-approvals.md)):

```bash
# Two owners or admins, other than the deployer, approve every release.
gregale approvals require --approvals 2
# One person, developers included, within four hours.
gregale approvals require --approvals 1 --approvers developer --timeout 4h

gregale approvals pending                 # releases waiting right now
gregale approvals approve <deployment> --comment "canary looks clean"
gregale approvals reject <deployment> --reason "checkout latency is up"
gregale approvals status <deployment>
gregale approvals off
```

The release starts its canary as usual. At the stage the policy names
(`--after-stage`, default 1), it waits. `gregale deploy` shows the wait and
the approve command. The canary still judges the waiting stage, so a
regression aborts the release even while it waits. Once enough people
approve, the rollout continues on its next step.

Waiting ends in one of three ways:
- **Enough approvals:** the rollout continues.
- **A rejection:** the rollout is aborted at once (rule
  `abort_approval_rejected`), and the previous release takes its traffic
  back.
- **The timeout passes** (default 24 hours): the rollout is aborted in the
  same way (rule `abort_approval_timeout`).

While a release waits, nothing gives it more traffic: not `canary advance`,
`rollouts recover --action promote`, `traffic set`, or
`POST /v1/deployments/{id}/promote`. Rollbacks and aborts are never held.

Who may approve:
- **Approvers:** with `--approvers admin` (the default), organization owners
  and admins; `developer` adds developers.
- **The deployer:** their own approval does not count unless you pass
  `--allow-self-approval`. An app outside an organization has one person,
  whose own approval always counts.
- **Changing the policy:** only owners and admins can change or remove it.

Tell people when a release waits, is approved or rejected, or times out:

```bash
echo "https://hooks.slack.com/services/…" | gregale channels add --name releases --kind slack --secret-stdin
gregale approvals require --approvals 2 --notify releases
```

`--notify` takes up to five Slack or email channels; PagerDuty channels are
refused, because a release waiting for approval is not an incident. Each
event is sent once per release.

The Slack message for a waiting release has **Approve**, **Reject** and
**Review** buttons, and the email has the same links. They open
`/dashboard/releases/<deployment>/approval` in Gregale; if you are not signed
in, Gregale asks you to sign in first. The decision is made on that page,
under your own Gregale account, so your organization role, MFA and the
self-approval rule apply exactly as in the CLI. The buttons grant nothing by
themselves.

Releases need a canary to wait in. While the policy is on, a production
release with `--rollout immediate` or `--traffic-percent` is refused with
`release_approval_canary_required`. To approve before any traffic, deploy
with `--no-traffic`, get the approval, then run `gregale traffic set`.

## Deploy freezes

A freeze stops new production releases of one app, or of every app in the
account, while it is active
([ADR-20261019071733](adr/20261019071733-deploy-freezes-and-release-windows.md)):

```bash
# Freeze one app for two hours, starting now.
gregale freeze create --app my-api --for 2h --reason "incident 4711"
# No releases from Friday 16:00 to Monday 08:00 (64 hours), Istanbul time, for every app.
gregale freeze create --all-apps --weekly fri --at 16:00 --for 64h --tz Europe/Istanbul --reason "no weekend releases"
# After Gregale rolls a release back on its own, freeze the app for an hour.
gregale freeze create --app my-api --after-rollback 1h --reason "settle after automatic rollbacks"

gregale freeze status --app my-api
gregale freeze list
gregale freeze lift <id>
```

While a freeze is active, every way of releasing to production is refused
with `release_frozen`, which names the freeze, its reason and its creator:
- `gregale deploy` and the deployment API;
- GitHub push deploys;
- `gregale deploy --project`;
- promotions into `production`.

A freeze covers the `default` deployment scope and the `production`
environment.

It never blocks:
- rollbacks;
- rollouts that were already in progress;
- previews and other environments;
- env and secret changes.

To release anyway, give a reason:

```bash
gregale deploy --override-freeze "hotfix for incident 4711"
gregale projects environments promote shop --from staging --to production --override-freeze "hotfix"
```

The override is recorded in the audit log (`release.freeze_overridden`) with
the freezes it overrode. A GitHub push can't carry an override, so a frozen
push fails, and the commit shows why.

An `--after-rollback` freeze is a policy. Each time the rollout circuit
breaker, the post-release watch, the first-wake 5xx guard or an alert rule
rolls an app back, Gregale freezes that app for the given time. The freeze
names the release that was rolled back, and you can lift it like any other.

## Revisions

Every deploy creates an immutable revision, numbered per app and shown as
`v41`, `v42`, `v43`. A revision is never rewritten — a redeploy supersedes it
and creates the next one — so it is a stable handle you can keep in a runbook
or an incident channel.

Use a revision anywhere a deployment ID is accepted:

```bash
gregale rollback my-api --to v41
gregale traffic set --app my-api --deployment v42 --percent 10
```

`gregale traffic status my-api` lists the live revisions and their weights.
Revision numbers are per app, so `v41` of one app is unrelated to `v41` of
another. Numbers may skip values: preview deploys and retries consume them
too.

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
  Production remains on v43. https://my-api.gregale.dev
  Promote: gregale traffic promote --app my-api --deployment v44 --if-serving v43
```

`--no-traffic` is the discoverable spelling for `--traffic-percent 0`. It is
mutually exclusive with `--traffic-percent`, `--safe`, and canary flags. With
`--json`, a waited dark deployment adds `preview_url` (when the platform's
preview zone is enabled) and `promotion_command` to the deployment receipt.
When one revision owns all production traffic, the command includes
`--if-serving` so promotion fails with `traffic_serving_changed` (409) if
another release takes over before you promote. If traffic is split or the
CLI cannot read current traffic, inspect `gregale traffic status APP` before
promoting; the receipt omits `promotion_command` in that case.
`--no-wait` returns once the dark deployment is queued, before a preview URL
is guaranteed to be live.

## Progressive rollouts

Instead of moving all traffic at once, shift it in stages and let the platform
promote only while the app stays healthy. `--rollout` picks how a release
reaches traffic ([ADR-1450](adr/1450-one-rollout-flag-and-live-rollout.md)):

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
gregale canary simulate my-api --canary-preset balanced
```

Promotion is health-gated. If an alert rule with a `rollback` or `demote`
action is firing, the ladder holds at its current stage rather than advancing.
The `safe` and `fast` rollouts add automatic rollback when the new revision
returns 5xx responses in its first window; `--rollback-on-5xx` adds the same
rollback to any deploy, and `--rollback-on-5xx=false` skips it.

The first window opens when Gregale first sees traffic for the completed
release and lasts 5 minutes. If at least 5 of the release's responses are 5xx,
and those are at least half its requests, Gregale rolls back to the release it
replaced. The check runs every 15 seconds, and the release keeps serving until
the previous one is ready again. If a rollout wedges,
`gregale rollouts recover my-api` is the manual escape hatch.

### Safe by default

A production release of an app that already serves traffic rolls out on the
`adaptive` ladder with first-wake 5xx rollback even when the deploy names no
rollout policy ([ADR-1446](adr/1446-safe-releases-by-default.md)). This covers
`gregale deploy`, GitHub App pushes, the API, and Terraform. It does not
apply to an app's first deploy, PR previews, named project environments, or
service, worker, and job apps, which keep their own rollout behavior.

The adaptive ladder sends 1%, then 10%, then 50%, then 100% of traffic to the
new revision. Each stage lasts at least a minute and advances as soon as both
revisions have answered at least 20 requests in that stage (your traffic, or
the probes below) with no regression, no OOM kills, and no liveness restarts
([ADR-1321](adr/1321-evidence-paced-default-rollouts.md)). A typical release
reaches 100% in 3 to 5 minutes. When the evidence isn't in after 2 minutes,
the stage follows the rules below.

At a terminal, `gregale deploy` follows the rollout live, printing each stage
and why it advanced, until the release reaches 100% or is aborted. Press
Ctrl-C to stop watching: the rollout keeps running, and
`gregale deployment wait <id> --rollout` picks it up again. In CI, with
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

To probe the routes that matter instead of `/`, name up to three of them
([ADR-1449](adr/1449-app-chosen-rollout-probe-routes.md)):

```bash
gregale app my-api --release-probe-paths /healthz,/api/orders
gregale app my-api --release-probe-paths ""   # back to /
```

Probes are always `GET`, and the twenty a minute are split across the paths.
Pick cheap, read-only routes your app answers without a login; each path must
be concrete, with no query, wildcard, or `{parameter}`. The setting is
`release_probe_paths` in the app manifest and the API.

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
deployment (`gregale deployment summary`). This also covers immediate
releases, and anything the canary's share of traffic did not show. Releases
deployed with `--rollback-on-5xx=false` are not watched.

Opt out when you need an immediate cutover:

```bash
gregale deploy --rollout immediate             # this deploy only
gregale deploy --rollback-on-5xx=false         # skip the automatic rollback
gregale app my-api --release-policy immediate  # every future deploy
```

An explicit `--rollout`, `--traffic-percent`, or `--rollback-on-5xx` (or the
deprecated `--canary-preset`) always wins over the default.

### Why a rollout advanced, waits, or stopped

Gregale records the evidence behind every rollout decision
([ADR-1447](adr/1447-explained-rollout-decisions.md)): the request, 5xx, probe,
latency, OOM, and restart counts for both revisions, and which rule decided.
`gregale deployment wait --rollout` prints why a stage is waiting and why each
stage advanced:

```text
Rollout held at 1% traffic (step 1/4): waiting for enough traffic to compare: this release 3 requests, the current one 41 (20 each needed); probes 4 and 3 so far
Rollout: 10% traffic · step 2/4 · rolling_out · advanced on synthetic probes: this release 0/30 probe 5xx, the current one 0/30
```

`gregale deployment summary <id>` shows the latest decision and the current
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
curl -H 'Gregale-Version-Key: customer-42' https://my-api.gregale.dev/account
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
rollout and is still subject to available capacity. If a node has no headroom,
the rollout holds at its current stage instead of promoting.

Automated canary progression also waits for the current stage's configured
dwell time. APID rechecks the persisted stage timestamp against the database
clock in the same transaction that changes traffic, so a worker clock skew or
premature request cannot skip the observation window. Customer-requested
manual stage advances remain immediate.

While a canary is pending or rolling out, the generic traffic-split endpoint
returns `409 traffic_change_during_canary`. Advance or recover the rollout
before applying a separate traffic split.
