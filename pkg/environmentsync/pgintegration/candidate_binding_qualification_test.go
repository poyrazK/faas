package pgintegration_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type candidateBindingTestStore interface {
	gitOpsTestStore
	state.EnvironmentGitOpsQualificationStore
	state.EnvironmentGitOpsQualificationGraphDispatchStore
	state.EnvironmentGitOpsQualificationInstanceStore
	state.EnvironmentGitOpsQualificationRuntimeStore
	state.EnvironmentQualificationExecutionStore
	state.EnvironmentQualificationSnapshotStore
	state.EnvironmentQualificationRestoreStore
	state.EnvironmentQualificationRestoreRuntimeStore
	state.EnvironmentQualificationConfigReceiptStore
	state.EnvironmentQualificationFrameworkReadyReceiptStore
	state.EnvironmentQualificationRestoreReceiptStore
	state.EnvironmentQualificationSmokeReceiptStore
	state.EnvironmentQualificationServiceStore
	state.EnvironmentQualificationServiceReceiptStore
	state.EnvironmentGitOpsGraphActivationStore
	state.BindingReleasePolicyStore
}

// These are owner-API fixtures, not native build, guest TLS or KVM evidence.
func candidateBindingFixture(t *testing.T, basic gitOpsTestStore, kind, age string) (state.EnvironmentGitOpsLease, environmentsync.Plan, []state.EnvironmentWorkloadQualificationRequest, state.EnvironmentWorkloadQualificationPlacement) {
	t.Helper()
	lease, plan, _ := workloadGraphFixtureWithWorkerSources(t, basic, true, api.ServiceBindingTransportHTTPS, nil, kind)
	store := basic.(candidateBindingTestStore)
	preparer := basic.(state.EnvironmentGitOpsPreparationStore)
	artifacts := map[string]state.EnvironmentWorkloadSourceArtifact{}
	if kind != "" {
		sources, err := preparer.EnvironmentGitOpsSourceRequests(t.Context(), lease, plan)
		if err != nil {
			t.Fatal(err)
		}
		for _, request := range sources {
			artifacts[request.Resource] = sourceArtifact(t, request)
		}
	}
	candidates, err := preparer.PrepareEnvironmentGitOpsCandidates(t.Context(), lease, plan, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range candidates {
		if err := basic.SetDeploymentRootfs(t.Context(), candidate.DeploymentID, "/reviewed.ext4", "candidate/"+candidate.DeploymentID, 4096); err != nil {
			t.Fatal(err)
		}
		if err := basic.UpdateDeploymentStatus(t.Context(), candidate.DeploymentID, state.DeploySnapshotting, ""); err != nil {
			t.Fatal(err)
		}
		requirements := map[string]string{}
		if candidate.Resource == "workload/worker" {
			requirements["orders"] = api.QueueEvidenceHandlerCompletion
		}
		if _, err := store.SetBindingReleasePolicy(t.Context(), lease.Source.AccountID, candidate.AppID, "production", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: new(int64), MaxVerificationAge: age, QueueRequirements: requirements}); err != nil {
			t.Fatal(err)
		}
	}
	if graph, err := basic.(state.EnvironmentGitOpsGraphPreparationStore).ReconcileEnvironmentGitOpsPreparation(t.Context(), lease, plan); err != nil || graph.Phase != "prepared" {
		t.Fatal("prepare", err)
	}
	cohort, err := store.QueueEnvironmentGitOpsQualification(t.Context(), lease, plan)
	if err != nil {
		t.Fatal(err)
	}
	placement := qualificationPlacement(t, basic, 4096)
	claimed, err := store.ClaimEnvironmentWorkloadQualificationGraphForNode(t.Context(), cohort[0].GraphID, placement.NodeID, "candidate-bindings", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return lease, plan, claimed, placement
}

func completeCandidateBindingCohort(t *testing.T, store candidateBindingTestStore, claimed []state.EnvironmentWorkloadQualificationRequest, placement state.EnvironmentWorkloadQualificationPlacement, exchange bool) {
	completeCandidateBindingCohortWithSecrets(t, store, claimed, placement, exchange, nil)
}

// secrets maps an app ID to the scoped secret versions its candidate runtime
// was captured and restored with.
func completeCandidateBindingCohortWithSecrets(t *testing.T, store candidateBindingTestStore, claimed []state.EnvironmentWorkloadQualificationRequest, placement state.EnvironmentWorkloadQualificationPlacement, exchange bool, secrets map[string]map[string]int64) {
	type member struct {
		request      state.EnvironmentWorkloadQualificationRequest
		frame        state.EnvironmentQualificationExecution
		runtime      state.EnvironmentWorkloadQualificationRuntime
		instance     state.Instance
		kernelBootID string
	}
	members := make([]member, 0, len(claimed))
	for i, request := range claimed {
		placement.WakeID = uuid.NewString()
		admitted, err := store.CreateEnvironmentWorkloadQualificationInstance(t.Context(), request, placement)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkEnvironmentQualificationDispatched(t.Context(), request, admitted.Execution); err != nil {
			t.Fatal(err)
		}
		inputs := state.RuntimeConfigInputs{Scope: "production", Boundary: time.Now().UTC(), Variables: request.FrozenInputs.Variables, SecretVersions: map[string]int64{}, SecretRefs: map[string]string{}, AllSecrets: true}
		maps.Copy(inputs.SecretVersions, secrets[request.AppID])
		if inputs.Variables == nil {
			inputs.Variables = map[string]string{}
		}
		runtime := state.EnvironmentWorkloadQualificationRuntime{NodeID: placement.NodeID, WakeID: placement.WakeID, Netns: fmt.Sprintf("candidate-source-%d", i), HostIP: fmt.Sprintf("10.100.0.%d", i+2), GuestUID: 20001 + i, Inputs: inputs}
		if _, err := store.PublishEnvironmentWorkloadQualificationRuntime(t.Context(), request, runtime); err != nil {
			t.Fatal(err)
		}
		if _, err := store.RecordEnvironmentQualificationConfigReceipt(t.Context(), request, admitted.Execution, strings.Repeat("a", 64)); err != nil {
			t.Fatal(err)
		}
		proof := captureProof(admitted.Execution)
		if _, err := store.RecordEnvironmentQualificationSnapshot(t.Context(), request, admitted.Execution, proof); err != nil {
			t.Fatal(err)
		}
		retirement := qualificationNativeProof()
		retirement.ReceiptID, retirement.NativeGeneration, retirement.KernelBootID = proof.CaptureID, proof.NativeGeneration, proof.KernelBootID
		if err := store.RetireEnvironmentQualificationExecution(t.Context(), admitted.Execution, retirement); err != nil {
			t.Fatal(err)
		}
		placement.WakeID = uuid.NewString()
		restored, err := store.CreateEnvironmentQualificationRestore(t.Context(), request, placement)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkEnvironmentQualificationRestoreDispatched(t.Context(), request, restored.Execution); err != nil {
			t.Fatal(err)
		}
		runtime.WakeID = placement.WakeID
		runtime.Netns = fmt.Sprintf("candidate-restored-%d", i)
		runtime.HostIP = fmt.Sprintf("10.100.1.%d", i+2)
		runtime.Inputs.Boundary = time.Now().UTC()
		instance, err := store.PublishEnvironmentQualificationRestoreRuntime(t.Context(), request, restored.Execution, runtime)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.RecordEnvironmentQualificationConfigReceipt(t.Context(), request, restored.Execution, strings.Repeat("b", 64)); err != nil {
			t.Fatal(err)
		}
		runtimeLabel := request.FrozenInputs.RuntimeBase
		if runtimeLabel == "" {
			runtimeLabel = "node22"
		}
		if _, err := store.RecordEnvironmentQualificationFrameworkReadyReceipt(t.Context(), restored.Execution, runtimeLabel, 42); err != nil {
			t.Fatal(err)
		}
		members = append(members, member{request, restored.Execution, runtime, instance, proof.KernelBootID})
	}
	if exchange {
		for _, m := range members {
			for name := range m.request.FrozenInputs.ServiceBindings {
				selector := state.EnvironmentQualificationServiceRequest{NodeID: m.instance.NodeID, HostIP: m.instance.HostIP, GraphID: m.request.GraphID, Binding: name}
				route, err := store.ResolveEnvironmentQualificationService(t.Context(), selector)
				if err != nil {
					t.Fatal(err)
				}
				forged := route
				forged.Target.DeploymentID = uuid.NewString()
				if err := store.RecordEnvironmentQualificationServiceExchange(t.Context(), selector, forged); err == nil {
					t.Fatal("substituted target produced evidence")
				}
				forged = route
				forged.Deadline = route.Deadline.Add(time.Minute)
				if err := store.RecordEnvironmentQualificationServiceExchange(t.Context(), selector, forged); err == nil {
					t.Fatal("forged deadline extended candidate authority")
				}

				proxy := gateway.NewEnvironmentQualificationServiceProxy(gateway.EnvironmentQualificationServiceProxyConfig{Store: store, ResolveNode: func(_ context.Context, _ string) (string, error) { return selector.NodeID, nil }, Forward: func(target gateway.Target) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if target.DeploymentID != route.Target.DeploymentID {
							t.Fatal("wrong target")
						}
						w.WriteHeader(http.StatusNoContent)
					})
				}})
				request := httptest.NewRequest(http.MethodGet, api.EnvironmentQualificationServicePrefix+m.request.GraphID+"/"+name+"/readyz", nil)
				request.RemoteAddr = m.instance.HostIP + ":12345"
				request.TLS = &tls.ConnectionState{Version: tls.VersionTLS13}
				response := httptest.NewRecorder()
				proxy.ServeHTTP(response, request)
				if response.Code != http.StatusNoContent {
					t.Fatalf("private HTTPS exchange: %d %s", response.Code, response.Body.String())
				}
			}
		}
	}
	for _, m := range members {
		retirement := qualificationNativeProof()
		retirement.KernelBootID = m.kernelBootID
		if err := store.RetireEnvironmentQualificationExecution(t.Context(), m.frame, retirement); err != nil {
			t.Fatal(err)
		}
		if _, err := store.RecordEnvironmentQualificationRestoreReceipt(t.Context(), m.request, m.frame, m.runtime.Inputs); err != nil {
			t.Fatal(err)
		}
		policy, digest, err := state.EnvironmentQualificationSmokePolicyFor(m.request)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.RecordEnvironmentQualificationSmokeReceipt(t.Context(), m.request, state.EnvironmentQualificationSmokeEvidence{Resource: m.request.Resource, InstanceID: m.frame.InstanceID, PolicyID: policy.ID, PolicySHA256: digest, ResultSHA256: strings.Repeat("c", 64), Passed: true}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCandidateBindingGraphActivation(t *testing.T) {
	for _, kind := range []string{"", "dockerfile"} {
		t.Run("kind="+kind, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				lease, plan, requests, placement := candidateBindingFixture(t, basic, kind, "")
				store := basic.(candidateBindingTestStore)
				full := basic.(state.Store)
				var sentinel state.Invocation
				for _, r := range requests {
					if r.Resource == "workload/worker" {
						queues, err := full.ListQueueBindingsForApp(t.Context(), lease.Source.AccountID, r.AppID)
						if err != nil || len(queues) != 1 {
							t.Fatal("worker queue", err)
						}
						sentinel, err = full.EnqueueInvocation(t.Context(), state.Invocation{AccountID: lease.Source.AccountID, AppID: r.AppID, Source: state.InvocationQueue, QueueName: queues[0].QueueName, QueueBindingID: queues[0].ID, DeploymentScope: "production", Payload: json.RawMessage(`{"unrelated":"retained"}`), DueAt: time.Now().UTC().Add(time.Hour)})
						if err != nil {
							t.Fatal("sentinel", err)
						}
					}
				}
				completeCandidateBindingCohort(t, store, requests, placement, true)
				release, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan)
				if err != nil || !active || len(release.Members) != 2 {
					t.Fatalf("checked activation: %+v %v %v", release, active, err)
				}
				for _, r := range requests {
					d, err := basic.(state.Store).DeploymentByID(t.Context(), r.DeploymentID)
					if err != nil || d.EnvironmentWorkloadHeld() || d.Status != state.DeployLive {
						t.Fatal("candidate not activated", err)
					}
					if kind != "" && d.ImageDigest != "" {
						t.Fatal("source build assigned a fake OCI digest")
					}
					p, err := store.GetBindingReleasePolicy(t.Context(), lease.Source.AccountID, r.AppID, "production")
					if err != nil || p.Mode != "enforce" {
						t.Fatal("policy weakened", err)
					}
				}
				role, target := "compute-only", "tcp://127.0.0.1:8080"
				if _, err := full.CreateComputeNode(t.Context(), state.ComputeNode{Name: "candidate-edge", Active: true, TargetURL: "tcp://127.0.0.1:50051", Role: &role, GatewayTargetURL: &target, AdmissionCeilingMB: 4096, MemMB: 8192, VPCPUs: 4, VCPUBudget: 160, MaxConcurrency: 5}); err != nil {
					t.Fatal(err)
				}
				serving, done, err := basic.(state.EnvironmentGitOpsWorkloadServingStore).PrepareEnvironmentGitOpsWorkloadServing(t.Context(), lease, plan, []string{"candidate-edge"})
				if err != nil || done || serving.ReleaseSetID != release.ID || len(serving.Routes) != 1 {
					t.Fatalf("checked traffic cutover: %+v %v %v", serving, done, err)
				}
				routed, err := full.DeploymentByID(t.Context(), serving.Routes[0].DeploymentID)
				if err != nil || routed.TrafficPercent != 100 {
					t.Fatal("HTTP candidate did not receive checked traffic", err)
				}
				untouched, err := full.InvocationByID(t.Context(), sentinel.ID)
				if err != nil || untouched.State != sentinel.State || untouched.Attempts != 0 || string(untouched.Payload) != string(sentinel.Payload) {
					t.Fatal("qualification consumed unrelated queue work", err)
				}
				again, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan)
				if err != nil || !active || again.ID != release.ID {
					t.Fatal("activation replay", err)
				}
			})
		})
	}
}

