# ADR 20261020214632 · Safety rails for automatic per-unit deploys

- **Status:** accepted by the owner, 2026-10-20 (merge queue: keep the fallback; the repository stays user-owned)
- **Date:** 2026-10-20
- **Related:** ADR-1650 (per-unit releases), ADR-1736 (zero-downtime
  restarts and the two-generation hand-off), ADR 202610171900 (release
  compatibility gates, `slo_gate.py`), ADR 20261018084150 (expand-only
  migrations in unit releases), ADR 20261018133100 (host-configuration
  lane), ADR 20261018104452 (releases decoupled from the full CI tier),
  ADR-1445 (post-release watch for customer releases), ADR
  20261019071733 (customer deploy freezes)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

Per-unit lanes (ADR-1650) went green on production-us on 2026-10-20
(cd-controlplane run 39494933691, 11.5 min; cd-edge-units run 39496715044,
19 min). Setting `UNIT_RELEASE_AUTODEPLOY_ENVIRONMENT` would make every merge
to main deploy itself. Until now a person started each deploy, and that
person supplied checks the pipeline does not have:

- **What was tested.** A pull request's light tier tests the PR merged into
  the main it last saw. The `main` ruleset requires six light-tier checks but
  not an up-to-date branch, so two PRs that pass alone can merge into a tree
  nobody tested. Main took 24 to 67 merges a day from 2026-10-14 to 10-20.
  Main's own light tier runs after each merge but is not a gate: its runs
  took 14 to 45 min (median 23) over the last 30 merges, and 9 of those 30
  never reported, because a newer pending run in the `ci-light-main` group
  replaces an older pending one.
- **When to start.** A person did not deploy during an incident, during a
  full release or another deploy, or during a declared freeze. The unit
  lanes check none of this. Their concurrency group (`cd-cp-units-<env>`)
  serialises unit runs only. cd-platform uses another group. deployctl's
  host lock (`/run/lock/faas-deploy.lock`) is held only while a release is
  activated.
- **After the gate.** The SLO gate (`slo_gate.py`) holds a host for 4 min
  (canary) or 90 s (follower), and caps each rule's `for:` at 2 min or 30 s.
  The platform SLO rules it reads average over 5 to 15 min and fire after
  2 to 15 min, so a regression that builds up (latency under load, cold-boot
  fallback, a slow leak) fires 15 to 25 min after the deploy, when nothing
  watches the release any more. A person watched dashboards and rolled back.
- **Before traffic.** On a reload, a hand-off successor accepts on the shared
  sockets the moment it reports ready (`readyGate`, ADR-1736). No step runs
  a request against the new process before customers reach it. On the
  control plane, one host, the canary is production.

GitHub offers merge queues only for repositories owned by an organization
(public ones, or private ones on Enterprise Cloud). poyrazK/faas is owned by
a user account.

## Decision

Four rails. Autodeploy stays off until all of them are on main and the
owner sets `UNIT_RELEASE_AUTODEPLOY_ENVIRONMENT`.

### 1. Merge queue: prepared, and its guarantee kept without one

- The light tier (`ci-light.yml`) also runs on `merge_group`, the event a
  queue sends, under the six check names the ruleset already requires. Its
  diff base is `merge_group.base_sha`: the queue's base, which already
  contains every PR ahead in the queue, so each entry is linted and tested
  for its own change on top of everything that merges before it. Checks
  that read pull-request metadata (fix-has-test, migration-version hygiene)
  skip on that event, as they do on pushes. A merge-group run is never
  cancelled: a cancelled check takes the PR out of the queue.
- The ruleset change is prepared in `docs/runbooks/MainMergeQueue.md`, not
  applied: a `merge_queue` rule with squash merges, groups that merge only
  when every entry passes (`ALLGREEN`), up to 5 entries built at once, up
  to 5 merged together, at least 1 after a 5 min wait, and a 60 min check
  timeout. The busiest day had 67 merges, about three an hour, and a light
  run takes 14 to 45 min on main, so five builds at once leave room for
  bursts. The timeout covers a hosted-runner queue on a busy day. Applying it needs the
  repository to move to an organization, which is the owner's decision. The
  ruleset's `strict_required_status_checks_policy` stays false. A queue does
  not need it.
