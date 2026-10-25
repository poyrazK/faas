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
	"github.com/onebox-faas/faas/pkg/rehearsalpg"
	pgruntime "github.com/onebox-faas/faas/pkg/rehearsalpg/runtime"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
)

func TestManagedWorkerEnrollmentGateValidationAndDurableAcknowledgement(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	defer apidserver.WithTestRecipient(t)()
	apidserver.EnableAppTaskAPIForTest(&e)
	s := *e.S()
	*s.RehearsalNetworkEnabled() = true
	*s.RehearsalSimulationEnabled() = true
	*s.RehearsalCommandsEnabled() = true
	run := strings.Repeat("9", 32)
	members := []api.ScenarioTestWorkload{}
	for _, name := range []string{"primary", "worker", "unbound"} {
		r := e.Do(t, "PUT", "/v1/dev/sessions/"+name, api.UpsertDevSessionRequest{WorkspaceID: run}, nil)
		if r.Code != http.StatusCreated {
			t.Fatal(r.Body.String())
		}
		var session api.DevSessionResponse
		if err := json.Unmarshal(r.Body.Bytes(), &session); err != nil {
			t.Fatal(err)
		}
		members = append(members, api.ScenarioTestWorkload{Workload: name, AppSlug: session.App.Slug})
	}
	plan := rehearsalwork.Plan{Version: 1, Primary: "primary", Participants: []rehearsalwork.Participant{{Workload: "primary", Objects: "read_write", Postgres: "read_write"}, {Workload: "worker", Async: true, Objects: "read", Postgres: "read_write"}, {Workload: "unbound"}}}
	simulation := api.ScenarioSimulationPlan{Version: 1, Fixtures: []api.ScenarioSimulationFixture{{Workload: "worker", Name: "notify", Integration: "email", Adapter: "resend", Method: "POST", Path: "/emails", Responses: []api.ScenarioSimulationResponse{{Status: 200}}}}}
	req := api.RegisterScenarioTestRequest{ManagedWork: &plan, CommandVersion: 1, NetworkMode: "contained", WebhookMode: "capture", ActionEvidenceVersion: 2, Simulation: &simulation, Members: members}
	url := "/v1/dev/test-runs/" + run
	if r := e.Do(t, "PUT", url, req, nil); r.Code != 503 {
		t.Fatalf("unqualified workers admitted: %d %s", r.Code, r.Body.String())
	}
	*s.RehearsalWorkersEnabled() = true
	plan.Participants[1].Workload = "unknown"
	if r := e.Do(t, "PUT", url, req, nil); r.Code != 400 {
		t.Fatalf("unknown participant admitted: %d %s", r.Code, r.Body.String())
	}
	plan.Participants[1].Workload = "worker"
	plan.WorkerTermination = &rehearsalwork.WorkerTermination{Workload: "worker", Checkpoint: "after-commit"}
	if r := e.Do(t, "PUT", url, req, nil); r.Code != http.StatusNotImplemented {
		t.Fatalf("unqualified termination admitted: %d %s", r.Code, r.Body.String())
	}
	*s.RehearsalWorkerFaultsEnabled() = true
	if r := e.Do(t, "PUT", url, req, nil); r.Code != http.StatusBadRequest {
		t.Fatalf("missing checkpoint admitted: %d %s", r.Code, r.Body.String())
	}
	simulation.Fixtures = append(simulation.Fixtures, api.ScenarioSimulationFixture{Workload: "worker", Name: "after-commit", Integration: "checkpoint", Adapter: "http", Method: "POST", Path: "/after-commit", Responses: []api.ScenarioSimulationResponse{{Status: 200}}})
	r := e.Do(t, "PUT", url, req, nil)
	if r.Code != 200 {
		t.Fatalf("enrollment: %d %s", r.Code, r.Body.String())
	}
	var ack api.RegisterScenarioTestResponse
	if err := json.Unmarshal(r.Body.Bytes(), &ack); err != nil {
		t.Fatal(err)
	}
	_, digest, err := api.ScenarioManagedWorkEnrollment(req)
	if err != nil || ack.ManagedWorkSHA256 != digest {
		t.Fatalf("durable plan acknowledgement differs: %+v %v", ack, err)
	}
	objects := objruntime.NewService(*e.Store())
	s.SetRehearsalObjectsForTest(objects, true)
	collectorCtx, stop := context.WithCancel(t.Context())
	defer stop()
	go objruntime.NewCollector(*e.Store()).Run(collectorCtx)
	initial, _ := rehearsalobj.Snapshot([]rehearsalobj.Data{})
	objectPath := url + "/objects"
	r = e.Do(t, "POST", objectPath, rehearsalobj.CreateRequest{AppSlug: members[0].AppSlug, Spec: rehearsalobj.Spec{Version: 1, Bucket: "imports", FixtureSHA256: rehearsalobj.Hash(initial)}, Objects: []rehearsalobj.Data{}}, nil)
	if r.Code != 201 {
		t.Fatalf("object bindings: %d %s", r.Code, r.Body.String())
	}
	t.Cleanup(func() { _ = objects.Delete(context.Background(), e.Acct().ID, run) })
	backend := &workerFixtureBackend{}
	postgres := pgruntime.NewService(*e.Store(), backend)
	s.SetRehearsalPostgresForTest(postgres, true)
	sql := "CREATE TABLE public.imports(item text);"
	r = e.Do(t, "POST", url+"/postgres", rehearsalpg.CreateRequest{AppSlug: members[0].AppSlug, SQL: sql, Spec: rehearsalpg.Spec{Version: 1, FixtureSHA256: rehearsalpg.HashBytes([]byte(sql)), Tables: []rehearsalpg.Table{{Schema: "public", Name: "imports", Columns: []string{"item"}}}, MaxLockWaitMS: 50}}, nil)
	if r.Code != 201 {
		t.Fatalf("database bindings: %d %s", r.Code, r.Body.String())
	}
	t.Cleanup(func() { _ = postgres.Delete(context.Background(), e.Acct().ID, run) })
	if len(backend.roles) != 1 || !strings.HasPrefix(backend.roles[0], "grpw_") {
		t.Fatal("worker did not receive its own fixture login")
	}
	for _, member := range members {
		app, err := (*e.Store()).AppBySlug(t.Context(), member.AppSlug)
		if err != nil {
			t.Fatal(err)
		}
		secrets, err := (*e.Store()).ListAppSecrets(t.Context(), e.Acct().ID, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		wanted := map[string]bool{"AWS_ACCESS_KEY_ID": true, "AWS_SECRET_ACCESS_KEY": true, "AWS_REGION": true, "AWS_ENDPOINT_URL_S3": true, "S3_BUCKET": true, "DATABASE_URL": true}
		count := 0
		for _, secret := range secrets {
			if wanted[secret.Key] {
				count++
				if secret.SecretClass != api.SecretClassEphemeral || len(secret.Ciphertext) == 0 {
					t.Fatal("fixture secret is not sealed and ephemeral")
				}
			}
		}
		expected := 6
		if member.Workload == "unbound" {
			expected = 0
		}
		if count != expected {
			t.Fatalf("%s received %d fixture secrets; expected %d", member.Workload, count, expected)
		}
	}
}

type workerFixtureBackend struct {
	postgresFixtureBackend
	roles []string
}

func (b *workerFixtureBackend) BindParticipant(_ context.Context, _ rehearsalpg.Resource, role, _ string) error {
	b.roles = append(b.roles, role)
	return nil
}
