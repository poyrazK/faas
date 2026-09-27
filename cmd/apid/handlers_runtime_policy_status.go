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
	LatestAppRequestPolicyRevision(context.Context, string) (int64, error)
	LatestAppEdgeRuleChangeID(context.Context, string) (int64, error)
	LatestAccountCorsPresetChangeID(context.Context, string) (int64, error)
	LatestAppResponseCachePurgeID(context.Context, string) (int64, error)
	LatestAppEgressPolicyRevision(context.Context, string) (int64, error)
	LatestAppCPUPolicyRevision(context.Context, string) (int64, error)
	ListServingGatewayControlPlaneStates(context.Context) ([]state.ServingGatewayControlPlaneState, error)
	ListServingAppEgressPolicyNodeStates(context.Context, string) ([]state.AppEgressPolicyNodeState, error)
	ListServingAppCPUPolicyNodeStates(context.Context, string) ([]state.AppCPUPolicyNodeState, error)
	GetAppScalingPolicyStatus(context.Context, string) (state.AppScalingPolicyStatus, error)
}

func summarizeRuntimePolicyComponent(
	scope string,
	desired int64,
	gateways []state.ServingGatewayControlPlaneState,
	now time.Time,
	revision func(state.ServingGatewayControlPlaneState) int64,
	observedAt func(state.ServingGatewayControlPlaneState) time.Time,
) api.RuntimePolicyComponentStatus {
	status := api.RuntimePolicyComponentStatus{
		Scope:           scope,
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

func summarizeRuntimePolicyStatus(appID string, appOwnerNodeID string, desired, requestPolicyDesired, edgeRulesDesired, corsPresetsDesired, cachePurgeDesired, egressDesired, cpuDesired int64, gateways []state.ServingGatewayControlPlaneState, egressNodes []state.AppEgressPolicyNodeState, cpuNodes []state.AppCPUPolicyNodeState, scaling state.AppScalingPolicyStatus, now time.Time) api.RuntimePolicyStatusResponse {
	controlPlaneComponent := summarizeRuntimePolicyComponent("app", desired, gateways, now,
		func(g state.ServingGatewayControlPlaneState) int64 { return g.LastChangeID },
		func(g state.ServingGatewayControlPlaneState) time.Time { return g.ObservedAt })
	requestPolicyComponent := summarizeRuntimePolicyComponent("app", requestPolicyDesired, gateways, now,
		func(g state.ServingGatewayControlPlaneState) int64 { return g.LastChangeID },
		func(g state.ServingGatewayControlPlaneState) time.Time { return g.ObservedAt })
	edgeRulesComponent := summarizeRuntimePolicyComponent("app", edgeRulesDesired, gateways, now,
		func(g state.ServingGatewayControlPlaneState) int64 { return g.LastEdgeRuleChangeID },
		func(g state.ServingGatewayControlPlaneState) time.Time { return g.EdgeRulesObservedAt })
	corsPresetsComponent := summarizeRuntimePolicyComponent("account", corsPresetsDesired, gateways, now,
		func(g state.ServingGatewayControlPlaneState) int64 { return g.LastCorsPresetChangeID },
		func(g state.ServingGatewayControlPlaneState) time.Time { return g.CorsPresetsObservedAt })
	responseCacheComponent := summarizeRuntimePolicyComponent("app", cachePurgeDesired, gateways, now,
		func(g state.ServingGatewayControlPlaneState) int64 { return g.LastResponseCachePurgeID },
		func(g state.ServingGatewayControlPlaneState) time.Time { return g.ResponseCachePurgesObservedAt })
	egressAllowlistComponent := summarizeRuntimePolicyNodeStatus("app", egressDesired, egressNodeObservations(egressNodes), now)
	cpuLimitComponent := summarizeRuntimePolicyNodeStatus("app", cpuDesired, cpuNodeObservations(cpuNodes), now)
	schedulerScalingComponent := summarizeRuntimePolicySchedulerStatus("app", appOwnerNodeID, scaling, now)
	status := api.RuntimePolicyStatusResponse{
		AppID:            appID,
		DesiredRevision:  controlPlaneComponent.DesiredRevision,
		State:            controlPlaneComponent.State,
		Coverage:         []string{"gateway_app_cache", "gateway_request_policy", "deployment_traffic", "response_cache_purge", "app_egress_allowlist", "app_cpu_limit", "scheduler_scaling"},
		ServingGateways:  controlPlaneComponent.ServingGateways,
		AppliedGateways:  controlPlaneComponent.AppliedGateways,
		PendingGateways:  controlPlaneComponent.PendingGateways,
		StaleGateways:    controlPlaneComponent.StaleGateways,
		RequestPolicy:    requestPolicyComponent,
		EdgeRules:        edgeRulesComponent,
		CorsPresets:      corsPresetsComponent,
		ResponseCache:    responseCacheComponent,
		EgressAllowlist:  egressAllowlistComponent,
		CPULimit:         cpuLimitComponent,
		SchedulerScaling: schedulerScalingComponent,
	}
	return status
}

func summarizeRuntimePolicySchedulerStatus(scope, expectedOwnerNodeID string, observed state.AppScalingPolicyStatus, now time.Time) api.RuntimePolicySchedulerStatus {
	status := api.RuntimePolicySchedulerStatus{
		Scope:            scope,
		DesiredRevision:  observed.DesiredRevision,
		ObservedRevision: observed.ObservedRevision,
		State:            "pending",
		SchedulerNodeID:  observed.SchedulerNodeID,
	}
	if observed.DesiredRevision == 0 {
		status.State = "unverified"
		return status
	}
	if observed.ObservedAt.IsZero() || !observed.ObservedAt.After(time.Unix(0, 0)) {
		// PostgreSQL's epoch sentinel represents the absence of an observation.
		if expectedOwnerNodeID == "" {
			status.State = "unverified"
		}
		return status
	}
	observedAt := observed.ObservedAt.UTC()
	status.ObservedAt = &observedAt
	if observedAt.Before(now.Add(-state.AppScalingPolicyObservationFreshness)) {
		status.Stale = true
		return status
	}
	if observed.ObservedRevision < observed.DesiredRevision {
		return status
	}
	if expectedOwnerNodeID != "" && observed.SchedulerNodeID != expectedOwnerNodeID {
		return status
	}
	status.State = "active"
	return status
}

type runtimePolicyNodeObservation struct {
	AppliedRevision int64
	ObservedAt      time.Time
}

func egressNodeObservations(nodes []state.AppEgressPolicyNodeState) []runtimePolicyNodeObservation {
	out := make([]runtimePolicyNodeObservation, len(nodes))
	for i, node := range nodes {
		out[i] = runtimePolicyNodeObservation{AppliedRevision: node.AppliedRevision, ObservedAt: node.ObservedAt}
	}
	return out
}

func cpuNodeObservations(nodes []state.AppCPUPolicyNodeState) []runtimePolicyNodeObservation {
	out := make([]runtimePolicyNodeObservation, len(nodes))
	for i, node := range nodes {
		out[i] = runtimePolicyNodeObservation{AppliedRevision: node.AppliedRevision, ObservedAt: node.ObservedAt}
	}
	return out
}

func summarizeRuntimePolicyNodeStatus(scope string, desired int64, nodes []runtimePolicyNodeObservation, now time.Time) api.RuntimePolicyNodeStatus {
	status := api.RuntimePolicyNodeStatus{
		Scope:           scope,
		DesiredRevision: desired,
		State:           "pending",
		ServingNodes:    len(nodes),
	}
	if desired == 0 || len(nodes) == 0 {
		status.State = "unverified"
		status.PendingNodes = len(nodes)
		return status
	}
	for _, node := range nodes {
		if node.ObservedAt.IsZero() || node.ObservedAt.Before(now.Add(-state.AppEgressPolicyObservationFreshness)) {
			status.StaleNodes++
			status.PendingNodes++
			continue
		}
		if node.AppliedRevision < desired {
			status.PendingNodes++
			continue
		}
		status.AppliedNodes++
	}
	if status.AppliedNodes == len(nodes) {
		status.State = "active"
	}
	return status
}

// getRuntimePolicyStatus reports observed application of independent gateway
// policy components, the app egress allowlist, and the mutable CPU cgroup
// ceiling on live VM nodes. It does not attest every host firewall rule or
// guest-process setting. A short optional wait lets clients poll without
// inventing a deployment or holding a mutation transaction open.
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
		requestPolicyDesired, err := store.LatestAppRequestPolicyRevision(r.Context(), app.ID)
		if err != nil {
			s.log.Warn("apid: load gateway request policy revision failed", "app", app.ID, "err", err)
			api.WriteProblem(w, api.ErrCapacity("runtime policy status is unavailable"))
			return
		}
		edgeRulesDesired, err := store.LatestAppEdgeRuleChangeID(r.Context(), app.ID)
		if err != nil {
			s.log.Warn("apid: load edge-rule policy revision failed", "app", app.ID, "err", err)
			api.WriteProblem(w, api.ErrCapacity("runtime policy status is unavailable"))
			return
		}
		corsPresetsDesired, err := store.LatestAccountCorsPresetChangeID(r.Context(), app.AccountID)
		if err != nil {
			s.log.Warn("apid: load CORS preset policy revision failed", "account", app.AccountID, "err", err)
			api.WriteProblem(w, api.ErrCapacity("runtime policy status is unavailable"))
			return
		}
		cachePurgeDesired, err := store.LatestAppResponseCachePurgeID(r.Context(), app.ID)
		if err != nil {
			s.log.Warn("apid: load response-cache purge revision failed", "app", app.ID, "err", err)
			api.WriteProblem(w, api.ErrCapacity("runtime policy status is unavailable"))
			return
		}
		egressDesired, err := store.LatestAppEgressPolicyRevision(r.Context(), app.ID)
		if err != nil {
			s.log.Warn("apid: load app egress policy revision failed", "app", app.ID, "err", err)
			api.WriteProblem(w, api.ErrCapacity("runtime policy status is unavailable"))
			return
		}
		cpuDesired, err := store.LatestAppCPUPolicyRevision(r.Context(), app.ID)
		if err != nil {
			s.log.Warn("apid: load app CPU policy revision failed", "app", app.ID, "err", err)
			api.WriteProblem(w, api.ErrCapacity("runtime policy status is unavailable"))
			return
		}
		gateways, err := store.ListServingGatewayControlPlaneStates(r.Context())
		if err != nil {
			s.log.Warn("apid: load gateway runtime policy states failed", "app", app.ID, "err", err)
			api.WriteProblem(w, api.ErrCapacity("runtime policy status is unavailable"))
			return
		}
		appEgressNodes, err := store.ListServingAppEgressPolicyNodeStates(r.Context(), app.ID)
		if err != nil {
			s.log.Warn("apid: load app egress policy node states failed", "app", app.ID, "err", err)
			api.WriteProblem(w, api.ErrCapacity("runtime policy status is unavailable"))
			return
		}
		appCPUNodes, err := store.ListServingAppCPUPolicyNodeStates(r.Context(), app.ID)
		if err != nil {
			s.log.Warn("apid: load app CPU policy node states failed", "app", app.ID, "err", err)
			api.WriteProblem(w, api.ErrCapacity("runtime policy status is unavailable"))
			return
		}
		scaling, err := store.GetAppScalingPolicyStatus(r.Context(), app.ID)
		if err != nil {
			s.log.Warn("apid: load scheduler scaling policy status failed", "app", app.ID, "err", err)
			api.WriteProblem(w, api.ErrCapacity("runtime policy status is unavailable"))
			return
		}
		status := summarizeRuntimePolicyStatus(app.ID, app.NodeID, desired, requestPolicyDesired, edgeRulesDesired, corsPresetsDesired, cachePurgeDesired, egressDesired, cpuDesired, gateways, appEgressNodes, appCPUNodes, scaling, time.Now().UTC())
		if wait == 0 || (status.State != "pending" && status.RequestPolicy.State != "pending" && status.EdgeRules.State != "pending" && status.CorsPresets.State != "pending" && status.ResponseCache.State != "pending" && status.EgressAllowlist.State != "pending" && status.CPULimit.State != "pending" && status.SchedulerScaling.State != "pending") || !time.Now().Before(deadline) {
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
