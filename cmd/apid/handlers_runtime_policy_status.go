package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	runtimePolicyGatewayFreshness = api.TrafficRuntimeObservationFreshness
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
		databaseNow := now
		if !gateway.DatabaseNow.IsZero() {
			databaseNow = gateway.DatabaseNow
		}
		if observed.IsZero() || observed.Before(databaseNow.Add(-runtimePolicyGatewayFreshness)) || observed.After(databaseNow) {
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
	wait, err := parseRuntimePolicyWait(r.URL.Query().Get("wait"))
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("wait must be a duration between 0s and 10s"))
		return
	}
	deadline := time.Now().Add(wait)
	for {
		status, err := s.readRuntimePolicyStatus(r.Context(), store, app)
		if err != nil {
			s.log.Warn("apid: load runtime policy status failed", "app", app.ID, "err", err)
			api.WriteProblem(w, api.ErrCapacity("runtime policy status is unavailable"))
			return
		}
		if wait == 0 || !runtimePolicyStatusPending(status) || !time.Now().Before(deadline) {
			writeJSON(w, http.StatusOK, status)
			return
		}
		if !waitRuntimePolicyPoll(r.Context(), min(runtimePolicyWaitPoll, time.Until(deadline))) {
			return
		}
	}
}

func parseRuntimePolicyWait(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, nil
	}
	wait, err := time.ParseDuration(raw)
	if err != nil || wait < 0 || wait > runtimePolicyWaitMax {
		return 0, errors.New("invalid runtime policy wait")
	}
	return wait, nil
}

func runtimePolicyStatusPending(s api.RuntimePolicyStatusResponse) bool {
	return s.State == "pending" || s.RequestPolicy.State == "pending" || s.EdgeRules.State == "pending" || s.CorsPresets.State == "pending" || s.ResponseCache.State == "pending" || s.EgressAllowlist.State == "pending" || s.CPULimit.State == "pending" || s.SchedulerScaling.State == "pending"
}

func waitRuntimePolicyPoll(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (s *server) readRuntimePolicyStatus(ctx context.Context, store runtimePolicyStatusStore, app state.App) (api.RuntimePolicyStatusResponse, error) {
	var desired, requestPolicy, edgeRules, corsPresets, cachePurge, egress, cpu int64
	for _, revision := range []struct {
		name   string
		target *int64
		read   func(context.Context, string) (int64, error)
		id     string
	}{
		{"app", &desired, store.LatestAppControlPlaneChangeID, app.ID},
		{"request", &requestPolicy, store.LatestAppRequestPolicyRevision, app.ID},
		{"edge rules", &edgeRules, store.LatestAppEdgeRuleChangeID, app.ID},
		{"CORS", &corsPresets, store.LatestAccountCorsPresetChangeID, app.AccountID},
		{"response cache", &cachePurge, store.LatestAppResponseCachePurgeID, app.ID},
		{"egress", &egress, store.LatestAppEgressPolicyRevision, app.ID},
		{"CPU", &cpu, store.LatestAppCPUPolicyRevision, app.ID},
	} {
		value, err := revision.read(ctx, revision.id)
		if err != nil {
			return api.RuntimePolicyStatusResponse{}, fmt.Errorf("read %s revision: %w", revision.name, err)
		}
		*revision.target = value
	}
	gateways, err := store.ListServingGatewayControlPlaneStates(ctx)
	if err != nil {
		return api.RuntimePolicyStatusResponse{}, fmt.Errorf("read gateway policies: %w", err)
	}
	egressNodes, err := store.ListServingAppEgressPolicyNodeStates(ctx, app.ID)
	if err != nil {
		return api.RuntimePolicyStatusResponse{}, fmt.Errorf("read egress policies: %w", err)
	}
	cpuNodes, err := store.ListServingAppCPUPolicyNodeStates(ctx, app.ID)
	if err != nil {
		return api.RuntimePolicyStatusResponse{}, fmt.Errorf("read CPU policies: %w", err)
	}
	scaling, err := store.GetAppScalingPolicyStatus(ctx, app.ID)
	if err != nil {
		return api.RuntimePolicyStatusResponse{}, fmt.Errorf("read scheduler policy: %w", err)
	}
	status := summarizeRuntimePolicyStatus(app.ID, app.NodeID, desired, requestPolicy, edgeRules, corsPresets, cachePurge, egress, cpu, gateways, egressNodes, cpuNodes, scaling, time.Now().UTC())
	status.TrafficRuntime, err = s.loadTrafficRuntimeStatus(ctx, gateways)
	if err != nil {
		return api.RuntimePolicyStatusResponse{}, fmt.Errorf("read traffic runtime: %w", err)
	}
	return status, nil
}
