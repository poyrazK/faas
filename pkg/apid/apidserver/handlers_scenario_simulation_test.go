// adr: 20261017201006
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestRehearsalSimulationEnrollmentCommitsExactFixtures(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	const run = "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"
	r := e.Do(t, "PUT", "/v1/dev/sessions/simulation-api", api.UpsertDevSessionRequest{WorkspaceID: run}, nil)
	if r.Code != http.StatusCreated {
		t.Fatalf("session: %d %s", r.Code, r.Body.String())
	}
	var session api.DevSessionResponse
	if err := json.Unmarshal(r.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	s := *e.S()
	*s.RehearsalNetworkEnabled() = true
	plan := api.ScenarioSimulationPlan{Version: 1, Fixtures: []api.ScenarioSimulationFixture{{Workload: "primary", Name: "notify", Integration: "email", Adapter: "resend", Method: "POST", Path: "/emails", Responses: []api.ScenarioSimulationResponse{{Status: 200, Body: `{"id":"simulated-1"}`}}}}}
	req := api.RegisterScenarioTestRequest{WebhookMode: "capture", NetworkMode: "contained", ActionEvidenceVersion: 2, Simulation: &plan, Members: []api.ScenarioTestWorkload{{Workload: "primary", AppSlug: session.App.Slug}}}
	r = e.Do(t, "PUT", "/v1/dev/test-runs/"+run, req, nil)
	if r.Code != http.StatusNotImplemented {
		t.Fatalf("unqualified simulation enrollment: %d %s", r.Code, r.Body.String())
	}
	*s.RehearsalSimulationEnabled() = true
	r = e.Do(t, "PUT", "/v1/dev/test-runs/"+run, req, nil)
	if r.Code != http.StatusOK {
		t.Fatalf("enrollment: %d %s", r.Code, r.Body.String())
	}
	var ack api.RegisterScenarioTestResponse
	if err := json.Unmarshal(r.Body.Bytes(), &ack); err != nil {
		t.Fatal(err)
	}
	_, digest, err := api.NormalizeScenarioSimulationPlan(plan, []string{"primary"})
	if err != nil {
		t.Fatal(err)
	}
	if ack.RecorderVersion != 1 || ack.SimulationSHA256 != digest || ack.ActionEvidenceVersion != 2 {
		t.Fatalf("missing simulation acknowledgement: %+v", ack)
	}
	store := *e.Store()
	app, err := store.AppBySlug(t.Context(), session.App.Slug)
	if err != nil {
		t.Fatal(err)
	}
	member, err := store.ScenarioTestMemberByApp(t.Context(), app.ID)
	if err != nil || api.ScenarioSimulationDigest([]byte(member.SimulationJSON)) != digest {
		t.Fatalf("acknowledged unstored fixtures: %+v %v", member, err)
	}
}
