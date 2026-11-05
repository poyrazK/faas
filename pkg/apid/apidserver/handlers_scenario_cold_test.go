package apidserver_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/rehearsalcold"
	"net/http"
	"strings"
	"testing"
)

func TestColdStartEnrollmentRequiresQualificationAndPinsBudget(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	defer apidserver.WithTestRecipient(t)()
	apidserver.EnableAppTaskAPIForTest(&e)
	s := *e.S()
	*s.RehearsalNetworkEnabled() = true
	*s.RehearsalSimulationEnabled() = true
	*s.RehearsalCommandsEnabled() = true
	run := strings.Repeat("7", 32)
	r := e.Do(t, "PUT", "/v1/dev/sessions/primary", api.UpsertDevSessionRequest{WorkspaceID: run}, nil)
	if r.Code != http.StatusCreated {
		t.Fatal(r.Body.String())
	}
	var session api.DevSessionResponse
	if err := json.Unmarshal(r.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	simulation := api.ScenarioSimulationPlan{Version: 1, Fixtures: []api.ScenarioSimulationFixture{{Workload: "primary", Name: "notify", Integration: "email", Adapter: "resend", Method: "POST", Path: "/emails", Responses: []api.ScenarioSimulationResponse{{Status: 200}}}}}
	req := api.RegisterScenarioTestRequest{ColdStart: &rehearsalcold.Spec{Version: 1, WithinMS: 1000}, CommandVersion: 1, NetworkMode: "contained", WebhookMode: "capture", ActionEvidenceVersion: 2, Simulation: &simulation, Members: []api.ScenarioTestWorkload{{Workload: "primary", AppSlug: session.App.Slug}}}
	path := "/v1/dev/test-runs/" + run
	if r = e.Do(t, "PUT", path, req, nil); r.Code != http.StatusNotImplemented {
		t.Fatalf("unqualified cold enrollment: %d %s", r.Code, r.Body.String())
	}
	*s.RehearsalColdStartEnabled() = true
	req.ColdStart.WithinMS = 120001
	if r = e.Do(t, "PUT", path, req, nil); r.Code != http.StatusBadRequest {
		t.Fatalf("invalid budget: %d %s", r.Code, r.Body.String())
	}
	req.ColdStart.WithinMS = 1000
	r = e.Do(t, "PUT", path, req, nil)
	if r.Code != http.StatusOK {
		t.Fatalf("enrollment: %d %s", r.Code, r.Body.String())
	}
	var ack api.RegisterScenarioTestResponse
	if err := json.Unmarshal(r.Body.Bytes(), &ack); err != nil {
		t.Fatal(err)
	}
	_, digest, _ := api.ScenarioColdStartEnrollment(req)
	if ack.ColdStartSHA256 != digest {
		t.Fatal("cold-start budget was not acknowledged")
	}
	app, err := (*e.Store()).AppBySlug(t.Context(), session.App.Slug)
	if err != nil {
		t.Fatal(err)
	}
	member, err := (*e.Store()).ScenarioTestMemberByApp(t.Context(), app.ID)
	if err != nil || rehearsalcold.Decode(member.ColdStartJSON).WithinMS != 1000 {
		t.Fatalf("durable enrollment: %+v %v", member, err)
	}
}
