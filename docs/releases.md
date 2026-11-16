# Safe releases

Every deploy to production is a release. This page covers what makes one safe: how Gregale handles changes it cannot undo, releases that change the database, rollbacks that restore it, release gates on your own signals, and the release page.

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

If the command exits non-zero or times out, the deployment fails with
`release_command_failed`. If Gregale could not run the command or read its
result (the task's environment could not be prepared, or its result channel
broke), it fails with `release_infrastructure_failed`; deploy again. In both
cases the previous release keeps serving.

Release commands of one app run one at a time per environment, oldest first.
If you deploy again while a release's command is still waiting to start, the
newer release replaces it and the older command never runs. A command that
already started finishes before the next one starts.

A release command can end without an exit code: its worker was lost, it timed
out, or it was cancelled while running. It may have changed part of the
database. The next release of that environment then fails with
`release_outcome_uncertain` before its own command runs. Check the database,
record what you found, and deploy again:

```bash
gregale release resolve v41 --app my-api --note "migration 42 applied; schema matches"
```

A later release command of the environment that succeeds also clears it.

A rollback restores code, never the database. Rolling back to the release
that was serving when the migration ran is the usual case and needs nothing
extra: that code already ran against the migrated schema. Rolling back
further is different. If a release command ran after the target last served,
the rollback is refused with `rollback_crosses_release`:

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
rollback, the post-release watch and alert-rule rollback actions) never
acknowledge, so they leave the current release serving instead. When the
post-release watch or the first-wake 5xx guard finds a regression it cannot
roll back, or its automatic rollback does not restore the previous release
(`rollback_skipped: "rollback_failed"`), the release's decision records it with `rollback_skipped: "rollback_crosses_release"`, its
timeline shows `watch_regressed` instead of a passed watch, and you decide:
roll back with `--acknowledge-releases` or ship a fix.

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
together:

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

Until the switch reaches step 4, any failure (or `gregale release rollback status
my-api <id> --cancel`) unwinds it, and the app keeps serving its current
release on the original database.

