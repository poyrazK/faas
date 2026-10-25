# Jobs and workflows

Operational owner: the scheduler owner (schedd: the workflow orchestrator
`pkg/sched/dispatch_workflows.go`, code-first workflows
`pkg/sched/workflow_code.go` and the jobs dispatcher `pkg/sched/jobs.go`),
with apid for the customer API. ADRs:
[ADR-081](../adr/081-durable-execution-workflows.md) (workflows),
[ADR-099](../adr/099-jobs.md) and its
[supplement](../adr/099-supplement-jobs-mega1.md) (jobs),
[code-first workflows](../adr/20261021204307-code-first-workflows.md),
[event plan caps](../adr/20261023215834-event-plan-caps.md) (parked runs) and
[capability flags](../adr/20261023115741-every-capability-behind-a-platform-flag.md).
Catalog: `workflows-and-jobs` (`beta`), with the sub-capabilities `workflows`
and `jobs`.

## How work runs

- **Workflows.** apid writes `workflow_runs`; schedd claims due runs on
  `api.WorkflowDispatchSlots` (4) off-loop slots per schedd, up to
  `api.WorkflowDispatchBatchPerSlot` (8) runs per slot turn, with at most
  `api.WorkflowDispatchMaxPerApp` (2) runs of one app and
  `api.WorkflowDispatchMaxPerTenant` (1) per platform tenant in flight. A slot
  makes each step call synchronously, for up to the plan's step timeout, so
  slow steps hold slots. Dispatch needs `FAAS_WORKFLOWS_ENABLED=1` on schedd
  and apid.
- **Jobs.** schedd's dispatch tick runs once a second, takes up to 32 ready
  tasks and starts each one in its own job VM, subject to the account's
  `JobConcurrentPerAccount` and the node admission ceiling.

## Release and rollback

The `workflows_and_jobs` platform flag gates both; `workflows` and `jobs` gate
each one ([platform flags](../ops/platform-flags.md)). The flags are beta, so
they are on for every entitled account unless an operator writes them.

- **Stop one capability for everyone:**
  `gregalectl flags disable --key workflows --reason <incident> --yes` (or
  `--key jobs`). Within seconds apid answers the routes with `404`, and schedd
  defers held runs and tasks a minute at a time without spending a delivery
  attempt. Nothing is failed or dropped: `gregalectl flags enable` resumes the
  backlog. Cancel the work as well if the capability stays off.
- **Stop one account:**
  `gregalectl flags deny --key workflows --account-id <uuid>` (or `jobs`).
  Use it when one account's long steps or jobs saturate dispatch for others.
- **Stop workflow dispatch on a host:** unset `FAAS_WORKFLOWS_ENABLED` on
  schedd and restart it. Runs stay `pending` and are claimed again once it is
  set back. Prefer the flag; this also hides the API on apid when unset
  there.
- **Code rollback:** schedd and apid release independently
  ([release compatibility](../release-compatibility.md)); roll back the unit
  that introduced the regression. Durable rows (`workflow_runs`,
  `workflow_steps`, `job_runs`, `job_tasks`) are forward-compatible within
  N-1, and a claimed run whose executor disappears is recovered after its
  lease (timeout + 5 minutes).

## FaasWorkflowDispatchSaturated

More than 90% of `schedd_loop_work_total{kind="workflow_dispatch"}`
submissions on one schedd found their slot still busy (`outcome="coalesced"`)
for 10 minutes. Due runs, including other customers' runs, wait until a slot
frees.

1. Find the steps holding slots:
   `topk(5, sum by (app) (rate(workflow_step_duration_seconds_sum[10m])))`.
   Long sums on one or two apps mean their steps are slow, not that the fleet
   is busy.
2. Check `FaasWorkflowDispatchLagHigh`: if it is quiet, the slots are busy but
   due runs still start on time, and no action is needed.
3. If one account dominates, deny the `workflows` flag for it (above) and
   tell the customer; its runs resume when the flag is allowed again.
4. If load is spread across many apps, dispatch capacity is short for the
   fleet: raise it through a reviewed change to `api.WorkflowDispatchSlots`
   (Hunt 13 H13-9), not by restarting schedd.

## FaasWorkflowDispatchLagHigh

The p95 of `workflow_dispatch_lag_seconds`, the delay between a run's
`scheduled_for` and schedd claiming it, has been over 60 seconds for 10
minutes, across at least five claims. Sleeps wake late, received events
resume runs late and step retries run late. Runs recovered from an expired
lease are not counted.

1. If `FaasWorkflowDispatchSaturated` fires too, follow it.
2. Otherwise check schedd health: `FaasScheddLoopWorkDropped`,
   `FaasDBPoolStarved` and the schedd journal for
   `workflow orchestrator: claim pending failed`. A failing claim query
   delays every run.
3. Runs over their app or tenant in-flight cap wait by design; a single app
   with thousands of due runs shows lag only for that app's runs. Compare
   with `sum by (app) (rate(workflow_runs_total[10m]))`.

## FaasJobsQueueBacklog

See [FaasJobsQueueBacklog](FaasJobsQueueBacklog.md).

## FaasJobInstanceCleanupFailed

See [FaasJobInstanceCleanupFailed](FaasJobInstanceCleanupFailed.md).
