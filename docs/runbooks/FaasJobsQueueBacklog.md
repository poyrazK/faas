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

# 2. If node RAM ceiling is the cause, list the heaviest accounts:
psql -U faas -d faas -c "
  SELECT account_id, COUNT(*) AS live, SUM(ram_mb) AS mb
  FROM instances
  WHERE kind='job_task' AND status IN ('claimed','running')
  GROUP BY 1 ORDER BY mb DESC LIMIT 10;"

# 3. Cancel the heaviest account's runs via the apid API (must be the
#    customer's auth — operators do NOT have a backdoor):
curl -fsS -X POST -H "Authorization: Bearer $CUSTOMER_TOKEN" \
  https://api.faas.example/v1/jobs/$JOB_NAME/runs/$RUN_ID/cancel
```

## Related

- `FaasBuildQueueBacklog.md` — same shape for the builder slot
  pool; different cap (1 guaranteed + 1 opportunistic).
- `FaasColdBootRatioHigh.md` — jobs are always cold-boot-only
  per ADR-005; a high cold-boot ratio alongside a queue backlog
  is expected, not a separate incident.
