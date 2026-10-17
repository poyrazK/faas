package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestScenarioActionsRequireAcknowledgementBeforeDeployment(t *testing.T) {
	captureSuiteTestOutput(t)
	for _, tc := range []struct {
		name, network, networkAck string
		simulation                bool
		simulationAck             string
		recorderMissing           bool
		acknowledged, applied     bool
	}{
		{name: "legacy-server"}, {name: "capture-applied", acknowledged: true, applied: true},
		{name: "network-omitted", network: "contained", acknowledged: true},
		{name: "network-applied", network: "contained", networkAck: "contained", acknowledged: true, applied: true},
		{name: "simulation-omitted", network: "contained", networkAck: "contained", simulation: true, acknowledged: true},
		{name: "simulation-changed", network: "contained", networkAck: "contained", simulation: true, simulationAck: "changed", acknowledged: true},
		{name: "recorder-omitted", network: "contained", networkAck: "contained", simulation: true, simulationAck: "exact", acknowledged: true, recorderMissing: true},
		{name: "simulation-applied", network: "contained", networkAck: "contained", simulation: true, simulationAck: "exact", acknowledged: true, applied: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, scenario := behaviorTestScenario(t)
			scenario.Project, scenario.Actions = "actions-api", &testActionSpec{Webhooks: "capture", Network: tc.network}
			evidenceVersion, simulationDigest := api.ScenarioActionSnapshotVersion, ""
			recorderVersion := 0
			if tc.simulation {
				scenario.Actions.Simulation = &api.ScenarioSimulationPlan{Version: 1, Fixtures: []api.ScenarioSimulationFixture{{Workload: "actions-api", Name: "notify", Integration: "email", Adapter: "resend", Method: "POST", Path: "/emails", Responses: []api.ScenarioSimulationResponse{{Status: 200, Body: `{"id":"simulated-1"}`}}}}}
				_, digest, err := api.NormalizeScenarioSimulationPlan(*scenario.Actions.Simulation, []string{"actions-api"})
				if err != nil {
					t.Fatal(err)
				}
				evidenceVersion, simulationDigest = api.ScenarioSimulationSnapshotVersion, digest
				recorderVersion = 1
			}
			if tc.recorderMissing {
				recorderVersion = 0
			}
			writeSuiteTestFile(t, filepath.Join(dir, "package.json"), `{"name":"actions-api","scripts":{"start":"node server.js"}}`)
			writeSuiteTestFile(t, filepath.Join(dir, "server.js"), "require('http').createServer((r,s)=>s.end('ok')).listen(8080);\n")
			configured, destroyed, released, sealed := 0, 0, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/actions/seal"):
					if destroyed != 0 || released != 0 {
						t.Error("action evidence collected after platform cleanup")
					}
					sealed++
					runID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/dev/test-runs/"), "/actions/seal")
					snapshot := testSealedActionSnapshot(runID, 0)
					snapshot.Workloads = []string{"actions-api"}
					if tc.simulation {
						snapshot.RecorderVersion, snapshot.RecorderSessions = 1, 1
						snapshot.Version, snapshot.Scope, snapshot.SimulationVersion, snapshot.SimulationSHA256 = 2, api.ScenarioSimulationScope, 1, simulationDigest
					}
					_ = json.NewEncoder(w).Encode(snapshot)
				case r.Method == "PUT" && r.URL.Path == "/v1/dev/sessions/actions-api":
					_ = json.NewEncoder(w).Encode(api.DevSessionResponse{App: api.AppResponse{Slug: "owned-actions-app"}})
				case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/v1/dev/test-runs/"):
					var req api.RegisterScenarioTestRequest
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.WebhookMode != "capture" || req.NetworkMode != tc.network || req.ActionEvidenceVersion != evidenceVersion || (req.Simulation != nil) != tc.simulation {
						t.Errorf("capture policy omitted: %+v %v", req, err)
					}
					if tc.acknowledged {
						ackDigest := ""
						if tc.simulationAck == "exact" {
							ackDigest = simulationDigest
						} else if tc.simulationAck == "changed" {
							ackDigest = strings.Repeat("e", 64)
						}
						_ = json.NewEncoder(w).Encode(api.RegisterScenarioTestResponse{WebhookMode: "capture", PolicyVersion: 1, ActionEvidenceVersion: evidenceVersion, NetworkMode: tc.networkAck, SimulationSHA256: ackDigest, RecorderVersion: recorderVersion})
					} else {
						w.WriteHeader(204)
					}
				case r.Method == "PATCH" && r.URL.Path == "/v1/apps/owned-actions-app":
					configured++
					w.WriteHeader(500) // End after the policy gate; no remote build.
				case r.Method == "DELETE" && r.URL.Path == "/v1/dev/sessions/actions-api":
					destroyed++
					w.WriteHeader(204)
				case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/v1/dev/test-runs/"):
					released++
					w.WriteHeader(204)
				default:
					_, _ = w.Write([]byte(`{}`))
				}
			}))
			defer server.Close()
			receipt := runTestProfile(context.Background(), NewClient(server.URL, "synthetic-key"), "notifications", scenario, dir, "warm")
			if receipt.Status != "failed" || destroyed != 1 {
				t.Fatalf("failed run lost cleanup: %+v destroyed=%d", receipt, destroyed)
			}
			if !tc.applied {
				if configured != 0 || receipt.Actions != nil || !strings.Contains(receipt.Error, "acknowledge") {
					t.Fatalf("source setup passed an unacknowledged gate: %+v configured=%d", receipt, configured)
				}
			} else if configured != 1 || released != 1 || sealed != 1 || receipt.Actions == nil || receipt.Actions.PolicyVersion != 1 || receipt.Actions.Status != "applied" || receipt.Actions.Network != tc.network || receipt.Actions.Evidence == nil || receipt.Actions.Evidence.Status != "complete" {
				t.Fatalf("applied policy lost: %+v configured=%d released=%d", receipt, configured, released)
			}
			if tc.applied && receipt.Actions.SimulationSHA256 != simulationDigest {
				t.Fatal("receipt lost the acknowledged fixture identity")
			}
		})
	}
}

func TestScenarioActionsRejectHostEnginesAndUnknownModes(t *testing.T) {
	captureSuiteTestOutput(t)
	dir, scenario := behaviorTestScenario(t)
	scenario.Actions = &testActionSpec{Webhooks: "capture"}
	for _, receipt := range []testRunReceipt{
		runLocalTest(context.Background(), "notifications", scenario, dir, "http://127.0.0.1:1", testDataCase{}),
		runSimulatedTest(context.Background(), "notifications", scenario, dir),
	} {
		if receipt.Status != "failed" || !strings.Contains(receipt.Error, "requires the real-vm engine") {
			t.Fatalf("unsupported action capture ran: %+v", receipt)
		}
	}
	for _, mode := range []string{"", "live", "simulate"} {
		if err := validateTestActions(&testActionSpec{Webhooks: mode}); err == nil {
			t.Fatalf("unknown capture mode %q accepted", mode)
		}
	}
}
