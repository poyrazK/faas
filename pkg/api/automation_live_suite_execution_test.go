// adr: 1297
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestManagedSuiteClientPreservesConfirmationAndRouteIdentity(t *testing.T) {
	var calls []string
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, r.Method+" "+r.URL.EscapedPath())
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing auth")
		}
		var body map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&body) != nil || string(body["confirm_real_actions"]) != "true" {
			t.Error("missing explicit confirmation")
		}
		if strings.HasSuffix(r.URL.Path, "/advance") && len(body) != 1 {
			t.Error("continuation retargeted the selection")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"suite/id","name":"case/report","managed":true,"outcome":"pending"}`))
	}))
	defer server.Close()
	client := NewClient(server.URL, "test-token")
	view, err := client.StartAutomationLiveSuite(t.Context(), "billing", "case/report", "suite/id", StartAutomationLiveSuiteRequest{ExpectedVersion: 7, ExpectedPublishedVersion: 4, ExpectedDeploymentID: "deployment", Cases: []string{"first", "second"}, ConfirmRealActions: true})
	if err != nil || !view.Managed {
		t.Fatal(view, err)
	}
	if _, err := client.AdvanceAutomationLiveSuite(t.Context(), "billing", "case/report", "suite/id", AdvanceAutomationLiveSuiteRequest{ConfirmRealActions: true}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 || calls[0] != "POST /v1/apps/billing/automations/case%2Freport/live-suites/suite%2Fid/start" || calls[1] != "POST /v1/apps/billing/automations/case%2Freport/live-suites/suite%2Fid/advance" {
		t.Fatal("changed route identity", calls)
	}
}
