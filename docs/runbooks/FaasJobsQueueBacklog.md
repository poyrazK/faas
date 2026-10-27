# FaasJobsQueueBacklog

Alert: `FaasJobsQueueBacklog` (warn, `family: job_dispatch`).
Metric: `schedd_job_dispatch_total{result}` (schedd `/metrics`), one count per
ready job task the once-a-second dispatch tick decided on.
Spec: ADR-099 supplement (`docs/adr/099-supplement-jobs-mega1.md`),
§3 — JobDispatch bucket separate from wakeBuckets so jobs can't
starve app wakes.
Severity: warn (no page tier per ADR-099; job runs are
eventually-consistent, not request/response).
Capability runbook: [FaasWorkflowsAndJobs](FaasWorkflowsAndJobs.md).

## Symptom

For 15 minutes schedd kept failing to start ready job tasks with
`result="node_capacity"`, `"compute_unavailable"` or `"wake_error"`.
Customers see tasks sitting in `status='queued'` with no started attempt.

These results are customer limits and never fire the alert:
`account_capacity` (`JobConcurrentPerAccount`), `quota`, `capability`
(the `jobs` platform flag is off for the account), `spend_cap`, `budget`
and `account_inactive`. `not_ready` counts benign races (another schedd
claimed the task first, a retry backoff or a flexible start window).

## Verify

```bash
curl -fsS http://127.0.0.1:9103/metrics | grep -E 'schedd_job_dispatch_total'
curl -fsS 'http://127.0.0.1:9095/api/v1/query' \
  --data-urlencode 'query=sum by (instance, result) (rate(schedd_job_dispatch_total[5m]))'
```

## Check

```bash
systemctl status schedd
journalctl -u schedd --since '-15m' --no-pager | grep -E 'job dispatch failed|dispatchJobsTick'
cat /sys/fs/cgroup/faas-tenant.slice/memory.current
# vs the 47,600 MB RAMAdmissionCeiling (CLAUDE.md hard limits)
```

By result:

1. **`node_capacity`.** `Σ(ram_mb + 8)` over live instances reached the
   admission ceiling, or no compute node is admitting. This is the
   load-bearing risk identified in ADR-099-supplement deviation 3. Operator
   action: add capacity through the reviewed node-join path, or drain
   long-running jobs (the reaper at `pkg/sched/reaper_jobs.go` enforces
   `task_timeout_s`); capacity returns within one job timeout window.
2. **`compute_unavailable`.** The financial compute authority could not
   decide whether the account may spend. Check the schedd journal for
   `financial` errors and Postgres reachability; tasks retry once a minute
   and start when it answers.
3. **`wake_error`.** Anything else that stopped a ready task before it was
   claimed: placement, lease, instance creation, sealed environment or
   database errors. The schedd journal line `sched: job dispatch failed`
   carries the run, task and error. One task failing forever usually means
   one broken run; cancel it (Recovery step 4) and file a bug.

## Silence

```bash
amtool silence add --alert=FaasJobsQueueBacklog --duration=2h --comment="investigating"
```

## Recovery

```bash
# 1. Drain stuck tasks (reaper picks them up next sweep)
journalctl -u schedd --since '-5m' --no-pager | grep reapStuckJobTasks

# 2. If node RAM ceiling is the cause, locate the largest active runs.
#    ram_mb * tasks_running is the per-run live-memory estimate.
gregalectl jobs active --json |
  jq '.runs | sort_by(.ram_mb * .tasks_running) | reverse | .[:10]'

# 3. Inspect the selected run's task states without reading task leases.
gregalectl jobs inspect --run-id "$RUN_ID" --task-limit 100

# 4. Cancel a stuck/capacity-dominating run through the strict operator API.
gregalectl auth step-up
gregalectl jobs cancel --run-id "$RUN_ID" --reason jobs_queue_incident --yes
```

The cancel command uses an idempotency key, requires explicit confirmation,
emits `operator.action.cancel_job_run`, and prints a trace ID. It never opens
PostgreSQL and never requires a customer token. Do not cancel healthy work only
because a plan's intended concurrency cap is full.

## Related

- `FaasBuildQueueBacklog.md` — same shape for the builder slot
  pool; different cap (one builder per compute node).
- `FaasColdBootRatioHigh.md` — jobs are always cold-boot-only
  per ADR-005; a high cold-boot ratio alongside a queue backlog
  is expected, not a separate incident.
