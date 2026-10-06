package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func readyVerificationBinding(t *testing.T, e testEnv, app state.App) (*managedpostgres.MemoryStore, managedpostgres.Binding) {
	t.Helper()
	store, _, database := configureSourceRefManagedPostgres(t, sourceRefTestEnv{acctID: e.acct.ID, srv: e.s})
	binding, _, err := store.ReserveBinding(context.Background(), managedpostgres.Binding{
		ID: uuid.NewString(), AccountID: e.acct.ID, DatabaseID: database, AppID: app.ID,
		Scope: "default", EnvironmentKey: "DATABASE_URL", Access: managedpostgres.CredentialReadWrite,
		CredentialGeneration: 1, State: managedpostgres.BindingStateProvisioning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return store, finishVerificationBinding(t, store, binding)
}

func finishVerificationBinding(t *testing.T, store *managedpostgres.MemoryStore, binding managedpostgres.Binding) managedpostgres.Binding {
	t.Helper()
	token := uuid.NewString()
	now := time.Now().UTC()
	_, err := store.ClaimBinding(context.Background(), binding.AccountID, binding.ID, token, managedpostgres.BindingStateProvisioning, now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	binding, err = store.FinishBindingProvision(context.Background(), binding.ID, token, "private-provider", "private-secret", now)
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func beginVerificationTask(t *testing.T, e testEnv, taskID string) state.AppTask {
	t.Helper()
	task, err := e.store.ClaimNextAppTask(context.Background(), "verification-test", time.Now().UTC().Add(time.Millisecond), time.Minute)
	if err != nil || task.ID != taskID {
		t.Fatalf("claim=%+v err=%v", task, err)
	}
	task, err = e.store.MarkAppTaskRunning(context.Background(), task.ID, *task.LeaseToken, task.CreatedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func completeVerificationTask(t *testing.T, e testEnv, task state.AppTask, output string) {
	t.Helper()
	exit := 0
	_, err := e.store.CompleteAppTask(context.Background(), state.CompleteAppTaskParams{
		ID: task.ID, LeaseToken: *task.LeaseToken, Status: state.AppTaskSucceeded, ExitCode: &exit,
		StdoutTail: output, FinishedAt: task.CreatedAt.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
}

const passedPostgresVerification = `{"environment_key":"DATABASE_URL","environment":{"status":"passed","detail":"PRIVATE_DETAIL"},"configuration":{"status":"passed"},"connection":{"status":"passed"},"query":{"status":"passed"}}`

func selectedPostgresVerification(t *testing.T, e testEnv, app state.App) api.AppBindingInventoryItem {
	t.Helper()
	response := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/bindings", nil, nil)
	inventory := decodeBindingInventory(t, response)
	if strings.Contains(response.Body.String(), "PRIVATE_") || strings.Contains(response.Body.String(), "private-secret") {
		t.Fatalf("raw report leaked: %s", response.Body.String())
	}
	for _, item := range inventory.Bindings {
		if item.Type == api.BindingTypePostgres {
			return item
		}
	}
	t.Fatalf("no PostgreSQL metadata: %+v", inventory)
	return api.AppBindingInventoryItem{}
}

func TestBindingVerificationRotationAndLateCompletion(t *testing.T) {
	e := setup(t, api.PlanPro)
	enableAppTaskAPIForTest(&e)
	app, _ := seedAppTaskDeployment(t, e, "verified-binding")
	store, binding := readyVerificationBinding(t, e, app)
	request := api.CreateAppTaskRequest{Command: []string{api.AppTaskPostgresBindingProbeCommand, "DATABASE_URL"}, MaxOutputBytes: 4096}
	first := createAppTaskForTest(t, e, app.Slug, request)
	running := beginVerificationTask(t, e, first.ID)
	completeVerificationTask(t, e, running, passedPostgresVerification)
	item := selectedPostgresVerification(t, e, app)
	if item.VerificationStatus != "passed" || item.Verification == nil || item.Verification.CheckedAt == nil || len(item.Verification.Checks) != 4 {
		t.Fatalf("first result=%+v", item)
	}
	old := createAppTaskForTest(t, e, app.Slug, request)
	oldRunning := beginVerificationTask(t, e, old.ID)
	rotated, _, err := store.BeginBindingRotation(context.Background(), e.acct.ID, binding.ID, uuid.NewString(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	finishVerificationBinding(t, store, rotated)
	if item = selectedPostgresVerification(t, e, app); item.VerificationStatus != "stale" {
		t.Fatalf("rotation retained current evidence: %+v", item)
	}
	current := createAppTaskForTest(t, e, app.Slug, request)
	currentRunning := beginVerificationTask(t, e, current.ID)
	completeVerificationTask(t, e, currentRunning, passedPostgresVerification)
	completeVerificationTask(t, e, oldRunning, passedPostgresVerification)
	item = selectedPostgresVerification(t, e, app)
	if item.VerificationStatus != "passed" || item.Verification.CredentialGeneration == nil || *item.Verification.CredentialGeneration != 2 {
		t.Fatalf("late result replaced new generation: %+v", item)
	}
	if err := e.store.MarkAppRuntimeConfigChanged(context.Background(), app.ID); err != nil {
		t.Fatal(err)
	}
	if item = selectedPostgresVerification(t, e, app); item.VerificationStatus != "stale" {
		t.Fatalf("config change retained current evidence: %+v", item)
	}
}

func TestBindingVerificationDeploymentChangeAndUnmanagedTasks(t *testing.T) {
	e := setup(t, api.PlanPro)
	enableAppTaskAPIForTest(&e)
	app, deployment := seedAppTaskDeployment(t, e, "verified-deployment")
	readyVerificationBinding(t, e, app)
	task := createAppTaskForTest(t, e, app.Slug, api.CreateAppTaskRequest{Command: []string{api.AppTaskPostgresBindingProbeCommand, "DATABASE_URL"}})
	completeVerificationTask(t, e, beginVerificationTask(t, e, task.ID), passedPostgresVerification)
	if err := e.store.UpdateDeploymentStatus(context.Background(), deployment.ID, state.DeploySuperseded, ""); err != nil {
		t.Fatal(err)
	}
	next, err := e.store.CreateDeployment(context.Background(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, TrafficPercent: 100, ImageDigest: appTaskTestDigest, CreatedAt: time.Now().UTC().Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetDeploymentRootfs(context.Background(), next.ID, "/private/image", "private/image", 4096); err != nil {
		t.Fatal(err)
	}
	item := selectedPostgresVerification(t, e, app)
	if item.VerificationStatus != "stale" || item.Verification.Reason != "deployment_changed" {
		t.Fatalf("deployment evidence=%+v", item)
	}
	task = createAppTaskForTest(t, e, app.Slug, api.CreateAppTaskRequest{Command: []string{"echo", passedPostgresVerification}})
	stored, err := e.store.AppTaskByID(context.Background(), e.acct.ID, app.ID, task.ID)
	if err != nil || stored.BindingVerification != nil {
		t.Fatalf("unmanaged task acquired evidence: %+v %v", stored, err)
	}
}

func TestBindingVerificationMalformedAndTruncatedReportsNeverPass(t *testing.T) {
	for _, tc := range []struct {
		name, output, status, reason string
		truncated                    bool
	}{
		{"malformed", "not-json", "succeeded", "report_invalid", false},
		{"missing stages", `{"environment_key":"DATABASE_URL"}`, "succeeded", "report_invalid", false},
		{"wrong binding", strings.ReplaceAll(passedPostgresVerification, "DATABASE_URL", "OTHER_URL"), "succeeded", "report_invalid", false},
		{"contradictory error", strings.TrimSuffix(passedPostgresVerification, "}") + `,"error":"PRIVATE_ERROR"}`, "succeeded", "check_failed", false},
		{"truncated", passedPostgresVerification, "succeeded", "report_truncated", true},
		{"cancelled", passedPostgresVerification, "cancelled", "probe_cancelled", false},
		{"pending", passedPostgresVerification, "running", "probe_pending", false},
		{"timeout", "", "timed_out", "probe_timed_out", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exit := 0
			result := normalizeBindingVerification(state.BindingVerificationTask{Pin: state.BindingVerificationPin{Type: api.BindingTypePostgres, Binding: "DATABASE_URL"}, Stdout: tc.output, Status: tc.status, ExitCode: &exit, OutputTruncated: tc.truncated})
			if result.Result == "passed" || result.Reason != tc.reason {
				raw, _ := json.Marshal(result)
				t.Fatalf("result=%s", raw)
			}
		})
	}
}

func TestBindingVerificationServiceConfigurationChange(t *testing.T) {
	e := setup(t, api.PlanPro)
	enableAppTaskAPIForTest(&e)
	app, _ := seedAppTaskDeployment(t, e, "verified-service")
	manifest := state.AppManifest{ServiceBindingPolicy: api.ServiceBindingPolicyDeclared,
		ServiceBindingTransport: api.ServiceBindingTransportHTTPS,
		ServiceBindings:         []api.AppServiceBinding{{Service: "billing", Binding: "GREGALE_SERVICE_BILLING_URL"}}}
	app, err := e.store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Manifest: &manifest})
	if err != nil {
		t.Fatal(err)
	}
	task := createAppTaskForTest(t, e, app.Slug, api.CreateAppTaskRequest{Command: []string{api.AppTaskServiceBindingProbeCommand, "billing"}})
	completeVerificationTask(t, e, beginVerificationTask(t, e, task.ID), `{"service":"billing","dns":{"status":"passed","detail":"PRIVATE_DETAIL"},"tls":{"status":"passed"},"authorization":{"status":"passed"},"routing":{"status":"passed"}}`)
	selectService := func() api.AppBindingInventoryItem {
		response := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/bindings", nil, nil)
		inventory := decodeBindingInventory(t, response)
		if strings.Contains(response.Body.String(), "PRIVATE_DETAIL") {
			t.Fatal("service report detail leaked")
		}
		for _, item := range inventory.Bindings {
			if item.Type == api.BindingTypeService {
				return item
			}
		}
		t.Fatal("service missing")
		return api.AppBindingInventoryItem{}
	}
	if item := selectService(); item.VerificationStatus != "passed" || item.Verification.Source != "task_guest" {
		t.Fatalf("service evidence=%+v", item)
	}
	manifest.ServiceBindingTransport = api.ServiceBindingTransportHTTP
	if _, err := e.store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	if item := selectService(); item.VerificationStatus != "stale" || item.Verification.Reason != "configuration_changed" {
		t.Fatalf("transport retained current evidence=%+v", item)
	}
}

func TestBindingVerificationServiceTargetPolicyChange(t *testing.T) {
	e := setup(t, api.PlanPro)
	enableAppTaskAPIForTest(&e)
	app, _ := seedAppTaskDeployment(t, e, "verified-caller")
	target, _ := seedAppTaskDeployment(t, e, "verified-target")
	manifest := state.AppManifest{ServiceBindings: api.ServiceBindingsForTargets([]string{target.Slug})}
	if _, err := e.store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	task := createAppTaskForTest(t, e, app.Slug, api.CreateAppTaskRequest{Command: []string{api.AppTaskServiceBindingProbeCommand, target.Slug}})
	completeVerificationTask(t, e, beginVerificationTask(t, e, task.ID), `{"service":"verified-target","dns":{"status":"passed"},"tls":{"status":"passed"},"authorization":{"status":"passed"},"routing":{"status":"passed"}}`)
	read := func() api.AppBindingInventoryItem {
		inventory := decodeBindingInventory(t, e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/bindings", nil, nil))
		for _, item := range inventory.Bindings {
			if item.Type == api.BindingTypeService {
				return item
			}
		}
		t.Fatal("service missing")
		return api.AppBindingInventoryItem{}
	}
	if item := read(); item.VerificationStatus != "passed" {
		t.Fatalf("initial evidence=%+v", item)
	}
	denied := []string{}
	targetManifest := state.AppManifest{AllowedServiceCallers: &denied}
	if _, err := e.store.UpdateApp(context.Background(), target.ID, state.UpdateAppParams{Manifest: &targetManifest}); err != nil {
		t.Fatal(err)
	}
	if item := read(); item.VerificationStatus != "stale" || item.Verification.Reason != "configuration_changed" {
		t.Fatalf("target policy retained passed evidence=%+v", item)
	}
}

type verificationReadStore struct {
	*state.MemStore
	kinds []string
	err   error
}

func (s *verificationReadStore) ListBindingVerificationTasks(ctx context.Context, accountID, appID string, kinds []string, selections ...state.BindingVerificationSelection) ([]state.BindingVerificationTask, error) {
	s.kinds = append([]string(nil), kinds...)
	if s.err != nil {
		return nil, s.err
	}
	return s.MemStore.ListBindingVerificationTasks(ctx, accountID, appID, kinds, selections...)
}

func TestBindingVerificationReadPermissionAndFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "permission", true: "query failure"}[fail], func(t *testing.T) {
			e := setupWithScopes(t, []string{api.ScopeAppsRead, api.ScopeDeployWrite})
			app, _ := seedAppTaskDeployment(t, e, "verification-read")
			reads := &verificationReadStore{MemStore: e.store}
			if fail {
				reads.err = errors.New("PRIVATE_ERROR postgres://user:password@host/db")
			}
			e.s.store = reads
			response := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/bindings", nil, nil)
			inventory := decodeBindingInventory(t, response)
			if len(reads.kinds) != 2 || reads.kinds[0] != api.BindingTypeService || reads.kinds[1] != api.BindingTypeOutbound {
				t.Fatalf("read unauthorized evidence kinds=%v", reads.kinds)
			}
			found := false
			for _, issue := range inventory.Issues {
				found = found || issue.Type == "verification"
			}
			if found != fail || inventory.Complete || strings.Contains(response.Body.String(), "PRIVATE_ERROR") || strings.Contains(response.Body.String(), "password") {
				t.Fatalf("failure projection=%+v", inventory)
			}
		})
	}
}

type serviceDependencyFailureStore struct{ *state.MemStore }

func (s serviceDependencyFailureStore) ReadServiceBindingRevision(context.Context, string, string) (string, error) {
	return "", errors.New("PRIVATE_DEPENDENCY postgres://user:password@host/db")
}

func TestBindingVerificationServiceDependencyReadFailsClosed(t *testing.T) {
	e := setup(t, api.PlanPro)
	enableAppTaskAPIForTest(&e)
	app, _ := seedAppTaskDeployment(t, e, "dependency-failure")
	manifest := state.AppManifest{ServiceBindings: api.ServiceBindingsForTargets([]string{"billing"})}
	if _, err := e.store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	e.s.store = serviceDependencyFailureStore{e.store}
	response := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/bindings", nil, nil)
	inventory := decodeBindingInventory(t, response)
	found := false
	for _, issue := range inventory.Issues {
		found = found || issue.Type == api.BindingTypeService && issue.Code == "query_failed"
	}
	if inventory.Complete || !found || strings.Contains(response.Body.String(), "PRIVATE_DEPENDENCY") || strings.Contains(response.Body.String(), "password") {
		t.Fatalf("unsafe inventory: %s", response.Body.String())
	}
	response = e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/tasks", api.CreateAppTaskRequest{Command: []string{api.AppTaskServiceBindingProbeCommand, "billing"}}, nil)
	if response.Code < 400 || strings.Contains(response.Body.String(), "PRIVATE_DEPENDENCY") {
		t.Fatalf("trusted unavailable dependency: %d %s", response.Code, response.Body.String())
	}
}
