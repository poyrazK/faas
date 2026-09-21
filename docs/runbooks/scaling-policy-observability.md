# Which signal is scaling this app?

Since ADR-194 an app declares a **list** of scaling targets and the platform
provisions for whichever demands the most instances. That makes "did it scale"
an incomplete answer: tuning a policy requires knowing which declared target
is actually binding, because changing any other one will do nothing.

This runbook covers the three questions that come up, and the one migration
hazard ADR-194 left behind.

## Which target is binding?

```promql
sum by (metric) (rate(schedd_scale_up_winning_signal_total{app="$app"}[15m]))
```

Each admission is attributed to exactly one metric — the one that produced
the highest desired instance count at that tick. The sum across `metric`
equals `schedd_scale_up_decisions_total{outcome="admit"}` for apps scaled by
the targets trigger; a divergence means an admission was made without an
arbitrated winner and is a bug worth reporting.

Reading it:

- **One metric dominates.** That target is your control. The others are
  either slack or redundant — raising them changes nothing until this one
  stops binding.
- **Attribution alternates between two metrics.** Both are near their
  targets. This is the healthy multi-signal case and the reason to declare
  more than one.
- **A declared target never appears.** Either it is never the highest, or it
  has no reading. Check the no-signal rate before assuming the former:

```promql
rate(schedd_scale_up_decisions_total{app="$app",outcome="no_signal"}[15m])
```

A target whose source is unavailable contributes `Have=false` and is
skipped, which is indistinguishable from "not the highest" in the winning
signal series alone. `queue_lag` is the usual suspect — it reports nothing
unless a broker answers, and deliberately does **not** fall back to
`queue_depth`.

## Is a scheduled window open right now?

```promql
schedd_scheduled_floor_instances{app="$app"}
```

Non-zero means an ADR-195 window is open and demanding that many warm
instances. Zero means no window is open — the series is published as zero
rather than omitted, so a closed window is distinguishable from a schedd that
has stopped ticking.

**A scheduled floor is billed**, so this is a revenue signal as much as an
operational one. Two shapes to watch for:

- **Non-zero when the window should be closed.** The cron or the timezone is
  not what the author intended. `timezone` defaults to UTC when unset, which
  is the common cause — a business that means 08:00 local gets 08:00 UTC.
- **Zero when the window should be open.** Same root cause, opposite
  direction. Confirm the app's stored policy before changing the cron:

```sql
SELECT slug,
       scaling_policy -> 'timezone'  AS timezone,
       scaling_policy -> 'schedules' AS schedules
  FROM apps
 WHERE id = '<app-id>';
```

Compare against the effective floor the scheduler is using:

```promql
schedd_scheduled_floor_instances{app="$app"}
```

## Apps still carrying a p99_latency_ms target

ADR-194 removed `p99_latency_ms` from the closed metric set. It never had a
source — no trigger implemented a latency axis — so removing it changed no
app's behaviour. But it is still **accepted in stored rows** and is now
**rejected on write**, which means an app that has it will 422 on its next
scaling PATCH, including one that only meant to change an unrelated field.

Nothing surfaces those apps automatically. Find them before a customer does:

```sql
SELECT a.id, a.slug, acct.email
  FROM apps a
  JOIN accounts acct ON acct.id = a.account_id
 WHERE a.scaling_policy -> 'target' ->> 'metric' = 'p99_latency_ms'
    OR a.scaling_policy -> 'targets' @> '[{"metric": "p99_latency_ms"}]'::jsonb;
```

The fix is a PATCH replacing the target with `concurrent_requests`, which is
the signal a latency target is reaching for — queueing on a saturated
instance is what drives p99 up, and in-flight requests measure it directly.
Pick a target value from the app's current steady-state in-flight count:

```promql
max_over_time(schedd_scale_up_admit_rps{app="$app"}[1h])
```

There is no automatic migration on purpose: the replacement value is a
judgement about the app's latency budget, and guessing it would silently
change the scaling behaviour of an app whose owner never asked for it.
