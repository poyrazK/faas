package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261111114059: an agent's changes to an app's standing capacity are
// priced against its monthly budget.

// appFloor is the largest number of instances an app's settings keep warm:
// min_instances, the scaling policy's floor or its highest schedule window.
func appFloor(app state.App) int {
	floor := app.MinInstances
	if p := app.ScalingPolicy; p != nil {
		floor = max(floor, p.MinInstances, p.MaxReachableMinInstances())
	}
	return floor
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
	if s.authMw == nil || after <= before {
		return func() {}, false
	}
	return s.authMw.ChargeAgentBudget(w, r, plan, after-before)
}

// chargeAppUpdate prices a PATCH of an app's RAM, min_instances or scaling
// policy.
func (s *server) chargeAppUpdate(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, req api.UpdateAppRequest, ram int) (func(), bool) {
	floor := app.MinInstances
	if req.MinInstances != nil {
		floor = *req.MinInstances
	}
	if req.SetScalingPolicy {
		floor = max(floor, policyFloor(req.ScalingPolicy))
	} else if p := app.ScalingPolicy; p != nil {
		floor = max(floor, p.MinInstances, p.MaxReachableMinInstances())
	}
	return s.chargeAgentBudget(w, r, acct.Plan, standingMillicents(appFloor(app), app.RAMMB, acct.Plan), standingMillicents(floor, ram, acct.Plan))
}

// chargePrewarm prices a prewarm once: the instances it holds above the
// app's own floor, for the length of its window.
func (s *server) chargePrewarm(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, req api.PrewarmRequest) (func(), bool) {
	extra := req.Count - appFloor(app)
	seconds := int64(req.ExpiresAt.Sub(req.WakeAt).Seconds())
	return s.chargeAgentBudget(w, r, acct.Plan, 0, api.AgentPrewarmMillicents(extra, appRAM(app.RAMMB, acct.Plan), seconds))
}
