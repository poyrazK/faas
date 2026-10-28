# Deploys

The normal deploy path is source-first and wait-by-default:

```bash
gregale deploy
gregale deploy --path services/api
gregale deploy --tarball build/source.tar.gz
gregale deploy --image ghcr.io/acme/api@sha256:...
```

## What gets uploaded

By default `gregale deploy` uploads the committed `HEAD` of the selected
directory when the repository has an `origin` remote, and the files on disk
otherwise (`--source=worktree` includes uncommitted changes). Before upload:

- **Excluded by default:** `.git`, `node_modules`, `vendor`, `__pycache__`,
  `.venv`, `dist`, `.next`, `target`, `coverage`, `.cache`, `.DS_Store`,
  `Thumbs.db` and `*.pyc`, at any depth. A Go `vendor/` directory with
  `vendor/modules.txt` ships, because it is source. When the app builds with
  a Dockerfile, `dist`, `.next`, `target`, `coverage` and `.cache` ship too
  (Dockerfiles commonly `COPY target/*.jar` or `COPY dist/`), and
  `.dockerignore` applies the way `docker build` reads it.
- **`.gregaleignore`** at the source root uses `.gitignore` syntax. Later lines
  win, so `!dist/` re-includes a directory the defaults exclude. `.git` can
  never be included.
- **Symlinks** inside the source directory are uploaded as the file or
  directory they point to. A link that leaves the directory, dangles or
  contains itself stops the deploy with the link's name; exclude it in
  `.gregaleignore` or copy its target in.
- **Submodules** that are checked out are uploaded at the commit the
  repository records. A submodule that is not checked out is uploaded empty
  with a warning; run `git submodule update --init --recursive`.
- **Git LFS** files are uploaded with their real content when the objects are
  downloaded locally. Otherwise the pointer files are uploaded with a
  warning; run `git lfs pull`.
- **`.env` files** are scanned locally and credential-shaped values are
  redacted before upload (`--secret-scan=off` disables this). Use
  `--secrets-file` or `gregale env set` for real secrets.

### Limits

| Limit | Free | Hobby | Pro | Scale |
|---|---|---|---|---|
| Upload (packed archive) | 100 MB | 100 MB | 250 MB | 250 MB |
| Expanded source | 2 GiB | 2 GiB | 2 GiB | 2 GiB |
| Files and directories | 10,000 | 10,000 | 10,000 | 10,000 |
| App layer after build | 256 MB | 512 MB | 1 GB | 2 GB |
| Build | 2 vCPU, 2048 MB, 15 min | same | same | same |

A deploy over a limit fails before upload with the limit and the observed
size. Large source uploads resume after a dropped connection.

### Accept-time checks

Every source deploy, from the CLI, the API, a GitHub push or a project, is
checked before a build starts:

- **Credentials.** A known credential format (cloud, payment, VCS and AI
  provider keys, private keys, Gregale deploy tokens) refuses the deploy with
  the file and line. Heuristic high-entropy values and test-mode keys do not
  block; they are listed on the deployment
  (`gregale deployment ID --show-secret-scan`). Subresource Integrity digests
  are never flagged. To keep a deliberately committed value, such as a test
  fixture or a documented example key, add `gregale:allow-secret` to its line
  in a comment.
- **Stateless contract.** Apps lose local writes when they park, so a
  database file under a top-level `data/` or `db/` directory (SQLite, DuckDB,
  Postgres, MySQL, MongoDB or Redis data) refuses the deploy, as does a
  Dockerfile `VOLUME` or filesystem mount. Migrations, schemas and seed files
  in those directories are fine. Keep data in managed Postgres or object
  storage.

### Dockerfile builds

A `Dockerfile` at the source root is built with BuildKit. To use another
Dockerfile or pass build options, declare them in `gregale.yaml`:

```yaml
build:
  dockerfile: docker/Dockerfile.prod   # relative to the source root
  target: runtime                      # a FROM … AS stage
  args:
    NODE_ENV: production
  secrets:
    - id: npmrc          # RUN --mount=type=secret,id=npmrc
      secret: NPM_TOKEN  # an app secret
```

Up to 64 arguments of at most 4 KiB each and 16 build secrets are accepted.
Secret values never reach the image or the build log. A `HEALTHCHECK` command
in the Dockerfile gates readiness: the deployment serves only after it passes,
and an instance whose check keeps failing restarts. Use `HEALTHCHECK NONE` to
opt out.

### Retries and retention

`gregale release retry` rebuilds from the uploaded source for 24 hours after
the upload; after that, deploy again from your source. A build waiting in the
queue for 6 hours, or a deployment making no progress for 3 hours, fails
with a stable error code.

## Waiting and resuming

