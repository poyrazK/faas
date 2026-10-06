// adr: 428 — a passing serving revision cannot validate a failing candidate.
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedBindingCandidate(t *testing.T, e testEnv, app state.App, scope string) state.Deployment {
	t.Helper()
	return seedBindingCandidateWithContext(context.Background(), t, e, app, scope)
}

func seedBindingCandidateWithContext(ctx context.Context, t *testing.T, e testEnv, app state.App, scope string) state.Deployment {
	t.Helper()
	dep, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, Scope: scope, TrafficPercent: 0, TrafficPercentExplicit: true, ImageDigest: appTaskTestDigest, CreatedAt: time.Now().UTC().Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetDeploymentRootfs(ctx, dep.ID, "/candidate/image", "candidate/"+dep.ID, 4096); err != nil {
		t.Fatal(err)
	}
	dep, err = e.store.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	return dep
}

func TestBindingDeploymentSelectionSeparatesServingAndCandidateEvidence(t *testing.T) {
	for _, kind := range []string{api.BindingTypeService, api.BindingTypePostgres, api.BindingTypeObjectStorage} {
		t.Run(kind, func(t *testing.T) {
			e := setup(t, api.PlanPro)
			enableAppTaskAPIForTest(&e)
			app, a := seedAppTaskDeployment(t, e, "pinned-"+strings.ReplaceAll(kind, "_", "-"))
			b := seedBindingCandidate(t, e, app, "default")
			command, selection, passed := "", "", ""
			switch kind {
			case api.BindingTypeService:
				manifest := state.AppManifest{ServiceBindingPolicy: api.ServiceBindingPolicyDeclared, ServiceBindingTransport: api.ServiceBindingTransportHTTPS, ServiceBindings: []api.AppServiceBinding{{Service: "billing", Binding: "GREGALE_SERVICE_BILLING_URL"}}}
				var err error
				app, err = e.store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Manifest: &manifest})
				if err != nil {
					t.Fatal(err)
				}
				command, selection, passed = api.AppTaskServiceBindingProbeCommand, "billing", `{"service":"billing","dns":{"status":"passed"},"tls":{"status":"passed"},"authorization":{"status":"passed"},"routing":{"status":"passed"}}`
			case api.BindingTypePostgres:
				readyVerificationBinding(t, e, app)
				command, selection, passed = api.AppTaskPostgresBindingProbeCommand, "DATABASE_URL", passedPostgresVerification
			case api.BindingTypeObjectStorage:
				readyObjectStorageVerificationBinding(t, e, app, "default")
				command, selection, passed = api.AppTaskObjectStorageBindingProbeCommand, "ASSETS", passedObjectStorageVerification
			}
			inventory := func(id string) api.AppBindingInventory {
				path := "/v1/apps/" + app.Slug + "/bindings"
				if id != "" {
					path += "?deployment_id=" + id
				}
				return decodeBindingInventory(t, e.do(t, http.MethodGet, path, nil, nil))
			}
			item := func(i api.AppBindingInventory) api.AppBindingInventoryItem {
				for _, binding := range i.Bindings {
					if binding.Type == kind {
						return binding
					}
				}
				t.Fatalf("missing %s: %+v", kind, i)
				return api.AppBindingInventoryItem{}
			}
			probe := func(id, output string) {
				task := createAppTaskForTest(t, e, app.Slug, api.CreateAppTaskRequest{Command: []string{command, selection}, VerificationDeploymentID: id})
				stored, err := e.store.AppTaskByID(context.Background(), e.acct.ID, app.ID, task.ID)
				if err != nil || stored.BindingVerification == nil || task.DeploymentID != id || stored.ImageDigest != appTaskTestDigest {
					t.Fatalf("target pin: %+v %v", stored, err)
				}
				if id == b.ID && (stored.ArtifactKey != b.RootfsKey || task.DeploymentScope != b.Scope) {
					t.Fatalf("wrong candidate artifact/scope: %+v", stored)
				}
				completeVerificationTask(t, e, beginVerificationTask(t, e, task.ID), output)
			}
			probe(a.ID, passed)
			before := inventory(b.ID)
			if before.RequestedDeploymentID != b.ID || before.VerificationDeploymentID != b.ID || item(before).VerificationStatus != "unknown" {
				t.Fatalf("unprobed B reused A: %+v", before)
			}
			probe(b.ID, strings.Replace(passed, `"status":"passed"`, `"status":"failed"`, 1))
			// A later passing probe also cannot replace B's failed result.
			probe(a.ID, passed)
			candidate := inventory(b.ID)
			if binding := item(candidate); binding.VerificationStatus != "failed" || binding.Verification == nil || binding.Verification.DeploymentID != b.ID {
				t.Fatalf("B lost failed evidence: %+v", binding)
			}
			for _, id := range []string{"", a.ID} {
				serving := inventory(id)
				if binding := item(serving); serving.VerificationDeploymentID != a.ID || binding.VerificationStatus != "passed" || binding.Verification.DeploymentID != a.ID {
					t.Fatalf("A evidence changed: %+v", serving)
				}
			}
			report, err := bindingcheck.Evaluate(candidate, bindingcheck.Policy{App: app.Slug, DeploymentID: b.ID, MaxVerificationAge: 10 * time.Minute}, candidate.GeneratedAt.Add(3*time.Second))
			found := false
			for _, blocker := range report.Blockers {
				found = found || blocker.Code == "verification_failed"
			}
			if err != nil || report.Passed || !found {
				t.Fatalf("failed B preflight accepted: %+v %v", report, err)
			}
			if err := e.store.MarkAppRuntimeConfigChanged(context.Background(), app.ID); err != nil {
				t.Fatal(err)
			}
			if binding := item(inventory(a.ID)); binding.VerificationStatus != "stale" {
				t.Fatalf("pinned evidence survived config change: %+v", binding)
			}
		})
	}
}