When the code is fine and the data is not, restore the database to a point
you pick and switch the app without changing its code:
`gregale postgres restore orders --name orders-0900 --point-in-time <time> --switch my-api`
runs the same steps with the current release
([Restore and switch an app](postgres.md#restore-and-switch-an-app)). The restored copy is kept either way;
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
- **The restored copy must fit inside the plan's database limit.** It
  becomes the app's database, so it cannot use the
  [restore slot](postgres.md#restore-to-a-point-in-time) a plain restore may
  take. At the limit the rollback is refused with
  `managed_postgres_quota_exceeded` (403) before anything changes. Delete a
  database you no longer need or upgrade, then roll back again. If the limit
  fills after the request, the switch fails with `database_limit` and the app
  keeps serving.

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

An enforced binding release policy also applies to a database-restoring
rollback. The switch checks the currently published bindings before changing
the serving code and again before restoring the previous code if it aborts.
The cutover separately verifies the restored database's staged credentials.
Missing binding evidence blocks the code change; the policy remains enforced.

## Release gates

Gregale already stops a release whose 5xx rate, latency, crashes or memory
regress. Release gates add the signals only your app knows about:

```bash
# Abort or roll back when the release burns the checkout SLO 10x too fast.
gregale release gate create --name checkout-slo --slo checkout --max-burn-rate 10
# ...when it introduces a new error group with 3 or more events.
gregale release gate create --name no-new-errors --new-errors --min-events 3
# ...when orders per minute average below 5.
gregale release gate create --name orders --metric orders_per_minute --below 5
# ...when orders per minute drop more than 30% from before the rollout.
gregale release gate create --name orders-drop --metric orders_per_minute --max-drop 30
# ...when the canary fails the "checkout" synthetic check more than the
# release it replaces (and more than 10% of the time).
gregale release gate create --name checkout-probe --check checkout
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
measured. Because the post-release watch needs `rollback_on_5xx`, a
production release of an app with gates that takes traffic without a canary
cannot also turn `--rollback-on-5xx` off (`409`): nothing would judge it.

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
- Both metric gate kinds read the metric's aggregate. Add
  `--metric-label key=value` (repeatable; `labels` in the API) to judge the one
  labelled series with exactly those labels, for example
  `--metric orders_per_minute --metric-label region=eu --below 5`.
- **`metric_change` gates** (`--max-rise` or `--max-drop`) compare the
  metric's average, read from its history the same way, with its average over
  the same length of time before the rollout started. Use them when a fixed
  bound would have to be guessed: "orders must not drop by 30%" holds across
  seasons. Both windows need `min_samples` values. A metric that averaged zero
  before the rollout is not judged.
- **`synthetic` gates** (`--check`) send one of the app's
  [synthetic checks](synthetic-checks.md) to the canary and to the stable revision
  once a minute, pinned to each revision. A probe passes when the response
  has the check's expected status, or any 2xx when it names none. The gate
  fails when the canary fails more than `--max-failure-percent` (default 10)
  of its probes and more than the stable revision does, so an outage of a
  shared dependency does not abort a good release. Probes that get no answer
  are not counted. Synthetic gates run during canaries only: a release
  without a canary keeps its scheduled synthetic checks. A check that a gate
  probes cannot be paused or deleted (`409`) until the gate is removed, as an
  SLO a gate judges cannot be deleted.
- **`ai` gates** compare the release's AI gateway calls with the previous
  release's:
  `error_rate` (provider errors), `flagged_rate` (answers truncated, refused,
  empty, invalid JSON or with a failed tool call), `cost_per_call` or
  `ttfb_ms` (time to first byte). The gate fails when the release is more
  than `--max-increase` percent worse (default 50), and worse by more than a
  floor: 1 percentage point, 1 millicent per call or 50 ms. Both releases need
  `--min-samples` calls. Only calls from the app's instances count;
  `gregale ai deployments` lists them by deployment.

A gate that has too few requests, probes or values (`--min-samples`: default
20, or 6 probes for synthetic gates) is not judged. A gate never holds a
rollout on missing evidence. The rollout decision still lists it, with
`not_judged: true` and what it was waiting for, such as "not judged: 4 of 20
requests so far", so a canary that completes on a quiet app shows which gates
never ran. An SLO that a gate judges cannot be deleted until the gate is
removed.

## What changed between releases

`gregale release diff` shows what a release changes compared with the
release that served before it:

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
A release whose build made no artifact (failed, cancelled or still running)
has no runtime base yet: the runtime base is not listed as a changed setting,
and the summary ends with `runtime base unknown: v2 made no build artifact`.
The approval page and the "approval needed" message show the same
comparison. `gregale release summary` remains the quick cockpit view:
rollout status, rollback target, and release fields against the previous
deployment.

## The release page

Every release has a page at `/dashboard/releases/<id>` that tells its whole
story in one place. The terminal shows
the same history:

```bash
gregale release timeline v42           # phase, gates, approvals, then every event
gregale release timeline v42 --json    # GET /v1/deployments/{id}/timeline
```

The page and the command show:
- **Where the release is:** building, scheduled, waiting for approval,
  rolling out, held, watched after the rollout, released, rolled back or
  replaced, with one line saying why. For example: "Held at step 2 of 4, 10%
  of traffic: 12 of 50 requests".
- **What happened, oldest first:** creation, the schedule, approval requests
  and decisions, the rollout start, each canary step with the evidence
  behind it, holds, release gate results, completion, the post-release
  watch, an abort or rollback, and the release that replaced it. Repeated
  holds for the same reason show as one line with a count.
- **What changed:** the same comparison as `gregale release diff`.

While a canary rolls out, the page can advance it one step or abort it and
return traffic to the stable release. It uses the same API routes as the CLI,
so the same roles, MFA, approval, freeze and gate checks apply. A release
waiting for approval links to its approval page. The page refreshes itself
while the release is in flight. The deployment page (build, scan and logs)
and the approval page link to it.