func TestCandidateBindingGraphMissingEvidenceStaysHeld(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		lease, plan, requests, placement := candidateBindingFixture(t, basic, "", "")
		store := basic.(candidateBindingTestStore)
		completeCandidateBindingCohort(t, store, requests, placement, false)
		if _, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan); active || !state.IsBindingReleaseRequired(err) {
			t.Fatalf("missing HTTPS exchange accepted: %v %v", active, err)
		}
		for _, r := range requests {
			d, err := basic.(state.Store).DeploymentByID(t.Context(), r.DeploymentID)
			if err != nil || !d.EnvironmentWorkloadHeld() || d.Status != state.DeploySnapshotting {
				t.Fatal("failed activation unheld candidate", err)
			}
		}
	})
}

func TestCandidateBindingGraphPolicyChangeInvalidatesEvidence(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		lease, plan, requests, placement := candidateBindingFixture(t, basic, "", "")
		store := basic.(candidateBindingTestStore)
		completeCandidateBindingCohort(t, store, requests, placement, true)
		revision := int64(1)
		if _, err := store.SetBindingReleasePolicy(t.Context(), lease.Source.AccountID, requests[0].AppID, "production", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &revision, MaxVerificationAge: "30s"}); err != nil {
			t.Fatal(err)
		}
		if _, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan); active || !state.IsBindingReleaseRequired(err) {
			t.Fatalf("stale policy accepted: %v %v", active, err)
		}
	})
}

