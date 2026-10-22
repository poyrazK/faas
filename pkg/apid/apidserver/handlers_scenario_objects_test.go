package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/rehearsalobj"
	objruntime "github.com/onebox-faas/faas/pkg/rehearsalobj/runtime"
)

func TestRehearsalObjectAPIGatesEnrollmentAndKeepsCleanupAvailable(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	defer apidserver.WithTestRecipient(t)()
	apidserver.EnableAppTaskAPIForTest(&e)
	run := strings.Repeat("d", 32)
	r := e.Do(t, "PUT", "/v1/dev/sessions/object-api", api.UpsertDevSessionRequest{WorkspaceID: run}, nil)
	if r.Code != http.StatusCreated {
		t.Fatalf("session: %d %s", r.Code, r.Body.String())
	}
	var session api.DevSessionResponse
	if json.Unmarshal(r.Body.Bytes(), &session) != nil {
		t.Fatal("invalid session")
	}
	server := *e.S()
	*server.RehearsalNetworkEnabled() = true
	*server.RehearsalSimulationEnabled() = true
	*server.RehearsalCommandsEnabled() = true
	plan := api.ScenarioSimulationPlan{Version: 1, Fixtures: []api.ScenarioSimulationFixture{{Workload: "primary", Name: "notify", Integration: "email", Adapter: "resend", Method: "POST", Path: "/emails", Responses: []api.ScenarioSimulationResponse{{Status: 200}}}}}
	r = e.Do(t, "PUT", "/v1/dev/test-runs/"+run, api.RegisterScenarioTestRequest{CommandVersion: 1, WebhookMode: "capture", NetworkMode: "contained", ActionEvidenceVersion: 2, Simulation: &plan, Members: []api.ScenarioTestWorkload{{Workload: "primary", AppSlug: session.App.Slug}}}, nil)
	if r.Code != http.StatusOK {
		t.Fatalf("register: %d %s", r.Code, r.Body.String())
	}
	service := objruntime.NewService(*e.Store())
	collectorCtx, stopCollector := context.WithCancel(t.Context())
	defer stopCollector()
	go objruntime.NewCollector(*e.Store()).Run(collectorCtx)
	server.SetRehearsalObjectsForTest(service, false)
	objects := []rehearsalobj.Data{{Key: "seed", Body: []byte{0, 255, 10}, ContentType: "application/octet-stream", Metadata: map[string]string{}}}
	initial, _ := rehearsalobj.Snapshot(objects)
	request := rehearsalobj.CreateRequest{AppSlug: session.App.Slug, Spec: rehearsalobj.Spec{Version: 1, Bucket: "imports", FixtureSHA256: rehearsalobj.Hash(initial)}, Objects: objects}
	path := "/v1/dev/test-runs/" + run + "/objects"
	r = e.Do(t, "POST", path, request, nil)
	if r.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled adapter allocated: %d", r.Code)
	}
	server.SetRehearsalObjectsForTest(service, true)
	r = e.Do(t, "POST", "/v1/dev/test-runs/"+strings.Repeat("e", 32)+"/objects", request, nil)
	if r.Code != http.StatusNotFound {
		t.Fatalf("foreign run allocated: %d", r.Code)
	}
	bad := request
	bad.Spec.FixtureSHA256 = strings.Repeat("1", 64)
	r = e.Do(t, "POST", path, bad, nil)
	if r.Code != http.StatusBadRequest {
		t.Fatalf("unverified fixture allocated: %d", r.Code)
	}
	r = e.Do(t, "POST", path, request, nil)
	if r.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", r.Code, r.Body.String())
	}
	var receipt rehearsalobj.Receipt
	if json.Unmarshal(r.Body.Bytes(), &receipt) != nil || receipt.AppID != session.App.ID || receipt.FixtureSHA256 != request.Spec.FixtureSHA256 {
		t.Fatal("receipt is not pinned")
	}
	if strings.Contains(r.Body.String(), "AWS_") || strings.Contains(r.Body.String(), "secret") || strings.Contains(r.Body.String(), "body") {
		t.Fatal("API exposed fixture bytes or credentials")
	}
	resource, err := service.Catalog.GetScenarioObjects(t.Context(), e.Acct().ID, run)
	if err != nil || len(resource.SecretSealed) == 0 || len(resource.Current) != 1 {
		t.Fatalf("namespace: %v", err)
	}
	r = e.Do(t, "POST", path+"/seal", rehearsalobj.SealRequest{DeploymentID: "11111111-1111-1111-1111-111111111111"}, nil)
	if r.Code != http.StatusBadRequest {
		t.Fatalf("unbound deployment sealed fixture: %d %s", r.Code, r.Body.String())
	}
	server.SetRehearsalObjectsForTest(service, false)
	for range 2 {
		r = e.Do(t, "DELETE", path, nil, nil)
		if r.Code != http.StatusNoContent {
			t.Fatalf("cleanup unavailable after disabling: %d %s", r.Code, r.Body.String())
		}
	}
	resource, err = service.Catalog.GetScenarioObjects(t.Context(), e.Acct().ID, run)
	if err != nil || resource.Status != "deleted" || len(resource.Current) != 0 {
		t.Fatal("cleanup retained object bytes")
	}
}
