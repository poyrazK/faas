# FaasCpuStarvation

Source: `deploy/ansible/roles/prometheus/files/faas.rules.yml`, the
`FaasCpuStarvation` alert block (issue #301, ADR-044).

Metric: `vmmd_cpu_throttle_ratio{slice}` — a gauge computed inside
vmmd as `throttle_delta / (throttle_delta + usage_delta)` over the
per-tick 5m window. The `slice` label is the per-plan cgroup
sub-slice (`tenant-free|tenant-hobby|tenant-pro|tenant-scale`), set
up by the ansible role `systemd_slices`. Sibling cumulative counter
`vmmd_cpu_throttle_seconds_total{account_id, app_id}` (top-100 hottest
apps) carries the per-app attribution; cardinality is bounded by
`pkg/wire/topn_app.go::topAppSet`.

Severity: `warn` (capacity, not outage). CPU starvation ≠ customer
action failing (the cgroup's `cpu.max` just caps their slice). The
response is operator-side capacity work, not a customer-facing
outage. The `family` label is `cpu_starvation` so the existing
`family`-based inhibition / silencing rules in `alertmanager.yml.j2`
compose with this alert.

## Symptom

The alert fires when a per-plan sub-slice has been > 80% throttled
over the rolling 5m window for 5m:

| Alert | Trigger | For |
|---|---|---|
| `FaasCpuStarvation` | `vmmd_cpu_throttle_ratio{slice=~"tenant-.*"} > 0.8` | 5m |

The `slice` regex preserves the sub-slice label so Alertmanager
groups by `[alertname, slice]` — a single plan starving fires one
page with the slice in the summary. Multiple plans starving at the
same time is rare and indicates fleet-wide capacity pressure
(separate signal — see FaasHighResidentRamPct).

A real customer id surfaces under its own label on
`vmmd_cpu_throttle_seconds_total{account_id, app_id}`, never under
`account_id="other", app_id="other"` while their throttle-second
contribution is in the top-100. Once they leave the top-N they
collapse to the overflow bucket (`pkg/wire/metrics.go` pre-instantiates
this row at zero so the "no data" vs "data is zero" distinction is
visible).

## Verify

The dashboard `faas-top-throttled-apps`
(`deploy/grafana/top-throttled-apps.json`,
`deploy/ansible/roles/grafana/files/top-throttled-apps.json`) has
four panels:

> **First-scrape-after-restart is approximate.** The cpu sampler
> ticks at 250 ms (`CPUSampleInterval` in `cmd/vmmd/cpu_poller.go`)
> and the throttle ratio is a delta across two consecutive ticks.
> On the very first tick after a daemon restart the ratio surface
> shows 0 (no prior baseline); the value converges to a true 5m
> window after ~5 minutes of samples. If you're investigating "why
> is this slice throttled right after a deploy?", wait ~5m and
> re-check.

- Panel 1: "Top-10 throttled apps (5m)" — `topk(10, rate(vmmd_cpu_throttle_seconds_total{app_id!="other"}[5m]))`.
- Panel 2: "Per-slice throttle ratio (5m, vmmd)" — `vmmd_cpu_throttle_ratio{slice=~"tenant-.*"}`.
- Panel 3: "Customer share of fleet throttling (5m, vmmd)" — top-10 by throttle-seconds share.
- Panel 4: "Other bucket growth (vmmd, 5m)" — flags overflow saturation of the top-100 admission primitive.

For ad-hoc Prometheus API queries:

```bash
# Top-20 by current 5m throttle-seconds (the panel-1 expression)
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=topk(20,rate(vmmd_cpu_throttle_seconds_total[5m]))' | jq .

# Per-slice throttle ratio (the alert's source expression)
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=vmmd_cpu_throttle_ratio' | jq .

# Single-app drill-down — replace $ACCOUNT_ID + $APP_ID
ACCOUNT_ID=<uuid>; APP_ID=<uuid>
curl -fsS --data-urlencode "query=rate(vmmd_cpu_throttle_seconds_total{account_id=\"${ACCOUNT_ID}\",app_id=\"${APP_ID}\"}[5m])" \
  'http://127.0.0.1:9095/api/v1/query' | jq .

# Cross-check cgroupstate at the kernel level — confirms the
# cpu.max write landed. Replace $SLICE with the offending plan.
SLICE=tenant-hobby
curl -fsS "http://127.0.0.1:9095/api/v1/query?query=node_cpu_cgroup_throttled_seconds_total{cgroup=\"${SLICE}\"}" | jq .

# Is the top-100 cap saturated?
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=vmmd_cpu_throttle_seconds_total{account_id="other",app_id="other"}' | jq .
```

## Check

```bash
# Confirm the per-plan systemd sub-slice is in place and its
# cpu.weight matches pkg/api/limits.go::Limits.CPUWeight
systemctl status faas-tenant-hobby.slice
# Expected: "CPUWeight: 4" in the cgroup attributes.

# Read the cgroup leaf's cpu.max — confirm vmmd's write landed
SLICE=tenant-hobby; INSTANCE=<id>
cat /sys/fs/cgroup/faas-tenant.slice/${SLICE}/${INSTANCE}/cpu.max
# Expected: "200000 100000" (= 200ms / 100ms quota for Hobby).

# Recent vmmd logs for the offending slice (cross-reference
# against the cpustats cache's Regression-detected entries)
journalctl -u vmmd --since '-15m' --no-pager | grep -i "throttle\|cpu.max"

# Cross-check the per-VM CPU rate — a slice throttled at 80% with
# one VM running at 0% means the cpu.max write didn't reach the
# kernel for that VM (the legacy 2-level path is still active)
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=topk(5,sum by (instance)(vmmd_cpu_seconds_total))' | jq .
```

A spike paired with a low `vmmd_cpu_seconds_total` per-instance is a
real saturation (the kernel is throttling because the workload
exceeds its quota — fix the workload or the quota). A spike paired
with high `vmmd_cpu_seconds_total` but a low `node_cpu_cgroup_throttled_seconds_total`
means the cpu.max write didn't land for some instances — check the
vmmd log for `cgroup fence: writePlanCgroup failed` entries.

A spike on `tenant-free` but not on `tenant-hobby|tenant-pro|tenant-scale`
is the Free tier's tight quota doing its job — Free plans cap at 1 vCPU
worth of compute (§1, ADR-005). The right response is to
communicate the limit to the customer (their app is too compute-heavy
for Free), not to raise the quota.

## Silence

```bash
# Silence a specific slice (e.g. expected Free-tier saturation
# during a marketing campaign). Replace $SLICE with the offending
# plan.
SLICE=tenant-free
amtool silence add \
  --matchers='alertname="FaasCpuStarvation",slice="'"${SLICE}"'"' \
  --duration=15m \
  --comment='planned Free-tier saturation; will raise plan-tier of offending customer'

# Silence every FaasCpuStarvation row during an active incident
# bridge (use sparingly; this hides ALL starving slices)
amtool silence add \
  --matchers='alertname="FaasCpuStarvation"' \
  --duration=30m \
  --comment='incident bridge open; investigating fleet capacity'
```

## Recover

Three-step cascade, ordered from least to most disruptive:

1. **Inspect the top-N panel and identify the offender.** The
   `top-throttled-apps` dashboard's Panel 1 names the
   `(account_id, app_id)` driving the saturation. If it's a
   single customer app, the right response is customer-side
   (their workload is too compute-heavy for their plan). If it's
   the platform's builder slot consuming the slice (rare —
   builderd lives under `faas-cp.slice`, but a stuck build
   manifest can keep a slice warm), park the offending instance
   via `faas instances park <id>`.

2. **Coordinate a plan raise with the customer.** For Hobby/Pro/Scale
   slices, a sustained > 80% throttle ratio is the signal that the
   customer's workload doesn't fit the per-plan quota. Email the
   account's primary contact (template at
   `pkg/grace/dunning_templates.go` or a future `pkg/capacity/`
   package — not in scope for this PR): "Your app is being
   throttled at > 80% on the Hobby tier quota. Consider upgrading
   to Pro or Scale, or reducing per-request work." Notification
   is best-effort — a transient mail failure does NOT block the
   operational response.