- Until a queue is on, the main commit's own light run is checked twice. A
  deploy does not start while that run has already failed (§2). The watch
  rolls the release back if the run fails during the watch (§3). The deploy
  does not wait for the run: waiting would add 14 to 45 min to every
  deploy, and a third of main's runs never report.

### 2. Deploy admission: wait for a healthy, idle and unfrozen platform

A new first job in `release-units.yml`, `preflight`, runs before the
control-plane lane on the fleet runner. `scripts/ci/deploy_preflight.py` runs
these checks every 60 s, for up to 40 min. A round costs about six GitHub API
requests, and the workflow token's budget is 1,000 requests an hour per
repository:

| check | passes when | otherwise |
| --- | --- | --- |
| freeze | no open issue carries the `deploy-freeze` label | **hold:** the run ends green and deploys nothing |
| newest | no newer push run of release-units exists | **stand down:** the newer, cumulative run deploys |
| main | the commit's push run of ci-light has not failed | **fail:** nothing deploys until a later commit's run is not red |
| idle | no other run of cd-platform, cd-compute, cd-controlplane, cd-edge-units or cd-edge-node is queued or running; no other release-units run is in a deploy job; the control plane's deploy lock is free; the watch (§3) does not hold the maximum number of unwatched releases | wait |
| healthy | no page-severity alert fires on the control plane's Prometheus (`127.0.0.1:9095`), except the tenant-signal families the SLO gate also ignores; Prometheus answers | wait |

After 40 min of waiting the job fails, and the next merge tries again.
That bound covers a full watch window (§3) and one lane deploy. The job
writes each check's state, what it waited for and for how long to the step
summary, and a `::notice::` per wait. The run page answers "why did my
merge not deploy".

- **Page alerts only.** They are the alerts that wake someone: an incident
  is open, and a deploy would change the system under the person handling
  it. Warn alerts fire often. They would hold deploys for conditions no
  deploy changes, and the SLO gate already judges any that start after a
  deploy. Tenant-signal families (traffic anomaly, tenant abuse, security
  anomaly, residency) measure what tenants do. The SLO gate ignores them for
  the same reason: the first production unit release was rolled back on an
  end-to-end test's traffic spike.
- **The freeze switch is a GitHub issue with the `deploy-freeze` label.**
  A person freezes by opening one with a reason, or labelling one, and
  lifts the freeze by closing it. Only people with triage access or more
  can apply labels, so an outside contributor cannot freeze. A
  non-collaborator cannot close another user's issue, so they cannot lift
  one either. An automatic rollback opens the issue itself, with its report
  (§3). The issue carries the reason, the report and the discussion, and
  closing it leaves an audit trail. The workflow token can create issues
  but cannot write repository variables. The preflight reads open issues
  live, not at run start.
- **Overrides.** Dispatching cd-controlplane (`units_run_id`) or
  cd-edge-units directly is the repair path and skips the preflight. The
  dispatcher is the override and is recorded as the run's actor. Rollbacks
  are never frozen. Full releases (cd-platform) are started by a person and
  are unchanged.

### 3. Post-deploy watch with automatic rollback

After the lanes, `release-units.yml` runs a `watch` job (reusable
`cd-unit-watch.yml`, on the fleet runner). It keeps the SLO gate running for
**30 min from the run's last activation**, then promotes or rolls back.

**What it judges.** It runs `slo_gate.py` in watch mode for every unit
deployed and not yet watched, on every host. The rule selection is the
gate's: platform SLOs, plus the unit's own page and warn rules outside the
tenant-signal families. The thresholds stay in Prometheus. The difference
is the hold: each rule must stay true for its full `for:` (max hold 30 min)
instead of a 2 min cap, so the watch rolls back only on an alert that
actually fires. An instance already true before the first unwatched
activation is ignored, as in the gate. The watch also reads the main commit's
light-tier run (§1) for each unwatched release, and a failure counts as a
breach.

**Why 30 minutes.**

- The slowest platform SLO rules need their window plus their `for:` to
  fire on a step regression: about 25 min for
  `FaasPlatformWakeLatencySLOBreach` (15 min window, `for: 10m`) and about
  20 min for `FaasColdBootFallbackHigh` (5 min rate, `for: 15m`).
