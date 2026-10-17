// adr: 1350
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func simulationAPIPlan() ScenarioSimulationPlan {
	return ScenarioSimulationPlan{Version: 1, Fixtures: []ScenarioSimulationFixture{{Workload: "primary", Name: "notify", Integration: "email", Adapter: "resend", Method: "POST", Path: "/emails", CaptureFields: []string{"to", "subject"}, Responses: []ScenarioSimulationResponse{{Status: 200, Body: `{"id":"simulated-1"}`}}}}}
}

func TestRehearsalSimulationContractRefusesAmbiguousOrUnboundedFixtures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*ScenarioSimulationPlan)
	}{
		{"unknown version", func(p *ScenarioSimulationPlan) { p.Version = 2 }},
		{"unregistered workload", func(p *ScenarioSimulationPlan) { p.Fixtures[0].Workload = "production" }},
		{"redirect", func(p *ScenarioSimulationPlan) { p.Fixtures[0].Responses[0].Status = 302 }},
		{"query secret", func(p *ScenarioSimulationPlan) { p.Fixtures[0].Path = "/emails?key=secret" }},
		{"encoded path", func(p *ScenarioSimulationPlan) { p.Fixtures[0].Path = "/%65mails" }},
		{"smtp", func(p *ScenarioSimulationPlan) { p.Fixtures[0].Adapter = "smtp" }},
		{"connect", func(p *ScenarioSimulationPlan) { p.Fixtures[0].Method = "CONNECT" }},
		{"unbounded delay", func(p *ScenarioSimulationPlan) { p.Fixtures[0].Responses[0].DelayMS = 2001 }},
		{"timeout plus success", func(p *ScenarioSimulationPlan) { p.Fixtures[0].Responses[0].Fault = "timeout" }},
		{"duplicate route", func(p *ScenarioSimulationPlan) {
			p.Fixtures = append(p.Fixtures, p.Fixtures[0])
			p.Fixtures[1].Name = "another"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := simulationAPIPlan()
			tc.change(&p)
			if _, _, err := NormalizeScenarioSimulationPlan(p, []string{"primary"}); err == nil {
				t.Fatal("invalid plan accepted")
			}
		})
	}
}

func TestRehearsalSimulationRegistrationRequiresExactFixtureAcknowledgement(t *testing.T) {
	plan := simulationAPIPlan()
	_, digest, err := NormalizeScenarioSimulationPlan(plan, []string{"primary"})
	if err != nil {
		t.Fatal(err)
	}
	for _, ack := range []string{"", strings.Repeat("a", 64), digest} {
		t.Run("ack="+ack, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req RegisterScenarioTestRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Simulation == nil || req.ActionEvidenceVersion != 2 {
					t.Error("simulation missing from enrollment")
				}
				_ = json.NewEncoder(w).Encode(RegisterScenarioTestResponse{WebhookMode: "capture", NetworkMode: "contained", PolicyVersion: 1, ActionEvidenceVersion: 2, SimulationSHA256: ack, RecorderVersion: 1})
			}))
			defer server.Close()
			err := NewClient(server.URL, "key").RegisterScenarioTest(t.Context(), strings.Repeat("b", 32), RegisterScenarioTestRequest{Members: []ScenarioTestWorkload{{Workload: "primary", AppSlug: "preview"}}, WebhookMode: "capture", NetworkMode: "contained", ActionEvidenceVersion: 2, Simulation: &plan})
			if (err == nil) != (ack == digest) {
				t.Fatalf("acknowledgement %q: %v", ack, err)
			}
		})
	}
}

