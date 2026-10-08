package faas_test

import (
	"encoding/json"
	faas "github.com/poyrazK/faas/sdk/go"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProfileCanaryGateClientAndOverride(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Fatal("auth missing")
		}
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(faas.ProfileCanaryGateDecision{Status: "regressed", PolicyRevision: 7, CanaryStep: 1})
			return
		}
		var request struct {
			ExpectedStep int                       `json:"expected_step"`
			Override     *faas.ProfileGateOverride `json:"profile_gate_override"`
			Rollback     bool                      `json:"profile_gate_rollback"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.ExpectedStep != 1 || request.Override == nil || request.Override.ExpectedPolicyRevision != 7 || request.Override.Reason != "Accepted cost after reviewing checkout profiles." || request.Rollback {
			t.Errorf("invalid override: %+v %v", request, err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"audit_id":"42","deployment":{},"profile_gate":{"status":"overridden","policy_revision":7,"canary_step":1}}`))
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	gate, err := client.GetProfileCanaryGate(t.Context(), "candidate")
	if err != nil || gate.Status != "regressed" || gate.PolicyRevision != 7 {
		t.Fatal(gate, err)
	}
	out, err := client.AdvanceCanaryWithProfileOverride(t.Context(), "candidate", 1, faas.ProfileGateOverride{ExpectedPolicyRevision: 7, Reason: "Accepted cost after reviewing checkout profiles."})
	if err != nil || out.AuditID != "42" || out.ProfileGate == nil || out.ProfileGate.Status != "overridden" {
		t.Fatal(out, err)
	}
}
