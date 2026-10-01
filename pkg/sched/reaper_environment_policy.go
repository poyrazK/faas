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
	for i := range snapshot {
		row := &snapshot[i]
		app := byApp[row.AppID]
		if row.DeploymentID != "" {
			policy, cached := policies[row.DeploymentID]
			if !cached {
				policy.app = app
				policy.deployment, policy.err = store.DeploymentByID(ctx, row.DeploymentID)
				if policy.err == nil && policy.deployment.AppID != app.ID {
					policy.err = state.ErrConflict
				}
				if policy.err == nil {
					policy.scope = normalizedDeploymentScope(policy.deployment.Scope)
					policy.app, policy.err = state.ResolveAppForDeployment(ctx, store, app, policy.deployment)
				}
				if policy.err == nil {
					var owner state.RuntimeAppValuesSnapshot
					owner, policy.err = store.RuntimeAppValuesForDeployment(ctx, app.AccountID, app.ID, row.DeploymentID)
					if policy.err == nil && (owner.AccountID != app.AccountID || owner.AppID != app.ID || owner.DeploymentID != row.DeploymentID || owner.Scope != policy.scope || api.ValidateScope(owner.Scope) != nil) {
						policy.err = state.ErrConflict
					}
					policy.environmentID = owner.EnvironmentID
				}
				if policy.err == nil {
					policy.app, policy.err = l.engine.resolveRuntimeScalingPolicy(ctx, policy.app, policy.deployment)
				}
				policies[row.DeploymentID] = policy
				if policy.err != nil {
					l.log.Warn("reaper: original workload policy unavailable", "app", app.ID, "deployment", row.DeploymentID, "err", policy.err)
				}
			}
			row.Scope, row.EnvironmentID = policy.scope, policy.environmentID
			if policy.err != nil || policy.app.ID != row.AppID || policy.app.AccountID != app.AccountID {
				row.PolicyUnavailable = true
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
		row.MinInstances = max(row.MinInstances, row.ConfiguredMinInstances)
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
