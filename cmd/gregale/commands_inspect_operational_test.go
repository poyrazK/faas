package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
)

func TestCmdInspectSummaryCurrentHealthRemainsSeparateFromVerifiedDeployment(t *testing.T) {
	base := newInspectSummaryServer(t, false)
	t.Cleanup(base.Close)
	operational := api.AppOperationalSummary{Version: 1, AppID: inspectAppID,
		Monitoring: api.AppOperationalMonitoring{Available: true, Status: "unknown", Reason: "insufficient_requests", Coverage: "observed_only"},
		Recovery: api.AppOperationalRecovery{RollbacksAvailable: true, RestartsAvailable: true,
			Workers: &api.AppWorkerRecovery{Available: true, Eligible: true, Scope: "app", Status: "waiting_capacity", Failures: 1,
				Pools: []api.AppWorkerPoolObservation{{Scope: "default", Desired: 2, Running: 1, ObservedAt: time.Now().UTC()}}},
			Rollbacks: []api.AppOperationalRollback{{ID: "rollback-id", Status: "blocked", Scope: "default", TargetDeploymentID: "previous", Code: "binding_verification_missing"}},
			Restarts:  []api.RuntimeConfigRestartStatusResponse{{WakeID: "restart-id", Status: "retrying", Attempts: 2, FailureReason: "requests_active"}}},
		Recommendations: []api.AppOperationalRecommendation{{Code: "production_health_unknown", Severity: "warning", Message: "Current production health is unknown.", Next: "Read the route-monitor report."}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/operational-summary") {
			writeInspectSummaryJSON(t, w, operational)
			return
		}
		base.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	configureInspectSummaryTest(t, server.URL)
	stdout, stderr, restore := swapIO(t)
	defer restore()
	if exit := cmdInspect([]string{inspectSlug}); exit != 0 {
		t.Fatalf("inspect failed: %d %s", exit, stderr())
	}
	for _, want := range []string{"/healthz · verified", "production routes: unknown · insufficient_requests", "rollback rollback-id: blocked", "restart restart-id: retrying", "Current production health is unknown.", "worker recovery (app-wide): waiting_capacity", "worker pool default: desired=2 running=1"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("missing %q in %s", want, stdout.String())
		}
	}
	stdout.Reset()
	jsonOutput = true
	t.Cleanup(resetJSONOutput)
	if exit := cmdInspect([]string{inspectSlug}); exit != 0 {
		t.Fatalf("JSON inspect failed: %d %s", exit, stderr())
	}
	var got inspectSummary
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Runtime.Health.Status != apihostingreceipt.SmokeVerified || got.Operational == nil || got.Operational.Monitoring.Status != "unknown" || len(got.Recommendations) != 1 {
		t.Fatalf("launch evidence masked current health: %+v", got)
	}
	if got.Operational.Recovery.Workers == nil || got.Operational.Recovery.Workers.Pools[0].Desired != 2 {
		t.Fatalf("JSON inspect lost worker recovery: %+v", got.Operational.Recovery)
	}
}

func TestCmdInspectSummaryOlderServerRemainsUsable(t *testing.T) {
	base := newInspectSummaryServer(t, false)
	t.Cleanup(base.Close)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/operational-summary") {
			http.NotFound(w, r)
			return
		}
		base.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	configureInspectSummaryTest(t, server.URL)
	stdout, stderr, restore := swapIO(t)
	defer restore()
	if exit := cmdInspect([]string{inspectSlug}); exit != 0 {
		t.Fatalf("older server inspect failed: %d %s", exit, stderr())
	}
	if !strings.Contains(stdout.String(), "unavailable: operational") || !strings.Contains(stdout.String(), inspectDepID) {
		t.Fatalf("older server silently lost existing data: %s", stdout.String())
	}
}