func TestRehearsalSimulationSealAcceptsItsScopeAndPendingRequests(t *testing.T) {
	for _, mode := range []string{"complete", "pending", "incomplete", "missing-digest", "mixed-scope", "negative-pending", "recorder-pending", "recorder-missing", "recorder-unknown", "recorder-negative", "recorder-false-complete"} {
		t.Run(mode, func(t *testing.T) {
			run := strings.Repeat("a", 32)
			s := ScenarioActionSnapshot{Version: 2, Scope: ScenarioSimulationScope, SimulationVersion: 1, RecorderVersion: 1, RecorderSessions: 1, SimulationSHA256: strings.Repeat("d", 64), RunID: run, Status: "complete", SealedAt: "2026-10-17T12:00:00Z", Workloads: []string{"primary"}, Intents: []ScenarioCapturedAction{}}
			status := 200
			switch mode {
			case "pending":
				s.Status, s.SealedAt, s.PendingSimulationAttempts, status = "pending", "", 1, 202
			case "incomplete":
				s.Status, s.IncompleteSimulationAttempts = "incomplete", 1
			case "missing-digest":
				s.SimulationSHA256 = ""
			case "mixed-scope":
				s.Scope = ScenarioActionScope
			case "recorder-pending":
				s.Status, s.SealedAt, s.PendingRecorderSessions, status = "pending", "", 1, 202
			case "recorder-missing":
				s.RecorderVersion = 0
			case "recorder-unknown":
				s.RecorderVersion = 2
			case "recorder-negative":
				s.PendingRecorderSessions = -1
			case "recorder-false-complete":
				s.PendingRecorderSessions = 1
			case "negative-pending":
				s.PendingSimulationAttempts = -1
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); _ = json.NewEncoder(w).Encode(s) }))
			defer server.Close()
			out, err := NewClient(server.URL, "key").SealScenarioTestActions(t.Context(), run)
			valid := mode == "complete" || mode == "pending" || mode == "incomplete" || mode == "recorder-pending"
			if (err == nil) != valid || valid && out.Scope != ScenarioSimulationScope {
				t.Fatalf("simulation seal %q: %+v %v", mode, out, err)
			}
		})
	}
}

func TestRehearsalRecorderRegistrationRequiresVersionBeforeDeployment(t *testing.T) {
	plan := simulationAPIPlan()
	_, digest, err := NormalizeScenarioSimulationPlan(plan, []string{"primary"})
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{0, 1, 2} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(RegisterScenarioTestResponse{WebhookMode: "capture", NetworkMode: "contained", PolicyVersion: 1, ActionEvidenceVersion: 2, SimulationSHA256: digest, RecorderVersion: version})
			}))
			defer server.Close()
			err := NewClient(server.URL, "key").RegisterScenarioTest(t.Context(), strings.Repeat("b", 32), RegisterScenarioTestRequest{Members: []ScenarioTestWorkload{{Workload: "primary", AppSlug: "preview"}}, WebhookMode: "capture", NetworkMode: "contained", ActionEvidenceVersion: 2, Simulation: &plan})
			if (err == nil) != (version == 1) {
				t.Fatalf("recorder version %d: %v", version, err)
			}
		})
	}
}

func TestContainedCommandRegistrationRequiresExactAcknowledgement(t *testing.T) {
	plan := simulationAPIPlan()
	_, digest, _ := NormalizeScenarioSimulationPlan(plan, []string{"primary"})
	for _, version := range []int{0, 1, 2} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req RegisterScenarioTestRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.CommandVersion != 1 {
					t.Error("missing requested command capability")
				}
				_ = json.NewEncoder(w).Encode(RegisterScenarioTestResponse{CommandVersion: version, RecorderVersion: 1, WebhookMode: "capture", NetworkMode: "contained", PolicyVersion: 1, ActionEvidenceVersion: 2, SimulationSHA256: digest})
			}))
			defer server.Close()
			err := NewClient(server.URL, "key").RegisterScenarioTest(t.Context(), strings.Repeat("a", 32), RegisterScenarioTestRequest{CommandVersion: 1, Members: []ScenarioTestWorkload{{Workload: "primary", AppSlug: "preview"}}, WebhookMode: "capture", NetworkMode: "contained", ActionEvidenceVersion: 2, Simulation: &plan})
			if (err == nil) != (version == 1) {
				t.Fatalf("command version %d: %v", version, err)
			}
		})
	}
}
