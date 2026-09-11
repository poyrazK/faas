# FaasBuildQueueBacklog

**Alert:** `FaasBuildQueueBacklog` (faas.rules.yml)
**Severity:** page
**Family:** build_queue

The build queue depth (count of `builds.status='queued'`
rows per compute_node) has been above the healthy ceiling
for 10 minutes. Customers pushing commits are seeing
delayed builds, and the backlog is growing faster than
builderd's durable worker can drain it.

## When to roll back

This alert does NOT warrant a rollback — there is no
deploy in progress. The remediation is operational
(force-evict stuck builds, increase builder capacity) not
code.

## Symptoms

- `faas_build_queue_depth{node_id="..."}` rising for
  >10m (alert threshold: 5 queued builds per node for
  10m sustained).
- Customer reports of "git push succeeded but build
  hasn't started in 20+ minutes" — verify via
  `psql -c "SELECT id, app_id, status, started_at FROM
  builds WHERE status='queued' ORDER BY created_at;"`.
- builderd `/metrics` shows `builderd_no_slot_total`
  climbing — builderd is rejecting claims because
  tenant residency exceeds the opportunistic-2nd-slot
  gate (`pkg/builderd/slot.go:30-58`,
  `slotThresholdFraction = 0.60`).

## Procedure

1. **Verify the alert is real.** Check the build queue
   depth in Postgres:
   ```sql
   SELECT target_node_id, COUNT(*) FROM builds
   WHERE status='queued' GROUP BY target_node_id;
   ```
   Confirm depth > 5 for at least one node for 10m+.

2. **Check for stuck-running builds.** builderd's
   in-process slot model means a build row in
   `status='running'` is occupying a slot even if the
   underlying builder VM has died. Find stuck rows:
   ```sql
   SELECT id, app_id, started_at, node_id
   FROM builds WHERE status='running'
   AND started_at < now() - interval '15 minutes';
   ```
   If any rows, the reaper should have caught them.
   Confirm the reaper is running: `ps aux | grep
   builderd` should show the ReaperLoop goroutine.

3. **Force-evict stuck builds** via the operator CLI:
   ```
   gregalectl auth step-up
   gregalectl builds sweep-stuck --older-than 15m --reason build_queue_incident --yes
   ```
   The authenticated API handler calls `state.Store.SweepStuckRunningBuilds`
   directly, flips each stuck row to
   `status='failed', failure_class='timeout'`, and
   emits an `operator.action.reclaim_build` audit row.
   Verify by re-running the SELECT in step 2 — expected
   to return 0 rows.

4. **Confirm queue depth drops.** Re-run the SELECT in
   step 1; expect depth ≤ 2 within 5 minutes.

5. **If the queue is STILL backed up after step 3**, the
   issue is genuine demand exceeding builder capacity.
   Check `pkg/builderd/builderd.go::ProcessNext` for
   `ErrNoSlot` returns — this means the 1+1 ceiling
   (1 guaranteed + 1 opportunistic) is binding.
   Consider scaling the builder fleet (out of scope
   for this runbook — see the multi-host rollout
   playbook).

## Validation matrix

After step 4, all of these must be true:

- [ ] `SELECT COUNT(*) FROM builds WHERE status='queued'`
      ≤ 5 per node (alert threshold).
- [ ] `SELECT COUNT(*) FROM builds WHERE status='running'
      AND started_at < now() - interval '15 minutes'`
      = 0 (no stuck-running builds).
- [ ] `faas_build_queue_depth{node_id}` gauge ≤ 5
      (alert cleared).
- [ ] `builderd_no_slot_total` counter not climbing
      (rate over 5m = 0).
- [ ] Audit-log search `kind_prefix=operator.action.`
      returns the `reclaim_build` row from step 3.

## Rollback

N/A — operational remediation only. If the alert
re-fires within 24h, the underlying demand curve
exceeds builder capacity and the fix is fleet scaling,
not more force-evicts (which would loop).

## Escalation

- Tier 0 (primary oncall) handles steps 1-4.
- Tier 1 (secondary oncall) if the queue is still
  backed up after step 3 OR if customer impact is
  escalating (more than 5 customer tickets about
  build delays).
- Tier 2 (engineering manager) if the backlog
  persists for >2h — fleet-scaling decision.

## References

- `pkg/builderd/reaper.go:38-67` — the ReaperLoop
  sweep.
- `pkg/builderd/builderd.go:71` — `ErrNoSlot`
  sentinel.
- `pkg/builderd/slot.go:35-59` — `DecideSlot`
  opportunistic-2nd-slot gate.
- `cmd/apid/handlers_admin_sweep_builds.go` — the
  force-evict endpoint.
- `pkg/state/pgstore.go` — `SweepStuckRunningBuilds`
  Store method.
- [`escalation.md`](escalation.md) — escalation matrix.
