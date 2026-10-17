package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBindingVerificationDiagnosticsClients(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	calls := []string{}
	checkPassed := false
	member := BindingVerificationRunDiagnostics{Budget: &BindingVerificationBudget{RunID: id, MaxConcurrency: 1, ActiveResources: 1, WaitingSteps: 2, Full: true}, RunID: id, App: "api", DeploymentID: "22222222-2222-4222-8222-222222222222", Scope: "production", Status: "pending", CheckedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Minute), Complete: true, CheckPassed: &checkPassed, Blockers: []BindingVerificationDiagnosticBlocker{{Code: "credential_generation_changed", Message: "Generation changed.", Recovery: "review_new_run", Type: "object_storage", Binding: "ASSETS"}}, Changes: []BindingVerificationDifference{{Field: "credential_generation", Reviewed: "1", Current: "2"}}, Recovery: []BindingVerificationRecoveryAction{{Action: "review_new_run", Message: "Review a new run."}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Method != http.MethodGet {
			t.Error("diagnostics performed a mutation")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/apps/api/bindings/verification-runs/"+id+"/diagnostics" {
			_ = json.NewEncoder(w).Encode(member)
		} else {
			_ = json.NewEncoder(w).Encode(ProjectBindingVerificationRunDiagnostics{Budget: &BindingVerificationBudget{RunID: id, MaxConcurrency: 1, ActiveResources: 1, WaitingSteps: 2, Full: true}, RunID: id, Project: "project", Environment: "production", CheckedAt: member.CheckedAt, ExpiresAt: member.ExpiresAt, Status: "pending", Complete: true, Members: map[string]BindingVerificationRunDiagnostics{"api": member}, Blockers: member.Blockers, Changes: member.Changes, Recovery: member.Recovery})
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, "test")
	app, err := client.GetBindingVerificationRunDiagnostics(testContext(t), "api", id)
	if err != nil || app.Budget == nil || !app.Budget.Full || app.Budget.MaxConcurrency != 1 || app.CheckPassed == nil || *app.CheckPassed || app.Changes[0].Current != "2" {
		t.Fatalf("app diagnostics: %+v %v", app, err)
	}
	graph, err := client.GetProjectBindingVerificationRunDiagnostics(testContext(t), "project", "production", id)
	if err != nil || graph.Budget == nil || graph.Budget.RunID != id || graph.Members["api"].RunID != id || graph.Recovery[0].Action != "review_new_run" {
		t.Fatalf("graph diagnostics: %+v %v", graph, err)
	}
	if len(calls) != 2 || calls[1] != "GET /v1/projects/project/environments/production/release-sets/verification-runs/"+id+"/diagnostics" {
		t.Fatalf("routes: %v", calls)
	}
}
