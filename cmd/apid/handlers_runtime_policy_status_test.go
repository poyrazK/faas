package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type runtimePolicyStatusTestStore struct {
	state.Store
	desired              int64
	requestPolicyDesired int64
	edgeRulesDesired     int64
	corsPresetsDesired   int64
	cachePurgeDesired    int64
	egressDesired        int64
	cpuDesired           int64
	gateways             []state.ServingGatewayControlPlaneState
	egressNodes          []state.AppEgressPolicyNodeState
	cpuNodes             []state.AppCPUPolicyNodeState
	scalingStatus        state.AppScalingPolicyStatus
	reads                int
	queriedAccountID     string
}

func (s *runtimePolicyStatusTestStore) LatestAppControlPlaneChangeID(context.Context, string) (int64, error) {
	return s.desired, nil
}

func (s *runtimePolicyStatusTestStore) LatestAppRequestPolicyRevision(context.Context, string) (int64, error) {
	return s.requestPolicyDesired, nil
}

func (s *runtimePolicyStatusTestStore) LatestAppEdgeRuleChangeID(context.Context, string) (int64, error) {
	return s.edgeRulesDesired, nil
}

func (s *runtimePolicyStatusTestStore) LatestAccountCorsPresetChangeID(_ context.Context, accountID string) (int64, error) {
	s.queriedAccountID = accountID
	return s.corsPresetsDesired, nil
}

func (s *runtimePolicyStatusTestStore) LatestAppResponseCachePurgeID(context.Context, string) (int64, error) {
	return s.cachePurgeDesired, nil
}

func (s *runtimePolicyStatusTestStore) LatestAppEgressPolicyRevision(context.Context, string) (int64, error) {
	return s.egressDesired, nil
}

func (s *runtimePolicyStatusTestStore) LatestAppCPUPolicyRevision(context.Context, string) (int64, error) {
	return s.cpuDesired, nil
}

func (s *runtimePolicyStatusTestStore) ListServingGatewayControlPlaneStates(context.Context) ([]state.ServingGatewayControlPlaneState, error) {
	s.reads++
	if s.reads > 1 {
		for i := range s.gateways {
			s.gateways[i].LastChangeID = s.desired
			s.gateways[i].LastEdgeRuleChangeID = s.edgeRulesDesired
			s.gateways[i].LastCorsPresetChangeID = s.corsPresetsDesired
			s.gateways[i].LastResponseCachePurgeID = s.cachePurgeDesired
		}
		for i := range s.egressNodes {
			s.egressNodes[i].AppliedRevision = s.egressDesired
			s.egressNodes[i].ObservedAt = time.Now().UTC()
		}
		for i := range s.cpuNodes {
			s.cpuNodes[i].AppliedRevision = s.cpuDesired
			s.cpuNodes[i].ObservedAt = time.Now().UTC()
		}
	}
	return s.gateways, nil
}

func (s *runtimePolicyStatusTestStore) ListServingAppEgressPolicyNodeStates(context.Context, string) ([]state.AppEgressPolicyNodeState, error) {
	return s.egressNodes, nil
}

func (s *runtimePolicyStatusTestStore) ListServingAppCPUPolicyNodeStates(context.Context, string) ([]state.AppCPUPolicyNodeState, error) {
	return s.cpuNodes, nil
}

func (s *runtimePolicyStatusTestStore) GetAppScalingPolicyStatus(context.Context, string) (state.AppScalingPolicyStatus, error) {
	if s.reads > 1 {
		s.scalingStatus.ObservedRevision = s.scalingStatus.DesiredRevision
		s.scalingStatus.ObservedAt = time.Now().UTC()
	}
	return s.scalingStatus, nil
}

