# Release approvals, freezes and schedules

Decide who may ship and when: require approvals before a release reaches traffic, freeze deploys for an app or organization, and schedule a release for a time your team is around to watch.

## Release approvals

Require people to approve each production release before it takes more than
its first canary stage:

```bash
# Two owners or admins, other than the deployer, approve every release.
gregale release approval require --approvals 2 --notify releases
# One person, developers included, within four hours.
gregale release approval require --approvals 1 --approvers developer --timeout 4h --notify releases

gregale release approval pending                 # releases waiting right now
gregale release reject <deployment> --reason "checkout latency is up"
gregale release approval status <deployment>
gregale release approval disable
```

A production release is one to the default scope or to the project's
`production` environment (`--environment production`); both serve the app's
production traffic, so the policy gates both. Other environments and scopes
are not gated.

### How a release waits

The release starts its canary as usual. At the stage the policy names
(`--after-stage`, default 1), it waits. `gregale deploy` shows the wait. The
canary still judges the waiting stage, so a regression aborts the release
even while it waits. Once enough people approve, the rollout continues on its
next step.

### Releases that run a migration

A release with a release command (a migration) waits earlier: after its
build, before the command runs, because a migration changes production data
the moment it runs and aborting a canary does not undo it. It is listed in
`gregale release approval pending` like any waiting release, for the same
timeout. Once approved, the command runs and the release rolls out without
waiting again. If it is rejected or times out, the deployment fails without
running the command and production keeps the current release. When it is
approved, Gregale checks again before running the command:
- **A newer release:** if you deployed a newer release of the app to the
  same environment meanwhile, this one is superseded and its command never
  runs.
- **The break-glass pause:** it waits until the pause is lifted.
- **A deploy freeze:** a freeze that started after you deployed fails it
  with `release_frozen`. A freeze you overrode when deploying does not.
- **Its schedule:** a scheduled release whose schedule ended (expired an
  hour after its time, blocked or cancelled) before it was approved fails
  without running its command. Deploy and schedule it again.

A newer release that failed or was cancelled does not replace one that
waits; only a newer release that can still ship does.

The app's first production release has no canary to wait in, so its command
runs at once.

### Approve or reject a release

A person approves on the release's approval page in the dashboard
(`/dashboard/releases/<deployment>/approval`, linked from the notification),
signed in (with MFA when the account uses it). An API key, the CLI's login token or a CI job cannot
approve: `gregale release approve` answers `403
release_approval_session_required` with the page's link. Rejecting works
from anywhere, because stopping a release needs no second person.

### Keep the policy in force

The policy stays in force until an owner or admin removes it. Changing or
removing it needs the admin scope (signed in, the CLI's login or an admin
key), so a CI key with `deploy:write` cannot switch it off; the same holds for
deleting a release gate. An app with a policy cannot leave request mode
(`409`): a service, worker or job has no canary stage to wait in, so its
releases would ship unapproved. Remove the policy first.

### How waiting ends

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

### Who may approve

- **Approvers:** with `--approvers admin` (the default), organization owners
  and admins; `developer` adds developers.
- **The deployer:** their own approval does not count unless you pass
  `--allow-self-approval`. An app outside an organization has one person,
  whose own approval always counts. A GitHub push names a GitHub login, not a
  Gregale account, so its author can't be recognised; without
  `--allow-self-approval` a pushed release needs one more approval than the
  policy asks.
- **Once approved:** a release that has its approvals is decided. Further
  approvals and rejections are refused; to stop it, abort its rollout.
- **Changing the policy:** only owners and admins can change or remove it.

### Notify approvers

Tell people when a release waits, is approved or rejected, or times out:

```bash
echo "https://hooks.slack.com/services/…" | gregale alert channel create --name releases --kind slack --secret-stdin
gregale release approval require --approvals 2 --notify releases
```

`--notify` takes one to five Slack or email channels and is required, so the
approvers learn a release is waiting; PagerDuty channels are refused, because a
release waiting for approval is not an incident. Each
event is sent once per release.

The Slack message for a waiting release has **Approve**, **Reject** and
**Review** buttons, and the email has the same links. They open
`/dashboard/releases/<deployment>/approval` in Gregale; if you are not signed
in, Gregale asks you to sign in first. The decision is made on that page,
under your own Gregale account, so your organization role, MFA and the
self-approval rule apply exactly as in the CLI. The buttons grant nothing by
themselves.

### Releases with no canary to wait in

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
That approval comes from a person signed in, not an API key or token, and on
a company app from someone whose role the policy lets approve.
A GitOps source releases into `production` only once a person approved the
revision in Gregale (approval policy `manual`); with `protected_branch` the
sync waits until you remove the app's policy or switch the source to manual
approval. That one approval stands in for the app's policy only when a person
gave it, the policy asks for one approval and, in an organization, the
approver holds a role the policy lets approve; otherwise the sync waits. An
API key's approval of the revision does not count: approve the same revision
on the environment's GitOps page in the dashboard.