Interrupted single-app deploys save a local session. Run
`gregale release resume --app my-api`, or `gregale release resume SESSION_ID --json`,
to reconnect to the same accepted deployment and finish pending lifecycle/scaling
settings without local source. Resume checks saved app identity and conflicting
settings before writing. See [saved deploy recovery](cli/deploy.md#recover-an-interrupted-deployment).

A release can be live while its cron configuration is still incomplete. Inspect
an API, Git, or CLI deployment with `gregale deployment <deployment-id>` or
`gregale deployment v42 --app my-api --json`. Deployment details include
`configuration.crons.status`, a stable `reason` code, an explanation and recovery
hint. Status is `pending`, `activated`, `conflicted`, `discarded`, or `not_managed`
when no deferred activation is recorded. Current schedules stay active while
builds or partial rollouts are in progress.

Pending cron changes activate automatically when the release is ready and owns
all production traffic. A conflict reports operator edits, changed quotas, or
cron identity changes. A discarded intent reports failed, canceled or
superseded work. Inspection and saved-session resume do not submit another build
or overwrite schedules. Inspect the stated reason and current crons before
starting a new deployment attempt to resolve a terminal conflict.

In JSON, `configuration.complete` covers server-managed cron activation.
The deploy receipt's `configuration_complete` also covers pending saved CLI
lifecycle and scaling settings. An `activated` result records that the intent
was applied; a later deployment can subsequently change schedules.

Use `--no-wait` when a CI job only needs the queued deployment ID. Use
`--timeout 900` to bound a wait, and `--idempotency-key KEY` when a retry must
represent the same logical deploy. Archive and full-commit-SHA deploys without
secrets derive a key from the source and flags, so repeating an identical deploy
returns the deployment already in progress or live. Branches, tags, short SHAs,
and secret bundles use a fresh key per invocation; transport retries reuse it.
A local wait timeout or interruption preserves accepted deployment configuration;
resume with `gregale release wait <deployment-id>`. The server bounds every
deployment on its own: a build still waiting in the builder queue after 6 hours
fails with `deployment_queue_timeout`, and a deployment with no build running
that makes no progress for 3 hours fails with `deployment_stalled` (a running
build is bounded by the build timeout). Deploy again after either. Submission errors report
accepted, rejected, or unknown acceptance. Accepted and unknown outcomes retain
staged triggers, profiling, and tracing; only definite rejection restores prior
settings. Inspect deployment history and reuse the printed retry key when
acceptance is unknown. A deployment that failed, was
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

## Adding services to an atomic project

Atomic projects can opt into adding independent HTTP source services with
`gregale deploy --project --atomic --add-services --verify --yes`. Run
`gregale deploy check --project --atomic --add-services --verify` first to inspect
prerequisites without reserving apps. Admission remains disabled by default
until [native qualification](adr/20261019120043-atomic-http-service-additions.md) passes.
Every existing serving service must remain present with unchanged settings.
New services pass startup readiness before the graph switches; `--verify` checks
existing critical routes and restores the original membership on regression.
New services have no baseline probe history. Failed or withdrawn additions keep
their app IDs and consume quota but have no default route. An identical retry
can reuse them after the earlier attempt finishes. Removal, dependencies, declared
environment keys, schedules, workers, images, migration commands, configuration
snapshots and progressive rollout are unsupported in this first version.

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

### Deploy tokens

Other CI systems deploy with a deploy token: a credential that can only deploy
one app. Minting and rotating one needs an MFA step-up from the last five
minutes, so run `gregale mfa verify` first. The token is shown once; store it
as `FAAS_TOKEN` in the CI secret store.

```bash
gregale app deploy-token create --app my-api --label ci   # default expiry: 90 days
gregale app deploy-token list --app my-api
gregale app deploy-token rotate <token-id> --app my-api   # the old token stops working
gregale app deploy-token delete <token-id> --app my-api
```

## Safe changes

Preview a change with `gregale deploy --diff` or `--dry-run`. For a bad live
release, use `gregale rollback APP`; rollback reuses the previous live
artifact instead of rebuilding it. Without `--to`, rollback (and automatic
rollback) returns to the deployment that most recently stopped serving
traffic, not the most recently created one, so a rollback after an earlier
rollback or a completed canary restores the release that was just replaced.
It rolls back production; an app that serves only in one other environment
rolls back that environment, and a release of any other environment needs
`--to`. A rollback that fails its readiness or hosting check leaves the current
release serving. The target returns to `superseded` with the error recorded,
so it remains available for another rollback. If another release goes live in
that environment while the target is getting ready, the rollback is not
applied (`rollback_release_changed`), and a rollback whose receipt already
reported a failure such as `rollback_timeout` never cuts over later.

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

See [`gregale release list`](cli-reference.md#release-list) for deployment
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
checked it. The point must be inside the database's restore window. To
restore it and move the app to it as part of the rollback, see
[Rollbacks that restore the database](#rollbacks-that-restore-the-database).
`gregale release summary <id>` and `GET /v1/deployments/{id}` show a
deployment's release run as `release`: its status, start and finish times,
and the databases it can migrate.

## Rollbacks that restore the database

`--restore-databases` rolls back the code and the managed PostgreSQL database
together ([ADR-20261023181931](adr/20261023181931-rollbacks-that-restore-the-database.md)):

```sh
gregale rollback my-api --to v41 --restore-databases
```

```text
This rollback undoes release commands that ran after its target last served:
  v42  succeeded  started 2026-10-23T12:03:10Z
orders is restored to 2026-10-23T12:03:10Z as a new database; writes to orders after that moment stay only in orders.
my-api stops serving while it switches (usually under a minute, then a cold start).
Type my-api to continue: my-api
restoring orders to 2026-10-23T12:03:10Z as orders-before-v42
staging and verifying credentials for orders-before-v42
fencing my-api; it stops serving until the switch ends
draining my-api: 3 retired, 1 parked, 0 pending
switching my-api to v41 and orders-before-v42
resuming my-api on v41
✓ Rolled back to v41 on orders-before-v42. orders keeps every write and is no longer bound to my-api; delete it when you no longer need it.
```

The switch runs in phases, and the app keeps serving its current release
until the copy is ready and its credentials are verified:

1. **Restore.** The database the release commands migrate is restored, as a
   new database named `<name>-before-v<revision>`, to the start of the earliest
   release the rollback undoes. A database already restored to that point
   under that name is reused.
2. **Stage and verify.** Gregale issues credentials on the copy for every
   binding the app has in the scope, and checks them over SQL.
3. **Fence and drain.** Nothing new starts for the app, in any scope. Running
   instances are stopped and their exit confirmed on the host; parked ones
   have no process and their snapshots are discarded.
4. **Switch.** The rollback target becomes the live release, and in one
   transaction the app's bindings move to the copy under the same variable
   names.
5. **Resume.** The fence lifts. The first request cold-boots the target with
   the copy's credentials.

Until the switch reaches step 4, any failure (or `gregale rollback switch
my-api <id> --cancel`) unwinds it, and the app keeps serving its current
release on the original database. The restored copy is kept either way;
delete it with `gregale postgres delete` if you do not need it.

What to expect:
- **Writes after the restore point are not copied.** The original database
  keeps them, unchanged, so you can compare or copy rows by hand.
- **The app is unavailable** from the fence until the first request after the
  switch is served, usually under a minute plus a cold start.
- **One database per switch.** When the release commands can migrate more
  than one managed database, restore them with `gregale postgres restore` and
  roll back with `--acknowledge-releases`.
- **The switch waits for the app to settle.** Frozen instances, starting
  instances and running tasks must finish within two minutes of the fence,
  and the drain must finish within ten; otherwise the switch fails and the app
  resumes as it was.
- **The restore point must be inside the database's restore window**,
  otherwise the request is refused before anything changes.
- **An account runs at most three switches at once.** A fourth is refused
  with `database_switches_active` (429) until one finishes or is cancelled.

After a switch, rolling back to a release whose migration it undid is refused
with `rollback_target_database_restored`, and automated rollbacks cannot go
there either: deploy that code again so its release command runs. Later
rollbacks no longer list the undone release commands as crossed.

`gregale rollback switches my-api` lists an app's switches and `gregale
rollback switch my-api <id> --wait` follows one. The API is
`"restore_databases": true` on `POST /v1/apps/{slug}/rollback` (it needs
`"acknowledge_releases": true`, the `postgres:manage` scope and a verified
email, like a database restore), then
`GET /v1/apps/{slug}/database-switches/{id}` and
`POST /v1/apps/{slug}/database-switches/{id}/cancel` (also `postgres:manage`).

## Release gates

Gregale already stops a release whose 5xx rate, latency, crashes or memory
regress. Release gates add the signals only your app knows about
([ADR-20261019071734](adr/20261019071734-release-gates-on-customer-signals.md)):

```bash
# Abort or roll back when the release burns the checkout SLO 10x too fast.
gregale release gate create --name checkout-slo --slo checkout --max-burn-rate 10
# ...when it introduces a new error group with 3 or more events.
gregale release gate create --name no-new-errors --new-errors --min-events 3
# ...when orders per minute average below 5.
gregale release gate create --name orders --metric orders_per_minute --below 5
# ...when its AI gateway calls cost 30% more each than the previous release's.
gregale release gate create --name ai-cost --ai cost_per_call --max-increase 30

gregale release gate list
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
- **`custom_metric` gates** bound the metric's average over the window, read
  from the metric's history: every value you pushed, not only the latest. Push
  at least `min_samples` values (default 20) in the window, or the gate is not
  judged. Custom metrics are reported per app, not per revision, so these
  gates are most useful after the release takes all the traffic.
- **`ai` gates** compare the release's AI gateway calls with the previous
  release's ([ADR 20261022153155](adr/20261022153155-ai-signals-per-deployment-and-release-gates.md)):
  `error_rate` (provider errors), `flagged_rate` (answers truncated, refused,
  empty, invalid JSON or with a failed tool call), `cost_per_call` or
  `ttfb_ms` (time to first byte). The gate fails when the release is more
  than `--max-increase` percent worse (default 50), and worse by more than a
  floor: 1 percentage point, 1 millicent per call or 50 ms. Both releases need
  `--min-samples` calls. Only calls from the app's instances count;
  `gregale ai deployments` lists them by deployment.

A gate that has too few requests or values (`--min-samples`, default 20) is
not judged. A gate never holds a rollout on missing evidence.

## What changed between releases

`gregale release diff` shows what a release changes compared with the
release that served before it
([ADR-20261021101117](adr/20261021101117-release-diff.md)):

```bash
gregale release diff v42                 # against the release before it
gregale release diff v42 --against v38   # against any release of the app
```

It reports:
- **Code:** both commits, a GitHub compare link when both releases came
  from the same repository, and whether the image changed.
- **Configuration:** env variables and secrets added, removed or changed,
  by key. Values are never shown.
- **Packages:** packages added, removed or upgraded, from the two builds'
  SBOMs (at most 100 per list).
- **Runtime settings:** handler, runtime base, port, entrypoint, command,
  probes, minimum instances, sidecars, workflows and env override keys.
- **Release command:** the step that runs database migrations.

A section shows as unknown when its history is gone: config history keeps
the last 100 revisions per scope, and image releases have no build SBOM.
The approval page and the "approval needed" message show the same
comparison. `gregale release summary` remains the quick cockpit view:
rollout status, rollback target, and release fields against the previous
deployment.

## Release approvals

Require people to approve each production release before it takes more than
its first canary stage
([ADR-20261020085706](adr/20261020085706-release-approvals.md)):

```bash
# Two owners or admins, other than the deployer, approve every release.
gregale release approval require --approvals 2
# One person, developers included, within four hours.
gregale release approval require --approvals 1 --approvers developer --timeout 4h

gregale release approval pending                 # releases waiting right now
gregale release approve <deployment> --comment "canary looks clean"
gregale release reject <deployment> --reason "checkout latency is up"
gregale release approval status <deployment>
gregale release approval off
```

A production release is one to the default scope or to the project's
`production` environment (`--environment production`); both serve the app's
production traffic, so the policy gates both. Other environments and scopes
are not gated.

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
echo "https://hooks.slack.com/services/…" | gregale alert channel create --name releases --kind slack --secret-stdin
gregale release approval require --approvals 2 --notify releases
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
`release_approval_canary_required`. A release deployed with `--no-traffic`
can't be given traffic by hand either; start it with
`gregale release schedule set <release> --at now`, which rolls it out
through a canary that waits for approval.

Production means the `default` scope and a project's `production`
environment, as for freezes. Releases that have no canary to wait in are
refused the same way:
- `gregale release retry` of a release that had no canary;
- a project apply that would release the app;
- a promotion into a `production` environment that is not protected.

Protect the environment (`gregale environment protect <project>
production`) and its promotion approval stands in for the app's policy.
A GitOps source releases into `production` only once a person approved the
revision in Gregale (approval policy `manual`); with `protected_branch` the
sync waits until you remove the app's policy or switch the source to manual
approval.

The app's **Release safety** page in the dashboard
(`/dashboard/apps/<app>/release-safety`) shows the policy and the releases
waiting for approval, next to the app's freezes, gates and scheduled
releases.

## Deploy freezes

A freeze stops new production releases of one app, or of every app in the
account, while it is active
([ADR-20261019071733](adr/20261019071733-deploy-freezes-and-release-windows.md)):

```bash
# Freeze one app for two hours, starting now.
gregale release freeze create --app my-api --for 2h --reason "incident 4711"
# No releases from Friday 16:00 to Monday 08:00 (64 hours), Istanbul time, for every app.
gregale release freeze create --all-apps --weekly fri --at 16:00 --for 64h --tz Europe/Istanbul --reason "no weekend releases"
# After Gregale rolls a release back on its own, freeze the app for an hour.
gregale release freeze create --app my-api --after-rollback 1h --reason "settle after automatic rollbacks"

gregale release freeze status --app my-api
gregale release freeze list
gregale release freeze lift <id>
```

While a freeze is active, every way of releasing to production is refused
with `release_frozen`, which names the freeze, its reason and its creator:
- `gregale deploy` and the deployment API;
- GitHub push deploys;
- `gregale deploy --project`;
- promotions into `production`;
- `gregale release retry` and the dashboard's retry button. A retry can't
  carry an override; deploy again with `--override-freeze` instead.

A GitOps source managing the `production` environment keeps its sync
unconverged while a freeze covers one of its apps, and activates the new
revision once the freeze ends.

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
gregale environment promote shop --from staging --to production --override-freeze "hotfix"
```

The override is recorded in the audit log (`release.freeze_overridden`) with
the freezes it overrode. A GitHub push can't carry an override, so a frozen
push fails, and the commit shows why.

An `--after-rollback` freeze is a policy. Each time the rollout circuit
breaker, the post-release watch, the first-wake 5xx guard or an alert rule
rolls an app back, Gregale freezes that app for the given time. The freeze
names the release that was rolled back, and you can lift it like any other.

### Platform release pause

While Gregale handles a security incident, it may pause new releases for
every account
([ADR-20261022140000](adr/20261022140000-break-glass-controls.md)). Deploys
and new schedules then fail with 503 `platform_release_paused`, which nothing
in the request overrides. Queued builds wait, and scheduled releases start
once the pause is lifted. Your running apps keep serving, rollbacks still
work, and you can still change env vars and secrets, so you can rotate a
credential during the incident. Retry the deploy after the pause is lifted.

## Scheduled releases

Build a release now and start it later: at a quiet hour, at a launch time, or
when the team is around to watch
([ADR-20261021152201](adr/20261021152201-scheduled-releases.md)):

```bash
# Build now; start the rollout at 02:00 local time and tell #releases how it went.
gregale deploy --at 02:00 --release-notify releases
# Other ways to say when: +2h, "2026-10-22 09:00" (local time), or an RFC 3339 time.
gregale deploy --at "2026-10-22 09:00" --rollout fast

gregale release schedule list
gregale release schedule get v44 --app my-api
gregale release schedule set v44 --app my-api --at +30m   # move it
gregale release schedule set v44 --app my-api --at now    # start it on the next pass
gregale release schedule cancel v44 --app my-api
```

The build, readiness check and snapshot happen right away, so a broken build
shows up while you are still there. The release then waits live without
traffic; production stays on the current release, and nothing wakes the
waiting one. At its time, Gregale starts the rollout the deploy would have
had: the same canary (`--rollout safe` or `fast`, or the app's release
policy), or all traffic at once for `--rollout immediate`.

Just before it starts, Gregale checks again:
- **Freezes.** A freeze active at that moment stops the release, including a
  freeze created after you scheduled it. Scheduling into a freeze you know
  about needs `--override-freeze REASON`, which covers exactly the freezes
  active at the scheduled time.
- **Approvals.** If the app requires approvals, the release must roll out
  through a canary, and it waits for approval in that canary like any other
  release. A policy added while the release waited applies to it.
- **Newer releases.** If a newer release serves traffic by then, the
  scheduled one is superseded and never starts.

If the build is still running, or another rollout is still in progress,
Gregale waits for up to an hour past the scheduled time, then gives up
(`expired`). A release that could not start stays built without traffic:
schedule it again, or promote it by hand. `--release-notify` channels (Slack
or email, up to five) hear when the release starts and when it can't.

While a release waits for its time, `gregale release traffic set` and `gregale
traffic promote` refuse it with `release_scheduled`; move or cancel the
schedule instead. `gregale release traffic promote --deployment v44 --at 09:00`
schedules a release you deployed with `--no-traffic`.

A scheduled release is a production release of a request app: previews,
named environments, services, workers and jobs release immediately. An app
has one pending schedule at a time; scheduling another release cancels the
earlier schedule and leaves that release waiting without traffic.

## Revisions

Every deploy creates an immutable revision, numbered per app and shown as
`v41`, `v42`, `v43`. A revision is never rewritten — a redeploy supersedes it
and creates the next one — so it is a stable handle you can keep in a runbook
or an incident channel.

Deploys of one environment go live in the order you start them, not the order
they finish. If you start another deploy before an earlier one goes live, the
earlier one never goes live: it is superseded when it reaches the cutover,
whether or not the newer deploy succeeds.

Use a revision anywhere a deployment ID is accepted:

```bash
gregale rollback my-api --to v41
gregale release traffic set --app my-api --deployment v42 --percent 10
```

`gregale release traffic status my-api` lists the live revisions and their weights.
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
  Promote: gregale release traffic promote --app my-api --deployment v44 --if-serving v43
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
gregale release canary simulate my-api --canary-preset balanced
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
deployment (`gregale release summary`). This also covers immediate
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

A saved `gregale deploy` attempt can recover a lost acceptance reply with
`gregale release resume SESSION_ID`. Image, multipart and source-ref submissions
use a durable server receipt; resumable uploads use their committed upload
receipt. Recovery reconnects to the original deployment without source files or
another submission. Missing receipts leave acceptance uncertain; see
[deploy recovery](cli/deploy.md) and [ADR-20261019120049](adr/20261019120049-deployment-submission-receipts.md).

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

### Check project deployment readiness

Run a read-only check before uploading source or starting builds:

```sh
gregale deploy check --project --project-slug shop --json
gregale deploy check --project --project-slug shop --atomic --verify --config-file release.json
gregale deploy check --project --project-slug shop --from staging --environment production --sync-config --verify --workers
```

The report collects missing runtime keys, invalid dependencies, quota and rollback
retention limits, incompatible release options, worker consumer restrictions,
unavailable verification policy and missing rollout qualification. It uses the
same repository detector and source-selection policy as deployment; use
`--source=worktree` to include local changes. A `--secrets-file` supplies proposed
key names for the check; its values stay on this machine. The command never
submits a deployment, stages credentials, records qualification or executes probes.

CI exits **0** for ready, **1** for blocked and **2** for unknown/unavailable.
Each check includes a stable code, affected workload when applicable and a remedy.
An older server produces unknown and a nonzero exit. Protected targets report that
the final deployment/promotion plan needs approval; this check does not issue,
consume or validate a separate approval credential.

Save `state_hash` and pass `--expected-state HASH` on a subsequent check to detect
changed intent or configuration. Unreadable required state has no comparison hash.
A ready result covers the declared inputs and current control-plane prerequisites;
deployment still validates source bytes, capacity and runtime health. See
[ADR-20261019120041](adr/20261019120041-project-deployment-readiness.md).

Verified atomic project deploys can opt into one existing push-queue worker with
`gregale deploy --project --atomic --verify --workers`. The first milestone
requires HTTP workloads plus exactly one production worker, one enabled push
binding and one matching consumer. It also accepts `--config-file` and `--migrate`.
The worker starts with its candidate configuration while production queue work
continues to use the previous graph. Startup/configuration proof gates the shared
HTTP/worker cutover; the previous generation then receives bounded graceful
shutdown (default 30 seconds, maximum accepted 60 seconds).

Rollback restores the prior graph and paired variables. Completed jobs and
external or database effects remain. Post-cutover jobs retain normal retry/lease
semantics. Admission is disabled by default until native KVM acceptance;
operators enable it with `FAAS_ATOMIC_WORKER_RELEASES_ENABLED=1` after completing
[ADR-20261019120008 qualification](adr/20261019120008-atomic-project-push-worker-handoff.md).

For real queue health verification, add `--worker-health`:

```sh
gregale deploy --project --atomic --verify --workers --worker-health
```

This requires at least five completed delivery attempts in each candidate verification
window and five baseline attempts in the five minutes before cutover. It compares
failures, retries, processing time, queue wait and due backlog. Low traffic reports
`insufficient_evidence`; it never finalizes a release as healthy. Repeated regressions
restore the retained release, worker admission and paired configuration. Job side
effects remain committed. The existing ten-minute verification deadline applies.
The baseline needs real delivery evidence from the updated gateway before opting in.
Worker admission remains disabled pending native qualification. See
[ADR-20261019120009](adr/20261019120009-project-worker-job-health-verification.md) for thresholds and scope.

Named production project worker jobs now persist their release graph at the first
claimed delivery. Retries and operator queue replays retain that graph across
cutovers and gateway restarts. New jobs select the current graph independently.
Expired graphs fail delivery rather than silently move the job to another release;
these pins do not extend the release retention window. Explicit revision pins keep
their existing behavior. See [ADR-20261019120011](adr/20261019120011-durable-project-worker-release-pins.md).
While an unexpired pin owns pending or dispatching work, the scheduler keeps a
separate retained worker pool and can restart a missing worker from that deployment.
It protects the last worker while draining excess replicas, then drains the pool
after the last job finishes. Existing worker/account/concurrency limits still apply;
insufficient capacity delays recovery rather than changing the job's release.
See [ADR-20261019120012](adr/20261019120012-retained-project-worker-pools.md).
Progressive canaries are now available through an explicit opt-in contract:

```sh
gregale deploy --project --atomic --verify --progressive --config-file release.json
# Include a production worker only with real delivery verification:
gregale deploy --project --atomic --verify --progressive --config-file release.json --workers --worker-health
```

The first version requires paired non-secret configuration snapshots. New unpinned
requests and first-delivery queue jobs select a whole graph at 5%, then 25%, then
100%. Each stage needs two fresh healthy rounds; low worker traffic cannot promote
a release. The baseline graph and configuration head remain active until the 100%
transition, when graph, configuration and controller checkpoint commit together.
The candidate graph ID remains stable through promotion and controller restarts.

Regression before activation stops new candidate cohorts; regression afterward
restores the previous graph/configuration together. Existing job and explicit version
pins retain their graph until retention expires. Operator graph/configuration changes
stop the attempt rather than being overwritten. Unknown baseline health requires
operator action; an expired full-traffic checkpoint fails closed for new unassigned
work until recovery. Public receipts show `verification.percent` and `generation`.

Server admission remains disabled pending native qualification. The
`FAAS_PROJECT_CANARIES_ENABLED=1` gate is for the private qualification harness;
workers additionally require the existing atomic-worker admission gate. See
[ADR-20261019120013](adr/20261019120013-staged-project-canary-publication.md) and the
[native progressive acceptance lane](ops/project-progressive-release-native-acceptance.md).

Inspect or control a progressive attempt using its durable submission ID:

```sh
gregale release attempt get ATTEMPT_ID
gregale release attempt cancel ATTEMPT_ID
gregale release recover ATTEMPT_ID
```

`status` shows the traffic stage, generation, blocking reason, latest control
request and available actions. Abort before 100% leaves the baseline graph/config
unchanged. At 100%, abort blocks new unassigned work while fresh baseline health
is checked; qualified restoration commits graph and configuration together.
`recover` retries that verification for a blocked attempt within the retained
graph's lifetime. It cannot force unhealthy or expired baselines, overwrite newer
operator changes, or retarget pinned jobs. Worker restoration still needs real
baseline delivery evidence; the command does not generate synthetic jobs.

Control commands return when their decision is durably accepted. Use `status`
to distinguish pending recovery from completed restoration. For an ambiguous
response, retry with the printed `--request-id ID --generation N`; the same
most recent decision is idempotent. See [ADR-20261019120014](adr/20261019120014-progressive-deploy-operator-controls.md).

For failures that appear after initial rollout success, opt into stabilization:

```sh
gregale deploy --project --atomic --verify --progressive --stabilize --config-file release.json
```

This keeps monitoring critical HTTP probes and real worker health for ten minutes
after success. Workers still require `--workers --worker-health`. Existing revision
retention must be at least 21 minutes. The deploy command returns on rollout
success; use `gregale release attempt get ATTEMPT_ID` to inspect the continuing watch.
Two confirmed regressions can restore graph/config together while the baseline
remains qualified and this attempt still owns production. Newer production intent
stops the old monitor. Unknown health never authorizes rollback or becomes a
healthy verdict merely because the window ended. Monitoring status and its health
verdict are separate fields. See [ADR-20261019120015](adr/20261019120015-post-success-project-stabilization.md).

### Migration compatibility before cutover

New `--migrate` atomic project plans check the current production version again
**after** the expansion command finishes and **before** publishing the candidate.
Configure read-only critical probes that exercise the application's database
paths. Command success alone does not permit cutover.

Missing, unauthenticated or unavailable probe results keep the deployment waiting
within its existing deadline. A complete failed baseline sample stops the attempt
and preserves the current production graph. Inspect the existing task logs and
database state before submitting another migration; schema changes are not
reversed and the completed command is not replayed. The receipt exposes
`release_command.baseline_verification`; `gregale release attempt get
<attempt-id>` also shows this checkpoint.

Passing probes provides evidence for the configured paths, not a proof that every
SQL change is compatible. Keep expansion backward compatible with retained code
and workers. Destructive cleanup is not part of `--migrate`; stabilization does
not by itself prove that old versions and pinned work have drained.

### Guarded schema cleanup

With schema-contract admission enabled on a qualified server, declare both phases
for the expansion owner:

```yaml
release:
  command: node migrate.js expand
  contract_command: node migrate.js contract
```

Deploy using `--project --atomic --verify --migrate --progressive --stabilize
--config-file release.json`. For a multi-workload project, keep one Procfile
`release:` owner and declare only `release.contract_command` in its manifest.
Workers additionally require the existing worker/health flags.

After rollout and stabilization succeed:

```sh
gregale release attempt get ATTEMPT_ID
gregale release contract run ATTEMPT_ID
```

Cleanup waits for expired retained versions and code pins, drained work, and old
instances that stopped or parked. Legacy fallback traffic weights must also select
the current revision at 100%; graph expiry alone does not disable an explicit old
revision that still has a positive traffic weight. Unknown health, uncertain old tasks, newer
production intent or changed runtime inputs block it. The command runs against
the expansion's artifact and runtime fingerprint; credential changes, including
rotation, conservatively require a fresh deployment rather than silently choosing
a different database target.

The CLI prints the durable request ID and generation. After an ambiguous response,
reuse them with `--request-id ID --generation N`; an admitted attempt keeps the
same cleanup task. Use `deploy status` and the existing app-task logs to check its
result. Acceptance is asynchronous. A failed or uncertain cleanup is not replayed,
and retired deployment IDs remain fenced, so they cannot be selected for rollback.
Schema changes are not reversed. Investigate the database before a new attempt.

`FAAS_PROJECT_MIGRATION_CONTRACTS_ENABLED` defaults off pending native qualification;
upgraded API, scheduler and state components are required. Passing critical probes
checks the configured paths, not every SQL operation or externally managed work.

### Roll forward after failed or uncertain cleanup

Inspect `gregale release attempt get FAILED_ATTEMPT` and its cleanup task logs. Establish
that the original execution has stopped, inspect actual database state, and repair
SQL explicitly if necessary. Keep that private operational evidence outside the
deploy journal. An unknown task exit remains unknown after recovery.

Deploy compatible corrected code as a different atomic verified attempt with
paired configuration (`--project --atomic --verify --config-file repair.json`).
Remove obsolete release declarations, or declare a separately reviewed expansion
when needed. The original cleanup command is not part of this repair deployment.
Wait for verification/stabilization, retire legacy fallback traffic through the
usual revision traffic controls, and allow retained graphs, pins, work and old
instances to drain.

Then record incident closure:

```bash
gregale release contract resolve FAILED_ATTEMPT \
  --replacement-attempt REPAIR_ATTEMPT \
  --database-state-checked --execution-quiescent \
  --evidence-hash SHA256_OF_PRIVATE_EVIDENCE
```

The CLI binds the inspected task and cleanup generation and prints a durable
request ID. Reuse `--request-id` and the same identities/attestations after an
ambiguous response. State and configured critical-route checks must still match
before first acceptance. Existing accepted resolutions replay after newer deploys.
Retirement fences extend to drained non-current code, while the original cleanup
keeps its failed or uncertain outcome. `deploy status` shows the resolution and
replacement attempt. A missing cleanup receipt cannot be overridden with force.

This action remains available for existing incidents when new cleanup admission
is disabled. Database and execution checks are explicit operator attestations;
configured HTTP probes do not establish arbitrary SQL compatibility or prove
that an unobserved external execution has stopped.

At full traffic, a new `abort` or `recover` decision requires an unexpired retained
baseline. If retention has ended, the server returns HTTP 410 with
`project_release_baseline_expired`; deploy a compatible replacement. Rejection
preserves the current graph, configuration and existing control decision. Replaying
an already accepted request still returns its historical decision.

### Atomic application secret rotation

Qualified operators can opt into encrypted production secret snapshots:

```sh
gregale deploy --project --atomic --verify --yes \
  --config-file release.json --release-secrets-file credentials.json
```

`credentials.json` maps existing workload names to complete secret value sets:

```json
{"frontend":{"API_KEY":"replacement-value"}}
```

Use existing application-owned production keys. Omitted workloads inherit their
prior secret snapshot; an empty workload map removes its serving secrets. The
first version supports HTTP workloads with persistent secrets and excludes managed
credentials, workers, migrations, sidecars, explicit secret grants and reload hooks.
The CLI excludes this file from source packing and sends values separately over
TLS. APID encrypts them before storage and returns version IDs, which are then
bound into the scan/apply plan. The candidate uses its proposed secrets while the
old release keeps its earlier versions. Cutover and rollback select code, variables
and immutable secret references through the same project graph.

If staging loses its response, reuse the printed ID with
`--release-secrets-request-id ID` and the same file contents within 24 hours.
Staging is limited to 32 live bundles per project and the shared 64 KiB document
bound. Public receipts and local sessions contain version IDs and fingerprints,
never secret values or encrypted envelopes. JSON receipts show candidate references
under `configuration.secrets.versions` and rollback references under
`configuration.secrets.previous_versions`.

Once pinned, production secret edits through `secrets set`, deletion or resealing
are rejected; use another atomic secret release. Keep both external credentials
valid while rollback remains possible. Host-key rotation of pinned applications
requires a future snapshot-aware rekey workflow; retain the old host identities.
The secret registry remains the legacy source, while release receipts identify
which versions a deployment actually uses.

`FAAS_PROJECT_RELEASE_SECRETS_ENABLED` defaults off until the
`project-release-secrets-only` native lane passes and its evidence is reviewed.
See [ADR-20261019120020](adr/20261019120020-atomic-project-secret-snapshots.md).

## Promote a tested HTTP configuration snapshot

Use the existing environment promotion command to transfer a tested artifact and
its pinned non-secret variables together:

```sh
gregale environment promote my-project --from staging --to production --sync-config --yes --wait
```

Both environments must have complete active HTTP release graphs. The preview
lists changed variable keys and binds the exact configuration versions. Production
keeps its own secrets, secret version pins, authentication credentials, service
bindings and infrastructure assignments. An explicitly empty source variable set
clears the candidate's variables. Promotion reuses the source artifact without a
new build, and protected targets still require source qualification and approval.

A change after preview requires a fresh preview. Once published, verification
checks the graph, artifact and captured configuration identities. Its automatic
rollback restores the previous graph and pinned variables with the previous
configuration document. Later production edits block rollback from overwriting
them. Interrupted requests resume from the stored capture using the same
idempotency key. Secret envelope rotation preserves the approved identity.

Workers, jobs, migrations, sidecars, reload hooks and owned work/queue policies
remain outside this snapshot transfer milestone. Generated service URL variables
require the opt-in verified service graph mode described below. Environment-owned
bindings remain at the destination. Qualify guest values,
cold boot and application health on the native acceptance host before rollout.
See [ADR-20261019120022](adr/20261019120022-snapshot-aware-environment-promotion.md).

### Verify destination health during environment promotion

For snapshot-aware HTTP promotion, operators can enable the qualified
`FAAS_PROMOTION_HEALTH_ENABLED=1` admission mode. It is disabled by default
pending [native qualification](ops/promotion-health-native-acceptance.md).
Configure read-only GET/HEAD route-health probes for every workload first.
Both environments need active complete HTTP graphs, pinned configuration and
at least 21 minutes of revision retention; at most five project probes apply.

```bash
gregale environment preview shop --from staging --to production --sync-config --verify
gregale environment promote shop --from staging --to production --sync-config --verify --yes --wait --progress --timeout 25m
```

The server returns an accepted running operation. Two complete healthy rounds,
one minute apart, are required while candidates serve no normal traffic. Graph
and configuration then cut over atomically, followed by two new healthy rounds
before success. Probe requests preserve destination scope and customer auth.
Use a dependency-checking application endpoint to test database access.

Inspect `gregale environment status shop PROMOTION_ID --to production`
for phase, verdict, deadline and bounded probe counts in JSON. Missing results,
authentication failures and unexpected HTTP responses remain unknown health.
Preflight has a ten-minute deadline and leaves the previous release active on
failure. Repeated post-cutover regressions with a healthy retained baseline
restore its graph, configuration and runtime variables. If baseline health cannot
be confirmed, or newer destination intent conflicts, status requires operator
action. A client timeout does not stop server processing; retries and daemon
restarts retain deadlines and checkpoints. See [ADR-20261019120023](adr/20261019120023-health-gated-environment-promotion.md).

### Promote a complete HTTP service graph

After native qualification, enable `FAAS_PROMOTION_SERVICE_GRAPH_ENABLED=1`
alongside the health admission flag and use the same `--sync-config --verify`
command. Every declared destination dependency must belong to the complete project
HTTP graph, and required source binding keys must exist at the destination.
The server regenerates platform service URLs using the destination assignments
and transport; production keeps its own secrets and service caller grants.

Preview reports `destination_config_hash` and changed variable keys. The source
and destination input document diff remains visible separately. The resolved
document and runtime pins publish together. Configure a read-only frontend probe
that actually calls the API and validates its response. Private candidate callers
route to candidate peers before cutover; retained baseline callers keep their
previous peers afterward. A failing candidate API prevents cutover or triggers
the existing health rollback. See [ADR-20261019120024](adr/20261019120024-destination-aware-service-graph-promotion.md).

### Promote a release with a database expansion

After native qualification, operators may enable
`FAAS_PROMOTION_MIGRATIONS_ENABLED=1` alongside promotion health admission.
Preview and submit with:

```sh
gregale environment preview shop --from staging --to production --sync-config --verify --migrate
gregale environment promote shop --from staging --to production --sync-config --verify --migrate --yes --wait --timeout 35m
```

`--migrate` explicitly asserts that the source release's single declared
`release_command` is backward-compatible with the destination's current code.
Both environments must have complete pinned HTTP release graphs. Service graphs
also require the service graph admission switch. The command reuses the approved
source artifact with destination variables, secrets, migration credentials and
bindings. Source migration success does not authorize destination cutover.

The server first prepares dark candidates, then admits one durable release task.
The task has one minute from admission to start and at most ten minutes to run.
It must return an owned zero exit receipt before candidate and previous-version
HTTP checks begin. Existing health checks gate atomic graph/config cutover and
post-cutover success. A process restart resumes the same task; commands dispatched
without a confirmed exit become uncertain and are never replayed automatically.
Status includes the migration task ID, outcome and exit code. A destination input
change invalidates dispatch or cutover; preview again after resolving the change.

Migration commands can commit partial database changes even if they fail.
Rollback restores code and configuration; it leaves the schema expansion in
place. Destructive schema cleanup and multiple migration owners are unsupported.
For an uncertain outcome, inspect task logs and the destination database before
operator recovery. New migration promotions are blocked while that outcome is
unresolved. Native qualification is documented in
[the acceptance procedure](ops/promotion-migration-native-acceptance.md).

### Recover an interrupted promotion migration

Inspect the original task and destination database first. Keep private inspection
notes and supply their SHA256; Gregale does not upload those notes or run repair
SQL. Read the current health generation with `projects environments status`.

If the expected expansion is present and compatible with both releases:

```sh
gregale environment recover-migration shop PROMOTION_ID --to production \
  --action resume --task-id TASK_ID --generation GENERATION \
  --database-state-checked --schema-compatible --evidence-sha256 EVIDENCE_SHA256 --wait
```

Recovery first requests scheduler teardown of the original VM. After its confirmed
acknowledgement, the server rechecks the original configuration/runtime pins and
starts a fresh health preflight. `--wait` waits for recovery acknowledgement;
`resumed` means promotion health continues, rather than that cutover has finished.
The original migration task remains failed or uncertain and is never replayed.

If repair code/configuration is required, publish a different complete snapshot
release without another migration, then complete a normal verified promotion into
the same destination. Close the old incident against that repair promotion:

```sh
gregale environment recover-migration shop PROMOTION_ID --to production \
  --action resolve --replacement-promotion REPAIR_PROMOTION_ID \
  --task-id TASK_ID --generation GENERATION --database-state-checked \
  --evidence-sha256 EVIDENCE_SHA256 --wait
```

The repair promotion must already be succeeded, verified and currently active.
Fresh server-owned probes and runtime/configuration guards run again before
resolution. A repair can include the supported worker using `--workers` without
`--migrate`. The server rechecks current worker startup and consumer configuration,
and compares deliveries by that replacement before and after the recovery request.
Both windows need sufficient complete, healthy attempts; low traffic leaves recovery
verifying until its deadline. The old promotion remains failed, with a separate resolved receipt.
A matching `--request-id` retries the same decision; when omitted, the CLI derives
a stable ID from the request. Changed attestations with the same ID conflict.
Blocked attempts can use a new request and the current health generation.

An unreachable executor or missing runtime registration cannot be acknowledged as
stopped. Recovery waits for the scheduler/vmmd path and expires safely if teardown
cannot be confirmed. There is no force option or automatic SQL retry. Migration
promotion and recovery still require the native acceptance qualification.

### Promote APIs and queue workers together

On a qualified server, enable `FAAS_PROMOTION_WORKERS_ENABLED=1` alongside the
promotion health gate. Promote a complete pinned release containing HTTP services
and existing production push-queue workers:

```sh
gregale environment preview shop --from staging --to production --sync-config --verify --workers
gregale environment promote shop --from staging --to production --sync-config --verify --workers --yes --wait
```

Each production worker keeps its queue binding, consumer policy and credentials.
Its source artifact and non-secret variables follow the tested release. A source
worker without a variable snapshot needs a live runtime proof matching its current
variables and credentials. Each source and previous worker pair needs matching
worker execution settings and stop grace.
Verified environment promotion supports up to eight push workers, each with one
production queue consumer. Account worker-replica quotas still apply to the
retained, candidate and any running staging pools. All workers must pass startup
and baseline checks;
post-cutover delivery health is tracked independently. A healthy subset cannot
complete the release. A qualified regression restores the entire application after
all retained workers are ready. Existing jobs keep their original version pins.
Multiple consumers per worker, pull/job workers and sidecars remain unsupported.
Multiple-worker promotion additionally requires the signed `worker_set` capability
and its native qualification receipts; production enablement remains pending.
See [ADR-20261019120040](adr/20261019120040-multiple-worker-environment-promotion.md).
Combined `--migrate --workers` promotions require the separately approved
`coordinated` capability; see [coordinated promotion](ops/coordinated-promotion-native-acceptance.md).
Migration-incident resolution requires a verified repair promotion without a
migration, with fresh startup and delivery evidence for each replacement worker.

Candidates start without receiving production queue jobs. Cutover requires
every worker's configuration/startup proof, healthy baseline job evidence and the configured
HTTP probes. Graph/configuration and new-job admission switch together. Existing
jobs retain their original release through retries; retained workers remain
available for eligible outstanding pins within the normal retention limits.

After cutover, real worker delivery health and HTTP probes must agree before
success. Low queue traffic remains unknown and does not count as healthy. Status
includes worker readiness, job sample counts, backlog and verdict. A regression
can restore the captured graph/configuration only after every previous worker has
current startup proof. Changed bindings or newer production intent require
operator action. If an explicit rollback is waiting for worker startup, retry
the same rollback request once all retained workers are ready. Completed jobs and
other external effects are never undone.

The admission flag defaults off pending native qualification. See
[ADR-20261019120027](adr/20261019120027-worker-aware-environment-promotion.md) and the executable
[native promotion acceptance lane](ops/environment-promotion-native-acceptance.md).

### Abort or recover an environment promotion

Inspect `gregale environment status PROJECT PROMOTION --to ENV` for the
health generation and available operator action. For a health-gated HTTP promotion,
including supported production push workers:

```bash
gregale environment abort shop PROMOTION_ID --to production --generation 7 --wait
# After a blocked restoration and dependency repair, inspect status again:
gregale environment recover shop PROMOTION_ID --to production --generation 12 --wait
```

Before cutover, abort keeps the destination graph/configuration selected, retires
owned unused candidates, and lets the scheduler drain their instances. After
cutover, abort requests fresh baseline-only checks before restoring the captured
graph/configuration and worker selection together. Two minute-spaced healthy HTTP
rounds and, for workers, current retained-worker startup proof and healthy real
job evidence are required. Unknown health and quiet workers cannot authorize a
switch. Existing jobs retain their first-admission deployment pins.

Each decision has a ten-minute server deadline. A blocked decision may be retried
with `recover` after inspecting status and fixing its cause. Recovery uses the
original baseline and artifacts; it does not extend retention or overwrite newer
destination intent. Migration incidents use `recover-migration` instead.

The CLI derives a stable request ID from the inspected generation, or accepts
`--request-id` with 32 lowercase hexadecimal characters. Replaying the same request
returns its durable receipt. A changed generation/action with that ID conflicts.
A local wait timeout leaves the server decision running. The API exposes POST
`.../promotions/{promotion}/abort` and `/recover`; Go, Node and Python SDKs include
the same request and receipt contract. Admission remains default-off pending
[native qualification](ops/promotion-health-native-acceptance.md).

Promotion health checks now use durable due-time scheduling. Four reconciliation
slots are shared by updated APID instances, with at most two per account. A slot
prefers rollback and recovery while the others serve the oldest due work. Slow
checks can no longer keep every later promotion behind the same 20-record batch.
APID restarts retain pending work; abandoned claims become eligible after their
one-minute lease expires. Required sample intervals, health deadlines and release
retention remain unchanged. Existing scheduler quotas still govern every VM wake.

## Promotion admission

Verified environment promotions start immediately when capacity is available.
The control plane admits up to 24 active promotions across the fleet and eight
per account. Excess requests wait durably with `status=running` and
`health.phase=queued`; the CLI displays the reason and wait deadline. Waiting
lasts at most 15 minutes, with up to 128 queued requests across the fleet and 32
per account. These limits do not increase your application's resource quotas.

The status endpoint includes `health.admission.queued_at`, `wait_deadline`,
`reason`, and, once dispatched, `started_at`. Before dispatch, the health
checkpoint's timestamps describe waiting. Dispatch starts a fresh health window
only after checking that the approved source, destination and configuration are
still current and retained. Changed inputs finish as conflicted; preview and
approve again. Expired waiting requests fail without starting workloads.

A full queue returns HTTP 429 with code `project_promotion_queue_full` and
`Retry-After: 5`. No promotion was accepted and the approval remains available.
Retry with the same idempotency key and approval while the preview is still valid.
Previously accepted keys continue to return their original operation.

Use the existing abort command with the current generation to cancel waiting,
including a queued migration. A local CLI wait timeout leaves the durable operation
running; use the printed status command to resume observing it. Rollback and
recovery continue through the existing dispatcher while new starts wait.
Promotion health remains disabled by default pending native qualification.

### Promote artifacts with production secret changes

For a complete HTTP project, stage a destination-only secret rotation while
promoting the tested artifacts:

```sh
gregale environment promote shop \
  --from staging --to production --sync-config --verify --yes \
  --release-secrets-file production.json --wait
```

`production.json` maps workload names to complete proposed secret maps, for
example `{"api":{"TOKEN":"new-production-credential"}}`. Omitted workloads
inherit production secrets; an empty workload map removes its serving secrets.
Only existing application-owned persistent production secret keys are supported.
Migrations, workers, managed credentials, sidecars, explicit grants and reload
hooks are outside this mode.

Values go only to encrypted secret staging. Preview and approval bind immutable
version references and show secret key changes. Use the printed request ID with
`--release-secrets-request-id ID` to replay staging within 24 hours. Candidates
receive the proposed production versions, and rollback restores the previous
code, configuration and secret versions. Keep old external credentials valid for
the rollback window. Staging credentials are never copied to production.

This capability requires the existing secret-release and verified-promotion
flags plus the operator's qualified `secrets` rollout scope. Production admission
remains unavailable until native secret-promotion qualification is reviewed; the
workspace storage/API tests do not establish guest delivery. See
[ADR-20261019120042](adr/20261019120042-secret-aware-environment-promotion.md).
