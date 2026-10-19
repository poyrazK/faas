package apidserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/rehearsalresume"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
)

func TestSuspendResumeEnrollmentQualificationAndAccountOwnedReceipts(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	defer apidserver.WithTestRecipient(t)()
	apidserver.EnableAppTaskAPIForTest(&e)
	s := *e.S()
	*s.RehearsalNetworkEnabled() = true
	*s.RehearsalSimulationEnabled() = true
	*s.RehearsalCommandsEnabled() = true
	run := strings.Repeat("6", 32)
	r := e.Do(t, "PUT", "/v1/dev/sessions/primary", api.UpsertDevSessionRequest{WorkspaceID: run}, nil)
	if r.Code != http.StatusCreated {
		t.Fatal(r.Body.String())
	}
	var session api.DevSessionResponse
	if err := json.Unmarshal(r.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	sim := api.ScenarioSimulationPlan{Version: 1, Fixtures: []api.ScenarioSimulationFixture{{Workload: "primary", Name: "notify", Integration: "email", Adapter: "resend", Method: "POST", Path: "/emails", Responses: []api.ScenarioSimulationResponse{{Status: 200}}}}}
	req := api.RegisterScenarioTestRequest{SuspendResume: &rehearsalresume.Spec{Version: 1, Checkpoint: "after_setup", Workloads: []string{"primary"}, PauseMS: 10, WithinMS: 1000}, CommandVersion: 1, NetworkMode: "contained", WebhookMode: "capture", ActionEvidenceVersion: 2, Simulation: &sim, Members: []api.ScenarioTestWorkload{{Workload: "primary", AppSlug: session.App.Slug}}}
	path := "/v1/dev/test-runs/" + run
	if r = e.Do(t, "PUT", path, req, nil); r.Code != http.StatusServiceUnavailable {
		t.Fatalf("unqualified enrollment: %d %s", r.Code, r.Body.String())
	}
	*s.RehearsalSuspendResumeEnabled() = true
	if r = e.Do(t, "PUT", path, req, nil); r.Code != http.StatusOK {
		t.Fatalf("enrollment: %d %s", r.Code, r.Body.String())
	}
	var ack api.RegisterScenarioTestResponse
	if err := json.Unmarshal(r.Body.Bytes(), &ack); err != nil {
		t.Fatal(err)
	}
	_, d, _ := api.ScenarioSuspendResumeEnrollment(req)
	if ack.SuspendResumeSHA256 != d {
		t.Fatal("plan not acknowledged")
	}
	*s.RehearsalSuspendResumeEnabled() = false
	if r = e.Do(t, "GET", path+"/suspend-resume", nil, nil); r.Code != http.StatusOK {
		t.Fatalf("durable evidence disappeared when admission disabled: %d %s", r.Code, r.Body.String())
	}
	if r = e.Do(t, "POST", path+"/suspend-resume", nil, nil); r.Code != http.StatusConflict {
		t.Fatalf("unbooted VM supplied proof: %d %s", r.Code, r.Body.String())
	}
	if r = e.Do(t, "GET", "/v1/dev/test-runs/"+strings.Repeat("5", 32)+"/suspend-resume", nil, nil); r.Code != http.StatusNotFound {
		t.Fatal("unowned run was visible")
	}
}

func TestRunningSuspendEnrollmentRequiresIndependentQualification(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	defer apidserver.WithTestRecipient(t)()
	apidserver.EnableAppTaskAPIForTest(&e)
	s := *e.S()
	*s.RehearsalNetworkEnabled() = true
	*s.RehearsalSimulationEnabled() = true
	*s.RehearsalCommandsEnabled() = true
	*s.RehearsalWorkersEnabled() = true
	*s.RehearsalSuspendResumeEnabled() = true
	run := strings.Repeat("7", 32)
	members := []api.ScenarioTestWorkload{}
	for _, name := range []string{"primary", "worker"} {
		response := e.Do(t, "PUT", "/v1/dev/sessions/"+name, api.UpsertDevSessionRequest{WorkspaceID: run}, nil)
		if response.Code != http.StatusCreated {
			t.Fatal(response.Body.String())
		}
		var session api.DevSessionResponse
		if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil {
			t.Fatal(err)
		}
		members = append(members, api.ScenarioTestWorkload{Workload: name, AppSlug: session.App.Slug})
	}
	simulation := api.ScenarioSimulationPlan{Version: 1, Fixtures: []api.ScenarioSimulationFixture{{Workload: "worker", Name: "after-commit", Integration: "checkpoint", Adapter: "http", Method: "POST", Path: "/after-commit", Responses: []api.ScenarioSimulationResponse{{Status: 200}}}}}
	plan := rehearsalwork.Plan{Version: 1, Primary: "primary", Participants: []rehearsalwork.Participant{{Workload: "primary"}, {Workload: "worker", Async: true}}}
	req := api.RegisterScenarioTestRequest{Members: members, CommandVersion: 1, NetworkMode: "contained", WebhookMode: "capture", ActionEvidenceVersion: 2, Simulation: &simulation, ManagedWork: &plan, SuspendResume: &rehearsalresume.Spec{Version: 1, Checkpoint: "managed_job", Workloads: []string{"worker"}, WorkerCheckpoint: "after-commit", PauseMS: 10, WithinMS: 1000}}
	path := "/v1/dev/test-runs/" + run
	if response := e.Do(t, "PUT", path, req, nil); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("running-job gate did not refuse: %d %s", response.Code, response.Body.String())
	}
	*s.RehearsalRunningSuspendEnabled() = true
	response := e.Do(t, "PUT", path, req, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("enrollment: %d %s", response.Code, response.Body.String())
	}
	var ack api.RegisterScenarioTestResponse
	if err := json.Unmarshal(response.Body.Bytes(), &ack); err != nil {
		t.Fatal(err)
	}
	_, digest, _ := api.ScenarioSuspendResumeEnrollment(req)
	if ack.SuspendResumeSHA256 != digest {
		t.Fatal("wrong checkpoint plan acknowledgement")
	}
	*s.RehearsalRunningSuspendEnabled() = false
	if response = e.Do(t, "GET", path+"/suspend-resume", nil, nil); response.Code != http.StatusOK {
		t.Fatal("durable observation depends on admission gate")
	}
	if response = e.Do(t, "POST", path+"/suspend-resume", nil, nil); response.Code != http.StatusConflict {
		t.Fatal("public API bypassed original cooperative checkpoint")
	}
}
