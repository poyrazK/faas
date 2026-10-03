# ADR-431 · Scheduled workflow starts

- **Status:** accepted; workflows remain preview and require the runtime gate
- **Date:** 2026-10-02
- **Extends:** [ADR-081](081-durable-execution-workflows.md)
- **Decision:** Allow a deployed workflow to declare a five-field cron schedule,
  IANA timezone, fixed JSON input, overlap policy, and enabled flag. `schedd`
  admits scheduled runs directly into the existing workflow ledger. Customers
  no longer need an application cron handler that calls the workflow-start API.
- **Why:** Scheduling is a workflow start condition. Application crons currently
  own HTTP or command invocations and their delivery lifecycle; introducing
  workflow runs there would couple two execution ledgers. Reuse `cronexpr` for
  validation and calendar behavior while keeping execution with the workflow
  owner.
- **Consequences:** Manual starts remain supported. Each admitted run snapshots
  the definition and fixed input and executes through the existing orchestrator,
  retry, wait, attempt-history, and retention paths. Step delivery remains at
  least once; admission deduplication does not make external effects exactly once.
- **Rejected alternatives:** A cron-to-API adapter retains customer credentials
  and an extra application wake. An in-memory schedule cursor duplicates starts
  after process restart. Replaying all missed occurrences creates an uncontrolled
  backlog after outages.

## Admission and recovery

`workflow_schedule_cursors` stores one high-water mark per app/workflow name,
the deployment and trigger snapshot, and the latest outcome. Run insertion and
cursor advancement commit in one transaction under the same per-app advisory
lock as manual admission. Active `pending`, `running`, and `awaiting_event` runs
share the existing plan concurrent-run limit. By default an active run with the
same workflow name, including a manual run, consumes the scheduled occurrence
as `skipped_overlap`; `overlap: allow` still enforces the app-wide quota.
Quota refusal consumes the occurrence as `skipped_quota`. Neither outcome is
automatically retried during that minute or replayed later.

A new deployment or changed trigger first arms at its observation time. It can
start at the next eligible minute strictly after that observation. The first
observation never starts a run. Only the current nominal minute is considered;
missed minutes are discarded. A clock rollback cannot replay a consumed minute.
The cursor survives workflow-run retention, with its deleted run reference set
to null. Retired names are pruned when the app's scheduled definitions are next
evaluated. An app without any schedules retains its last cursor set until a
later scheduled deployment or app deletion; this is bounded by the largest
previously deployed workflow set rather than the number of elapsed occurrences.

Discovery uses bounded pages of eligible apps owned by the scheduler node.
Schedule evaluation has its own bounded worker slot so long handler executions
cannot occupy its capacity. Errors are retried within the minute without
stopping evaluation of other apps; independent scheduler processes rely on
database admission for duplicate suppression. Nominal fire times are calendar
times, not execution-latency guarantees.

## Deployment and availability

Only the preferred live deployment in the `default` scope creates scheduled
starts; previews never create background copies. Selection follows the existing
traffic-bearing deployment preference. Account suspension, abuse holds,
maintenance mode, Free-plan downgrade, and tenant-required apps block new starts.
The first slice supplies no platform-tenant identity, so it cannot bypass that
gateway admission requirement. Disabling or redeploying does not cancel existing
runs. Workflow definitions and run input are snapshotted; this does not add
deployment-pinned handler code to the existing workflow executor.

Scheduling requires the existing `FAAS_WORKFLOWS_ENABLED=1` configuration on
both `apid` and `schedd` and a configured workflow executor. Apply the additive
migration before deploying the scheduler. Turning off the scheduler gate stops
new starts and existing workflow dispatch; disabling just one trigger stops only
that schedule's new starts. The schedule inspection API reports apid's runtime
configuration, not a scheduler heartbeat; operators must keep the gates aligned.

Upgrade the workflow dispatchers before accepting schedule declarations or
enabling scheduled starts. Older workflow validators reject `type: schedule`
in a run's definition snapshot. Before rolling back to that older runtime,
pause new schedules and drain or cancel those runs; keep the additive cursor
table during rollback so its admission history is preserved.

## Customer surface and evidence

YAML/JSON manifests accept `trigger.type: schedule`; omitted/manual triggers
retain their previous behavior. Schedule declarations count toward existing
workflow definition limits, not the separate application-cron quota. Fixed input
has the ordinary workflow input limit. `GET /v1/apps/{slug}/workflows/schedules`
and `gregale workflows schedules --app SLUG` expose the current configuration,
next nominal fire time when available, and latest admission outcome. Existing
run/step/attempt endpoints provide execution history. This is a latest-outcome
cursor, not a full history of skipped occurrences.

Qualification covers real PostgreSQL concurrent admissions, contention with
manual quota admission, transaction rollback, deployment changes, account/app
guards, retention-independent deduplication, and scheduling through the actual
orchestrator with a fake executor. Native gateway/VM execution and operational
rollout remain required before customer promotion. Event-start bindings, visual
editing, connector credentials, and cross-app workflows are separate work.
