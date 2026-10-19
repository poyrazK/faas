package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type managedInventoryAPIStore struct {
	MemStore     *state.MemStore
	dep          state.Deployment
	route        state.EnvironmentGitOpsServiceBindingRoute
	queriedKinds []string
	legacy       []state.BindingVerificationTask
	targetDep    *state.Deployment
	admitted     []state.CreateAppTaskParams
}

func (s *managedInventoryAPIStore) DeploymentByID(ctx context.Context, id string) (state.Deployment, error) {
	if s.targetDep != nil && id == s.targetDep.ID {
		return *s.targetDep, nil
	}
	selected, err := uuid.Parse(id)
	own, _ := uuid.Parse(s.dep.ID)
	if err == nil && selected == own {
		return s.dep, nil
	}
	return s.MemStore.DeploymentByID(ctx, id)
}

func (s *managedInventoryAPIStore) CreateAppTask(ctx context.Context, params state.CreateAppTaskParams) (state.AppTask, error) {
	if s.targetDep == nil {
		return s.MemStore.CreateAppTask(ctx, params)
	}
	s.admitted = append(s.admitted, params)
	return state.AppTask{ID: uuid.NewString(), AccountID: params.AccountID, AppID: params.AppID, DeploymentID: params.DeploymentID, DeploymentScope: s.dep.Scope, Kind: params.Kind, Command: params.Command, BindingVerification: params.BindingVerification, Status: state.AppTaskQueued, CreatedAt: params.CreatedAt}, nil
}
func (s *managedInventoryAPIStore) LiveDeployment(ctx context.Context, app string) (state.Deployment, error) {
	if app == s.dep.AppID {
		return s.dep, nil
	}
	return s.MemStore.LiveDeployment(ctx, app)
}
func (s *managedInventoryAPIStore) ResolveEnvironmentGitOpsServiceBinding(context.Context, string, string, string) (state.EnvironmentGitOpsServiceBindingRoute, bool, bool, error) {
	return s.route, true, true, nil
}
func (s *managedInventoryAPIStore) ListBindingVerificationTasks(_ context.Context, _, _ string, kinds []string, _ ...state.BindingVerificationSelection) ([]state.BindingVerificationTask, error) {
	s.queriedKinds = slices.Clone(kinds)
	return s.legacy, nil // Even an unexpected ordinary receipt must be ignored.
}