While a policy or a pending scheduled release names a channel as its last
Slack or email channel, that channel can't be deleted (`409
notification_channel_in_use`): name another channel there first.

The app's **Release safety** page in the dashboard
(`/dashboard/apps/<app>/release-safety`) shows the policy and the releases
waiting for approval, next to the app's freezes, gates and scheduled
releases.

## Deploy freezes

A freeze stops new production releases of one app, or of all of its owner's
apps, while it is active:

```bash
# Freeze one app for two hours, starting now.
gregale release freeze create --app my-api --for 2h --reason "incident 4711"
# No releases from Friday 16:00 to Monday 08:00 (64 hours), Istanbul time, for every personal app.
gregale release freeze create --all-apps --weekly fri --at 16:00 --for 64h --tz Europe/Istanbul --reason "no weekend releases"
# After Gregale rolls a release back on its own, freeze the app for an hour.
gregale release freeze create --app my-api --after-rollback 1h --reason "settle after automatic rollbacks"
# Freeze every app of the company organization acme for three days.
gregale release freeze create --org acme --for 72h --reason "holiday freeze"

gregale release freeze status --app my-api
gregale release freeze list [--app my-api | --org acme]
gregale release freeze lift <id> [--app my-api | --org acme]
```

A company organization owns the freezes of its apps, whoever created the app.
A freeze on all apps with `--org` covers every app of that organization; a
freeze with `--all-apps` covers only your personal apps. On a company app,
every member sees its freezes, and a member who deploys can freeze it with a
window and lift a window they created. Weekly and after-rollback freezes are
standing policies, so only owners and admins create them, and they can lift
any freeze. A company's freezes stay when the member who created them leaves,
and they appear in the organization's activity.

While a freeze is active, every way of releasing to production is refused
with `release_frozen`, which names the freeze, its reason and its creator:
- `gregale deploy` and the deployment API;
- GitHub push deploys;
- `gregale deploy --project`;
- promotions into `production`;
- `gregale release retry` and the dashboard's retry button. A retry can't
  carry an override; deploy again with `--override-freeze` instead.
- giving traffic to a release deployed without traffic before the freeze
  began (`traffic`, promote, or a rollback to it): it has never served, so it
  waits like a new deploy. A release deployed during the freeze, or one that
  served before, is not refused.
- a rollback to a release that never served, even after a newer release
  replaced it. A rollback can't carry an override, and a rollback to a
  rejected release is always refused.

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
the freezes it overrode and the deployment it created. A company
organization's freeze is overridden only by someone who may lift it: an owner
or admin, or the member who created the window, signed in or with an admin
key. Anyone else, and a key that can only deploy, gets `403
release_freeze_override_not_allowed`. Lifting a freeze needs the admin scope
too; a CI key with `deploy:write` cannot lift one. A GitHub push can't carry an override, so a frozen
push fails, and the commit shows why.

An `--after-rollback` freeze is a policy. Each time the rollout circuit
breaker, the post-release watch, the first-wake 5xx guard or an alert rule
rolls an app back, Gregale freezes that app for the given time. The freeze
names the release that was rolled back, and you can lift it like any other.
If that rollback does not restore the previous release, Gregale lifts the
freeze again: the release still serves, and its fix has to ship.

### Platform release pause

While Gregale handles a security incident, it may pause new releases for
every account. Deploys
and new schedules then fail with 503 `platform_release_paused`, which nothing
in the request overrides. A release you deployed earlier with `--no-traffic`
can't take its first traffic during the pause either. Queued builds wait, and
scheduled releases start once the pause is lifted. A release deployed before
the pause still finishes its build, but its release command (a migration)
waits for the pause to be lifted. Your running apps keep
serving, rollbacks to a release that served before still
work, and you can still change env vars and secrets, so you can rotate a
credential during the incident. Retry the deploy after the pause is lifted.

## Scheduled releases

Build a release now and start it later: at a quiet hour, at a launch time, or
when the team is around to watch:

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

Because the build and any release command (a migration) run as soon as you
deploy (once approved, when the app requires approvals), a freeze active
right now refuses a scheduled release just as it refuses an immediate one,
even when the scheduled time falls after the freeze. Wait for it to end, or deploy with `--override-freeze REASON`.

Just before it starts, Gregale checks again:
- **Freezes.** A freeze active at that moment stops the release, including a
  freeze created after you scheduled it. Scheduling into a freeze you know
  about needs `--override-freeze REASON`, which covers exactly the freezes
  active at the scheduled time. Overriding a company organization's freeze
  this way needs the same right as an immediate override: someone who may
  lift the freeze.
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
schedule instead. `gregale release traffic promote --release v44 --at 09:00`
schedules a release you deployed with `--no-traffic`.

A scheduled release is a production release of a request app: previews,
named environments, services, workers and jobs release immediately. An app
has one pending schedule at a time; scheduling another release cancels the
earlier schedule and leaves that release waiting without traffic.
