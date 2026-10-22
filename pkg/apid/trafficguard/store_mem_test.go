package trafficguard_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func memMCPAccessExpectation(t *testing.T, m *state.MemStore, ctx context.Context, accountID string) api.MCPAccessPolicyExpectation {
	t.Helper()
	rules, err := m.ListEdgeRulesForAccount(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	responses := []api.EdgeRuleResponse{}
	for _, r := range rules {
		action, _ := json.Marshal(r.Action)
		responses = append(responses, api.EdgeRuleResponse{ID: r.ID, AccountID: r.AccountID, AppID: r.AppID, Kind: string(r.Kind), MatchHost: r.MatchHost, MatchPath: r.MatchPath, MatchMethods: r.MatchMethods, MatchHeaders: r.MatchHeaders, Priority: r.Priority, Enabled: r.Enabled, Action: action, ExpiresAt: r.ExpiresAt, Match: r.Match, Mode: r.Mode})
	}
	fingerprint, err := api.MCPAccessPolicyFingerprint("https://reports.example/mcp", responses, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return api.MCPAccessPolicyExpectation{Resource: "https://reports.example/mcp", Fingerprint: fingerprint}
}

func TestMemMCPAccessPolicyTrafficFence(t *testing.T) {
	for _, mutation := range []string{"unchanged", "insert wildcard", "update", "delete", "unrelated host", "wrong account", "wrong app"} {
		t.Run(mutation, func(t *testing.T) {
			ctx := context.Background()
			m := state.NewMemStore()
			account, app, stable := seedMCPAccessLive(t, m, ctx, mutation)
			candidate, err := m.CreateDeployment(ctx, state.Deployment{AppID: app, Kind: state.DeploymentKindImage, ImageDigest: "sha256:candidate", Status: state.DeployPending, TrafficPercent: 0, TrafficPercentExplicit: true})
			if err != nil {
				t.Fatal(err)
			}
			if err := m.MarkDeploymentLive(ctx, candidate.ID); err != nil {
				t.Fatal(err)
			}
			rule, err := m.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: account, AppID: app, Kind: state.EdgeRuleKindJWT, Enabled: true, MatchHost: "reports.example", MatchPath: "/**", Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindJWT, JWT: &state.EdgeRuleJWTAction{Issuer: "https://issuer.example"}}})
			if err != nil {
				t.Fatal(err)
			}
			expectation := memMCPAccessExpectation(t, m, ctx, account)
			guardAccount, guardApp := account, app
			switch mutation {
			case "insert wildcard", "unrelated host":
				host := "*.example"
				if mutation == "unrelated host" {
					host = "unrelated.example"
				}
				_, err = m.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: account, AppID: app, Kind: state.EdgeRuleKindJWT, Enabled: true, MatchHost: host, MatchPath: "/**", Action: rule.Action})
			case "update":
				enabled := false
				_, err = m.UpdateEdgeRule(ctx, rule.ID, state.UpdateEdgeRuleParams{Enabled: &enabled})
			case "delete":
				err = m.DeleteEdgeRule(ctx, rule.ID)
			case "wrong account":
				guardAccount = "other"
			case "wrong app":
				guardApp = "other"
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = m.UpdateDeploymentTraffic(state.WithMCPAccessPolicyExpectation(ctx, guardAccount, guardApp, expectation), candidate.ID, 100, stable)
			passed := mutation == "unchanged" || mutation == "unrelated host"
			if passed && err != nil {
				t.Fatal(err)
			}
			if !passed && !errors.Is(err, state.ErrMCPAccessPolicyChanged) {
				t.Fatalf("error=%v", err)
			}
			before, _ := m.DeploymentByID(ctx, stable)
			after, _ := m.DeploymentByID(ctx, candidate.ID)
			if !passed && (before.TrafficPercent != 100 || after.TrafficPercent != 0) {
				t.Fatal("rejected guard changed traffic")
			}
			if passed && (before.TrafficPercent != 0 || after.TrafficPercent != 100) {
				t.Fatal("accepted guard did not promote")
			}
		})
	}
}
