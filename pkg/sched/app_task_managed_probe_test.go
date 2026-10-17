package sched

// adr:1543 — managed probes require the original live caller and reviewed release.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type managedProbeDispatchStore struct {
	MemStore       *state.MemStore
	apps           map[string]state.App
	deps           map[string]state.Deployment
	tasks          map[string]state.AppTask
	runtimeReadErr error
	runtimeReads   int
	route          state.EnvironmentGitOpsServiceBindingRoute
}

func (s *managedProbeDispatchStore) AppByID(_ context.Context, id string) (state.App, error) {
	a, ok := s.apps[id]
	if !ok {
		return a, state.ErrNotFound
	}
	return a, nil
}
func (s *managedProbeDispatchStore) DeploymentByID(_ context.Context, id string) (state.Deployment, error) {
	d, ok := s.deps[id]
	if !ok {
		return d, state.ErrNotFound
	}
	return d, nil
}
func (s *managedProbeDispatchStore) ResolveEnvironmentGitOpsServiceBinding(context.Context, string, string, string) (state.EnvironmentGitOpsServiceBindingRoute, bool, bool, error) {
	return s.route, true, true, nil
}
func (s *managedProbeDispatchStore) ReadServiceBindingRevision(context.Context, string, string) (string, error) {
	return "private-policy-revision", nil
}

func TestManagedServiceProbeDispatchFencesReleaseChange(t *testing.T) {
	for _, worker := range []bool{false, true} {
		name := "http"
		if worker {
			name = "worker"
		}
		t.Run(name, func(t *testing.T) { managedServiceProbeDispatchFencesReleaseChange(t, worker) })
	}
}

func managedServiceProbeDispatchFencesReleaseChange(t *testing.T, worker bool) {
	t.Helper()
	account, project, caller, target := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	depID, targetID := uuid.NewString(), uuid.NewString()
	f := state.EnvironmentWorkloadRuntime{AppID: caller, AppType: state.AppTypeApp, RuntimeBase: "node22", Scope: "production", Resource: "workload/frontend", WorkloadClass: state.WorkloadClassHTTP, SourceID: uuid.NewString(), EnvironmentID: uuid.NewString(), RevisionID: uuid.NewString(), Generation: 1, IntentVersion: 1, PlanHash: strings.Repeat("a", 64), DefinitionDigest: strings.Repeat("b", 64), Runtime: map[string]json.RawMessage{}, Source: &api.EnvironmentWorkloadSource{Kind: "image", Image: "registry.example/app@sha256:" + strings.Repeat("c", 64)}, Baseline: state.AppManifest{ServiceBindingTransport: api.ServiceBindingTransportHTTPS}, ServiceBindings: map[string]state.EnvironmentScopedServiceBinding{"billing": {Workload: "billing", EnvKey: "CUSTOM_BILLING_URL", TargetAppID: target}}}
	freeze := func(id, app string, f state.EnvironmentWorkloadRuntime) state.Deployment {
		raw, _ := json.Marshal(f)
		held := false
		return state.Deployment{ID: id, AppID: app, Kind: state.DeploymentKindImage, Scope: "production", Status: state.DeployLive, RootfsKey: "layer", ImageDigest: f.Source.Image, EnvironmentWorkloadRuntime: string(raw), EnvironmentWorkloadHeldValue: &held}
	}
	if worker {
		f.WorkloadClass = state.WorkloadClassWorker
		f.Runtime["execution_mode"] = json.RawMessage(`"worker"`)
	}
	dep := freeze(depID, caller, f)
	f.WorkloadClass = state.WorkloadClassHTTP
	f.Runtime = map[string]json.RawMessage{}
	f.AppID, f.Resource, f.ServiceBindings = target, "workload/billing", nil
	fake := &managedProbeDispatchStore{MemStore: state.NewMemStore(), apps: map[string]state.App{caller: {ID: caller, AccountID: account, ProjectID: project, Status: state.AppActive}, target: {ID: target, AccountID: account, ProjectID: project, Status: state.AppActive}}, deps: map[string]state.Deployment{depID: dep, targetID: freeze(targetID, target, f)}, route: state.EnvironmentGitOpsServiceBindingRoute{CallerAppID: caller, CallerDeploymentID: depID, TargetAppID: target, TargetDeploymentID: targetID, AccountID: account, ReleaseSetID: uuid.NewString(), RequireHTTPS: true}}
	store := statetest.NewMem(fake)
	spec, err := state.ResolveManagedServiceProbe(t.Context(), store, caller, depID, "billing")
	if err != nil {
		t.Fatal(err)
	}
	task := state.AppTask{AppID: caller, DeploymentID: depID, BindingVerification: &state.BindingVerificationPin{ManagedService: &spec}}
	coordinator := &AppTaskCoordinator{store: store}
	calls := 0
	session := appTaskSessionFuncs{execute: func(_ context.Context, req AppTaskExecuteRequest) (AppTaskOutcome, error) {
		calls++
		got, ok := api.ParseManagedServiceProbeCommand(req.Command, false)
		if !ok || got != spec {
			t.Fatalf("guest command=%+v", req)
		}
		return AppTaskOutcome{}, nil
	}}
	if _, err := coordinator.executeTaskCommand(t.Context(), session, task, nil); err != nil || calls != 1 {
		t.Fatalf("current dispatch=%d %v", calls, err)
	}
	fake.route.ReleaseSetID = uuid.NewString()
	if _, err := coordinator.executeTaskCommand(t.Context(), session, task, nil); err == nil || calls != 1 {
		t.Fatal("changed release reached guest dispatch")
	}
}

