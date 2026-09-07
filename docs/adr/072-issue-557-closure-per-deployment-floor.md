# ADR-072 — Issue #557 closure: per-deployment min_instances axis + audit rename + 00129 backfill

Status: accepted (2026-08-04). Companion to ADR-071. Builds the per-deployment axis
ADR-071 §Rejected alternatives deferred; the inheritance rule (new deployments default
to inheriting the parent app's floor) sidesteps the original foot-gun (every new deploy
resetting the floor to 0).

## Context

PR #618 shipped the per-app axis of issue #557 (proactive min-instances floor reconciler,
`EffectiveMinInstances()` helper, `MaxMinInstances` plan caps, `floor.wake` audit kind,
`min_instances_target` wire field). All 21 auto CI gates are green.

Three pieces remained vs. the literal issue acceptance criteria:

1. **Per-deployment axis missing.** The issue title is
   *"min-instances per deployment (Hobby+)"*. ADR-071 §Rejected alternatives deferred
   this axis to issue #556 (traffic-splitting) because every new deploy would reset the
   floor to 0 unless the customer re-set it. The inheritance rule (a new deployment
   defaults `min_instances=0` = "inherit from parent app") sidesteps the foot-gun:
   a customer's first deploy after PR #618 wakes the parent's floor without any
   additional config, and only an explicit PATCH overrides it.

2. **Audit-kind name nit.** The issue AC #5 names `instances.warmed_min_instances`; we
   shipped `floor.wake` in PR #618. Semantically equivalent (different prefixes), but
   the issue's wording is the audit kind downstream consumers grep for. We ship both
   for one release as a compat layer; a follow-up drops `floor.wake`.

3. **00131 migration.** ADR-071 §Downstream files `00131_align_min_instances.sql` as
   cleanup for divergent `apps.min_instances` vs `apps.scaling_policy->>'min_instances'`
   rows. The helper returns `max(column, jsonb)` so divergent rows behave correctly today
   (safer direction — billing and enforcement both see what the customer configured).
   The migration lands in this PR so a future wire shape that reads the jsonb directly
   agrees with the helper.

## Decisions

