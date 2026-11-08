package sched

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func reaperProductionScope(scope string) bool {
	return scope == "" || scope == "default" || scope == "production"
}

func reaperEnvironmentKey(instance InstanceInfo) string {
	if instance.EnvironmentID != "" {
		return instance.AppID + "\x00environment:" + instance.EnvironmentID
	}
	if reaperProductionScope(instance.Scope) {
		return instance.AppID
	}
	return instance.AppID + "\x00scope:" + instance.Scope
}

// reaperDeploymentEnded reports a deployment that will never serve again
// and has no runtime policy to resolve. Production-us hunt #9: a rollback
// cancelled mid-flight left its target's running instance with no runtime
// values, so every tick logged "policy unavailable", the reaper skipped it,
// and it held the app's only concurrency slot for 36 minutes.
func reaperDeploymentEnded(status state.DeploymentStatus) bool {
	return status == state.DeployCancelled || status == state.DeployFailed
}

// reaperAbortedCanary reports a canary candidate the circuit breaker aborted:
// still live but at 0% traffic, so nothing routes to it again unless an
// operator recovers it. Production-us hunt #9: the aborted candidate's idle
// instance kept the rollout grant slot of a max_concurrency=1 app until the
// idle timeout, so the next deploy's hosting smoke answered 429 for four
// minutes. It drains like a superseded revision, keeping its snapshot.
func reaperAbortedCanary(d state.Deployment) bool {
	return d.Status == state.DeployLive && d.TrafficPercent == 0 && state.AbortedCanaryCandidate(d)
}

type reaperDeploymentPolicy struct {
	app           state.App
	deployment    state.Deployment
	scope         string
	environmentID string
	err           error
}

