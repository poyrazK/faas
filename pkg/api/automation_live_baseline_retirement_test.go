// adr: 1305
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRetireAutomationLiveBaselineClientIdentity(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "PUT" || r.URL.EscapedPath() != "/v1/apps/billing/automations/case%2Freport/live-baselines/production/retirements/retirement%2Fid" || r.Header.Get("Authorization") != "Bearer token" {
			t.Error("invalid retirement request")
		}
		var request RetireAutomationLiveBaselineRequest
		if json.NewDecoder(r.Body).Decode(&request) != nil || request.ExpectedRevision != 3 {
			t.Error("changed revision")
		}
		_ = json.NewEncoder(w).Encode(AutomationLiveBaselineRetirement{RetirementID: "retirement/id", Revision: 4})
	}))
	defer server.Close()
	client := NewClient(server.URL, "token")
	for range 2 {
		receipt, err := client.RetireAutomationLiveBaseline(t.Context(), "billing", "case/report", "production", "retirement/id", RetireAutomationLiveBaselineRequest{ExpectedRevision: 3})
		if err != nil || receipt.Revision != 4 || receipt.RetirementID != "retirement/id" {
			t.Fatal("retirement receipt", err)
		}
	}
	if calls != 2 {
		t.Fatal("unexpected retirement requests", calls)
	}
}