3. **Escalate: raise the slice's cpu.max or faas-tenant's
   CPUQuota.** If multiple customers on the same plan are starving
   simultaneously (Panel 3's "Customer share" panel shows > 1
   dominant offender), the slice is the bottleneck, not any single
   customer. Options:
   - **Plan quota bump**: edit `pkg/api/limits.go::Limits.CPUQuotaUS`
     for the affected plan and ship a new release. This is a
     customer-facing change — verify with the §1 financial model
     that the new quota still supports the steady-state plan mix.
   - **Tenant slice CPUQuota bump**: edit
     `deploy/ansible/roles/systemd_slices/tasks/main.yml`'s
     `CPUQuota=1600%` on `faas-tenant.slice` (1600% = 16 vCPU; the
     §1 model assumes the box has 16 vCPU worth of headroom).
     Re-run the ansible role. This is a fleet-wide capacity change
     and must be matched on the spec side via an ADR.
   - **Move the offending customer to a different box**: out of
     scope for this runbook (covered by issue #56, multi-host). The
     migration flow is a separate operational surface.

Recovery verification:

```bash
# After plan raise / customer fix: the slice's throttle ratio
# should drop below 0.8 within 1 sample window (~250ms in vmmd,
# 5m on the alert). The for:5m debounce means the alert clears
# ~5m after the ratio stabilizes.
curl -fsS 'http://127.0.0.1:9095/api/v1/query?query=vmmd_cpu_throttle_ratio{slice="'"${SLICE}"'"}' | jq .

# After notify: customer should self-correct within 30m; if not,
# proceed to step 3 (raise the slice's CPUQuota).
# After cpu.max bump: the per-instance kernel cpu.max file should
# reflect the new quota. Replace $INSTANCE.
cat /sys/fs/cgroup/faas-tenant.slice/${SLICE}/${INSTANCE}/cpu.max
```

A sustained recovery (no further `FaasCpuStarvation` fires for
24h) closes the incident; the silence expires on its own and the
gauge surfaces the slice at its normal throttle ratio. Memory
pressure is **not** part of this runbook — `FaasCpuStarvation`
governs CPU only; memory starvation is governed by
`FaasHighResidentRamPct` (spec §12 row).