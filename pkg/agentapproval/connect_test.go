package agentapproval

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// The Connect grant covers exactly what the Connect screen names
// (ADR-20261127160000 §3); every other gated request keeps asking.
func TestClassifyMarksWhatTheConnectConsentCovers(t *testing.T) {
	cases := []struct {
		pattern  string
		category string
		connect  bool
	}{
		// Reviewed deployments.
		{"POST /v1/apps/{slug}/deployments", api.AgentApprovalProduction, true},
		{"POST /v1/apps/{slug}/deployments/source-ref", api.AgentApprovalProduction, true},
		{"POST /v1/apps/{slug}/deployments/source-tarball", api.AgentApprovalProduction, true},
		{"POST /v1/deployments/{id}/retry", api.AgentApprovalProduction, true},
		{"PATCH /v1/apps/{slug}", api.AgentApprovalProduction, true},
		// Production promotion.
		{"PATCH /v1/deployments/{id}/traffic", api.AgentApprovalProduction, true},
		{"POST /v1/deployments/{id}/promote", api.AgentApprovalProduction, true},
		{"POST /v1/deployments/{id}/promote-with-application-ack", api.AgentApprovalProduction, true},
		{"POST /v1/deployments/{id}/canary/advance", api.AgentApprovalProduction, true},
		// Checked rollback and restarts.
		{"POST /v1/apps/{slug}/rollback", api.AgentApprovalProduction, true},
		{"POST /v1/apps/{slug}/restart", api.AgentApprovalProduction, true},
		// Domain and cron changes.
		{"DELETE /v1/domains/{domain}", api.AgentApprovalDelete, true},
		{"DELETE /v1/domains/{domain}/hosts", api.AgentApprovalDelete, true},
		{"DELETE /v1/crons/{id}", api.AgentApprovalDelete, true},
		// Configuration (env:write).
		{"PUT /v1/apps/{slug}/env/{key}", api.AgentApprovalProduction, true},
		{"POST /v1/apps/{slug}/env/batch", api.AgentApprovalProduction, true},
		{"POST /v1/apps/{slug}/config/rollback", api.AgentApprovalProduction, true},
		// Undo of the agent's own changes.
		{"POST /v1/agent-connections/self/undo", api.AgentApprovalUndo, true},

		// Not named on the Connect screen: they keep asking.
		{"DELETE /v1/apps/{slug}", api.AgentApprovalDelete, false},
		{"DELETE /v1/apps/{slug}/env/{key}", api.AgentApprovalDelete, false},
		{"DELETE /v1/postgres/databases/{id}", api.AgentApprovalDelete, false},
		{"POST /v1/keys", api.AgentApprovalCredentials, false},
		{"PATCH /v1/account/plan", api.AgentApprovalBilling, false},
		{"PUT /v1/apps/{slug}/secrets/{key}", api.AgentApprovalProduction, false},
		{"POST /v1/apps/{slug}/tasks", api.AgentApprovalProduction, false},
		{"PUT /v1/projects/{slug}/environments/{environment}/flags", api.AgentApprovalProduction, false},
		{"POST /v1/apps/{slug}/dlq/{id}/replay", api.AgentApprovalProduction, false},
		{"POST /v1/workflows/runs/{id}/resume", api.AgentApprovalProduction, false},
		{"POST /v1/apps/{slug}/edge-rules", api.AgentApprovalProduction, false},
		{"POST /v1/projects/{slug}/environments/{environment}/promote", api.AgentApprovalProduction, false},
		{"POST /v1/apps/{slug}/deployments/dev-source", api.AgentApprovalProduction, false},
		{"POST /v1/deployments/{id}/approvals", api.AgentApprovalProduction, false},
		{"GET /v1/apps/{slug}/debug", api.AgentApprovalCredentials, false},
	}
	for _, tc := range cases {
		t.Run(tc.pattern, func(t *testing.T) {
			rule, ok := Classify(tc.pattern)
			if !ok || rule.Category != tc.category || rule.Connect != tc.connect {
				t.Fatalf("Classify(%q) = %+v, %v; want category %q, connect %v", tc.pattern, rule, ok, tc.category, tc.connect)
			}
		})
	}
	if rule, ok := Classify("GET /v1/apps/{slug}"); ok || rule.Connect {
		t.Fatalf("an ungated read = %+v, %v; want no rule", rule, ok)
	}
	if !UploadCommitRule.Connect || UploadCommitRule.Category != api.AgentApprovalProduction {
		t.Fatalf("UploadCommitRule = %+v; want a covered production rule", UploadCommitRule)
	}
}

// Every covered route is one the gate asks for at all, so the table cannot
// name a route the classification lost.
func TestConnectRoutesAreGated(t *testing.T) {
	for pattern := range connectRoutes {
		if _, ok := classify(pattern); !ok {
			t.Errorf("%s is covered by the Connect grant but never asks", pattern)
		}
	}
}

func TestConnectApproves(t *testing.T) {
	production := Rule{Category: api.AgentApprovalProduction, Connect: true}
	deletion := Rule{Category: api.AgentApprovalDelete, Connect: true}
	undo := Rule{Category: api.AgentApprovalUndo, Connect: true}
	uncovered := Rule{Category: api.AgentApprovalProduction}
	cases := []struct {
		name   string
		rule   Rule
		chosen bool
		floor  []string
		want   bool
	}{
		{"covered production", production, false, nil, true},
		{"covered delete", deletion, false, nil, true},
		{"covered undo", undo, false, nil, true},
		{"route the consent does not name", uncovered, false, nil, false},
		{"key chose its own categories", production, true, nil, false},
		{"organization floor asks for production", production, false, []string{api.AgentApprovalProduction}, false},
		{"organization floor asks for delete", deletion, false, []string{api.AgentApprovalDelete}, false},
		{"floor of another category", production, false, []string{api.AgentApprovalBilling, api.AgentApprovalDelete}, true},
		{"undo under a production floor", undo, false, []string{api.AgentApprovalProduction}, false},
		{"undo under a delete floor", undo, false, []string{api.AgentApprovalDelete}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ConnectApproves(tc.rule, tc.chosen, tc.floor); got != tc.want {
				t.Fatalf("ConnectApproves(%+v, %v, %v) = %v; want %v", tc.rule, tc.chosen, tc.floor, got, tc.want)
			}
		})
	}
}