func TestBindingDeploymentSelectionRejectsUnavailableOrForeignTargets(t *testing.T) {
	e := setup(t, api.PlanPro)
	enableAppTaskAPIForTest(&e)
	app, _ := seedAppTaskDeployment(t, e, "target-validation")
	readyVerificationBinding(t, e, app)
	_, foreign := seedAppTaskDeployment(t, e, "foreign-target")
	retired := seedBindingCandidate(t, e, app, "default")
	if err := e.store.UpdateDeploymentStatus(context.Background(), retired.ID, state.DeploySuperseded, ""); err != nil {
		t.Fatal(err)
	}
	unmaterialized, err := e.store.CreateDeployment(context.Background(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: appTaskTestDigest})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id   string
		code int
	}{{"v12", 422}, {uuid.NewString(), 404}, {foreign.ID, 404}, {retired.ID, 409}, {unmaterialized.ID, 409}} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			path := "/v1/apps/" + app.Slug + "/bindings?deployment_id=" + tc.id
			var body any
			if method == http.MethodPost {
				path = "/v1/apps/" + app.Slug + "/tasks"
				body = api.CreateAppTaskRequest{Command: []string{api.AppTaskPostgresBindingProbeCommand, "DATABASE_URL"}, VerificationDeploymentID: tc.id}
			}
			response := e.do(t, method, path, body, nil)
			if response.Code != tc.code {
				t.Fatalf("%s target %s: %d %s", method, tc.id, response.Code, response.Body.String())
			}
		}
	}
	for _, command := range [][]string{{"echo", "DATABASE_URL"}, {api.AppTaskServiceBindingSmokeCommand, "billing"}} {
		response := e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/tasks", api.CreateAppTaskRequest{Command: command, VerificationDeploymentID: foreign.ID}, nil)
		if response.Code != 422 {
			t.Fatalf("ordinary task targeted deployment: %d %s", response.Code, response.Body.String())
		}
	}
}

