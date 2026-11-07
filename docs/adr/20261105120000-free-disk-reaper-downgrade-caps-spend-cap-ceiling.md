# ADR 20261105120000 · Free-tier disk reaper; floors and warm pools follow the current plan; paid spend caps have a ceiling

- **Status:** proposed.
- **Date:** 2026-11-05
- **Amends:** spec §4.3 (Free-tier disk reaper), ADR-20261029180000 (default
  spend caps: "a cap cleared on a paid plan is the customer's choice"),
  ADR-20261103131742 decision 4 (plans bind after a downgrade), ADR-071
  (min-instances floor)
- **Related:** ADR-005 (snapshots are cache), ADR-074 (warm pools),
  ADR-20261103164725 (bulk cap reads)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

A cost-control audit (2026-11-05, round 9) found three gaps:

1. **The Free-tier disk reaper did not exist.** Spec §4.3 says a Free app with
   no requests for 14 days moves its snapshot and rootfs to object storage
   and becomes `EVICTED_COLD`. `api.FreeTierColdEvictDays` was declared and
   never read. Snapshots are not billed, so an abandoned Free app kept about
   200 MB of snapshot on the node indefinitely; only the pressure sweep at
   `SnapshotBudgetAlarmPct` reclaimed any.
2. **Floors and warm pools outlived the plan.** apid checks `min_instances`
   against `MaxMinInstances` and `warm_pool_size` against max concurrency
   only when they are written. After a downgrade:
   - the floor trigger held a stored floor up to the plan's max concurrency;
   - the reaper never idled out instances below the stored floor, even on
     Free;
   - the warm pool kept the stored size.
   So Scale → Hobby kept 2 always-on instances (Hobby allows 1) and up to 20
   paused VMs. meterd's `planFloorCap` already billed only the capped floor,
   so the extra instances took node RAM the plan does not pay for.
3. **A paid subject could remove its spend cap.** `overage_cap_cents: null`
   meant no cap, with no ceiling and no trust check. Egress billing is priced
   below its cost ($0.03/GiB against roughly $0.08–0.12/GiB), so an uncapped
   account's loss is unbounded once egress bills live.

## Decision

1. **Free-tier disk reaper.**
   - Each imaged GC tick (after the rollback window, step A2) deletes every
     snapshot of up to `FreeTierColdEvictAppsPerTick` (50) dormant Free apps.
   - An app is dormant (`ListDormantFreeSnapshotApps`) when:
     - its account is on Free, and so is its shared organization if it has
       one;
     - its status is active or evicted_cold, and it has a snapshot;
     - it has no live instance;
     - nothing has happened for `FreeTierColdEvictDays` (14): no request,
       start or park on any instance row, and no deployment or app creation.
   - Instance rows are retained 30 days, so a request inside the window is
     always seen.
   - The live deployment's app layer stays (`deleteSnapshotsAndFiles` already
     keeps it). The next request cold-boots and re-snapshots (ADR-005), so
     invariant 3 holds.
   - Each evicted app gets an audit event, `snapshot.free_dormant_evicted`.
   - **Deviation from §4.3:** the rootfs is not moved to object storage, and
     imaged does not set `apps.status` (apid and schedd own that). Moving
     the app layer off the node is a follow-up.
2. **Floors and warm pools follow the current plan.**
   - The floor trigger (`Tick`, `evaluateDeployment`) caps the floor at the
     current plan's `MaxMinInstances`.
   - The reaper's rows cap the configured floor the same way
     (`planCappedFloor`): zero on a plan without floors, unchanged when the
     plan read failed. Temporary prewarm floors are not capped.
   - The warm pool's target is `min(warm_pool_size, effective max
     concurrency)` under the current plan (`warmPoolTarget`).
   - Stored values are kept, so an upgrade restores them.
3. **Paid spend caps have a self-service ceiling.**
   - `SelfServiceOverageCapMultiplier` is 100 × the plan's default cap:
     Hobby $200, Pro $1,000, Scale $3,000. **These are assumed numbers,
     pending the owner's.**
   - On a paid plan, `POST /v1/account/overage-cap`, the organization route
     and the dashboard form refuse `null` (422 `overage_cap_required`) and
     any value above the ceiling (422 `overage_cap_above_max`, with limit,
     observed value and docs). Free may still set or clear its cap; Free
     stops at its allowance either way.
   - A paid subject whose stored cap is NULL (saved before this record) is
     held to its plan's ceiling everywhere the cap is read:
     `GetAccountOverageCapCents`, `LoadAllOverageCapCents` and
     `AccountOverageCaps`, in both stores (`EffectiveOverageCapCents`).
   - A plan change brings a cap above the target plan's ceiling down to that
     ceiling (`PlanChangeOverageCap`).
   - A higher cap is an operator change (direct row update); it is honoured
     until the next plan change.

## Consequences

- Dormant Free apps give back their snapshot disk. Their first request after
  14 idle days is a cold boot instead of a restore.
- A downgrade takes effect on floors and warm pools at once. The customer's
  saved values return on upgrade.
- No paid account or organization runs without a spend cap. The GET route
  shows the ceiling for a legacy NULL row. Clients that sent `null` to
  remove the cap on a paid plan now get 422.
- No schema change.