- After a reload, apid's and the gateways' old generation keeps requests
  for up to `api.HandoffDrainMaxSeconds` (30 min). Only after that does the
  new release serve every request, long ones included.
- Customer releases use the same window (`api.PostReleaseWatchWindow`,
  ADR-1445).
- Main's light run reports within 14 to 45 min (median 23), so most of its
  verdicts arrive inside the window.

A longer window would occupy one of the two fleet runners for longer. It
would also delay promotion to known-good, which bounds the next rollback.
Regressions that appear after 30 min page a person through the normal
alerts.

**Superseding instead of blocking.** A later run may deploy while a watch
runs. Otherwise every fix would queue for up to 30 min behind the previous
deploy's watch, at 24 to 67 merges a day. The control plane keeps a record
of what is unwatched, `/var/lib/faas/deploy/unit-watch.json` (root, 0600):

- the run that owns the watch;
- for each host changed since the last clean watch (the control plane, and
  each compute node with its SSH target), the **known-good** release, the
  base full release, the services changed since known-good, the gate units,
  and the first and last activation times.

Every unit deploy (control-plane lane, the edge lane on the control plane,
each edge node) updates the record just before it activates a host. It
keeps an existing host entry's known-good, adds its services, and takes
ownership. A host whose base full release changed since its entry was
written (a full release ran) starts a new entry.

- **Superseded.** The watch reads the record every round. When another run
  owns it, the watch stops and reports how long it watched cleanly. The
  newer run's watch covers both releases, from the first unwatched
  activation.
- **At most two unwatched releases per host.** At two, the preflight waits
  for the watch to close the record (the `idle` check). Retention keeps the
  three newest inactive releases besides the protected ones, so known-good
  stays on disk. The cap also limits how many merges one rollback undoes.
- **Pass.** The watch removes the record if it still owns it. Every host's
  active release is then known-good.
- **Breach.** For each host in the record, newest change first (followers,
  the control plane, the canary), the watch runs `deployctl rollback --only
  <services> --expect-current <active release> --to <known-good>` with the
  run's signed deployctl, then checks every unit as the lanes do. A host
  whose active release is not the one the record expects, or whose
  known-good is gone, is reported and left alone. Then it opens a
  `deploy-freeze` issue. The issue holds the alert, its labels and
  expression, each host's from and to releases, the commit range rolled
  back, and links to the runs. The watch removes the record and fails the
  run.
- **Inconclusive** (Prometheus blind for 4 rounds, as in the gate). The
  watch neither promotes nor rolls back. The record stays open for the next
  watch, and the run fails. The preflight's health check holds deploys
  while Prometheus does not answer.

**Every automatic rollback pauses autodeploy.** Unit releases accumulate
over their base, so the next merge's release contains the rolled-back
change again. The existing canary and follower gate rollbacks
(cd-controlplane `deploy-units`, cd-edge-node) therefore also open the
freeze issue. A person closes it after the cause is reverted or fixed.

### 4. Pre-traffic check inside the hand-off

When deployctl reloads apid or gatewayd-public (ADR-1736), the old
generation checks the successor before the successor accepts a connection
from the shared sockets:

1. The old generation creates one connected socket pair per check. It
   passes one end of each pair to the successor, with the address of the
   shared listener the check belongs to (`FAAS_HANDOFF_CHECK_FDS`), and a
   pipe the successor waits on.
2. The successor's ready gate on that listener returns the check's
   connection from `Accept` before the successor is ready. The request goes
   through the same `http.Server`, middleware and handlers that will serve
   customers. No other connection reaches the successor. A socket pair is
   not a listener, so there is no new address and no permission to manage.
3. When ready, the successor reports `checks` instead of `ready` and keeps
   holding its shared listeners. The old generation runs each check, with
   10 s each:
   - apid: `GET /readyz` on its control listener (`127.0.0.1:9101`)
     answers 200, and one unauthenticated API request on its public
     listener gets the 401 problem a request without credentials gets;
   - gatewayd-public on the control plane: `GET /readyz` on its control
     listener (`127.0.0.1:9092`) answers 200, and the same unauthenticated
     API request on its public listener, which it proxies to apid, gets
     apid's 401. A compute edge (`gatewayd-public-edge`) serves app hosts
     only and checks readyz only, until it gets a request of its own.

   Neither answer depends on tenant data. The API request passes through
   routing, authentication and, for the gateway, the hop to apid.