func TestBindingDeploymentSelectionUsesTargetScopeAndRequiresManagedBinding(t *testing.T) {
	e := setup(t, api.PlanPro)
	enableAppTaskAPIForTest(&e)
	app, _ := seedAppTaskDeployment(t, e, "target-scope")
	candidate := seedBindingCandidate(t, e, app, "staging")
	readyObjectStorageVerificationBinding(t, e, app, "staging")
	task := createAppTaskForTest(t, e, app.Slug, api.CreateAppTaskRequest{Command: []string{api.AppTaskObjectStorageBindingProbeCommand, "ASSETS"}, VerificationDeploymentID: candidate.ID})
	if task.DeploymentScope != "staging" {
		t.Fatalf("wrong scope: %+v", task)
	}
	completeVerificationTask(t, e, beginVerificationTask(t, e, task.ID), passedObjectStorageVerification)
	i := decodeBindingInventory(t, e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/bindings?deployment_id="+candidate.ID+"&scope=staging", nil, nil))
	if i.VerificationScope != "staging" || len(i.Bindings) != 1 || i.Bindings[0].VerificationStatus != "passed" {
		t.Fatalf("target scope evidence: %+v", i)
	}
	r := e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/tasks", api.CreateAppTaskRequest{Command: []string{api.AppTaskObjectStorageBindingProbeCommand, "UNMANAGED"}, VerificationDeploymentID: candidate.ID}, nil)
	if r.Code != 403 {
		t.Fatalf("unmanaged selected task: %d %s", r.Code, r.Body.String())
	}
}

type supersedingBindingAdmissionStore struct{ *state.MemStore }

func (s *supersedingBindingAdmissionStore) CreateAppTask(ctx context.Context, p state.CreateAppTaskParams) (state.AppTask, error) {
	if err := s.UpdateDeploymentStatus(ctx, p.DeploymentID, state.DeploySuperseded, ""); err != nil {
		return state.AppTask{}, err
	}
	return s.MemStore.CreateAppTask(ctx, p)
}

func TestBindingDeploymentSelectionRechecksLiveStatusAtAdmission(t *testing.T) {
	e := setup(t, api.PlanPro)
	enableAppTaskAPIForTest(&e)
	app, dep := seedAppTaskDeployment(t, e, "target-race")
	readyVerificationBinding(t, e, app)
	e.s.store = &supersedingBindingAdmissionStore{MemStore: e.store}
	r := e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/tasks", api.CreateAppTaskRequest{Command: []string{api.AppTaskPostgresBindingProbeCommand, "DATABASE_URL"}, VerificationDeploymentID: dep.ID}, nil)
	if r.Code != 409 {
		t.Fatalf("superseded during admission: %d %s", r.Code, r.Body.String())
	}
}

func TestBindingDeploymentSelectionRequiresResourceReadPermission(t *testing.T) {
	e := setupWithScopes(t, []string{api.ScopeAppsRead, api.ScopeDeployWrite})
	enableAppTaskAPIForTest(&e)
	app, dep := seedAppTaskDeployment(t, e, "target-permission")
	readyVerificationBinding(t, e, app)
	readyObjectStorageVerificationBinding(t, e, app, "default")
	for _, command := range []string{api.AppTaskPostgresBindingProbeCommand, api.AppTaskObjectStorageBindingProbeCommand} {
		selection := "DATABASE_URL"
		if command == api.AppTaskObjectStorageBindingProbeCommand {
			selection = "ASSETS"
		}
		r := e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/tasks", api.CreateAppTaskRequest{Command: []string{command, selection}, VerificationDeploymentID: dep.ID}, nil)
		if r.Code != 403 {
			t.Fatalf("resource read permission bypassed: %d %s", r.Code, r.Body.String())
		}
	}
}

func TestBindingDeploymentSelectionRejectsExclusiveOperationSelector(t *testing.T) {
	e := setup(t, api.PlanPro)
	enableAppTaskAPIForTest(&e)
	app, dep := seedAppTaskDeployment(t, e, "target-exclusive")
	r := e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/operations/tasks", api.ExclusiveAppTaskOperationRequest{Policy: "maintenance", Key: json.RawMessage(`"key"`), Task: api.CreateAppTaskRequest{Command: []string{api.AppTaskPostgresBindingProbeCommand, "DATABASE_URL"}, VerificationDeploymentID: dep.ID}}, nil)
	if r.Code != 422 || !strings.Contains(r.Body.String(), "direct binding verification") {
		t.Fatalf("exclusive operation ignored selector: %d %s", r.Code, r.Body.String())
	}
}