func TestManagedServiceInventoryAPIUsesSelectedFrozenGraph(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.EnableAppTaskAPIForTest(&e)
	(*e.S()).ConfigureCheckedReleaseCatalog(t, (*e.Acct()).ID)
	caller, dep := apidserver.SeedAppTaskDeployment(t, e, "inventory-managed")
	storedCaller, err := (*e.Store()).AppByID(t.Context(), caller.ID)
	if err != nil {
		t.Fatal(err)
	}
	caller = storedCaller
	dep = apidserver.SeedBindingCandidate(t, e, caller, "production")
	target, _ := apidserver.SeedAppTaskDeployment(t, e, "git-billing")
	frozen := state.EnvironmentWorkloadRuntime{
		AppID: caller.ID, AppType: state.AppTypeApp, RuntimeBase: "node22", Scope: "production", Resource: "workload/frontend", WorkloadClass: state.WorkloadClassHTTP,
		SourceID: uuid.NewString(), EnvironmentID: uuid.NewString(), RevisionID: uuid.NewString(), Generation: 1, IntentVersion: 1, PlanHash: strings.Repeat("a", 64), DefinitionDigest: strings.Repeat("b", 64), Runtime: map[string]json.RawMessage{},
		Source:          &api.EnvironmentWorkloadSource{Kind: "image", Image: "ghcr.io/example/worker@sha256:" + strings.Repeat("c", 64)},
		ServiceBindings: map[string]state.EnvironmentScopedServiceBinding{"billing": {Workload: "billing", EnvKey: "BILLING_URL", TargetAppID: target.ID}},
	}
	raw, err := json.Marshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	held := false
	dep.EnvironmentWorkloadRuntime = string(raw)
	dep.EnvironmentWorkloadHeldValue = &held
	dep.ImageDigest = frozen.Source.Image
	if _, err := dep.ScopedWorkloadRuntime(); err != nil {
		t.Fatalf("invalid managed deployment fixture: %v", err)
	}
	fake := &managedInventoryAPIStore{MemStore: *e.Store(), dep: dep, route: state.EnvironmentGitOpsServiceBindingRoute{AccountID: caller.AccountID, CallerAppID: caller.ID, CallerDeploymentID: dep.ID, TargetAppID: target.ID, TargetDeploymentID: uuid.NewString(), ReleaseSetID: uuid.NewString(), RequireHTTPS: true}}
	zero := 0
	now := time.Now().UTC()
	fake.legacy = []state.BindingVerificationTask{{DeploymentID: dep.ID, Scope: "production", Pin: state.BindingVerificationPin{Type: api.BindingTypeService, Binding: "billing", Revision: "old-app-context"}, Status: string(state.AppTaskSucceeded), ExitCode: &zero, FinishedAt: &now, Stdout: `{"service":"billing","dns":{"status":"passed"},"tls":{"status":"passed"},"authorization":{"status":"passed"},"routing":{"status":"passed"}}`}}
	(*(*e.S()).Store()) = statetest.NewMem(fake)
	base := "/v1/apps/" + caller.Slug + "/bindings"
	for _, suffix := range []string{"", "?deployment_id=" + dep.ID} {
		r := e.Do(t, http.MethodGet, base+suffix, nil, nil)
		if r.Code != 200 {
			t.Fatalf("inventory %d %s", r.Code, r.Body)
		}
		var i api.AppBindingInventory
		if err := json.Unmarshal(r.Body.Bytes(), &i); err != nil {
			t.Fatal(err)
		}
		if i.ServiceBindingSource != "environment_release" || i.VerificationDeploymentID != dep.ID {
			t.Fatalf("inventory=%+v", i)
		}
		var service *api.AppBindingInventoryItem
		for index := range i.Bindings {
			if i.Bindings[index].Type == api.BindingTypeService {
				service = &i.Bindings[index]
				break
			}
		}
		if service == nil || service.Name != "billing" || service.Binding != "BILLING_URL" || service.ServiceContext.TargetApp != "git-billing" || service.ServiceContext.ReleaseID != fake.route.ReleaseSetID || service.ServiceTargetDeploymentID != fake.route.TargetDeploymentID || service.Transport != "https" || service.VerificationStatus != "unsupported" || service.Verification != nil {
			t.Fatalf("service=%+v inventory=%+v", service, i)
		}
		if slices.Contains(fake.queriedKinds, api.BindingTypeService) {
			t.Fatal("managed inventory selected ordinary service receipts")
		}
	}
	// Neither selectors nor ordinary task admission may replace graph authority.
	targets, _ := json.Marshal(map[string]string{"billing": fake.route.TargetDeploymentID})
	r := e.Do(t, http.MethodGet, base+"?deployment_id="+dep.ID+"&service_targets="+url.QueryEscape(string(targets)), nil, nil)
	if r.Code != 400 {
		t.Fatalf("managed override=%d %s", r.Code, r.Body)
	}
	for _, command := range [][]string{{api.AppTaskServiceBindingProbeCommand, "billing"}, {api.AppTaskServiceBindingTargetProbeCommand, "billing", fake.route.TargetDeploymentID}} {
		r = e.Do(t, http.MethodPost, "/v1/apps/"+caller.Slug+"/tasks", api.CreateAppTaskRequest{VerificationDeploymentID: dep.ID, Command: command}, nil)
		if r.Code != 409 {
			t.Fatalf("managed probe=%d %s", r.Code, r.Body)
		}
	}
	tasks, err := (*e.Store()).ListAppTasks(t.Context(), caller.AccountID, caller.ID, 10, 0)
	if err != nil || len(tasks) != 0 {
		t.Fatalf("read or rejected probe created tasks: %+v %v", tasks, err)
	}
	// Malformed frozen inputs do not resurrect current app-level bindings.
	fake.dep.EnvironmentWorkloadRuntime = "{invalid"
	r = e.Do(t, http.MethodGet, base+"?deployment_id="+dep.ID, nil, nil)
	var invalid api.AppBindingInventory
	if err := json.Unmarshal(r.Body.Bytes(), &invalid); err != nil {
		t.Fatal(err)
	}
	if r.Code != 200 || invalid.Complete || invalid.ServiceBindingSource != "environment_release" {
		t.Fatalf("invalid context=%d %+v", r.Code, invalid)
	}
	for _, item := range invalid.Bindings {
		if item.Type == api.BindingTypeService {
			t.Fatal("invalid graph fell back to app settings")
		}
	}
}