func TestSummarizeRuntimePolicyStatus(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name               string
		desired            int64
		edgeRulesDesired   int64
		corsPresetsDesired int64
		gateways           []state.ServingGatewayControlPlaneState
		state              string
		applied            int
		stale              int
		edgeState          string
		edgeApplied        int
		edgeStale          int
		corsState          string
		corsApplied        int
		corsStale          int
		cachePurgeDesired  int64
		cachePurgeState    string
		cachePurgeApplied  int
		cachePurgeStale    int
		egressDesired      int64
		egressNodes        []state.AppEgressPolicyNodeState
		egressState        string
		egressApplied      int
		egressStale        int
		cpuDesired         int64
		cpuNodes           []state.AppCPUPolicyNodeState
		cpuState           string
		cpuApplied         int
		cpuStale           int
	}{
		{name: "no serving fleet", desired: 12, edgeRulesDesired: 23, corsPresetsDesired: 31, cachePurgeDesired: 41, state: "unverified", edgeState: "unverified", corsState: "unverified", cachePurgeState: "unverified", egressDesired: 5, egressState: "unverified"},
		{name: "no revision", gateways: []state.ServingGatewayControlPlaneState{{NodeName: "n1", LastChangeID: 12, ObservedAt: now}}, state: "unverified", edgeState: "unverified", corsState: "unverified", cachePurgeState: "unverified", egressNodes: []state.AppEgressPolicyNodeState{{NodeName: "vm1", DesiredRevision: 5, AppliedRevision: 5, ObservedAt: now}}, egressState: "unverified"},
		{name: "all applied", desired: 12, edgeRulesDesired: 23, corsPresetsDesired: 31, cachePurgeDesired: 41, gateways: []state.ServingGatewayControlPlaneState{{NodeName: "n1", LastChangeID: 12, ObservedAt: now, LastEdgeRuleChangeID: 23, EdgeRulesObservedAt: now, LastCorsPresetChangeID: 31, CorsPresetsObservedAt: now, LastResponseCachePurgeID: 41, ResponseCachePurgesObservedAt: now}, {NodeName: "n2", LastChangeID: 14, ObservedAt: now, LastEdgeRuleChangeID: 25, EdgeRulesObservedAt: now, LastCorsPresetChangeID: 34, CorsPresetsObservedAt: now, LastResponseCachePurgeID: 42, ResponseCachePurgesObservedAt: now}}, state: "active", applied: 2, edgeState: "active", edgeApplied: 2, corsState: "active", corsApplied: 2, cachePurgeState: "active", cachePurgeApplied: 2, egressDesired: 7, egressNodes: []state.AppEgressPolicyNodeState{{NodeName: "vm1", DesiredRevision: 7, AppliedRevision: 7, ObservedAt: now}}, egressState: "active", egressApplied: 1, cpuDesired: 3, cpuNodes: []state.AppCPUPolicyNodeState{{NodeName: "vm1", DesiredRevision: 3, AppliedRevision: 3, ObservedAt: now}}, cpuState: "active", cpuApplied: 1},
		{name: "egress policy pending on live node", state: "unverified", edgeState: "unverified", corsState: "unverified", egressDesired: 8, egressNodes: []state.AppEgressPolicyNodeState{{NodeName: "vm1", DesiredRevision: 8, AppliedRevision: 7, ObservedAt: now}}, egressState: "pending"},
		{name: "egress policy observation stale", state: "unverified", edgeState: "unverified", corsState: "unverified", egressDesired: 8, egressNodes: []state.AppEgressPolicyNodeState{{NodeName: "vm1", DesiredRevision: 8, AppliedRevision: 8, ObservedAt: now.Add(-2 * time.Minute)}}, egressState: "pending", egressStale: 1},
		{name: "CPU policy pending on live node", state: "unverified", edgeState: "unverified", corsState: "unverified", cpuDesired: 4, cpuNodes: []state.AppCPUPolicyNodeState{{NodeName: "vm1", DesiredRevision: 4, AppliedRevision: 3, ObservedAt: now}}, cpuState: "pending"},
		{name: "CPU policy observation stale", state: "unverified", edgeState: "unverified", corsState: "unverified", cpuDesired: 4, cpuNodes: []state.AppCPUPolicyNodeState{{NodeName: "vm1", DesiredRevision: 4, AppliedRevision: 4, ObservedAt: now.Add(-2 * time.Minute)}}, cpuState: "pending", cpuStale: 1},
		{name: "control plane lagging", desired: 12, edgeRulesDesired: 23, corsPresetsDesired: 31, gateways: []state.ServingGatewayControlPlaneState{{NodeName: "n1", LastChangeID: 12, ObservedAt: now, LastEdgeRuleChangeID: 23, EdgeRulesObservedAt: now, LastCorsPresetChangeID: 31, CorsPresetsObservedAt: now}, {NodeName: "n2", LastChangeID: 11, ObservedAt: now, LastEdgeRuleChangeID: 23, EdgeRulesObservedAt: now, LastCorsPresetChangeID: 31, CorsPresetsObservedAt: now}}, state: "pending", applied: 1, edgeState: "active", edgeApplied: 2, corsState: "active", corsApplied: 2},
		{name: "edge rules lagging", desired: 12, edgeRulesDesired: 23, corsPresetsDesired: 31, gateways: []state.ServingGatewayControlPlaneState{{NodeName: "n1", LastChangeID: 12, ObservedAt: now, LastEdgeRuleChangeID: 22, EdgeRulesObservedAt: now, LastCorsPresetChangeID: 31, CorsPresetsObservedAt: now}}, state: "active", applied: 1, edgeState: "pending", corsState: "active", corsApplied: 1},
		{name: "stale control-plane observation", desired: 12, edgeRulesDesired: 23, corsPresetsDesired: 31, gateways: []state.ServingGatewayControlPlaneState{{NodeName: "n1", LastChangeID: 12, ObservedAt: now.Add(-time.Minute), LastEdgeRuleChangeID: 23, EdgeRulesObservedAt: now, LastCorsPresetChangeID: 31, CorsPresetsObservedAt: now}}, state: "pending", stale: 1, edgeState: "active", edgeApplied: 1, corsState: "active", corsApplied: 1},
		{name: "stale edge-rule observation", desired: 12, edgeRulesDesired: 23, corsPresetsDesired: 31, gateways: []state.ServingGatewayControlPlaneState{{NodeName: "n1", LastChangeID: 12, ObservedAt: now, LastEdgeRuleChangeID: 23, EdgeRulesObservedAt: now.Add(-time.Minute), LastCorsPresetChangeID: 31, CorsPresetsObservedAt: now}}, state: "active", applied: 1, edgeState: "pending", edgeStale: 1, corsState: "active", corsApplied: 1},
		{name: "stale CORS preset observation", desired: 12, edgeRulesDesired: 23, corsPresetsDesired: 31, gateways: []state.ServingGatewayControlPlaneState{{NodeName: "n1", LastChangeID: 12, ObservedAt: now, LastEdgeRuleChangeID: 23, EdgeRulesObservedAt: now, LastCorsPresetChangeID: 31, CorsPresetsObservedAt: now.Add(-time.Minute)}}, state: "active", applied: 1, edgeState: "active", edgeApplied: 1, corsState: "pending", corsStale: 1},
		{name: "cache purge lagging", cachePurgeDesired: 41, gateways: []state.ServingGatewayControlPlaneState{{NodeName: "n1", LastResponseCachePurgeID: 40, ResponseCachePurgesObservedAt: now}}, state: "unverified", edgeState: "unverified", corsState: "unverified", cachePurgeState: "pending"},
		{name: "stale cache purge observation", cachePurgeDesired: 41, gateways: []state.ServingGatewayControlPlaneState{{NodeName: "n1", LastResponseCachePurgeID: 41, ResponseCachePurgesObservedAt: now.Add(-time.Minute)}}, state: "unverified", edgeState: "unverified", corsState: "unverified", cachePurgeState: "pending", cachePurgeStale: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := summarizeRuntimePolicyStatus("app-1", "", tc.desired, tc.desired, tc.edgeRulesDesired, tc.corsPresetsDesired, tc.cachePurgeDesired, tc.egressDesired, tc.cpuDesired, tc.gateways, tc.egressNodes, tc.cpuNodes,
				state.AppScalingPolicyStatus{DesiredRevision: 1, ObservedRevision: 1, ObservedAt: now}, now)
			if got.State != tc.state || got.AppliedGateways != tc.applied || got.StaleGateways != tc.stale ||
				got.EdgeRules.State != tc.edgeState || got.EdgeRules.AppliedGateways != tc.edgeApplied || got.EdgeRules.StaleGateways != tc.edgeStale ||
				got.CorsPresets.State != tc.corsState || got.CorsPresets.AppliedGateways != tc.corsApplied || got.CorsPresets.StaleGateways != tc.corsStale ||
				(tc.cachePurgeState != "" && (got.ResponseCache.State != tc.cachePurgeState || got.ResponseCache.AppliedGateways != tc.cachePurgeApplied || got.ResponseCache.StaleGateways != tc.cachePurgeStale)) ||
				(tc.egressState != "" && (got.EgressAllowlist.State != tc.egressState || got.EgressAllowlist.AppliedNodes != tc.egressApplied || got.EgressAllowlist.StaleNodes != tc.egressStale)) ||
				(tc.cpuState != "" && (got.CPULimit.State != tc.cpuState || got.CPULimit.AppliedNodes != tc.cpuApplied || got.CPULimit.StaleNodes != tc.cpuStale)) {
				t.Fatalf("status = %+v, want app %s applied %d stale %d; edge_rules %s applied %d stale %d; cors_presets %s applied %d stale %d",
					got, tc.state, tc.applied, tc.stale, tc.edgeState, tc.edgeApplied, tc.edgeStale, tc.corsState, tc.corsApplied, tc.corsStale)
			}
			if got.RequestPolicy.Scope != "app" || got.EdgeRules.Scope != "app" || got.CorsPresets.Scope != "account" || got.ResponseCache.Scope != "app" || got.EgressAllowlist.Scope != "app" || got.CPULimit.Scope != "app" || got.SchedulerScaling.Scope != "app" {
				t.Fatalf("component scopes = request_policy:%q edge_rules:%q cors_presets:%q response_cache:%q egress_allowlist:%q cpu_limit:%q scheduler_scaling:%q; want app/app/account/app/app/app/app", got.RequestPolicy.Scope, got.EdgeRules.Scope, got.CorsPresets.Scope, got.ResponseCache.Scope, got.EgressAllowlist.Scope, got.CPULimit.Scope, got.SchedulerScaling.Scope)
			}
		})
	}
	if got := summarizeRuntimePolicySchedulerStatus("app", "node-a", state.AppScalingPolicyStatus{
		DesiredRevision: 4, ObservedRevision: 4, SchedulerNodeID: "node-a", ObservedAt: now.Add(-2 * time.Minute),
	}, now); got.State != "pending" || !got.Stale {
		t.Fatalf("stale scheduler observation = %+v; want pending/stale", got)
	}
	if got := summarizeRuntimePolicySchedulerStatus("app", "node-a", state.AppScalingPolicyStatus{
		DesiredRevision: 4,
	}, now); got.State != "pending" {
		t.Fatalf("missing observation for assigned owner = %+v; want pending", got)
	}
	if got := summarizeRuntimePolicySchedulerStatus("app", "node-b", state.AppScalingPolicyStatus{
		DesiredRevision: 4, ObservedRevision: 4, SchedulerNodeID: "node-a", ObservedAt: now,
	}, now); got.State != "pending" {
		t.Fatalf("wrong scheduler owner observation = %+v; want pending", got)
	}
}

