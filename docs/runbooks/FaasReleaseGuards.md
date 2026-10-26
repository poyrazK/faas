# FaasReleaseGuards

The release guards hold production releases back:

- deploy freezes (ADR-20261019071733);
- release approvals (ADR-20261020085706);
- scheduled releases (ADR-20261021152201);
- rollbacks that restore the database (ADR-20261023181931);
- the post-release watch (ADR-1445).

apid runs the scheduled-release, database-switch and post-release-watch
workers. meterd delivers approval and schedule notices from the
`release_approval_events` outbox. All of them export `pkg/releasemetrics`
series:

| Series | Meaning |
|---|---|
| `faas_release_worker_last_pass_timestamp_seconds{worker}` | Unix time the worker last read its queue. A worker that never ran on an instance exports nothing. |
| `faas_release_worker_errors_total{worker}` | Failed passes or items. Each one is retried on the next pass. |
| `faas_release_guard_decisions_total{guard,outcome}` | What each guard decided. `pkg/releasemetrics` lists the outcomes. |

Workers are `scheduled_releases`, `database_switches`, `post_release_watch`
(apid) and `approval_notices` (meterd).

## Alerts

All alerts are `family: release_guards` and `public_status: internal`.

### FaasReleaseWorkerStalled (warn)

An apid release worker has not read its queue for 5 minutes. Effects by
worker:

- `scheduled_releases`: due schedules do not start.
- `database_switches`: switches do not advance. An app inside its fence
  stays paused.
- `post_release_watch`: completed releases are not judged, so a regression
  is not rolled back.

What to do:

1. Check apid's health and its Postgres connections. Every worker reads
   through the shared store.
2. Read the worker's warnings in the apid journal:
   - `scheduled releases:` for the scheduled-release worker;
   - `database switch:` for the database-switch worker;
   - `post-release watch` for the post-release watch.
3. A break-glass release pause still counts as a pass for scheduled releases,
   so the pause alone never fires this alert.
4. A restarted apid resumes every worker from durable state. Schedules,
   switches and watched releases need no repair.

### FaasReleaseApprovalNoticesStalled (warn)

meterd's alert evaluator has not read the approval outbox for 10 minutes.
Releases still wait for approval and time out on schedule. Approvers just
are not told.

What to do:

1. Check meterd's alert evaluator. Its tick also delivers alert rule fires.
2. Approvers can list waiting releases with
   `gregale approvals pending --app <app>`.

### FaasReleaseWorkerErrors (warn)

An apid worker recorded more than 5 failures in 15 minutes. Read its
warnings in the apid journal. Common causes:

- a read timeout;
- a release that changed under the worker (decided again on the next pass);
- a traffic change the gateway refused.

For `database_switches`, every step that ends in a retry counts as a
failure. A switch that keeps retrying while its app is fenced holds the
app's requests. List the app's switches with `gregale rollback switches <app>`
and read the last error code. After repeated errors the switch aborts by
itself.

### FaasDatabaseSwitchAborting (warn)

A rollback with `--restore-databases` hit an error it could not retry past,
before its activation committed. It is undoing itself:

- it puts the previous release back if it had switched in;
- it cancels the cutover, which revokes the staged credentials and lifts the
  fence and the pins;
- it keeps the restored copy.

What to do: follow it with
`gregale rollback switch <app> <switch-id> --wait` until it ends. The audit
event `app.database_switch_aborting` names the app and the failure code.
If the activation committed first, the switch finishes as completed
instead: the bindings already pointed at the copy.

### FaasDatabaseSwitchFailed (warn)

A database switch gave up and finished undoing itself. The app serves the
release it had before, on its source database, and is no longer fenced.
Nothing is left half-done.

What to do:

1. Find the switch from the `app.database_switch_failed` audit event. Its
   payload has the app, switch ID, phase and failure code. Show it with
   `gregale rollback switch <app> <switch-id>`.
2. Fix the cause the failure code names (restore, verification, drain or
   activation), then let the customer retry the rollback.
3. The restored copy is kept and billed as a database. Delete it once the
   customer no longer needs it.

### FaasReleaseApprovalNoticeDropped (warn)

An approval or schedule notice failed on every channel for all
`api.ReleaseApprovalNotifyAttemptsMax` attempts. The release it announced
still waits for approval or for its schedule.

What to do:

1. Read the failure in `release_approval_events.last_error` for that
   deployment (read-only query).
2. Fix the channel: Slack token, email domain, or channel deleted.
3. Tell the approvers by hand. New events deliver normally once the channel
   works.

## Guard decisions worth watching

These outcomes are not alerts. Dashboards and incident review use them.

- `freeze/refused`, `freeze/overridden`: releases a freeze held, and audited
  overrides. A burst of overrides during a freeze is a change-control
  question for the account, not a platform fault.
- `approval/canary_required`: deploys refused because the app requires
  approvals and the release had no canary. Pipelines that use
  `--rollout immediate` fail this way after a policy is set.
- `post_release/kept`: the watch saw a regression but kept the release
  serving because rolling back would cross release commands (ADR-1448). A
  person decides.