// enrichReaperEnvironmentPolicies preserves the physical instance identity and
// resolves each deployment once per tick. No current desired head is adopted.
// The returned app map is production-only compatibility data for floor audits.
func (l *Loop) enrichReaperEnvironmentPolicies(ctx context.Context, apps []state.App, snapshot []InstanceInfo, prewarmFloors map[string]int) map[string]int {
	store := l.engine.Store()
	byApp := make(map[string]state.App, len(apps))
	for _, app := range apps {
		byApp[app.ID] = app
	}
	policies := make(map[string]reaperDeploymentPolicy)
	floors := make(map[string]int)
	deployments := l.reaperDeployments(ctx, snapshot)
	for i := range snapshot {
		row := &snapshot[i]
		app := byApp[row.AppID]
		if row.DeploymentID != "" {
			policy, cached := policies[row.DeploymentID]
			if !cached {
				policy.app = app
				if d, ok := deployments[row.DeploymentID]; ok {
					policy.deployment = d
				} else {
					policy.deployment, policy.err = store.DeploymentByID(ctx, row.DeploymentID)
				}
				if policy.err == nil && policy.deployment.AppID != app.ID {
					policy.err = state.ErrConflict
				}
				if policy.err == nil {
					policy.scope = normalizedDeploymentScope(policy.deployment.Scope)
				}
				// A cancelled or failed deployment has no runtime policy left to
				// resolve; its instances only need retiring.
				resolve := policy.err == nil && !reaperDeploymentEnded(policy.deployment.Status)
				if resolve {
					policy.app, policy.err = state.ResolveAppForDeployment(ctx, store, app, policy.deployment)
				}
				// The scaling state resolves the deployment's runtime owner with
				// the same query shape as RuntimeAppValuesForDeployment, which
				// this used to read too, with every sealed secret, only for the
				// scope and environment (ADR 20261102205122).
				if resolve && policy.err == nil {
					var scaling state.RuntimeScalingState
					scaling, policy.err = l.engine.runtimeScalingStateForDeployment(ctx, policy.app, policy.deployment)
					if policy.err == nil && (scaling.AccountID != app.AccountID || scaling.AppID != app.ID || scaling.DeploymentID != row.DeploymentID || scaling.Scope != policy.scope || api.ValidateScope(scaling.Scope) != nil) {
						policy.err = state.ErrConflict
					}
					policy.environmentID = scaling.EnvironmentID
					policy.app.LastScaleInAt, policy.app.LastScaleOutAt = scaling.LastScaleInAt, scaling.LastScaleOutAt
				}
				policies[row.DeploymentID] = policy
				if policy.err != nil {
					l.log.Warn("reaper: original workload policy unavailable", "app", app.ID, "deployment", row.DeploymentID, "err", policy.err)
				}
			}
			row.Scope, row.EnvironmentID = policy.scope, policy.environmentID
			if policy.err == nil && reaperDeploymentEnded(policy.deployment.Status) {
				row.DeploymentEnded = true
				continue
			}
			if policy.err != nil || policy.app.ID != row.AppID || policy.app.AccountID != app.AccountID {
				row.PolicyUnavailable = true
				continue
			}
			if policy.deployment.Status == state.DeploySuperseded || reaperAbortedCanary(policy.deployment) {
				row.DeploymentSuperseded = true
				continue
			}
			app = policy.app
			row.MinInstances = policy.deployment.EffectiveMinInstances()
		} else {
			// Compatibility for historical instance rows without a deployment.
			row.MinInstances = 0
		}
		row.IdleTimeoutS = app.IdleTimeoutS
		row.WarmPoolSize = app.WarmPoolSize
		row.WorkloadClass = app.WorkloadClass
		row.EvictionPriority = app.EvictionPriority
		row.LastScaleInAt, row.LastScaleOutAt = app.LastScaleInAt, app.LastScaleOutAt
		row.ScaleInCooldownS = state.ScalingPolicyOrDefault(app.ScalingPolicy).ScaleInCooldownS
		row.ConfiguredMinInstances = app.EffectiveMinInstances()
		row.MinInstances = planCappedFloor(row.Plan, max(row.MinInstances, row.ConfiguredMinInstances))
		row.PrewarmMinInstances = 0
		if reaperProductionScope(row.Scope) {
			row.PrewarmMinInstances = prewarmFloors[row.AppID]
			row.MinInstances = max(row.MinInstances, row.PrewarmMinInstances)
		}
		key := reaperEnvironmentKey(*row)
		floors[key] = max(floors[key], row.MinInstances)
	}
	productionFloors := make(map[string]int)
	for i := range snapshot {
		row := &snapshot[i]
		if row.PolicyUnavailable {
			continue
		}
		row.MinInstances = floors[reaperEnvironmentKey(*row)]
		if reaperProductionScope(row.Scope) {
			productionFloors[row.AppID] = max(productionFloors[row.AppID], row.MinInstances)
		}
	}
	return productionFloors
}

// reaperDeployments reads the tick's deployments in one query when the store
// can (ADR 20261102205122). A deployment missing from the result, or every
// one after a failed read, is read on its own as before.
func (l *Loop) reaperDeployments(ctx context.Context, snapshot []InstanceInfo) map[string]state.Deployment {
	reader, ok := l.engine.Store().(state.DeploymentsByIDsReader)
	if !ok {
		return nil
	}
	seen := make(map[string]struct{}, len(snapshot))
	ids := make([]string, 0, len(snapshot))
	for _, row := range snapshot {
		if _, dup := seen[row.DeploymentID]; row.DeploymentID != "" && !dup {
			seen[row.DeploymentID] = struct{}{}
			ids = append(ids, row.DeploymentID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	deployments, err := reader.DeploymentsByIDs(ctx, ids)
	if err != nil {
		l.log.Warn("reaper: bulk deployment read; reading each", "err", err)
		return nil
	}
	return deployments
}

// planCappedFloor caps a configured min-instances floor at what plan allows
// (ADR-20261105120000). A floor saved under a larger plan otherwise kept that
// many instances from idling out after a downgrade. An unresolved plan ("")
// leaves the floor as configured, as a failed plan read does elsewhere in the
// reaper. Prewarm floors are not configured floors and are not capped here.
func planCappedFloor(plan api.Plan, floor int) int {
	if plan == "" {
		return floor
	}
	if !plan.MinInstancesAllowed() {
		return 0
	}
	return min(floor, plan.MaxMinInstances())
}