func TestSummarizeRuntimeRequestPolicySeparatelyFromTraffic(t *testing.T) {
	now := time.Now().UTC()
	gateways := []state.ServingGatewayControlPlaneState{{NodeName: "node-a", LastChangeID: 41, ObservedAt: now}}
	got := summarizeRuntimePolicyStatus("app-1", "", 42, 41, 0, 0, 0, 0, 0, gateways, nil, nil, state.AppScalingPolicyStatus{}, now)
	if got.State != "pending" || got.RequestPolicy.State != "active" || got.RequestPolicy.DesiredRevision != 41 {
		t.Fatalf("traffic lag should not hide applied request policy: overall=%+v request_policy=%+v", got, got.RequestPolicy)
	}

	gateways[0].LastChangeID = 40
	got = summarizeRuntimePolicyStatus("app-1", "", 42, 41, 0, 0, 0, 0, 0, gateways, nil, nil, state.AppScalingPolicyStatus{}, now)
	if got.RequestPolicy.State != "pending" || got.RequestPolicy.PendingGateways != 1 {
		t.Fatalf("request policy behind gateway cursor = %+v; want pending", got.RequestPolicy)
	}
}

func TestGetRuntimePolicyStatusWaitsForGatewayApplication(t *testing.T) {
	base := state.NewMemStore()
	acct, err := base.CreateAccount(context.Background(), "policy-status@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := base.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "policy-status", Type: state.AppTypeApp,
		RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	apiKey, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if _, err := base.CreateAPIKey(context.Background(), acct.ID, hash, "test", api.ScopesReadSurface); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	store := &runtimePolicyStatusTestStore{
		Store: base, desired: 42, requestPolicyDesired: 42, edgeRulesDesired: 9, corsPresetsDesired: 13, cachePurgeDesired: 21, egressDesired: 7, cpuDesired: 4,
		scalingStatus: state.AppScalingPolicyStatus{DesiredRevision: 3, ObservedRevision: 2, ObservedAt: time.Now().UTC()},
		gateways:      []state.ServingGatewayControlPlaneState{{NodeName: "node-a", LastChangeID: 42, ObservedAt: time.Now().UTC(), LastEdgeRuleChangeID: 8, EdgeRulesObservedAt: time.Now().UTC(), LastCorsPresetChangeID: 12, CorsPresetsObservedAt: time.Now().UTC(), LastResponseCachePurgeID: 20, ResponseCachePurgesObservedAt: time.Now().UTC()}},
		egressNodes:   []state.AppEgressPolicyNodeState{{NodeName: "vm-a", DesiredRevision: 7, AppliedRevision: 6, ObservedAt: time.Now().UTC()}},
		cpuNodes:      []state.AppCPUPolicyNodeState{{NodeName: "vm-a", DesiredRevision: 4, AppliedRevision: 3, ObservedAt: time.Now().UTC()}},
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := newServerWithDeps(store, log, "gregale.dev", &captureNotifier{}, "", noopMailer{}, stubGithubdClient{}, nil, nil, 0, "")
	req := httptest.NewRequest(http.MethodGet, "/v1/apps/"+app.Slug+"/policy/status?wait=1s", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, body = %s", rec.Code, rec.Body.String())
	}
	var response api.RuntimePolicyStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.AppID != app.ID || response.DesiredRevision != 42 || response.State != "active" || response.AppliedGateways != 1 || response.RequestPolicy.State != "active" || response.RequestPolicy.DesiredRevision != 42 || response.EdgeRules.State != "active" || response.CorsPresets.State != "active" || response.CorsPresets.Scope != "account" || response.ResponseCache.State != "active" || response.ResponseCache.DesiredRevision != 21 || response.EgressAllowlist.State != "active" || response.EgressAllowlist.AppliedNodes != 1 || response.CPULimit.State != "active" || response.CPULimit.AppliedNodes != 1 || response.SchedulerScaling.State != "active" || response.SchedulerScaling.ObservedRevision != 3 || store.queriedAccountID != acct.ID || store.reads < 2 {
		t.Fatalf("response = %+v, reads = %d; want active after polling", response, store.reads)
	}
}

func TestSummarizeRuntimePolicyComponentUsesDatabaseClockAndRejectsFutureProgress(t *testing.T) {
	databaseNow := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		observed time.Time
		state    string
		stale    int
	}{
		{"fresh despite API clock skew", databaseNow.Add(-time.Second), "active", 0},
		{"stale by database clock", databaseNow.Add(-time.Minute), "pending", 1},
		{"future observation", databaseNow.Add(time.Second), "pending", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := []state.ServingGatewayControlPlaneState{{DatabaseNow: databaseNow, LastChangeID: 4, ObservedAt: tc.observed}}
			got := summarizeRuntimePolicyComponent("app", 4, rows, databaseNow.Add(time.Hour),
				func(g state.ServingGatewayControlPlaneState) int64 { return g.LastChangeID },
				func(g state.ServingGatewayControlPlaneState) time.Time { return g.ObservedAt })
			if got.State != tc.state || got.StaleGateways != tc.stale {
				t.Fatalf("status = %+v, want %s, stale %d", got, tc.state, tc.stale)
			}
		})
	}
}
