# Financial-model addendum: M-2 execution-mode billing (issue #1186 / ADR-137)

**Status (issue #1186 / ADR-137 §"Addendum"):** in-repo record of the
financial-model spreadsheet's scenario columns for the M-2 execution
modes (`request`, `worker`, `service`, `job`). The xlsx is offline
(lives on the reference node only, git-ignored per CLAUDE.md), so this
file is the canonical **in-repo** source of truth until the
corresponding spreadsheet row is patched. The two records MUST agree.

## Billable-RAM formula

The M-2 mode introduction is **formula-preserving**. Per-instance
billable MB is unchanged from M-1:

```
per_instance_billable_mb = plan.RAMMB + PerVMOverheadMB + Σ(sidecar.ram_mb)
```

- `plan.RAMMB` — plan-tier RAM quota (Free 128 / Hobby 256 / Pro 512 / Scale 1024). Source: `pkg/api/limits.go`.
- `PerVMOverheadMB = 8` — host-side parent cgroup surcharge.
  See `docs/financial/SIDECARS_ADDENDUM.md` for the full derivation.
- `Σ(sidecar.ram_mb)` — PR-C addition, unchanged.

The M-2 delta lives in **HOW MANY INSTANCES** are running at any
given moment:

| Mode       | Replica accounting                                  | Idle behaviour                                  |
|------------|-----------------------------------------------------|--------------------------------------------------|
| `request`  | Awake on demand, parked on idle (today's behaviour).| Idled instances PARK; consumer-only billing.    |
| `worker`   | Single long-lived instance per app.                | **Exempt from idle reap** (stays RUNNING).       |
| `service`  | `desired` replicas kept alive by the engine.        | Re-converged to `desired` on drain.              |
| `job`      | One instance per invocation; clean exit = success.  | Lifecycle ends on exit ≠0 retry.                 |

`service.replicas.desired` is the new billing multiplier M-2 introduces.
A Service-mode Hobby app running `desired = 3` instances at 256 MB
bills at 3× the request-mode equivalent. Replica ceiling per plan
(`ServiceReplicasMax`) lives in `pkg/api/limits.go` and bounds the
multiplier.

## Scenario columns (M-2)

| Plan  | Plan RAM (MB) | Mode      | Replicas (desired)  | Per-VM overhead | Per-instance billable (MB) | 30-day @ 720h × N replicas (GB-h)  | Plan ceiling (GB-h) | Overage (€0.01 / GB-h) |
|-------|---------------|-----------|---------------------|-----------------|----------------------------|-------------------------------------|---------------------|------------------------|
| Hobby | 256           | request   | 1                   | 8               | 264                        | ~178                                | 50 (included)       | ~€1.28                 |
| Hobby | 256           | worker    | 1                   | 8               | 264                        | ~178                                | 50 (included)       | ~€1.28                 |
| Hobby | 256           | service   | 1                   | 8               | 264                        | ~178                                | 50 (included)       | ~€1.28                 |
| Hobby | 256           | service   | 3                   | 8               | 264                        | ~534                                | 50 (included)       | ~€3.56                 |
| Hobby | 256           | service   | 5                   | 8               | 264                        | ~890                                | 50 (included)       | ~€6.40                 |
| Pro   | 512           | request   | 1                   | 8               | 520                        | ~351                                | 250 (included)      | ~€1.01                 |
| Pro   | 512           | worker    | 1                   | 8               | 520                        | ~351                                | 250 (included)      | ~€1.01                 |
| Pro   | 512           | service   | 3                   | 8               | 520                        | ~1053                               | 250 (included)      | ~€8.03                 |
| Pro   | 512           | service   | 5                   | 8               | 520                        | ~1755                               | 250 (included)      | ~€15.05                |
| Scale | 1024          | request   | 1                   | 8               | 1032                       | ~696                                | 1500 (included)     | (under)                |
| Scale | 1024          | worker    | 1                   | 8               | 1032                       | ~696                                | 1500 (included)     | (under)                |
| Scale | 1024          | service   | 3                   | 8               | 1032                       | ~2088                               | 1500 (included)     | ~€5.88                 |
| Scale | 1024          | service   | 5                   | 8               | 1032                       | ~3480                               | 1500 (included)     | ~€19.80                |
| Free  | 128           | request   | 1                   | 8               | 136                        | ~92                                 | 5 (included)        | ~€0.87                 |
| Free  | 128           | worker    | n/a                 | 8               | (rejected — Free disallows worker / service)  |
| Free  | 128           | service   | n/a                 | 8               | (rejected — Free disallows worker / service)  |

The "30-day @ 720h × N" column is the linchpin: a Hobby
`service` customer that scales to `desired = 5` already
**exceeds the plan ceiling** at ~890 GB-h vs the 50 GB-h Hobby
ceiling. This is the documented customer-trust path: dashboards
surface the `metered_mb_seconds_total{mode="service"}` rate so the
customer sees their overage before they hit it. The
`metered_mb_seconds_total{mode,plan}` Prometheus counter (commit 9
of M-2) is the operational view; `usage_minutes` is the audit-log
view (unchanged).

## Sidecar interaction

`docs/financial/SIDECARS_ADDENDUM.md` already accounts for sidecar RAM
via the billable MB formula; the service-replica multiplier compounds
on top. A Hobby `service { desired: 3, sidecars: [64 MB] }` instance
is 264 + 64 - 8 + 8 = 328 MB × 3 = 984 MB billable at any moment, or
~665 GB-h per 30-day window — well over the 50 GB-h Hobby ceiling.
The replica ceiling caps `desired` per plan:

```go
// pkg/api/limits.go (M-2 / ADR-137 §Decision 1)
WorkerReplicasMax   = map[Plan]int{PlanHobby: 1,  PlanPro: 3,  PlanScale: 10}
ServiceReplicasMax  = map[Plan]int{PlanHobby: 3,  PlanPro: 5,  PlanScale: 20}
JobMaxRuntimeS      = map[Plan]int{PlanHobby: 300, PlanPro: 1800, PlanScale: 3600}
```

Free plan: WorkerReplicasMax = 0 / ServiceReplicasMax = 0; Free customers
cannot select `execution_mode != "request"`. PR-A's PATCH gate rejects
explicit mode choices on Free with 403 `plan_execution_mode_not_allowed`.

## `metered_mb_seconds_total` (new counter)

Commit 9 adds a Prometheus counter splitting billable MB-seconds by
`{mode, plan}`:

```
metered_mb_seconds_total{mode="request",plan="hobby"}
metered_mb_seconds_total{mode="worker",plan="hobby"}
metered_mb_seconds_total{mode="service",plan="hobby"}
metered_mb_seconds_total{mode="job",plan="hobby"}
# + {normal,worker,service,job} × {free,hobby,pro,scale} = 16 series
```

Dashboards (`§12`) plot `rate(metered_mb_seconds_total[5m])` per
mode to show worker-idle separation from request-traffic. The counter
is **cumulative MB-seconds**, NOT row count — a rate query yields
MB/sec, summing cleanly against `usage_minutes` for reconciliation.
Mirror-mode rows are filtered upstream (`IsMeteredSkippableMode`) and
do not contribute to the counter — a customer who enables a mirror
rule sees no MIRROR mode label on the dashboard.

## Reconciliation with `usage_minutes`

`usage_minutes.mb_seconds` (Postgres) and
`metered_mb_seconds_total{mode,plan}` (Prometheus) MUST reconcile
1:1 over any minute window. The sampler writes both in the same
`SampleAndRoll` tick (`pkg/meter/sampler.go::SampleAndRoll`). A
divergence indicates either a row that reached the storage path but
not the wire path (or vice versa) — both would surface as a §12
alert `metered_mb_vs_usage_minutes_drift` (added in M-4 workstream E).

## What this addendum does NOT change

- **Per-mode rate** is identical: worker / service / job bill at the
  same `mb_seconds` rate as request-mode (no formula change). The
  replica multiplier covers scale-out, not overage.
- **Free plan** stays request-only (no worker / service / job).
- **Hobby / Pro / Scale overage rate** stays €0.01 / GB-h (per the
  financial-model spreadsheet).
- **`pkg/api/limits.go`** is the single-source-of-truth for
  `WorkerReplicasMax`, `ServiceReplicasMax`, `JobMaxRuntimeS`,
  `DefaultStopGracePeriodS{X}`, `DefaultStartupDeadlineS{X}` — these
  constants are mirrored to this addendum's table for readability
  only. Drift between the two files is a §17 gap.