func (s *managedProbeDispatchStore) AppTaskByID(ctx context.Context, account, app, id string) (state.AppTask, error) {
	if task, exists := s.tasks[id]; exists && task.AccountID == account && task.AppID == app {
		return task, nil
	}
	return s.MemStore.AppTaskByID(ctx, account, app, id)
}

func TestSourceManagedProbeRuntimeAdmissionAndDrift(t *testing.T) {
	memory, account, app, dep, tasks := newAppTaskCoordinatorFixture(t, 1, 30, 4096)
	// MemStore's legacy deployment fixture uses compact UUIDs; managed probe
	// receipts require the canonical wire representation used by PostgreSQL.
	deploymentID, err := uuid.Parse(dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	dep.ID = deploymentID.String()
	dep.Scope = "production"
	app.ProjectID = uuid.NewString()
	target := app
	target.ID, target.Slug = uuid.NewString(), "billing"
	frozen := state.EnvironmentWorkloadRuntime{AppID: app.ID, AppType: state.AppTypeApp, RuntimeBase: "node22", Scope: dep.Scope, Resource: "workload/frontend", WorkloadClass: state.WorkloadClassHTTP, SourceID: uuid.NewString(), EnvironmentID: uuid.NewString(), RevisionID: uuid.NewString(), Generation: 1, IntentVersion: 1, PlanHash: strings.Repeat("a", 64), DefinitionDigest: strings.Repeat("b", 64), Runtime: map[string]json.RawMessage{}, Source: &api.EnvironmentWorkloadSource{Kind: "dockerfile", Directory: "app", Dockerfile: "Dockerfile"}, Baseline: state.AppManifest{ServiceBindingTransport: api.ServiceBindingTransportHTTPS}, ServiceBindings: map[string]state.EnvironmentScopedServiceBinding{"billing": {Workload: "billing", EnvKey: "CUSTOM_BILLING_URL", TargetAppID: target.ID}}}
	build, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	archive := state.EnvironmentWorkloadSourceArtifact{RevisionID: frozen.RevisionID, CommitSHA: strings.Repeat("c", 40), DefinitionDigest: frozen.DefinitionDigest, BuildID: build.String(), Path: "/reviewed/source.tar.gz", SHA256: strings.Repeat("d", 64), Bytes: 256, LogPath: "/reviewed/build.log"}
	frozen.SourceArchive = &archive
	raw, err := json.Marshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	held := false
	dep.Kind, dep.ImageDigest, dep.RootfsBytes = state.DeploymentKindGitHub, "", 4096
	dep.BuildID, dep.SourcePath, dep.SourceSHA256, dep.SourceBytes, dep.SourceRoot, dep.LogPath, dep.CommitSHA = archive.BuildID, archive.Path, archive.SHA256, archive.Bytes, "app", archive.LogPath, archive.CommitSHA
	dep.EnvironmentWorkloadRuntime, dep.EnvironmentWorkloadHeldValue = string(raw), &held
	targetFrozen := frozen
	targetFrozen.AppID, targetFrozen.Resource, targetFrozen.ServiceBindings = target.ID, "workload/billing", nil
	targetFrozen.SourceArchive = nil
	targetFrozen.Source = &api.EnvironmentWorkloadSource{Kind: "image", Image: "registry.invalid/target@sha256:" + strings.Repeat("e", 64)}
	targetRaw, err := json.Marshal(targetFrozen)
	if err != nil {
		t.Fatal(err)
	}
	targetDep := state.Deployment{ID: uuid.NewString(), AppID: target.ID, Scope: dep.Scope, Kind: state.DeploymentKindImage, Status: state.DeployLive, RootfsKey: "target-layer", ImageDigest: targetFrozen.Source.Image, EnvironmentWorkloadRuntime: string(targetRaw), EnvironmentWorkloadHeldValue: &held}
	for _, candidate := range []state.Deployment{dep, targetDep} {
		if _, err := candidate.ScopedWorkloadRuntime(); err != nil {
			t.Fatalf("invalid reviewed scheduler fixture: %v", err)
		}
	}
	fake := &managedProbeDispatchStore{MemStore: memory, apps: map[string]state.App{app.ID: app, target.ID: target}, deps: map[string]state.Deployment{dep.ID: dep, targetDep.ID: targetDep}, tasks: map[string]state.AppTask{}, route: state.EnvironmentGitOpsServiceBindingRoute{CallerAppID: app.ID, CallerDeploymentID: dep.ID, TargetAppID: target.ID, TargetDeploymentID: targetDep.ID, AccountID: account.ID, ReleaseSetID: uuid.NewString(), RequireHTTPS: true}}
	fake.runtimeReadErr = errors.New("owned source runtime fixture unavailable")
	store := statetest.NewMem(fake)
	spec, err := state.ResolveManagedServiceProbe(t.Context(), store, app.ID, dep.ID, "billing")
	if err != nil {
		t.Fatal(err)
	}
	task := tasks[0]
	task.DeploymentID = dep.ID
	task.DeploymentScope = dep.Scope
	task.ImageDigest = ""
	task.BindingVerification = &state.BindingVerificationPin{Type: api.BindingTypeService, Binding: spec.Service, Revision: spec.ContextRevision, TargetDeploymentID: targetDep.ID, ManagedService: &spec}
	fake.tasks[task.ID] = task
	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	request := AppTaskRestoreRequest{ID: task.ID, AccountID: account.ID, AppID: app.ID, DeploymentID: dep.ID, Kind: task.Kind, DeploymentScope: dep.Scope, ArtifactKey: dep.RootfsKey, ImageDigest: ""}
	// Reach the actual runtime-values owner boundary with a current source pin.
	// Full guest restoration is covered by the native source-build contract;
	// this fault verifies that admission does not confuse a source with a
	// missing OCI digest or bypass a failed owned runtime-values read.
	if _, err := engine.ResolveAppTaskRuntime(t.Context(), request); !errors.Is(err, fake.runtimeReadErr) || fake.runtimeReads != 1 || engine.ledger.Concurrency(app.ID) != 0 {
		t.Fatalf("source admission lost the owned-values fence: reads=%d error=%v", fake.runtimeReads, err)
	}
	task.BindingVerification = nil
	fake.tasks[task.ID] = task
	if _, err := engine.ResolveAppTaskRuntime(t.Context(), request); !errors.Is(err, state.ErrAppTaskDeploymentUnavailable) || fake.runtimeReads != 1 {
		t.Fatal("ordinary empty-digest task reached runtime values")
	}
	task.BindingVerification = &state.BindingVerificationPin{ManagedService: &spec}
	fake.tasks[task.ID] = task
	dep.RootfsBytes++
	fake.deps[dep.ID] = dep
	if _, err := engine.ResolveAppTaskRuntime(t.Context(), request); !errors.Is(err, state.ErrAppTaskDeploymentUnavailable) || fake.runtimeReads != 1 {
		t.Fatal("rebuilt source reached runtime values with stale context")
	}
}

func (s *managedProbeDispatchStore) RuntimeAppValuesForDeployment(ctx context.Context, account, app, deployment string) (state.RuntimeAppValuesSnapshot, error) {
	if s.runtimeReadErr != nil {
		s.runtimeReads++
		return state.RuntimeAppValuesSnapshot{}, s.runtimeReadErr
	}
	return s.MemStore.RuntimeAppValuesForDeployment(ctx, account, app, deployment)
}