1. **New `deployments.min_instances` column** with the inheritance default = 0 (=
   "inherit from parent app"). The column lands in migration 00133 (a separate
   `ALTER TABLE deployments ADD COLUMN` migration); 00131 stays focused on the
   apps.align_min_instances backfill and the migration set stays contiguous
   (00131 + 00132 + 00133, with 00129/00130 as fences past PR #623's slot claim).

2. **Effective per-instance floor**
   `= max(app.EffectiveMinInstances(), d.EffectiveMinInstances())`. Composed at the
   trigger + reaper + meterd sites; the helper itself returns only the deployment's
   contribution. The parent app's floor is the lower bound.

3. **Plan caps apply to the effective floor**, not separately per axis. A Scale
   customer's effective floor cannot exceed 10 even when both axes are pinned.
   Validated at the PATCH handler against the *parent app's* `plan.MaxMinInstances()`
   (the cap is per-account, not per-row).

4. **New `PATCH /v1/deployments/{id}` route** for explicit override. Body is
   `{"min_instances": int}`; `0` resets to inheritance. Reuses the deploy-write
   scope (`api.ScopesDeployWriteSurface`).

5. **`Engine.AdmitInstanceForDeployment` is the new per-deployment entry point**.
   `Engine.AdmitInstance` (the wake path) is unchanged — it resolves the live
   deployment internally via `LiveDeployment`. The trigger's per-deployment sweep
   calls `AdmitInstanceForDeployment` so the explicit deployment id survives
   into the instances row and the ledger's per-(app, deployment) counter.

6. **Audit kind: emit both** `floor.wake` (the original PR #618 name) **and**
   `instances.warmed_min_instances` (the issue AC name) for one release as a
   compat layer. The trigger's `auditor.Emit` call site has the kind as a parameter;
   one extra Emit per wake is the load-bearing change. A follow-up PR drops
   `floor.wake` after one release's downstream tooling migrates.

7. **Migration `00131_apps_align_min_instances.sql`** lands in this PR (no longer a
   follow-up). The backfill projects the legacy column into the jsonb on rows where
   the column is strictly greater than the jsonb; the inverse direction (jsonb > column)
   is untouched on purpose (the jsonb is the customer's explicit PATCH intent).

## Migration slot landscape (post-rebase)

Main's highest migration slot is 00128 (events_sidecar_name_idx). PR #618 originally
targeted 129 + 130 in this branch, but PR #623 (iam-6 PR-6, open against main) has
already claimed slot 129 (`00129_api_keys_org_bound.sql`). Per the cross-PR slot gate
race memory (`migration-gates-collision-and-replay.md`), PR #618 renumbers to 131 + 132
so the merge order does not matter — goose would 42P07 "duplicate version 129" on
whichever PR lands second. The prior reservation fences at 124/125 from PR #618 land
as part of PR #618's own embedded set; the new content at 131/132 is real, not a fence.

* 00131: `apps_align_min_instances.sql` (apps backfill)
* 00132: `instances_app_deployment_idx.sql` (partial index)
* 00133: `deployments_min_instances.sql` (ADD COLUMN min_instances)

## Failure modes

| Failure | Defence |
|---|---|
| Customer deploys with no override; trigger wakes N extra instances on first tick | Expected behaviour (ADR-060 "billed from t=0"); release note + dashboard banner |
| Latest deployment becomes `failed`, `superseded`, or `cancelled` with no live replacement | Trigger skips it and meterd stops synthetic floor billing; terminal capacity cannot satisfy the configured floor |
| Customer pins `deployments.min_instances=5` and `apps.min_instances=10` | Effective = 10; the trigger honours the higher; meter bills at 10 |
| Customer pins `deployments.min_instances=8` on Free plan | PATCH handler returns 422 plan_min_instances_not_allowed |
| Pre-00131 rows with NULL `instances.deployment_id` | `ConcurrencyForDeployment` predicate `deployment_id = $2` excludes NULL rows; under-counts but safe (engine backstop) |
| Trigger walks `ListAllDeployments` on a fleet > 100 apps × 10 deploys | Same planner index plan as `ListAllApps`; sub-10ms at v1 scale |

## Security

No new attack surface. The PATCH route is gated by `ScopesDeployWriteSurface` (matches
the rest of the deployments surface); the new ledger counter is in-process only. The
schema migration is replay-safe per ADR-041.

## Consequences

* `pkg/state/types.go::Deployment` gains `MinInstances int`.
* `pkg/state/pgstore.go` gains `ListAllDeployments`, `ListDeploymentsByNodeID`,
  `ConcurrencyForDeployment`, `UpdateDeploymentMinInstances`. The deployment select
  projections (`deploymentSelectColumns` and friends) gain a trailing `min_instances`
  column; the scan helpers (`scanDeployment`, `scanDeploymentWithRootfs`,
  `scanDeployments`) read it.
* `pkg/state/memstore.go` mirrors the four new methods for unit tests.
* `pkg/sched/admission.go::NodeLedger` gains a `perAppDeployment` map keyed by
  `appID + "\x00" + deploymentID`. `ConcurrencyForDeployment` returns the count.
* `pkg/sched/engine.go` gains `AdmitInstanceForDeployment`; `Wake` and `Prime` thread
  `deploymentID` through `Request.DeploymentID` so the ledger counts per-deployment.
* `pkg/sched/floor/trigger.go` walks deployments when `DeploymentStore` is wired;
  dual-emits audit kinds on success.
* `pkg/meter/sampler.go` reads `max(app, deployment)` floor per instance for the
  ADR-060 billing math.
* `pkg/api/dto.go` gains `DeploymentResponse.MinInstances int` and a new
  `UpdateDeploymentRequest` type.
* `cmd/apid/server.go` registers `PATCH /v1/deployments/{id}`.
* `cmd/apid/handlers_ext.go` gains `updateDeploymentMinInstances` and the
  `MinInstances` echo in `deploymentResponse`.
* `cmd/schedd/main.go` passes the store as both `appStore` and `deploymentStore`;
  the `schedFloorLedger` and `schedFloorEngine` adapters gain the two new methods.
* `api/openapi.yaml` + `pkg/apid/openapi.yaml` add the PATCH route and the
  `min_instances` field on `DeploymentResponse`.

## Rejected alternatives

* **Issue #556 traffic-splitting first**: would have made the inheritance rule safer
  (no risk of a customer's first deploy after a traffic split silently waking N extra
  instances). Blocked; we accept the inheritance default as the load-bearing safety
  mechanism and document the customer-visible behaviour in release notes.

* **Per-deployment ScalingPolicy jsonb**: would mirror the apps.scaling_policy shape
  but a deployment row doesn't carry a per-deployment policy (the override columns are
  deliberately flat — the deployment is a single config snapshot, not a scaling-policy
  target). Adding a jsonb column for one integer would widen the surface for no
  consumer benefit.

* **Audit kind straight rename** (drop `floor.wake`, ship only
  `instances.warmed_min_instances`): no deprecation path for any external tooling
  grepping on `floor.wake`. Dual-emit for one release is a 4-line change.

## Downstream

* Migration 00131 lives here; migration 00132 (instances_app_deployment_idx) is the
  partial index backing `ConcurrencyForDeployment`.
* `instances.deployment_id` is already populated by `pkg/state/pgstore.go::CreateInstance`
  (line 5250 — INSERT includes `deployment_id`). No backfill needed.
* Drop `floor.wake` after one release (follow-up PR; the dual-emit doubles the audit
  rate for ~30 days; negligible storage cost).
