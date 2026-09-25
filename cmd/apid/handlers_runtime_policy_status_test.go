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
	desired          int64
	edgeRulesDesired int64
	gateways         []state.ServingGatewayControlPlaneState
	reads            int
}

func (s *runtimePolicyStatusTestStore) LatestAppControlPlaneChangeID(context.Context, string) (int64, error) {
	return s.desired, nil
}

func (s *runtimePolicyStatusTestStore) LatestAppEdgeRuleChangeID(context.Context, string) (int64, error) {
	return s.edgeRulesDesired, nil
}

func (s *runtimePolicyStatusTestStore) ListServingGatewayControlPlaneStates(context.Context) ([]state.ServingGatewayControlPlaneState, error) {
	s.reads++
	if s.reads > 1 {
		for i := range s.gateways {
			s.gateways[i].LastChangeID = s.desired
			s.gateways[i].LastEdgeRuleChangeID = s.edgeRulesDesired
		}
	}
	return s.gateways, nil
}

func TestSummarizeRuntimePolicyStatus(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name             string
		desired          int64
		edgeRulesDesired int64
		gateways         []state.ServingGatewayControlPlaneState
		state            string
		applied          int
		stale            int
		edgeState        string
		edgeApplied      int
		edgeStale        int
	}{
		{name: "no serving fleet", desired: 12, edgeRulesDesired: 23, state: "unverified", edgeState: "unverified"},
		{name: "no revision", gateways: []state.ServingGatewayControlPlaneState{{NodeName: "n1", LastChangeID: 12, ObservedAt: now}}, state: "unverified", edgeState: "unverified"},
		{name: "all applied", desired: 12, edgeRulesDesired: 23, gateways: []state.ServingGatewayControlPlaneState{{NodeName: "n1", LastChangeID: 12, ObservedAt: now, LastEdgeRuleChangeID: 23, EdgeRulesObservedAt: now}, {NodeName: "n2", LastChangeID: 14, ObservedAt: now, LastEdgeRuleChangeID: 25, EdgeRulesObservedAt: now}}, state: "active", applied: 2, edgeState: "active", edgeApplied: 2},
		{name: "control plane lagging", desired: 12, edgeRulesDesired: 23, gateways: []state.ServingGatewayControlPlaneState{{NodeName: "n1", LastChangeID: 12, ObservedAt: now, LastEdgeRuleChangeID: 23, EdgeRulesObservedAt: now}, {NodeName: "n2", LastChangeID: 11, ObservedAt: now, LastEdgeRuleChangeID: 23, EdgeRulesObservedAt: now}}, state: "pending", applied: 1, edgeState: "active", edgeApplied: 2},
		{name: "edge rules lagging", desired: 12, edgeRulesDesired: 23, gateways: []state.ServingGatewayControlPlaneState{{NodeName: "n1", LastChangeID: 12, ObservedAt: now, LastEdgeRuleChangeID: 22, EdgeRulesObservedAt: now}}, state: "active", applied: 1, edgeState: "pending"},
		{name: "stale control-plane observation", desired: 12, edgeRulesDesired: 23, gateways: []state.ServingGatewayControlPlaneState{{NodeName: "n1", LastChangeID: 12, ObservedAt: now.Add(-time.Minute), LastEdgeRuleChangeID: 23, EdgeRulesObservedAt: now}}, state: "pending", stale: 1, edgeState: "active", edgeApplied: 1},
		{name: "stale edge-rule observation", desired: 12, edgeRulesDesired: 23, gateways: []state.ServingGatewayControlPlaneState{{NodeName: "n1", LastChangeID: 12, ObservedAt: now, LastEdgeRuleChangeID: 23, EdgeRulesObservedAt: now.Add(-time.Minute)}}, state: "active", applied: 1, edgeState: "pending", edgeStale: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := summarizeRuntimePolicyStatus("app-1", tc.desired, tc.edgeRulesDesired, tc.gateways, now)
			if got.State != tc.state || got.AppliedGateways != tc.applied || got.StaleGateways != tc.stale ||
				got.EdgeRules.State != tc.edgeState || got.EdgeRules.AppliedGateways != tc.edgeApplied || got.EdgeRules.StaleGateways != tc.edgeStale {
				t.Fatalf("status = %+v, want state %s applied %d stale %d; edge_rules state %s applied %d stale %d",
					got, tc.state, tc.applied, tc.stale, tc.edgeState, tc.edgeApplied, tc.edgeStale)
			}
		})
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
		Store: base, desired: 42, edgeRulesDesired: 9,
		gateways: []state.ServingGatewayControlPlaneState{{NodeName: "node-a", LastChangeID: 42, ObservedAt: time.Now().UTC(), LastEdgeRuleChangeID: 8, EdgeRulesObservedAt: time.Now().UTC()}},
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
	if response.AppID != app.ID || response.DesiredRevision != 42 || response.State != "active" || response.AppliedGateways != 1 || response.EdgeRules.State != "active" || store.reads < 2 {
		t.Fatalf("response = %+v, reads = %d; want active after polling", response, store.reads)
	}
}
