package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261111114059: an agent's changes to an app's standing capacity are
// priced against its monthly budget.

// appFloor is the largest number of instances an app's settings keep warm:
// min_instances, the scaling policy's floor or its highest schedule window,
// and the replicas a service or worker always runs.
func appFloor(app state.App) int {
	floor := app.MinInstances
	if p := app.ScalingPolicy; p != nil {
		floor = max(floor, p.MinInstances, p.MaxReachableMinInstances())
	}
	return max(floor, modeFloor(apiManifestFromState(app.Manifest), app.ScalingPolicy != nil))
}

// modeFloor is how many instances an app's execution mode keeps running
// whatever its traffic: a service runs its desired replicas (one when it
// declares none), a worker its minimum (one when it declares neither
// replicas nor a scaling policy, whose own floor appFloor already counts).
// Request and job apps keep none.
func modeFloor(m api.AppManifest, hasPolicy bool) int {
	switch m.EffectiveExecutionMode() {
	case api.ExecutionModeService:
		if m.ServiceReplicas == nil {
			return 1
		}
		return max(m.ServiceReplicas.Desired, 0)
	case api.ExecutionModeWorker:
		switch {
		case m.WorkerReplicas != nil:
			return max(m.WorkerReplicas.Min, 0)
		case hasPolicy:
			return 0
		default:
			return 1
		}
	}
	return 0
}

// policyFloor is a requested scaling policy's floor.
func policyFloor(p *api.ScalingPolicy) int {
	if p == nil {
		return 0
	}
	return max(p.MinInstances, p.MaxReachableMinInstances())
}

// appRAM is the RAM an app's instances are billed at: an unset size is the
// plan's default.
func appRAM(ramMB int, plan api.Plan) int {
	if ramMB > 0 {
		return ramMB
	}
	if limits, ok := api.LimitsFor(plan); ok {
		return api.StandaloneAppDefaultRAMMB(limits)
	}
	return api.StandaloneAppDefaultMaxRAMMB
}

// standingMillicents is what floor instances of ramMB cost a month.
func standingMillicents(floor, ramMB int, plan api.Plan) int64 {
	return api.AgentStandingMillicents(floor, appRAM(ramMB, plan))
}

// chargeAgentBudget charges an agent key for raising standing cost from
// before to after. answered reports that a 428 or an error was written.
func (s *server) chargeAgentBudget(w http.ResponseWriter, r *http.Request, plan api.Plan, before, after int64) (commit func(), answered bool) {
	if s.authMw != nil && authmw.Previewing(r) {
		// A preview is answered even for a change that adds nothing.
		return s.authMw.ChargeAgentBudget(w, r, plan, after-before)
	}
	if s.authMw == nil || after <= before {
		return func() {}, false
	}
	return s.authMw.ChargeAgentBudget(w, r, plan, after-before)
}

// chargeAppUpdate prices a PATCH of an app's RAM, min_instances, scaling
// policy, execution mode or service and worker replicas.
func (s *server) chargeAppUpdate(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, req api.UpdateAppRequest, ram int) (func(), bool) {
	floor := app.MinInstances
	if req.MinInstances != nil {
		floor = *req.MinInstances
	}
	hasPolicy := app.ScalingPolicy != nil
	if req.SetScalingPolicy {
		hasPolicy = req.ScalingPolicy != nil
		floor = max(floor, policyFloor(req.ScalingPolicy))
	} else if p := app.ScalingPolicy; p != nil {
		floor = max(floor, p.MinInstances, p.MaxReachableMinInstances())
	}
	manifest, changed := mergedLifecycleManifest(app, &req)
	if !changed {
		manifest = apiManifestFromState(app.Manifest)
	}
	floor = max(floor, modeFloor(manifest, hasPolicy))
	return s.chargeAgentBudget(w, r, acct.Plan, standingMillicents(appFloor(app), app.RAMMB, acct.Plan), standingMillicents(floor, ram, acct.Plan))
}

// chargeAppCreate prices a new app: one that is a service or worker runs
// its replicas from the start. A request app commits nothing until its
// floor is raised.
func (s *server) chargeAppCreate(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App) (func(), bool) {
	return s.chargeAgentBudget(w, r, acct.Plan, 0, standingMillicents(appFloor(app), app.RAMMB, acct.Plan))
}

// chargeDeploymentFloor prices a deployment's own min_instances: its
// instances run at the larger of it and the app's floor.
func (s *server) chargeDeploymentFloor(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, d state.Deployment, minInstances int) (func(), bool) {
	floor := appFloor(app)
	return s.chargeAgentBudget(w, r, acct.Plan,
		standingMillicents(max(floor, d.EffectiveMinInstances()), app.RAMMB, acct.Plan),
		standingMillicents(max(floor, minInstances), app.RAMMB, acct.Plan))
}

// chargePrewarm prices a prewarm once: the instances it holds above the
// app's own floor, for the length of its window.
func (s *server) chargePrewarm(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, req api.PrewarmRequest) (func(), bool) {
	extra := req.Count - appFloor(app)
	seconds := int64(req.ExpiresAt.Sub(req.WakeAt).Seconds())
	return s.chargeAgentBudget(w, r, acct.Plan, 0, api.AgentPrewarmMillicents(extra, appRAM(app.RAMMB, acct.Plan), seconds))
}
