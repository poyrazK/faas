// adr: 20261018000200
package api

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/rehearsalcold"
	"github.com/onebox-faas/faas/pkg/rehearsalfault"
	"github.com/onebox-faas/faas/pkg/rehearsalresume"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
)

func TestPostgresFaultEnrollmentRejectsUnsupportedScope(t *testing.T) {
	for _, mode := range []string{"valid", "wrong_worker", "no_database", "too_long", "no_checkpoint", "delayed_checkpoint", "wrong_integration", "termination", "suspend", "cold"} {
		t.Run(mode, func(t *testing.T) {
			p := &rehearsalwork.Plan{Version: 1, Primary: "primary", Participants: []rehearsalwork.Participant{{Workload: "primary", Postgres: "read_write"}, {Workload: "worker", Async: true, Postgres: "read_write"}}, PostgresFault: &rehearsalfault.Postgres{Workload: "worker", Checkpoint: "after-commit", UnavailableMS: 1000, RecoverWithinMS: 5000}}
			s := &ScenarioSimulationPlan{Version: 1, Fixtures: []ScenarioSimulationFixture{{Workload: "worker", Name: "after-commit", Integration: "checkpoint", Adapter: "http", Method: "POST", Path: "/after-commit", Responses: []ScenarioSimulationResponse{{Status: 200}}}}}
			r := RegisterScenarioTestRequest{CommandVersion: 1, ManagedWork: p, Simulation: s, NetworkMode: "contained", WebhookMode: "capture", ActionEvidenceVersion: 2, Members: []ScenarioTestWorkload{{Workload: "primary", AppSlug: "preview-api"}, {Workload: "worker", AppSlug: "preview-worker"}}}
			switch mode {
			case "wrong_worker":
				p.PostgresFault.Workload = "primary"
			case "no_database":
				p.Participants[1].Postgres = ""
			case "too_long":
				p.PostgresFault.UnavailableMS = 30001
			case "no_checkpoint":
				s.Fixtures = nil
			case "delayed_checkpoint":
				s.Fixtures[0].Responses[0].DelayMS = 1
			case "wrong_integration":
				s.Fixtures[0].Integration = "other"
			case "termination":
				p.WorkerTermination = &rehearsalwork.WorkerTermination{Workload: "worker", Checkpoint: "after-commit"}
			case "suspend":
				r.SuspendResume = &rehearsalresume.Spec{}
			case "cold":
				r.ColdStart = &rehearsalcold.Spec{}
			}
			if _, _, err := ScenarioManagedWorkEnrollment(r); (err == nil) != (mode == "valid") {
				t.Fatalf("%s: %v", mode, err)
			}
		})
	}
}
