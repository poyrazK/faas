// adr: 568 — dependency calls stay in the original reviewed private graph.
package pgintegration_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func privateQualificationServiceFixture(t *testing.T, basic gitOpsTestStore) (state.EnvironmentGitSource, []state.EnvironmentWorkloadQualificationRequest, []state.Instance) {
	t.Helper()
	store, source, desired, _, _, _ := workloadIntentFixture(t, basic, "enforce")
	allowed := []string{"api"}
	scopes := api.ServiceCallerScopes{"api": {Methods: []string{"GET"}, PathPrefixes: []string{"/health"}}}
	backend, err := store.CreateApp(t.Context(), state.App{AccountID: source.AccountID, ProjectID: source.ProjectID, Slug: "shop-backend", WorkloadName: "backend", Type: state.AppTypeApp,
		Status: state.AppActive, RAMMB: 512, CPUMillicores: 250, MaxConcurrency: 1, Manifest: state.AppManifest{Port: 8079, ExecutionMode: api.ExecutionModeService, AllowedServiceCallers: &allowed, AllowedServiceCallScopes: &scopes}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: backend.ID, Scope: "production", Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "registry.example/backend@sha256:" + strings.Repeat("c", 64)}); err != nil {
		t.Fatal(err)
	}
	caller := desired.Definition.Workloads["api"]
	caller.ServiceBindings = map[string]api.EnvironmentServiceBinding{"backend": {Workload: "backend", EnvKey: "BACKEND_URL"}}
	caller.Runtime = json.RawMessage(`{"port":8080,"healthz":"/health"}`)
	desired.Definition.Workloads["api"] = caller
	desired.Definition.Workloads["backend"] = api.EnvironmentWorkload{App: backend.Slug, Source: &api.EnvironmentWorkloadSource{Kind: "image", Image: "registry.example/backend@sha256:" + strings.Repeat("d", 64)}, Runtime: json.RawMessage(`{"port":8082,"execution_mode":"service","healthz":"/readyz"}`)}
	desired, err = environmentsync.Compile(desired.Definition)
	if err != nil {
		t.Fatal(err)
	}
	source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("b", 40)))
	if err != nil {
		t.Fatal(err)
	}
	adoptWorkloadIntent(t, store, source)
	lease, err := store.ClaimEnvironmentGitOps(t.Context(), "preparer", time.Now(), 3*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyEnvironmentGitOps(t.Context(), lease, claimedIntentPlan(t, store, lease, desired)); err != nil {
		t.Fatal(err)
	}
	plan := claimedIntentPlan(t, store, lease, desired)
	candidates, err := basic.(state.EnvironmentGitOpsPreparationStore).PrepareEnvironmentGitOpsImageCandidates(t.Context(), lease, plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range candidates {
		if err := basic.SetDeploymentRootfs(t.Context(), candidate.DeploymentID, "/reviewed.ext4", "reviewed-"+candidate.Resource, 4096); err != nil {
			t.Fatal(err)
		}
		if err := basic.UpdateDeploymentStatus(t.Context(), candidate.DeploymentID, state.DeploySnapshotting, ""); err != nil {
			t.Fatal(err)
		}
	}
	if graph, err := basic.(state.EnvironmentGitOpsGraphPreparationStore).ReconcileEnvironmentGitOpsPreparation(t.Context(), lease, plan); err != nil || graph.Phase != "prepared" {
		t.Fatal("graph preparation", err)
	}
	requests, err := basic.(state.EnvironmentGitOpsQualificationStore).QueueEnvironmentGitOpsQualification(t.Context(), lease, plan)
	if err != nil || len(requests) != 2 {
		t.Fatal("cohort", err)
	}
	placement := qualificationPlacement(t, basic, 4096)
	requests, err = basic.(state.EnvironmentGitOpsQualificationGraphDispatchStore).ClaimEnvironmentWorkloadQualificationGraphForNode(
		t.Context(), requests[0].GraphID, placement.NodeID, "scheduler", time.Minute)
	if err != nil || len(requests) != 2 {
		t.Fatalf("atomic private service graph claim: %+v %v", requests, err)
	}
	instances := make([]state.Instance, 0, len(requests))
	for i, request := range requests {
		placement.WakeID = uuid.NewString()
		admission, err := basic.(state.EnvironmentGitOpsQualificationInstanceStore).CreateEnvironmentWorkloadQualificationInstance(t.Context(), request, placement)
		if err != nil {
			t.Fatal(err)
		}
		if err := basic.(state.EnvironmentQualificationExecutionStore).MarkEnvironmentQualificationDispatched(t.Context(), request, admission.Execution); err != nil {
			t.Fatal(err)
		}
		ins, err := basic.(state.EnvironmentGitOpsQualificationRuntimeStore).PublishEnvironmentWorkloadQualificationRuntime(t.Context(), request, state.EnvironmentWorkloadQualificationRuntime{
			NodeID: placement.NodeID, WakeID: placement.WakeID, Netns: "private-" + request.ReservedInstanceID, HostIP: fmt.Sprintf("10.100.0.%d", i+2), GuestUID: 20001 + i,
			Inputs: state.RuntimeConfigInputs{Scope: "production", Boundary: time.Now().UTC(), Variables: map[string]string{}, SecretVersions: map[string]int64{}, SecretRefs: map[string]string{}, AllSecrets: true}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := basic.(state.EnvironmentQualificationConfigReceiptStore).RecordEnvironmentQualificationConfigReceipt(
			t.Context(), request, admission.Execution, strings.Repeat("a", 64)); err != nil {
			t.Fatal("guest config acknowledgement", err)
		}
		instances = append(instances, ins)
	}
	return source, requests, instances
}

func TestEnvironmentQualificationPrivateServiceBindsBothOriginalAttempts(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		_, requests, instances := privateQualificationServiceFixture(t, basic)
		services := basic.(state.EnvironmentQualificationServiceStore)
		request := state.EnvironmentQualificationServiceRequest{NodeID: instances[0].NodeID, HostIP: instances[0].HostIP, GraphID: requests[0].GraphID, Binding: "backend"}
		held, err := services.EnvironmentQualificationNetworkCaller(t.Context(), request.NodeID, request.HostIP)
		if err != nil || !held {
			t.Fatal("original network identity", held, err)
		}
		route, err := services.ResolveEnvironmentQualificationService(t.Context(), request)
		if err != nil || route.Caller.InstanceID != instances[0].ID || route.Target.InstanceID != instances[1].ID || route.Port != 8082 || route.Caller.CleanupToken != "" || route.Target.CleanupToken != "" || route.Deadline.After(*requests[0].LeaseUntil) || route.Deadline.After(*requests[1].LeaseUntil) {
			t.Fatal("private graph resolution", route, err)
		}
		aliases := basic.(state.EnvironmentQualificationServiceAliasStore)
		if allowed, err := aliases.EnvironmentQualificationServiceAliasAllowed(t.Context(), requests[0].AppID, "backend"); err != nil || !allowed {
			t.Fatalf("current scoped alias: %v %v", allowed, err)
		}
		if allowed, err := aliases.EnvironmentQualificationServiceAliasAllowed(t.Context(), requests[0].AppID, "unbound"); err != nil || allowed {
			t.Fatalf("unbound alias: %v %v", allowed, err)
		}
		if route.CallScope == nil || !route.CallScope.Allows("GET", "/health") || route.CallScope.Allows("POST", "/health") || route.CallScope.Allows("GET", "/admin") {
			t.Fatal("private route lost frozen target authorization")
		}
		cohort, err := basic.(state.EnvironmentQualificationGraphStore).EnvironmentQualificationGraphRequests(t.Context(), requests[0])
		if err != nil || len(cohort) != 2 {
			t.Fatal("persisted graph cohort", err)
		}
		cohort[0].FrozenInputs.Runtime["port"] = json.RawMessage(`1`)
		again, err := basic.(state.EnvironmentQualificationGraphStore).EnvironmentQualificationGraphRequests(t.Context(), requests[0])
		if err != nil || string(again[0].FrozenInputs.Runtime["port"]) != "8080" {
			t.Fatal("cohort inputs were mutable", err)
		}
		for _, change := range []string{"graph", "binding", "node", "address", "ambiguous_address"} {
			forged := request
			switch change {
			case "graph":
				forged.GraphID = uuid.NewString()
			case "binding":
				forged.Binding = "unknown"
			case "node":
				forged.NodeID = uuid.NewString()
			case "address":
				forged.HostIP = instances[1].HostIP
			case "ambiguous_address":
				forged.HostIP = "010.100.0.2"
			}
			if _, err := services.ResolveEnvironmentQualificationService(t.Context(), forged); err == nil {
				t.Fatal("substituted selector routed", change)
			}
		}
	})
}

func TestEnvironmentQualificationPrivateServiceUsesRestoredRuntimeReceipts(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		_, requests, original := privateQualificationServiceFixture(t, basic)
		snapshots := basic.(state.EnvironmentQualificationSnapshotStore)
		executor := basic.(state.EnvironmentQualificationExecutionStore)
		retirements := make([]state.EnvironmentQualificationSnapshotReceipt, len(requests))
		for i, request := range requests {
			status, err := executor.EnvironmentQualificationExecution(t.Context(), original[i].ID)
			if err != nil {
				t.Fatal(err)
			}
			retirements[i], err = snapshots.RecordEnvironmentQualificationSnapshot(t.Context(), request, status.Execution, captureProof(status.Execution))
			if err != nil {
				t.Fatal(err)
			}
			proof := qualificationNativeProof()
			proof.ReceiptID, proof.NativeGeneration, proof.KernelBootID = retirements[i].Snapshot.CaptureID, retirements[i].Snapshot.NativeGeneration, retirements[i].Snapshot.KernelBootID
			if err := executor.RetireEnvironmentQualificationExecution(t.Context(), status.Execution, proof); err != nil {
				t.Fatal(err)
			}
		}
		admissions := basic.(state.EnvironmentQualificationRestoreStore)
		publisher := basic.(state.EnvironmentQualificationRestoreRuntimeStore)
		instances := make([]state.Instance, len(requests))
		for i, request := range requests {
			placement := qualificationPlacement(t, basic, 4096)
			admission, err := admissions.CreateEnvironmentQualificationRestore(t.Context(), request, placement)
			if err != nil {
				t.Fatal(err)
			}
			if err := admissions.MarkEnvironmentQualificationRestoreDispatched(t.Context(), request, admission.Execution); err != nil {
				t.Fatal(err)
			}
			runtime := state.EnvironmentWorkloadQualificationRuntime{NodeID: admission.Instance.NodeID, WakeID: admission.Instance.WakeID,
				Netns: "restored-" + admission.Instance.ID, HostIP: fmt.Sprintf("10.100.1.%d", i+2), GuestUID: 20201 + i, Inputs: retirements[i].Inputs}
			if i == 0 {
				instances[i], err = publisher.PublishEnvironmentQualificationRestoreRuntime(t.Context(), request, admission.Execution, runtime)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				instances[i] = admission.Instance
				if _, err := basic.(state.EnvironmentQualificationServiceStore).ResolveEnvironmentQualificationService(t.Context(), state.EnvironmentQualificationServiceRequest{
					NodeID: instances[0].NodeID, HostIP: instances[0].HostIP, GraphID: requests[0].GraphID, Binding: "backend",
				}); err == nil {
					t.Fatal("binding routed to a restored target before its runtime receipt")
				}
				instances[i], err = publisher.PublishEnvironmentQualificationRestoreRuntime(t.Context(), request, admission.Execution, runtime)
				if err != nil {
					t.Fatal(err)
				}
			}
			if i == 1 {
				if _, err := basic.(state.EnvironmentQualificationServiceStore).ResolveEnvironmentQualificationService(t.Context(), state.EnvironmentQualificationServiceRequest{
					NodeID: instances[0].NodeID, HostIP: instances[0].HostIP, GraphID: requests[0].GraphID, Binding: "backend",
				}); err == nil {
					t.Fatal("binding routed to restored target before guest config acknowledgement")
				}
			}
			if _, err := basic.(state.EnvironmentQualificationConfigReceiptStore).RecordEnvironmentQualificationConfigReceipt(
				t.Context(), request, admission.Execution, strings.Repeat("b", 64)); err != nil {
				t.Fatal("restored guest config acknowledgement", err)
			}
		}
		route, err := basic.(state.EnvironmentQualificationServiceStore).ResolveEnvironmentQualificationService(t.Context(), state.EnvironmentQualificationServiceRequest{
			NodeID: instances[0].NodeID, HostIP: instances[0].HostIP, GraphID: requests[0].GraphID, Binding: "backend",
		})
		if err != nil || route.Caller.InstanceID != instances[0].ID || route.Target.InstanceID != instances[1].ID || route.Port != 8082 {
			t.Fatalf("restored private service route: %+v %v", route, err)
		}
	})
}

