//go:build !no_pg

// adr: 570
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/trafficdeadline"
)

func TestTrafficFleetDaemonDeadlineSurvivesManagedBridgeChain(t *testing.T) {
	t.Setenv("FAAS_SESSION_KEY", strings.Repeat("2a", 32))
	f, node := prepareFleetAdmission(t)
	caller, err := f.store.AppByID(t.Context(), f.apps[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	policy := caller.Manifest.ServiceReliability["fleet-retry"]
	policy.TimeoutMS = 5000
	caller.Manifest.ServiceReliability["fleet-retry"] = policy
	if _, err := f.store.UpdateApp(t.Context(), caller.ID, state.UpdateAppParams{Manifest: &caller.Manifest}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.CreateEdgeRule(t.Context(), state.CreateEdgeRuleParams{AccountID: f.app.AccountID,
		AppID: f.apps[0].ID, MatchHost: f.apps[0].Host, MatchPath: "/chain", MatchMethods: []string{http.MethodGet}, Enabled: true,
		Kind: state.EdgeRuleKindBudget, Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindBudget,
			Budget: &state.EdgeRuleBudgetAction{BudgetMs: 5000, TotalDeadlineMs: 1200}}}); err != nil {
		t.Fatal(err)
	}
	first := startFleetDaemonMode(t, f.pool, f.apps, f.nodes[0].ID, f.nodes[0].Name, f.usage, true)
	second := startFleetDaemonMode(t, f.pool, f.apps, f.nodes[0].ID, f.nodes[1].Name, f.usage, true)
	encoded, err := json.Marshal(second.ready.ServiceEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	configured, err := http.Post(node.Control, "application/json", strings.NewReader(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	_ = configured.Body.Close()
	if configured.StatusCode != http.StatusNoContent {
		t.Fatalf("chain fixture configuration status=%d", configured.StatusCode)
	}
	request, err := fleetAdmissionRequest(t.Context(), first, f.apps[0], "public", "/chain", false)
	if err != nil {
		t.Fatal(err)
	}
	// Public ingress must replace an untrusted carrier before guest work.
	request.Header.Set(trafficdeadline.Header, "forged-longer-deadline")
	client := &http.Client{Timeout: 3 * time.Second}
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || response.StatusCode != http.StatusGatewayTimeout || !strings.Contains(string(body), api.CodeRequestBudgetExceeded) {
		t.Fatalf("configured chain status=%d body=%q err=%v", response.StatusCode, body, err)
	}
	var observed fleetAdmissionObservation
	for until := time.Now().Add(time.Second); ; {
		observed = observeFleetAdmission(t, node)
		if observed.Chain.ChildFinishedNS != 0 && observed.Instances[f.apps[0].Instances[0]].Inflight == 0 && observed.Instances[f.apps[2].Instances[0]].Inflight == 0 {
			break
		}
		if time.Now().After(until) {
			t.Fatalf("chain deadline did not stop independent downstream or release both node permits: %+v", observed)
		}
		time.Sleep(10 * time.Millisecond)
	}
	parent, parentOK := observed.Chain.Claims["/chain"]
	leaf, leafOK := observed.Chain.Claims["/hold"]
	if !parentOK || !leafOK || len(observed.Chain.ClaimErrors) != 0 || parent.AppID != f.apps[0].ID || leaf.AppID != f.apps[2].ID ||
		parent.AccountID != f.app.AccountID || leaf.AccountID != parent.AccountID || leaf.ChainID != parent.ChainID ||
		leaf.DeadlineNS > parent.DeadlineNS || leaf.IssuedNS-parent.IssuedNS < int64(300*time.Millisecond) ||
		parent.Deadline().After(started.Add(1300*time.Millisecond)) || observed.RPCs != 2 || observed.GuestCalls != 2 ||
		observed.Chain.ChildError || observed.Chain.ChildStatus != http.StatusGatewayTimeout ||
		observed.Chain.ChildFinishedNS > parent.DeadlineNS+int64(700*time.Millisecond) {
		t.Fatalf("configured chain reset, lost or failed its carrier: %+v", observed)
	}
	for _, call := range []struct{ endpoint, host, path string }{
		{first.ready.Endpoint, f.apps[0].Host, "/work"},
		{second.ready.ServiceEndpoint, f.apps[2].Host, "/v1/internal/services/fleet-retry/work"},
	} {
		fresh, err := requestFleetDaemon(t.Context(), call.endpoint, call.host, call.path)
		if err != nil || fresh.status != http.StatusOK || fresh.body != "guest served" {
			t.Fatalf("fresh work after chain cleanup: %+v err=%v", fresh, err)
		}
	}
	t.Log("configured public policy and a declared managed call retain one authenticated deadline across real node/bridge exchanges; independent child and both permits finish")
}
