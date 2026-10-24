package state

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func managedProbeFixture(t *testing.T) (*MemStore, App, Deployment, App, Deployment) {
	t.Helper()
	store, caller, dep, target, targetDep, _ := environmentGitOpsServiceBindingFixture(t, true)
	frozen, err := dep.ScopedWorkloadRuntime()
	if err != nil {
		t.Fatal(err)
	}
	frozen.Baseline.ServiceBindingTransport = api.ServiceBindingTransportHTTPS
	raw, _ := json.Marshal(frozen)
	dep.EnvironmentWorkloadRuntime, dep.RootfsKey = string(raw), "caller-layer"
	targetDep.RootfsKey = "target-layer"
	store.deployments[dep.ID], store.deployments[targetDep.ID] = dep, targetDep
	return store, caller, dep, target, targetDep
}

func TestManagedServiceProbeContextAndReceiptIsolation(t *testing.T) {
	for _, worker := range []bool{false, true} {
		name := "http"
		if worker {
			name = "worker"
		}
		t.Run(name, func(t *testing.T) { managedServiceProbeContextAndReceiptIsolation(t, worker) })
	}
}

func managedServiceProbeContextAndReceiptIsolation(t *testing.T, worker bool) {
	t.Helper()
	store, app, dep, target, _ := managedProbeFixture(t)
	if worker {
		frozen, err := dep.ScopedWorkloadRuntime()
		if err != nil {
			t.Fatal(err)
		}
		frozen.WorkloadClass = WorkloadClassWorker
		frozen.Runtime["execution_mode"] = json.RawMessage(`"worker"`)
		raw, _ := json.Marshal(frozen)
		dep.EnvironmentWorkloadRuntime = string(raw)
		store.deployments[dep.ID] = dep
	}

	spec, err := ResolveManagedServiceProbe(t.Context(), store, app.ID, dep.ID, "database")
	if err != nil || !spec.Valid() || spec.Binding != "DATABASE_URL" || spec.TargetAppID != target.ID {
		t.Fatalf("resolve: %+v %v", spec, err)
	}
	params := CreateAppTaskParams{AccountID: app.AccountID, AppID: app.ID, DeploymentID: dep.ID, Kind: AppTaskKindManual, Command: []string{api.AppTaskServiceBindingTargetProbeCommand, spec.Service, spec.TargetDeploymentID}, RequireLiveDeployment: true, CreatedAt: time.Now().UTC(), TimeoutSeconds: 15, MaxOutputBytes: 4096,
		BindingVerification: &BindingVerificationPin{Type: api.BindingTypeService, Binding: spec.Service, TargetDeploymentID: spec.TargetDeploymentID, Revision: spec.ContextRevision, ManagedService: &spec}}
	task, err := store.CreateAppTask(t.Context(), params)
	if err != nil {
		t.Fatal(err)
	}
	if parsed, valid := api.ParseManagedServiceProbeCommand(AppTaskGuestCommand(task), false); !valid || parsed != spec {
		t.Fatal("managed context did not reach the guest command")
	}
	params.BindingVerification.ManagedService = nil
	params.CreatedAt = params.CreatedAt.Add(time.Millisecond)
	if _, err := store.CreateAppTask(t.Context(), params); err != nil {
		t.Fatal(err)
	}
	selection := BindingVerificationSelection{DeploymentID: dep.ID, ServiceTargets: map[string]string{spec.Service: spec.TargetDeploymentID}, ManagedService: true}
	rows, err := store.ListBindingVerificationTasks(t.Context(), app.AccountID, app.ID, []string{api.BindingTypeService}, selection)
	if err != nil || len(rows) != 1 || rows[0].TaskID != task.ID || rows[0].Pin.ManagedService == nil {
		t.Fatalf("ordinary receipt masked managed evidence: %+v %v", rows, err)
	}
	target.Manifest.ServiceBindingPolicy = api.ServiceBindingPolicyDeclared
	store.apps[target.ID] = target
	after, err := ResolveManagedServiceProbe(t.Context(), store, app.ID, dep.ID, spec.Service)
	if err != nil || after.ContextRevision == spec.ContextRevision || ManagedServiceProbeCurrent(t.Context(), store, task) == nil {
		t.Fatalf("policy change retained managed evidence: %+v %v", after, err)
	}
	params.BindingVerification.ManagedService = &spec
	if _, err := store.CreateAppTask(t.Context(), params); err == nil {
		t.Fatal("stale context admitted")
	}
}

