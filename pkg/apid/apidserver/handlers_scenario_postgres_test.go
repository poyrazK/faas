package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/rehearsalpg"
	pgruntime "github.com/onebox-faas/faas/pkg/rehearsalpg/runtime"
)

type postgresFixtureBackend struct{ created, dropped int }

func (b *postgresFixtureBackend) Identity() string { return strings.Repeat("f", 64) }
func (b *postgresFixtureBackend) Provision(context.Context, rehearsalpg.Resource, string, string) ([]rehearsalpg.Snapshot, error) {
	b.created++
	return []rehearsalpg.Snapshot{{Table: "public.imports", Rows: []json.RawMessage{}}}, nil
}
func (b *postgresFixtureBackend) FenceAndCapture(context.Context, rehearsalpg.Resource) ([]rehearsalpg.Snapshot, error) {
	return []rehearsalpg.Snapshot{{Table: "public.imports", Rows: []json.RawMessage{}}}, nil
}
func (b *postgresFixtureBackend) LockWait(context.Context, rehearsalpg.Resource) (int64, error) {
	return 0, nil
}
func (b *postgresFixtureBackend) Drop(context.Context, rehearsalpg.Resource) error {
	b.dropped++
	return nil
}

func TestRehearsalPostgresAPIGatesProvisioningAndKeepsCleanupAvailable(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	defer apidserver.WithTestRecipient(t)()
	apidserver.EnableAppTaskAPIForTest(&e)
	run := strings.Repeat("e", 32)
	r := e.Do(t, "PUT", "/v1/dev/sessions/postgres-api", api.UpsertDevSessionRequest{WorkspaceID: run}, nil)
	if r.Code != http.StatusCreated {
		t.Fatalf("session: %d %s", r.Code, r.Body.String())
	}
	var session api.DevSessionResponse
	if json.Unmarshal(r.Body.Bytes(), &session) != nil {
		t.Fatal("invalid session")
	}
	s := *e.S()
	*s.RehearsalNetworkEnabled() = true
	*s.RehearsalSimulationEnabled() = true
	*s.RehearsalCommandsEnabled() = true
	plan := api.ScenarioSimulationPlan{Version: 1, Fixtures: []api.ScenarioSimulationFixture{{Workload: "primary", Name: "notify", Integration: "email", Adapter: "resend", Method: "POST", Path: "/emails", Responses: []api.ScenarioSimulationResponse{{Status: 200}}}}}
	registration := api.RegisterScenarioTestRequest{CommandVersion: 1, WebhookMode: "capture", NetworkMode: "contained", ActionEvidenceVersion: 2, Simulation: &plan, Members: []api.ScenarioTestWorkload{{Workload: "primary", AppSlug: session.App.Slug}}}
	r = e.Do(t, "PUT", "/v1/dev/test-runs/"+run, registration, nil)
	if r.Code != http.StatusOK {
		t.Fatalf("register: %d %s", r.Code, r.Body.String())
	}
	driver := &postgresFixtureBackend{}
	service := pgruntime.NewService(*e.Store(), driver)
	s.SetRehearsalPostgresForTest(service, false)
	fixture := "CREATE TABLE public.imports(item text);"
	request := rehearsalpg.CreateRequest{AppSlug: session.App.Slug, SQL: fixture, Spec: rehearsalpg.Spec{Version: 1, FixtureSHA256: rehearsalpg.HashBytes([]byte(fixture)), Tables: []rehearsalpg.Table{{Schema: "public", Name: "imports", Columns: []string{"item"}}}, MaxLockWaitMS: 50}}
	path := "/v1/dev/test-runs/" + run + "/postgres"
	r = e.Do(t, "POST", path, request, nil)
	if r.Code != http.StatusServiceUnavailable || driver.created != 0 {
		t.Fatalf("disabled adapter allocated: %d", r.Code)
	}
	s.SetRehearsalPostgresForTest(service, true)
	r = e.Do(t, "POST", "/v1/dev/test-runs/"+strings.Repeat("f", 32)+"/postgres", request, nil)
	if r.Code != http.StatusNotFound || driver.created != 0 {
		t.Fatalf("foreign run allocated: %d", r.Code)
	}
	r = e.Do(t, "POST", path, request, nil)
	if r.Code != http.StatusCreated || driver.created != 1 {
		t.Fatalf("create: %d %s", r.Code, r.Body.String())
	}
	var receipt rehearsalpg.Receipt
	if json.Unmarshal(r.Body.Bytes(), &receipt) != nil || receipt.AppID != session.App.ID || receipt.FixtureSHA256 != request.Spec.FixtureSHA256 {
		t.Fatal("receipt is not pinned")
	}
	if strings.Contains(r.Body.String(), "postgresql://") || strings.Contains(r.Body.String(), "DATABASE_URL") {
		t.Fatal("API exposed database credentials")
	}
	r = e.Do(t, "POST", path+"/seal", rehearsalpg.SealRequest{DeploymentID: "11111111-1111-1111-1111-111111111111"}, nil)
	if r.Code != http.StatusBadRequest {
		t.Fatalf("unbound deployment sealed fixture: %d %s", r.Code, r.Body.String())
	}
	s.SetRehearsalPostgresForTest(service, false)
	r = e.Do(t, "DELETE", path, nil, nil)
	if r.Code != http.StatusNoContent || driver.dropped != 1 {
		t.Fatalf("cleanup unavailable after disabling: %d %s", r.Code, r.Body.String())
	}
	r = e.Do(t, "DELETE", path, nil, nil)
	if r.Code != http.StatusNoContent || driver.dropped != 1 {
		t.Fatal("cleanup was not idempotent")
	}
}
