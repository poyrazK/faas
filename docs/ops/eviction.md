# RAM-pressure eviction

This runbook covers the schedd reaper when a node crosses the RAM-pressure
threshold. The ledger remains the source of truth: the reaper selects a
deterministic victim, and `Engine.Evict` releases the instance through the
normal lifecycle before the next wake is admitted.

## Policy

- Admission targets 47,600 MB per node. RAM-pressure eviction starts above
  38,080 MB (80% of that target).
- Only `RUNNING` instances are candidates. Parked, stopped, waking, and
  cold-booting rows are never selected by this selector; service replicas are
  also excluded so desired-count reconciliation does not flap them.
- An instance younger than 30 seconds is protected. This gives a newly woken
  instance time to serve its first request before pressure can select it.
- Candidates are ordered by eviction priority first: `best_effort` before
  `reserved`. Within each priority, non-Scale plans precede Scale, then the
  oldest `LastRequest` wins, with instance ID as the deterministic tie-breaker.
  Thus Scale is evicted last within a priority tier, and a reserved instance is
  only selected after all eligible best-effort candidates.
- The `AdmissionControl`/`NodeLedger` decision is the tie-breaker after an
  eviction: every subsequent wake must pass the per-node RAM, vCPU, and
  per-app limits. Do not delete database rows or bypass the ledger to create
  headroom.

## Triage

1. Silence the alert while investigating:

   ```bash
   amtool silence add \
     --matchers='alertname=~"FaasHighResidentRam.*"' \
     --duration=30m \
     --comment='investigating tenant eviction'
   ```

2. Check the node pressure and eviction outcomes in Prometheus:

   ```promql
   max(fcvm_resident_ram_pct)
   sum by (tenant_tier) (rate(schedd_eviction_fired_total{reason="ram_pressure"}[5m]))
   rate(schedd_eviction_fired_total{reason="eviction_aggressive"}[5m])
   ```

   If the gateway rejection series is enabled in the deployment, correlate
   wake failures with pressure:

   ```promql
   rate(gateway_wake_rejections_total{reason="ram_pressure"}[5m])
   ```

3. Inspect the live ordering directly when the metric rate does not explain
   the pressure. The query mirrors the selector's inputs; `ram_mb + 8` is the
   per-instance admission charge:

   ```sql
   SELECT i.id,
          i.app_id,
          i.state,
          i.ram_mb,
          a.plan,
          a.eviction_priority,
          i.last_request_at,
          i.started_at
     FROM instances AS i
     JOIN apps AS a ON a.id = i.app_id
    WHERE i.state IN ('running', 'waking', 'cold_booting')
    ORDER BY i.last_request_at NULLS FIRST, i.id;

   SELECT sum(i.ram_mb + 8) AS resident_mb
     FROM instances AS i
    WHERE i.state IN ('running', 'waking', 'cold_booting');
   ```

4. Review the reaper and admission logs:

   ```bash
   journalctl -u faas-schedd --since '-15m' --no-pager \
     | grep -E 'reaper: eviction|admit|reject'
   ```

   A `reserved` or Scale eviction is expected only after eligible
   best-effort/non-Scale candidates are exhausted. A young-instance or service
   selection indicates a policy regression and should be escalated with the
   evidence record.

## Recovery and evidence

- Let the reaper drain pressure naturally. New wakes will be admitted only
  after `NodeLedger` observes the released charge.
- Do not manually mark instances parked, remove rows, or restart schedd as a
  first response; those actions can desynchronise the ledger from the VM.
- Reproduce the ordering without touching production state:

  ```bash
  make eviction-dryrun
  ```

  The target writes `docs/drills/<UTC-date>-<UTC-time>-eviction.md` and fails
  if the order, 30-second carve-out, service exclusion, or threshold check
  changes.

For the alert entry point and resident-RAM verification, see the
[high-resident-RAM runbook](../runbooks/FaasHighResidentRam.md).
