# JobsBacklog

Source: ADR-099 PR-C/D deploy (jobs cluster).
Metric: `jobs_pending_tasks{account_id=…}` (schedd `metrics`),
`jobs_run_age_seconds_bucket{run_id=…}`.
Spec: §4.7.4 (jobs — run-to-completion); `runJobsTick` defaults
to a 1 s dispatch interval (`FAAS_JOBS_TICK_INTERVAL` env).
Severity: warn at p50 > 30 s, page at p95 > 5 m for any single
account (job tasks are user-visible — a customer waiting > 5 min
for a queued task will file a support ticket).

## Symptom

The `jobs_run_age_seconds` p95 has crossed 5 min — meaning at
least 5% of task VMs that schedd claimed are older than 5 min
without a terminal `job_exit` vsock message (port 1026,
msg_type 4). Most common causes in priority order:

1. **Task VM is wedged post-boot**: the `guest-init` supervisor
   ran, but the user's job image is hung (infinite loop, deadlock
   on a synchronous RPC, missing binary). Vsock is open, but the
   supervisor never exits, so `job_exit` never lands. The watchdog
   (ADR-099 §Decision 9) kills the task at `task_timeout_s + 30s`
   and stamps `error_class='deadline_exceeded'`.
2. **Vsock message lost in flight**: rare, but the
   `pkg/fcvm/vmm.go::WaitJobExit` decode can return EOF when
   the vmmd side truncated the connection (vmmd OOM under load).
   The schedd engine stamps the task `error_class='vm_disconnect'`.
3. **Fan-in counters don't reconcile**:
   `jobs_run_age_seconds` is bounded by the watchdog + the
   `deadline_exceeded` check, but the `aggregate_status` row
   transitions to `'cancelled'` only after every task row has
   reached a terminal state. If a task is stuck in `'claimed'`
   (claimed by schedd but no instance_id recorded), the run
   stays in `'running'` even though every other task is
   terminal — the run fan-in is asymmetric.

## Verify

```bash
# 1. Per-account backlog depth.
curl -fsS http://127.0.0.1:9103/metrics | grep jobs_pending_tasks
# 2. Per-run age distribution (label cardinality: bounded by
#    JobMaxTasksPerRun = 5000 Scale, but typically << 100).
curl -fsS 'http://127.0.0.1:9090/api/v1/query?query=histogram_quantile(0.95,sum(rate(jobs_run_age_seconds_bucket[5m]))by(account_id))'
# 3. Stuck task rows (claimed > 1 minute, no instance_id yet).
psql -At -c "SELECT run_id, count(*) FROM jobs.job_tasks WHERE status='claimed' AND claimed_at < now() - interval '1 minute' GROUP BY 1 ORDER BY 2 DESC LIMIT 10;"
```

## Check

```bash
# 1. Watchdog timer — verify the deadline_stamp is firing.
curl -fsS http://127.0.0.1:9103/metrics | grep jobs_watchdog_kill_total
# 2. vmmd health — any disconnect storm surfaces as vmmd OOM.
systemctl status vmmd
journalctl -u vmmd --since '-15m' --no-pager | grep -iE 'oom|disconnect|vsock|epmd'
# 3. schedd dispatch tick latency.
journalctl -u schedd --since '-15m' --no-pager | grep -iE 'runJobsTick|claimed|deadline'
# 4. Compute p95 fan-in lag.
psql -At -c "SELECT EXTRACT(EPOCH FROM (now() - max(finished_at))) FROM jobs.job_run_fanin WHERE finished_at IS NOT NULL;"
```

If (1) shows `jobs_watchdog_kill_total > 0`, the customer's image
is unhealthy — the watchdog DID fire; the customer needs to
investigate their container. The fix here is operator silence +
escalation, not operator intervention.

## Silence

```bash
amtool silence add \
  --matchers='alertname=JobsBacklog,account_id=<acct>' \
  --duration=1h \
  --comment "Hobby job-run backlog — operator investigating image"
```

## Mitigate (when customer can't ship a fix)

```bash
# 1. Cancel the stuck run (returns 202 per PR-D).
FAAS_API=http://127.0.0.1:9101 gregale jobs cancel <run-id>
# 2. Drop the job template (soft-delete; row preserved).
FAAS_API=http://127.0.0.1:9101 gregale jobs rm <job-id>
# 3. Force-reap the underlying instances (kind='job' is filtered
#    by the reaper; this is operator-only and AUDITED).
psql -c "UPDATE instances SET status='reaped' WHERE id IN (SELECT instance_id FROM jobs.job_tasks WHERE run_id='<run-id>');"
```

The third step is a last resort — every row the operator updates
emits an audit-event row that surfaces in `/v1/audit-events` for
the account. The customer should normally iterate on their image
+ redeploy.