func TestCandidateBindingGraphExpiryStaysHeld(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		lease, plan, requests, placement := candidateBindingFixture(t, basic, "", "1s")
		store := basic.(candidateBindingTestStore)
		completeCandidateBindingCohort(t, store, requests, placement, true)
		// Use the actual receipt clock; an old timestamp is never manufactured.
		timer := time.NewTimer(1100 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		}
		if _, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan); active || !errors.Is(err, state.ErrBindingPromotionExpired) {
			t.Fatalf("expired candidate evidence accepted: %v %v", active, err)
		}
		for _, r := range requests {
			d, err := basic.(state.Store).DeploymentByID(t.Context(), r.DeploymentID)
			if err != nil || !d.EnvironmentWorkloadHeld() {
				t.Fatal("expiry unheld a candidate", err)
			}
		}
	})
}

func TestCandidateBindingGraphSourceReplacementRequiresFreshCohort(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		old, plan, requests, placement := candidateBindingFixture(t, basic, "dockerfile", "")
		store := basic.(candidateBindingTestStore)
		completeCandidateBindingCohort(t, store, requests, placement, true)
		first, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), old, plan)
		if err != nil || !active {
			t.Fatal("initial enforced graph", err)
		}
		var definition api.EnvironmentDefinition
		if err := json.Unmarshal(old.Revision.Definition, &definition); err != nil {
			t.Fatal(err)
		}
		w := definition.Workloads["worker"]
		w.Variables = map[string]string{"SOURCE_REVISION": "next-reviewed-build"}
		definition.Workloads["worker"] = w
		desired, err := environmentsync.Compile(definition)
		if err != nil {
			t.Fatal(err)
		}
		intent := basic.(intentTestStore)
		source, err := intent.EnvironmentGitSource(t.Context(), old.Source.AccountID, old.Source.ProjectID, old.Source.EnvironmentSlug)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := intent.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("c", 40))); err != nil {
			t.Fatal(err)
		}
		lease, err := intent.ClaimEnvironmentGitOps(t.Context(), "checked-source-rebuild", time.Now(), 3*time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		nextPlan := claimedIntentPlan(t, intent, lease, desired)
		if _, err := intent.ApplyEnvironmentGitOps(t.Context(), lease, nextPlan); err != nil {
			t.Fatal(err)
		}
		nextPlan = claimedIntentPlan(t, intent, lease, desired)
		preparer := basic.(state.EnvironmentGitOpsPreparationStore)
		sourceRequests, err := preparer.EnvironmentGitOpsSourceRequests(t.Context(), lease, nextPlan)
		if err != nil {
			t.Fatal(err)
		}
		artifacts := map[string]state.EnvironmentWorkloadSourceArtifact{}
		for _, r := range sourceRequests {
			artifacts[r.Resource] = sourceArtifact(t, r)
		}
		candidates, err := preparer.PrepareEnvironmentGitOpsCandidates(t.Context(), lease, nextPlan, artifacts)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range candidates {
			if err := basic.SetDeploymentRootfs(t.Context(), c.DeploymentID, "/next.ext4", "next/"+c.DeploymentID, 4096); err != nil {
				t.Fatal(err)
			}
			if err := basic.UpdateDeploymentStatus(t.Context(), c.DeploymentID, state.DeploySnapshotting, ""); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := basic.(state.EnvironmentGitOpsGraphPreparationStore).ReconcileEnvironmentGitOpsPreparation(t.Context(), lease, nextPlan); err != nil {
			t.Fatal(err)
		}
		queued, err := store.QueueEnvironmentGitOpsQualification(t.Context(), lease, nextPlan)
		if err != nil {
			t.Fatal(err)
		}
		next, err := store.ClaimEnvironmentWorkloadQualificationGraphForNode(t.Context(), queued[0].GraphID, placement.NodeID, "checked-source-rebuild", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if next[0].GraphID == requests[0].GraphID || next[0].Artifact.BuildID == requests[0].Artifact.BuildID {
			t.Fatal("rebuild reused old cohort/artifact")
		}
		if _, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, nextPlan); active || err != nil {
			t.Fatalf("old graph evidence activated new cohort: %v %v", active, err)
		}
		retained, err := basic.(state.ProjectReleaseSetReader).ActiveProjectReleaseSet(t.Context(), old.Source.AccountID, old.Source.ProjectID, "production")
		if err != nil || retained.ID != first.ID {
			t.Fatal("failed qualification changed the active graph", err)
		}
		completeCandidateBindingCohort(t, store, next, placement, true)
		second, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, nextPlan)
		if err != nil || !active || second.ID == first.ID {
			t.Fatalf("fresh enforced source replacement: %v %v", active, err)
		}
	})
}

