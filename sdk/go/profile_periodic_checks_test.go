package faas_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestProfilePeriodicClientPolicyAndHistory(t *testing.T) {
	config := faas.ProfileDeploymentPolicyConfig{Enabled: true, Runtime: "node24", WindowSeconds: 60, Options: faas.DefaultProfileRegressionOptions(), Periodic: &faas.PeriodicProfilePolicy{IntervalSeconds: 900, Confirmations: 2}}
	config.Options.Routes = []string{"POST /checkout"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/apps/demo/profiles/periodic-monitors" || r.Header.Get("Authorization") != "Bearer token" {
			t.Error("incorrect periodic history request", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(faas.ListProfilePeriodicMonitorsResponse{Monitors: []faas.ProfilePeriodicMonitor{{ID: "monitor", Active: true, Route: "POST /checkout", Config: config, History: []faas.ProfilePeriodicObservation{{ID: "observation", Status: "baseline_pinned"}}}}})
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.ListProfilePeriodicMonitors(context.Background(), "demo")
	if err != nil || len(result.Monitors) != 1 {
		t.Fatal(result, err)
	}
	mon := result.Monitors[0]
	if !mon.Active || mon.Config.Periodic == nil || mon.Config.Periodic.Confirmations != 2 || mon.Config.Periodic.IntervalSeconds != 900 || len(mon.History) != 1 {
		t.Fatal("periodic contract lost", mon)
	}
}
