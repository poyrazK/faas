package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/rehearsalcommand"
)

func TestContainedCommandEnrollmentRequiresOperatorGateAndAcknowledgement(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.EnableAppTaskAPIForTest(&e)
	run := strings.Repeat("e", 32)
	r := e.Do(t, "PUT", "/v1/dev/sessions/command-api", api.UpsertDevSessionRequest{WorkspaceID: run}, nil)
	if r.Code != http.StatusCreated {
		t.Fatalf("session: %d %s", r.Code, r.Body.String())
	}
	var session api.DevSessionResponse
	if err := json.Unmarshal(r.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	s := *e.S()
	*s.RehearsalNetworkEnabled() = true
	*s.RehearsalSimulationEnabled() = true
	plan := api.ScenarioSimulationPlan{Version: 1, Fixtures: []api.ScenarioSimulationFixture{{Workload: "primary", Name: "notify", Integration: "email", Adapter: "resend", Method: "POST", Path: "/emails", Responses: []api.ScenarioSimulationResponse{{Status: 200}}}}}
	req := api.RegisterScenarioTestRequest{CommandVersion: 1, WebhookMode: "capture", NetworkMode: "contained", ActionEvidenceVersion: 2, Simulation: &plan, Members: []api.ScenarioTestWorkload{{Workload: "primary", AppSlug: session.App.Slug}}}
	r = e.Do(t, "PUT", "/v1/dev/test-runs/"+run, req, nil)
	if r.Code != http.StatusNotImplemented {
		t.Fatalf("unqualified command enrollment: %d %s", r.Code, r.Body.String())
	}
	*s.RehearsalCommandsEnabled() = true
	r = e.Do(t, "PUT", "/v1/dev/test-runs/"+run, req, nil)
	if r.Code != http.StatusOK {
		t.Fatalf("enrollment: %d %s", r.Code, r.Body.String())
	}
	var ack api.RegisterScenarioTestResponse
	if err := json.Unmarshal(r.Body.Bytes(), &ack); err != nil || ack.CommandVersion != 1 || ack.RecorderVersion != 1 {
		t.Fatalf("missing command acknowledgement: %+v %v", ack, err)
	}
}

func TestContainedCommandTaskCannotBypassDisabledOperatorGate(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.EnableAppTaskAPIForTest(&e)
	run := strings.Repeat("f", 32)
	r := e.Do(t, "PUT", "/v1/dev/sessions/command-api", api.UpsertDevSessionRequest{WorkspaceID: run}, nil)
	if r.Code != http.StatusCreated {
		t.Fatalf("session: %d %s", r.Code, r.Body.String())
	}
	var session api.DevSessionResponse
	if err := json.Unmarshal(r.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	argv, err := rehearsalcommand.Encode(rehearsalcommand.Spec{Version: 1, RunID: run, DeploymentID: "11111111-1111-1111-1111-111111111111", Command: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	r = e.Do(t, "POST", "/v1/apps/"+session.App.Slug+"/tasks", api.CreateAppTaskRequest{Command: argv, MaxOutputBytes: rehearsalcommand.MaxReceiptBytes}, nil)
	if r.Code != http.StatusNotImplemented {
		t.Fatalf("disabled runner admitted a command: %d %s", r.Code, r.Body.String())
	}
}
