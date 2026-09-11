# FaasJobsQueueBacklog

Source: Mega-1 §12 (issue #1184 Workstream A / Mega-1).
Metric: `jobs_queue_depth{plan}` (schedd `/metrics`).
Spec: ADR-099 supplement (`docs/adr/099-supplement-jobs-mega1.md`),
§3 — JobDispatch bucket separate from wakeBuckets so jobs can't
starve app wakes.
Severity: warn (no page tier per ADR-099; job runs are
eventually-consistent, not request/response).

## Symptom

`jobs_queue_depth{plan="Hobby"}` is rising over a 10-minute window
while `jobs_dispatch_total{plan}` plateaus. Customers see tasks
sitting in `status='queued'` longer than the plan's parallelism
cap would predict.

## Verify

```bash
curl -fsS http://127.0.0.1:9103/metrics | grep -E 'jobs_(queue_depth|dispatch_total|dispatch_rejected_total)'
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=jobs_queue_depth'
```

Per-plan breakdown matters: a Hobby backlog with empty Pro/Scale
queues means Hobby's `JobMaxParallelismPerRun=10` is exhausted
across Hobby accounts; not a cluster-wide admission problem.

## Check

```bash
systemctl status schedd
journalctl -u schedd --since '-15m' --no-pager | grep -iE 'job|admit|reject|kind=job_task'
cat /sys/fs/cgroup/faas-tenant.slice/memory.current
# vs the 47,600 MB RAMAdmissionCeiling (CLAUDE.md hard limits)
```

The two common root causes:

1. **Per-account concurrent cap exhausted.** Hobby allows
   `JobConcurrentPerAccount=3`. If 3 Hobby accounts are
   concurrently at 3/3, the 4th account's tasks queue. Operator
   action: none — the cap is the intended fair-share
   semantics. Investigate whether the customers involved are
   scaling up to Pro (JobConcurrentPerAccount=8).
2. **Node RAM ceiling.** `Σ(ram_mb + 8)` over live job
   instances approaches 47,600 MB and admission rejects new
   dispatches (`jobs_dispatch_rejected_total{reason="ram"}`).
   This is the load-bearing risk identified in
   ADR-099-supplement deviation 3. Operator action: drain
   long-running jobs (the reaper at `pkg/sched/reaper_jobs.go`
   enforces `task_timeout_s`); the cap will release within one
   job timeout window.

## Silence

```bash
amtool silence add --alert=FaasJobsQueueBacklog --duration=2h --comment="investigating"
```

After the silence, page the on-call for one of the following:

- `jobs_dispatch_rejected_total{reason="plan"}` rising on a
  single plan — quota misconfiguration in `pkg/api/limits.go`.
- `jobs_dispatch_rejected_total{reason="lease"}` rising —
  lease primitive is leaking (ADR-099 deviation 5 surface).
  Roll back the schedd deploy, file an incident.

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
  pool; different cap (1 guaranteed + 1 opportunistic).
- `FaasColdBootRatioHigh.md` — jobs are always cold-boot-only
  per ADR-005; a high cold-boot ratio alongside a queue backlog
  is expected, not a separate incident.
