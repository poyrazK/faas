# JobTaskOOMStorm

Source: ADR-099 PR-C/D deploy (jobs cluster).
Metric: `jobs_task_oom_kill_total{job_id=…,account_id=…}`
(schedd `metrics`).
Spec: §4.7.4; per-task RAM is enforced by the cgroup
`memory.max = job.RAMMB + 8 MB` (ADR-006 §4); same shape as
the wake path, the OOM kill originates in the cgroup, not the
guest kernel.
Severity: page after 50 sustained OOM kills across a 5-min
window for a single job_id — the customer's image is broken
and every subsequent run will repeat the failure.

## Symptom

`jobs_task_oom_kill_total` counter is climbing at > 10/min for
a single `job_id`. Each kill cancels the task but NOT the run
(unless `aggregate_status` transitions through `tasks_failed >
threshold`); the next task picks up the same image and OOMs
again. The customer's `$0.05/task × 100 tasks/run × 5 runs/day`
is hitting their overage cap while nothing useful completes.

Common root causes:

1. **Image memory bug**: the user's container has an unbounded
   cache (e.g. a DuckDB-in-process dataset that grows each
   invocation). Profiling would show memory rising per call.
2. **Cold-boot baseline too high**: the `runner-*` base image
   already uses X MB on boot, plus the job binary uses Y MB →
   X+Y > the plan's `JobMaxRAMMB`. The fix is `gregale jobs
   update --ram N`, not a re-deploy.
3. **Concurrency regression**: jobs `--parallelism 4` ×
   RAM-bound tasks lands the customer's account over the
   `JobMaxConcurrentPerAccount` cap; the rate limiter holds
   the rest as queued, and each `concurrency=4` parallel slice
   is in its own cgroup — OOM per task. The fix is `--parallelism 1`.

## Verify

```bash
# 1. Sustained OOM counter.
curl -fsS http://127.0.0.1:9103/metrics | grep jobs_task_oom_kill_total
# 2. Per-task error_class distribution for the job.
psql -At -c "SELECT error_class, count(*) FROM jobs.job_tasks WHERE job_id='<job>' GROUP BY 1 ORDER BY 2 DESC;"
# 3. The scheduled RAM vs the cgroup max (sanity).
psql -At -c "SELECT name, ram_mb FROM jobs.jobs WHERE id='<job>';"
# 4. The actual cgroup max the OOM killer enforced — per-task VM.
journalctl -u vmmd --since '-15m' --no-pager | grep -iE 'oom|memory.max' | head -20
```

If (2) shows `error_class='oom'` on > 80% of rows for the
offending `run_id`, the customer's image is guaranteed to be
OOMing. Move to **Mitigate**.

## Silence + customer escalation

```bash
amtool silence add \
  --matchers='alertname=JobTaskOOMStorm,job_id=<job>' \
  --duration=4h \
  --comment "Customer image OOMing; temporary silence for mitigation window"
```

## Mitigate

```bash
# 1. Cancel the in-flight run (returns 202).
FAAS_API=http://127.0.0.1:9101 gregale jobs cancel <run-id>
# 2. Bump the per-task RAM (plan cap or below).
FAAS_API=http://127.0.0.1:9101 gregale jobs update <job-id> \
  --ram 1024
# 3. Drop parallelism if the customer's pattern is concurrency-bound.
FAAS_API=http://127.0.0.1:9101 gregale jobs update <job-id> \
  --parallelism 1
# 4. Audit per-task exit codes.
psql -At -c "SELECT task_index, status, error_class, error_message FROM jobs.job_tasks WHERE run_id='<run-id>' ORDER BY task_index;"
```

Steps 2 and 3 trigger a job-changed notification (the same path
`crons` use); the customer can ALSO call `gregale jobs update`
from their workstation — the ops flow above is the same surface,
just an operator key.

**Do NOT** bump RAM past the plan's `JobMaxRAMMB` to mask an
image memory bug — the customer will hit their monthly cap and
file a billing dispute. Escalate to the customer side first:

```
Subject: Your job '<name>' is OOMing per task

We've observed <N> consecutive `oom` errors per task for your job
<name> (job_id=<uuid>). Each kill cancels a task; every retry
repeats. This is consuming <cents>/h against your monthly cap.

Recommended next steps (in order):
  1. Profile your image with `--ram 256 --timeout 30` to confirm
     the cold-boot baseline.
  2. Bump `--ram` if the baseline is healthy but a steady-state
     cache exceeds current RAM. We can support up to <plan>
     JobMaxRAMMB = <MB>.
  3. Open an issue against this runbook if your image is < MB
     under the cap and still OOMing — that's a cgroup/policy
     regression and we'd want a reproducer.
```