4. If every check passes, the old generation writes `go`. The successor
   opens its gates and reports `ready`, and the hand-off continues as today
   (`MAINPID=`, drain). If a check fails, the old generation stops the
   successor and reports `STATUS=hand-off failed at <time>: rejected by
   pre-traffic check <name>: <detail>`. The old generation keeps serving.

deployctl currently restarts a daemon whose hand-off failed. It now treats
a rejection as a failed activation: no restart onto the new binary. The
deploy rolls back while the old generation is still serving, so no customer
request reaches the rejected binary.

The checks belong to the old generation's code. A check is a contract the
serving release expects the next one to keep, and a candidate cannot weaken
its own test. So a new or changed check takes effect one release after it
ships. Compatibility, one release apart (N±1):

- An old generation without checks starts a new successor as today.
- A new old generation whose successor predates checks gets `ready`
  without `checks`. It proceeds and logs that the successor was not checked.
- An older deployctl reads the rejection's `hand-off failed at` prefix as a
  failed hand-off and restarts, as it does today.

A restart (not a reload) is not checked. Socket activation hands queued
connections straight to the new process. deployctl reloads these daemons
whenever their unit and binaries allow it. gatewayd-internal and realtimed
can define checks later.

## Consequences

- Merge to production stays about 20 min when no more than one release is
  unwatched. When the platform is healthy and idle, the preflight adds a
  runner pickup and one round of checks.
- A watch holds one of the two fleet runners (both on gregale-cp) for
  30 min per run. A waiting preflight can hold the other. cd-platform and
  pki-renew queue behind them meanwhile. The watch adds one Prometheus rules
  request per unit every 15 s on the control plane, as the gate does.
- A rollback undoes everything deployed since the last clean watch: at most
  two releases per host, possibly several merges. Each automatic rollback
  pauses autodeploy until a person closes the freeze issue. A person, not
  the pipeline, decides what to re-ship.
- Main is still not guaranteed green without a merge queue. The guarantee
  is that production does not keep a tree whose own post-merge light run
  failed, or whose platform SLOs regressed within 30 min.
- The watch record is new host state. It lives beside the host-config
  record (ADR 20261018133100) and is only an index of releases deployctl
  already retains. Deleting it loses the known-good point: the next deploy's
  watch then rolls back only to the release it replaced.

## Implementation

Small PRs, in this order, each with tests:

1. This ADR.
2. `ci-light.yml` on `merge_group` with one diff-base script for every
   event; the prepared ruleset and runbook.
3. `deploy_preflight.py` and the `preflight` job; the `deploy-freeze`
   label and issue helpers.
4. The watch record, `slo_gate.py` watch mode, `cd-unit-watch.yml`, the
   automatic freeze on every automatic rollback.
5. Pre-traffic checks in `pkg/wire`/`pkg/daemonunit`, the apid and
   gatewayd-public checks, and the rejection in deployctl.

## Rejected alternatives

- **A watch that holds the deploy lane.** It is simpler: the previous
  release is always known-good. But at 24 to 67 merges a day, a fix would
  wait up to 30 min behind every deploy's watch.
- **Deploying only commits whose post-merge light run passed.** It adds
  14 to 45 min to every deploy. A third of main's runs are replaced before
  they start and never report.
- **Requiring up-to-date branches (`strict_required_status_checks_policy`).**
  With dozens of merges a day and 15 to 45 min light runs, every merge
  makes every other ready PR stale and restarts its checks.
- **A repository variable as the freeze switch.** The workflow token cannot
  set one, so an automatic rollback could not pause autodeploy. `vars` is
  read when a run starts.
- **A file on the control plane as the freeze switch.** Lifting it needs
  root SSH, and nobody sees it on GitHub.
- **Running the watch on the control plane as a systemd unit.** It would
  hold no runner. But the control plane has no SSH key for compute nodes,
  which edge-lane rollbacks need, and no token to report on GitHub.
- **A probe listener per generation for the pre-traffic check.** It adds
  a listening address per daemon and generation, with ports or socket
  permissions to manage. A socket pair reaches only the successor.
- **Checking the successor after `MAINPID=`.** By then it takes traffic.
