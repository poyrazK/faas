package main

import (
	"context"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	runtimePolicyGatewayFreshness = 10 * time.Second
	runtimePolicyWaitMax          = 10 * time.Second
	runtimePolicyWaitPoll         = 250 * time.Millisecond
)

type runtimePolicyStatusStore interface {
	LatestAppControlPlaneChangeID(context.Context, string) (int64, error)
	LatestAppEdgeRuleChangeID(context.Context, string) (int64, error)
	ListServingGatewayControlPlaneStates(context.Context) ([]state.ServingGatewayControlPlaneState, error)
}

func summarizeRuntimePolicyComponent(
	desired int64,
	gateways []state.ServingGatewayControlPlaneState,
	now time.Time,
	revision func(state.ServingGatewayControlPlaneState) int64,
	observedAt func(state.ServingGatewayControlPlaneState) time.Time,
) api.RuntimePolicyComponentStatus {
	status := api.RuntimePolicyComponentStatus{
		DesiredRevision: desired,
		State:           "pending",
		ServingGateways: len(gateways),
	}
	if desired == 0 {
		status.State = "unverified"
		status.PendingGateways = len(gateways)
		return status
	}
	for _, gateway := range gateways {
		observed := observedAt(gateway)
		if observed.IsZero() || observed.Before(now.Add(-runtimePolicyGatewayFreshness)) {
			status.StaleGateways++
			status.PendingGateways++
			continue
		}
		if revision(gateway) < desired {
			status.PendingGateways++
			continue
		}
		status.AppliedGateways++
	}
	if len(gateways) == 0 {
		status.State = "unverified"
	} else if status.AppliedGateways == len(gateways) {
		status.State = "active"
	}
	return status
}

func summarizeRuntimePolicyStatus(appID string, desired, edgeRulesDesired int64, gateways []state.ServingGatewayControlPlaneState, now time.Time) api.RuntimePolicyStatusResponse {
	controlPlaneComponent := summarizeRuntimePolicyComponent(desired, gateways, now,
		func(g state.ServingGatewayControlPlaneState) int64 { return g.LastChangeID },
		func(g state.ServingGatewayControlPlaneState) time.Time { return g.ObservedAt })
	edgeRulesComponent := summarizeRuntimePolicyComponent(edgeRulesDesired, gateways, now,
		func(g state.ServingGatewayControlPlaneState) int64 { return g.LastEdgeRuleChangeID },
		func(g state.ServingGatewayControlPlaneState) time.Time { return g.EdgeRulesObservedAt })
	status := api.RuntimePolicyStatusResponse{
		AppID:           appID,
		DesiredRevision: controlPlaneComponent.DesiredRevision,
		State:           controlPlaneComponent.State,
		Coverage:        []string{"gateway_app_cache", "deployment_traffic"},
		ServingGateways: controlPlaneComponent.ServingGateways,
		AppliedGateways: controlPlaneComponent.AppliedGateways,
		PendingGateways: controlPlaneComponent.PendingGateways,
		StaleGateways:   controlPlaneComponent.StaleGateways,
		EdgeRules:       edgeRulesComponent,
	}
	return status
}

// getRuntimePolicyStatus reports observed gateway application of app cache
// invalidations and traffic weights, plus edge-rule convergence in its own
// revision domain. It does not attest scheduler, VM, or guest-side policy
// convergence. A short optional wait lets clients poll without inventing a
// deployment or holding a mutation transaction open.
func (s *server) getRuntimePolicyStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(runtimePolicyStatusStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("runtime policy status is unavailable"))
		return
	}
	wait := time.Duration(0)
	if raw := r.URL.Query().Get("wait"); raw != "" {
		var err error
		wait, err = time.ParseDuration(raw)
		if err != nil || wait < 0 || wait > runtimePolicyWaitMax {
			api.WriteProblem(w, api.ErrValidation("wait must be a duration between 0s and 10s"))
			return
		}
	}
	deadline := time.Time{}
	if wait > 0 {
		deadline = time.Now().Add(wait)
	}
	for {
		desired, err := store.LatestAppControlPlaneChangeID(r.Context(), app.ID)
		if err != nil {
			s.log.Warn("apid: load runtime policy revision failed", "app", app.ID, "err", err)
			api.WriteProblem(w, api.ErrCapacity("runtime policy status is unavailable"))
			return
		}
		edgeRulesDesired, err := store.LatestAppEdgeRuleChangeID(r.Context(), app.ID)
		if err != nil {
			s.log.Warn("apid: load edge-rule policy revision failed", "app", app.ID, "err", err)
			api.WriteProblem(w, api.ErrCapacity("runtime policy status is unavailable"))
			return
		}
		gateways, err := store.ListServingGatewayControlPlaneStates(r.Context())
		if err != nil {
			s.log.Warn("apid: load gateway runtime policy states failed", "app", app.ID, "err", err)
			api.WriteProblem(w, api.ErrCapacity("runtime policy status is unavailable"))
			return
		}
		status := summarizeRuntimePolicyStatus(app.ID, desired, edgeRulesDesired, gateways, time.Now().UTC())
		if wait == 0 || (status.State != "pending" && status.EdgeRules.State != "pending") || !time.Now().Before(deadline) {
			writeJSON(w, http.StatusOK, status)
			return
		}
		delay := min(runtimePolicyWaitPoll, time.Until(deadline))
		timer := time.NewTimer(delay)
		select {
		case <-r.Context().Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
		}
	}
}