func TestManagedServiceProbeRejectsUnsupportedContexts(t *testing.T) {
	for _, scenario := range []string{"held", "worker-class", "worker-mode", "job", "target-worker", "target-job", "target-held", "release-changed", "missing-binding"} {
		t.Run(scenario, func(t *testing.T) {
			store, app, dep, _, targetDep := managedProbeFixture(t)
			frozen, _ := dep.ScopedWorkloadRuntime()
			switch scenario {
			case "held":
				dep.EnvironmentWorkloadHeldValue = environmentWorkloadHeldFlag(true)
			case "worker-class":
				frozen.WorkloadClass = WorkloadClassWorker
			case "worker-mode":
				frozen.Baseline.ExecutionMode = api.ExecutionModeWorker
			case "job":
				frozen.WorkloadClass, frozen.Baseline.ExecutionMode = WorkloadClassJob, api.ExecutionModeJob
			case "target-worker", "target-job":
				target, _ := targetDep.ScopedWorkloadRuntime()
				target.WorkloadClass, target.Baseline.ExecutionMode = WorkloadClassWorker, api.ExecutionModeWorker
				if scenario == "target-job" {
					target.WorkloadClass, target.Baseline.ExecutionMode = WorkloadClassJob, api.ExecutionModeJob
				}
				raw, _ := json.Marshal(target)
				targetDep.EnvironmentWorkloadRuntime = string(raw)
				store.deployments[targetDep.ID] = targetDep
			case "release-changed":
				delete(store.activeProjectReleaseSets, releaseKey(app.ProjectID, dep.Scope))
			case "target-held":
				targetDep.EnvironmentWorkloadHeldValue = environmentWorkloadHeldFlag(true)
				store.deployments[targetDep.ID] = targetDep
			case "missing-binding":
				frozen.ServiceBindings = nil
			}
			raw, _ := json.Marshal(frozen)
			dep.EnvironmentWorkloadRuntime = string(raw)
			store.deployments[dep.ID] = dep
			if _, err := ResolveManagedServiceProbe(t.Context(), store, app.ID, dep.ID, "database"); err == nil {
				t.Fatal("unsupported managed context admitted")
			}
		})
	}
}

// ADR-20261019200059: an HTTP caller's edge resolves to the HTTP contract,
// and the HTTPS spec keeps its original context revision.
func TestManagedServiceProbeResolvesHTTPCallers(t *testing.T) {
	store, app, dep, _, _ := managedProbeFixture(t)
	secure, err := ResolveManagedServiceProbe(t.Context(), store, app.ID, dep.ID, "database")
	if err != nil || secure.Transport != "" || secure.Contract() != api.ManagedServiceProbeContract {
		t.Fatalf("HTTPS spec: %+v %v", secure, err)
	}
	frozen, _ := dep.ScopedWorkloadRuntime()
	frozen.Baseline.ServiceBindingTransport = api.ServiceBindingTransportHTTP
	raw, _ := json.Marshal(frozen)
	dep.EnvironmentWorkloadRuntime = string(raw)
	store.deployments[dep.ID] = dep
	plain, err := ResolveManagedServiceProbe(t.Context(), store, app.ID, dep.ID, "database")
	if err != nil || !plain.Valid() || plain.Transport != api.ServiceBindingTransportHTTP || plain.Contract() != api.ManagedServiceHTTPProbeContract ||
		plain.ContextRevision == secure.ContextRevision {
		t.Fatalf("HTTP spec: %+v %v", plain, err)
	}
}