func TestPgCandidateBindingProofAndAuditAreImmutable(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	lease, plan, requests, placement := candidateBindingFixture(t, store, "", "")
	completeCandidateBindingCohort(t, store, requests, placement, true)
	if _, err := pool.Exec(t.Context(), "UPDATE environment_workload_qualification_requests SET binding_context=NULL WHERE id=$1", requests[0].ID); err == nil {
		t.Fatal("qualification context was mutable")
	}
	if _, err := pool.Exec(t.Context(), "UPDATE environment_qualification_service_receipts SET recorded_at=clock_timestamp() WHERE request_id=$1", requests[1].ID); err == nil {
		t.Fatal("service exchange history was mutable")
	}
	release, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan)
	if err != nil || !active {
		t.Fatal("checked activation", err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE environment_binding_release_receipts SET valid_until=clock_timestamp()+interval '1 day' WHERE release_id=$1", release.ID); err == nil {
		t.Fatal("release audit expiry was mutable")
	}
	var graph string
	if err := pool.QueryRow(t.Context(), "SELECT graph_id::text FROM environment_binding_release_receipts WHERE release_id=$1", release.ID).Scan(&graph); err != nil || graph != requests[0].GraphID {
		t.Fatal("release lost qualification audit", err)
	}
	source, err := os.ReadFile("../../../migrations/20261017200000001_candidate_binding_qualification.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, down, ok := strings.Cut(string(source), "-- +goose Down")
	if !ok {
		t.Fatal("missing downgrade")
	}
	if _, err := pool.Exec(t.Context(), down); err == nil {
		t.Fatal("downgrade discarded qualification history")
	}
	var stillThere bool
	if err := pool.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM environment_binding_release_receipts WHERE release_id=$1)", release.ID).Scan(&stillThere); err != nil || !stillThere {
		t.Fatal("rejected downgrade changed the audit", err)
	}
}