func TestManagedServiceProbeAPIAdmissionAndFreshEvidence(t *testing.T) {
	for _, worker := range []bool{false, true} {
		name := "http"
		if worker {
			name = "worker"
		}
		t.Run(name, func(t *testing.T) { managedServiceProbeAPIAdmissionAndFreshEvidence(t, worker) })
	}
}

func managedServiceProbeAPIAdmissionAndFreshEvidence(t *testing.T, worker bool) {
	t.Helper()
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.EnableAppTaskAPIForTest(&e)
	(*e.S()).ConfigureCheckedReleaseCatalog(t, (*e.Acct()).ID)
	seed := func(slug string) (state.App, state.Deployment) {
		app, err := (*e.Store()).CreateApp(t.Context(), state.App{ID: uuid.NewString(), AccountID: (*e.Acct()).ID, Slug: slug, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
		if err != nil {
			t.Fatal(err)
		}
		d, err := (*e.Store()).CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, Scope: "production", ImageDigest: "sha256:" + strings.Repeat("c", 64)})
		if err != nil {
			t.Fatal(err)
		}
		if err := (*e.Store()).SetDeploymentRootfs(t.Context(), d.ID, "/test/image", "test/"+d.ID, 4096); err != nil {
			t.Fatal(err)
		}
		d, err = (*e.Store()).DeploymentByID(t.Context(), d.ID)
		if err != nil {
			t.Fatal(err)
		}
		return app, d
	}
	caller, dep := seed("probe-managed")
	target, targetDep := seed("probe-physical")
	frozen := state.EnvironmentWorkloadRuntime{AppID: caller.ID, AppType: state.AppTypeApp, RuntimeBase: "node22", Scope: "production", Resource: "workload/frontend", WorkloadClass: state.WorkloadClassHTTP,
		SourceID: uuid.NewString(), EnvironmentID: uuid.NewString(), RevisionID: uuid.NewString(), Generation: 1, IntentVersion: 1, PlanHash: strings.Repeat("a", 64), DefinitionDigest: strings.Repeat("b", 64), Runtime: map[string]json.RawMessage{},
		Source:   &api.EnvironmentWorkloadSource{Kind: "image", Image: "ghcr.io/example/managed@sha256:" + strings.Repeat("c", 64)},
		Baseline: state.AppManifest{ServiceBindingTransport: api.ServiceBindingTransportHTTPS}, ServiceBindings: map[string]state.EnvironmentScopedServiceBinding{"billing": {Workload: "billing", EnvKey: "CUSTOM_BILLING_URL", TargetAppID: target.ID}}}
	freeze := func(d *state.Deployment, value state.EnvironmentWorkloadRuntime) {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		held := false
		d.EnvironmentWorkloadRuntime, d.EnvironmentWorkloadHeldValue, d.ImageDigest = string(raw), &held, value.Source.Image
	}
	if worker {
		frozen.WorkloadClass = state.WorkloadClassWorker
		frozen.Runtime["execution_mode"] = json.RawMessage(`"worker"`)
	}
	freeze(&dep, frozen)
	frozen.WorkloadClass = state.WorkloadClassHTTP
	frozen.Runtime = map[string]json.RawMessage{}
	frozen.AppID, frozen.Resource, frozen.ServiceBindings = target.ID, "workload/billing", nil
	freeze(&targetDep, frozen)
	fake := &managedInventoryAPIStore{MemStore: *e.Store(), dep: dep, targetDep: &targetDep, route: state.EnvironmentGitOpsServiceBindingRoute{AccountID: caller.AccountID, CallerAppID: caller.ID, CallerDeploymentID: dep.ID, TargetAppID: target.ID, TargetDeploymentID: targetDep.ID, ReleaseSetID: uuid.NewString(), RequireHTTPS: true}}
	(*(*e.S()).Store()) = statetest.NewMem(fake)
	path := "/v1/apps/" + caller.Slug
	read := func() api.AppBindingInventoryItem {
		r := e.Do(t, http.MethodGet, path+"/bindings?deployment_id="+dep.ID, nil, nil)
		var inventory api.AppBindingInventory
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &inventory) != nil || !inventory.Complete {
			t.Fatalf("inventory=%d %s", r.Code, r.Body)
		}
		for _, item := range inventory.Bindings {
			if item.Type == api.BindingTypeService {
				return item
			}
		}
		t.Fatal("missing managed edge")
		return api.AppBindingInventoryItem{}
	}
	item := read()
	spec, supported := api.ManagedServiceProbeForItem(item)
	if !supported || item.VerificationStatus != "unknown" {
		t.Fatalf("unsupported fixture: %+v", item)
	}
	r := e.Do(t, http.MethodPost, path+"/tasks", api.CreateAppTaskRequest{VerificationDeploymentID: dep.ID, Command: []string{api.AppTaskServiceBindingProbeCommand, "billing"}}, nil)
	if r.Code != 202 || len(fake.admitted) != 1 {
		t.Fatalf("admission=%d %s", r.Code, r.Body)
	}
	params := fake.admitted[0]
	if !params.RequireLiveDeployment || params.BindingVerification.ManagedService == nil || *params.BindingVerification.ManagedService != spec || len(params.Command) != 3 || params.Command[2] != targetDep.ID {
		t.Fatalf("pin=%+v", params)
	}
	check := api.ServiceBindingProbeCheck{Status: "passed", Detail: "PRIVATE_DETAIL"}
	report, _ := json.Marshal(api.ServiceBindingProbeReport{ManagedService: &spec, Service: spec.Service, TargetDeploymentID: spec.TargetDeploymentID, HTTPStatus: 204, URL: "https://billing.internal", DNS: check, TLS: check, Authorization: check, Routing: check})
	zero, now := 0, time.Now().UTC()
	fake.legacy = []state.BindingVerificationTask{{TaskID: uuid.NewString(), Pin: *params.BindingVerification, DeploymentID: dep.ID, Scope: dep.Scope, Status: string(state.AppTaskSucceeded), Stdout: string(report), ExitCode: &zero, FinishedAt: &now}}
	if item = read(); item.VerificationStatus != "passed" || item.Verification.ManagedService == nil {
		t.Fatalf("managed receipt=%+v", item)
	}
	fake.route.ReleaseSetID = uuid.NewString()
	if item = read(); item.VerificationStatus != "stale" {
		t.Fatalf("release change retained pass: %+v", item)
	}
	r = e.Do(t, http.MethodPost, path+"/tasks", api.CreateAppTaskRequest{VerificationDeploymentID: dep.ID, Command: []string{api.AppTaskServiceBindingTargetProbeCommand, "billing", uuid.NewString()}}, nil)
	if r.Code != 409 || len(fake.admitted) != 1 {
		t.Fatalf("override admitted: %d %s", r.Code, r.Body)
	}
}
