# FaasDebugRegressionDetected

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`.
Metrics: `apid_debug_regression_detected_total` (counter, single-registry,
unlabelled — only apid increments).
ADR: ADR-127 Debugger UX v1.
Severity: warn (ADR-1236) on `FaasDebugRegressionDetected` (≥1 PRIMARY KEY upsert
on `debug_regression_observations` in the last 5m, with 1m debounce).
The alert is the customer-facing regression banner tripwire — page
on-call so they can correlate with the dashboard before customers
report slowness.

## Symptom

The per-app regression detector (cmd/apid/debug_regression_cron.go::
runRegressionForApp, ADR-127 PR-B) has persisted ≥1 upsert to
`debug_regression_observations` in the last 5 minutes. Each upsert
means the per-(deployment, route) comparator found:

```
current.p95 > baseline.p95 * 1.20   AND   affected_count ≥ 100
```

over a 30-minute current window vs. a 30-minute prior-deployment
baseline window. The `(app_id, deployment_id, route)` PRIMARY KEY
upserts on conflict — a new regression fires the counter once;
an existing regression that persists across cron passes re-fires
every 5m. PagerDuty dedupes a sustained-fire alert into one
ongoing incident.

- **`apid_debug_regression_detected_total` (counter)**: bumps
  once per successful `UpsertRegressionObservation` call from the
  cron. Unlabelled — single-registry pattern so the dashboard keeps
  the series on fleet roll-out, only apid increments.
- **Customer-visible signal**: the
  `/dashboard/apps/{slug}/debug` route shows a regression banner
  feed; the customer sees "regression detected: <route> +<factor>x"
  with a `compare vN` button (PR-B) and a `replay` button (issue #72).
  The alert fires before the customer clicks anything — proactive
  page.

## Likely causes (most common offenders)

1. **Real regression**: a customer deployed a new build, the new
   build's per-route p95 crossed 1.20× baseline for ≥100 affected
   rows. The "compare vN" dashboard drill-down shows the
   offending deployment. This is the desired path — the alert
   surfaces a real production regression for the operator to
   review BEFORE the customer opens a ticket.
2. **Baseline drift after a long-quiet deployment**: a customer
   that hasn't shipped traffic in 7+ days returns to active
   service. The baseline p95 over a 30-min prior-deployment
   window collapses to a tiny sample (n < debugRegressionMinAffected)
   so the cron skips the row. But if the customer ramped on the
   new deployment WITHOUT prior traffic, the baseline is sampled
   over the wrong window. Check `cmd/apid/debug_regression_cron.go:298-309`
   — the `affected < debugRegressionMinAffected` short-circuit
   should have skipped this case.
3. **Time-of-day / weekly seasonality**: a Hobby customer's
   traffic pattern swings weekly; a Friday-afternoon p95 that
   matches a Saturday-morning baseline reads as a regression. The
   1.20× factor absorbs some seasonality but not all of it. The
   dashboard's "compare vN" panel surfaces the factor; an
   operator-side decision is whether to widen the factor or
   re-baseline. ADR-127 PR-B chose 1.20× as the threshold; an
   adjustment would be a separate ADR.
4. **Postgres partition churn**: a new `request_telemetry`
   partition was just created (range partitions rotate daily per
   PR-A's `pg_partman` setup). The cron's 30-min window straddles
   the partition boundary; the DISTINCT scan can over-count rows
   that landed in the previous partition. Cross-check
   `apid_request_telemetry_recorded_total` — a partition hop
   shows as a step.
5. **PR-B fleet ramp**: if this alert is brand new (Debugger UX
   v1 just merged), the metric has no historical baseline.
   PagerDuty's first incident may be a cold-start false positive
   where a pre-existing regression was already in
   `debug_regression_observations` and the cron's first post-merge
   pass upserted the existing row (re-firing the counter once on
   every cron tick). Set `for: 1m` ≥ 5m to absorb this; document
   the merge-time misfire in the post-mortem.

## Triage (escalate by severity)

1. **Identify the offending (app, deployment, route)**: run
   `gregale debug regressions <slug>` from `cmd/gregalectl/commands_debug.go`
   (or the dashboard's regression banner list) to see the most
   recent upserts. Each row carries `p95_ms`, `baseline_p95_ms`,
   `affected_count`, and `regression_factor` — the math is at
   `cmd/apid/debug_regression_cron.go:298-309`.
2. **Drill into the route's latency distribution**: `gregale debug
   compare <slug> --source <deploy-uuid> --mirror <deploy-uuid-2>`
   surfaces per-route p50/p95/p99 + count (Debugger UX v1 stage 4
   extends this — see `cmd/apid/handlers_debug_telemetry.go`). A
   high p99 with a sane p50 means a tail-latency regression (one
   bad query), not a systemic regression.
3. **Check the upstream regression detector**:
   `apid_request_telemetry_recorded_total{outcome="inserted"}`
   should match the customer's recent traffic. If it's dropped
   simultaneously, the regression is the symptom, not the cause —
   look at the publisher (`gatewayd-internal ::request_telemetry_publisher`)
   for stalls.
4. **Confirm the customer agrees**: the dashboard's regression
   banner shows the same `compare vN` data the alert references.
   If the customer hasn't noticed yet, the operator can decide
   whether to page (visible regression) or wait 30m for the next
   cron pass to confirm (transient blip).

## Mitigation

1. **Real regression: ship a rollback**: `gregale deploys rollback
   <deploy-uuid>` rolls the deployment back to the prior build
   (PR-A's blue/green machinery). The next cron pass should clear
   the regression row. PR-B's regression banner surfaces the
   rollback button alongside `compare vN` / `replay`.
2. **Transient blip: do nothing**. The cron re-upserts on every
   pass; if the regression clears within 30m the next pass drops
   the affected_count below `debugRegressionMinAffected` and the
   row's `last_detected_at` ages out. The alert resolves naturally.
3. **Baseline drift: widen the factor** (separate ADR — never
   inline) OR re-baseline by inserting a sentinel baseline row
   into `debug_regression_observations` via a backfill migration.
   The backfill path is documented at
   `migrations/00478_deployment_audit_backfill_90d.sql` as a
   precedent.
4. **Cold-start false positive**: bump the alert's `for:` to ≥5m
   so a single post-merge cron pass doesn't page. Document the
   bump in the post-mortem and revert after 24h.

## When NOT to escalate

- The alert fires during the **first 1h after Debugger UX v1
  merges**. The metric has no historical signal; the cron's
  first post-merge pass upserts every existing row in
  `debug_regression_observations` and the counter spikes. Set the
  alert's `for:` at ≥5m to absorb this; revert after 24h.
- The alert fires during a **planned PR-A request_telemetry
  partition hop**. The cron upserts one extra time per crossing
  route; the next cron pass clears the over-count. Page only if
  the alert persists >30m.
- The customer has **explicitly accepted the regression** via
  the dashboard's "dismiss" button (PR-B UI affordance). The
  alert still fires because the cron doesn't know about the
  dismiss — it's a passive page for the operator to know the
  dismiss happened. Note in the incident that the customer is
  aware.