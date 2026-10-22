# FaasScheddLoopWork

Covers `FaasScheddLoopWorkDropped` and `FaasScheddLoopWorkPanicked`.
Severity: warn. Spec §12, ADR-191 §2.

schedd's notification loop hands reconcile work to a bounded off-loop pool,
with a slot budget per `kind`. `schedd_loop_work_total{kind,outcome}` counts
each handoff.

## Symptom

- `FaasScheddLoopWorkDropped`: more than one task of one `kind` per minute was
  dropped for 15 minutes because that kind's slots were full. The durable table
  and its safety ticker still retry the work, so the effect is latency (slower
  wakes, parks or reconciles of that kind), not loss.
- `FaasScheddLoopWorkPanicked`: an off-loop handler panicked. This is a bug,
  not a capacity signal. The pool recovered the goroutine, but the task did not
  complete.

## Check

1. `rate(schedd_loop_work_total{outcome="dropped"}[5m])` by `kind` and
   `histogram_quantile(0.95, rate(schedd_loop_work_duration_seconds_bucket[5m]))`
   for the same kind: slow handlers fill slots as surely as a high rate.
2. If handlers are slow, look for Postgres or vmmd latency behind them
   (`FaasDBPoolNearCap`, `FaasScheddInstanceStatsCollectSlow`).
3. For a panic, find the stack in `journalctl -u faas-schedd` around the alert
   time.

## Recover

- Dropped work: if the rate is genuinely higher, the kind's slot budget is too
  small for this fleet. Budgets are code constants in the `workSpecs` table in
  `pkg/sched/loopwork.go`; raising one is a reviewed code change and a release,
  not a runtime setting. Slow handlers recover with the Postgres or vmmd
  latency behind them.
- Panic: file the stack with the `kind` label; the task did not complete and
  needs the bug fixed.