func TestEnvironmentQualificationPrivateServiceRejectsRevokedEndpoints(t *testing.T) {
	for _, change := range []string{"source", "node", "account", "protocol", "caller_retired", "target_retired"} {
		t.Run(change, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				source, requests, instances := privateQualificationServiceFixture(t, basic)
				switch change {
				case "source":
					_, err := basic.(state.EnvironmentGitOpsControlStore).UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID, state.EnvironmentGitSourceUpdate{ExpectedGeneration: source.Generation, Mode: "report"})
					if err != nil {
						t.Fatal(err)
					}
				case "node":
					if err := basic.NodeSetLifecycle(t.Context(), instances[1].NodeID, state.NodeLifecycleActive, state.NodeLifecycleDraining); err != nil {
						t.Fatal(err)
					}
				case "account":
					if err := basic.MarkAccountDeletionPending(t.Context(), source.AccountID); err != nil {
						t.Fatal(err)
					}
				case "protocol":
					protocol := api.AppProtocolGRPC
					if _, err := basic.UpdateApp(t.Context(), requests[1].AppID, state.UpdateAppParams{SetAppProtocol: true, AppProtocol: &protocol}); err != nil {
						t.Fatal(err)
					}
				case "caller_retired", "target_retired":
					i := 0
					if change == "target_retired" {
						i = 1
					}
					executor := basic.(state.EnvironmentQualificationExecutionStore)
					execution, err := executor.EnvironmentQualificationExecution(t.Context(), instances[i].ID)
					if err != nil {
						t.Fatal(err)
					}
					if err := executor.RetireEnvironmentQualificationExecution(t.Context(), execution.Execution, qualificationNativeProof()); err != nil {
						t.Fatal(err)
					}
				}
				_, err := basic.(state.EnvironmentQualificationServiceStore).ResolveEnvironmentQualificationService(t.Context(), state.EnvironmentQualificationServiceRequest{NodeID: instances[0].NodeID, HostIP: instances[0].HostIP, GraphID: requests[0].GraphID, Binding: "backend"})
				if !errors.Is(err, state.ErrConflict) && !errors.Is(err, state.ErrNotFound) && !(change == "protocol" && errors.Is(err, state.ErrEnvironmentWorkloadPreparationUnavailable)) {
					t.Fatal("revoked graph endpoint routed", change, err)
				}
				if change == "caller_retired" {
					if allowed, err := basic.(state.EnvironmentQualificationServiceAliasStore).EnvironmentQualificationServiceAliasAllowed(t.Context(), requests[0].AppID, "backend"); err != nil || allowed {
						t.Fatalf("retired scoped alias remained available: %v %v", allowed, err)
					}
				}
			})
		})
	}
}
