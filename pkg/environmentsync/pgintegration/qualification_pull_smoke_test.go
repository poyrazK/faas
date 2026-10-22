// adr: 20261022155812 — a pull worker leases only its own reviewed smoke.
package pgintegration_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func pullQualificationFixture(t *testing.T, basic gitOpsTestStore) (state.EnvironmentWorkloadQualificationRequest, state.Instance, state.App) {
	t.Helper()
	store := basic.(intentTestStore)
	source, _ := seedMode(t, store, "enforce")
	app, err := store.CreateApp(t.Context(), state.App{AccountID: source.AccountID, ProjectID: source.ProjectID, Slug: "shop-worker", WorkloadName: "worker",
		Type: state.AppTypeApp, Status: state.AppActive, WorkloadClass: state.WorkloadClassWorker, RAMMB: 512, CPUMillicores: 250, MaxConcurrency: 1,
		Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeWorker, Port: 8080}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: source.EnvironmentSlug, Kind: state.DeploymentKindImage,
		Status: state.DeployLive, ImageDigest: "registry.example/worker@sha256:" + strings.Repeat("c", 64)}); err != nil {
		t.Fatal(err)
	}
	enabled := true
	desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: "shop", Environment: source.EnvironmentSlug,
		Workloads: map[string]api.EnvironmentWorkload{"worker": {App: app.Slug,
			Source:        &api.EnvironmentWorkloadSource{Kind: "image", Image: "registry.example/worker@sha256:" + strings.Repeat("d", 64)},
			Runtime:       json.RawMessage(`{"port":8080,"execution_mode":"worker"}`),
			QueueBindings: map[string]api.EnvironmentQueueBinding{"orders": {QueueName: "orders", Mode: "pull", WorkloadClass: "worker", Enabled: &enabled}},
			QueueSmoke:    map[string]api.EnvironmentQueueSmoke{"orders": {Payload: json.RawMessage(`{"id":"smoke"}`)}},
		}}})
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
	if err != nil || len(candidates) != 1 {
		t.Fatalf("prepare pull worker: %+v %v", candidates, err)
	}
	if err := basic.SetDeploymentRootfs(t.Context(), candidates[0].DeploymentID, "/reviewed.ext4", "reviewed-worker", 4096); err != nil {
		t.Fatal(err)
	}
	if err := basic.UpdateDeploymentStatus(t.Context(), candidates[0].DeploymentID, state.DeploySnapshotting, ""); err != nil {
		t.Fatal(err)
	}
	if graph, err := basic.(state.EnvironmentGitOpsGraphPreparationStore).ReconcileEnvironmentGitOpsPreparation(t.Context(), lease, plan); err != nil || graph.Phase != "prepared" {
		t.Fatal("graph preparation", err)
	}
	requests, err := basic.(state.EnvironmentGitOpsQualificationStore).QueueEnvironmentGitOpsQualification(t.Context(), lease, plan)
	if err != nil || len(requests) != 1 {
		t.Fatal("cohort", err)
	}
	placement := qualificationPlacement(t, basic, 4096)
	requests, err = basic.(state.EnvironmentGitOpsQualificationGraphDispatchStore).ClaimEnvironmentWorkloadQualificationGraphForNode(
		t.Context(), requests[0].GraphID, placement.NodeID, "scheduler", time.Minute)
	if err != nil || len(requests) != 1 {
		t.Fatalf("pull worker graph claim: %+v %v", requests, err)
	}
	request := requests[0]
	placement.WakeID = uuid.NewString()
	admission, err := basic.(state.EnvironmentGitOpsQualificationInstanceStore).CreateEnvironmentWorkloadQualificationInstance(t.Context(), request, placement)
	if err != nil {
		t.Fatal(err)
	}
	if err := basic.(state.EnvironmentQualificationExecutionStore).MarkEnvironmentQualificationDispatched(t.Context(), request, admission.Execution); err != nil {
		t.Fatal(err)
	}
	ins, err := basic.(state.EnvironmentGitOpsQualificationRuntimeStore).PublishEnvironmentWorkloadQualificationRuntime(t.Context(), request, state.EnvironmentWorkloadQualificationRuntime{
		NodeID: placement.NodeID, WakeID: placement.WakeID, Netns: "private-" + request.ReservedInstanceID, HostIP: "10.100.0.2", GuestUID: 20001,
		Inputs: state.RuntimeConfigInputs{Scope: source.EnvironmentSlug, Boundary: time.Now().UTC(), Variables: map[string]string{}, SecretVersions: map[string]int64{}, SecretRefs: map[string]string{}, AllSecrets: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := basic.(state.EnvironmentQualificationConfigReceiptStore).RecordEnvironmentQualificationConfigReceipt(
		t.Context(), request, admission.Execution, strings.Repeat("a", 64)); err != nil {
		t.Fatal("guest config acknowledgement", err)
	}
	return request, ins, app
}

func TestEnvironmentQualificationPullSmokeLeasesOnlyTheCandidatesReviewedMessage(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		request, ins, app := pullQualificationFixture(t, basic)
		pulls := basic.(state.EnvironmentQualificationPullSmokeStore)
		caller := state.EnvironmentQualificationPullCaller{NodeID: ins.NodeID, HostIP: ins.HostIP, App: app.Slug, Queue: "orders"}
		if leases, err := pulls.LeaseEnvironmentQualificationPullSmoke(t.Context(), caller, 30*time.Second); err != nil || len(leases) != 0 {
			t.Fatalf("unarmed lease = %+v %v", leases, err)
		}
		messageID := uuid.NewString()
		smoke := state.EnvironmentQualificationPullSmoke{RequestID: request.ID, Attempt: request.Attempt, InstanceID: ins.ID, Binding: "orders",
			MessageID: messageID, Deadline: time.Now().Add(time.Minute)}
		if err := pulls.ArmEnvironmentQualificationPullSmoke(t.Context(), smoke); err != nil {
			t.Fatal("arm", err)
		}
		if err := pulls.ArmEnvironmentQualificationPullSmoke(t.Context(), smoke); err != nil {
			t.Fatal("re-arming the same message must be idempotent", err)
		}
		other := smoke
		other.MessageID = uuid.NewString()
		if err := pulls.ArmEnvironmentQualificationPullSmoke(t.Context(), other); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("a second message for the same execution = %v, want conflict", err)
		}
		forged := smoke
		forged.InstanceID = uuid.NewString()
		if err := pulls.ArmEnvironmentQualificationPullSmoke(t.Context(), forged); err == nil {
			t.Fatal("armed a smoke for an execution the request does not own")
		}

		for _, bad := range []state.EnvironmentQualificationPullCaller{
			{NodeID: ins.NodeID, HostIP: ins.HostIP, App: app.Slug, Queue: "payments"},
			{NodeID: ins.NodeID, HostIP: ins.HostIP, App: "shop-api", Queue: "orders"},
		} {
			if _, err := pulls.LeaseEnvironmentQualificationPullSmoke(t.Context(), bad, 30*time.Second); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("lease %+v err = %v, want not found", bad, err)
			}
		}
		stranger := caller
		stranger.HostIP = "10.100.0.250"
		if _, err := pulls.LeaseEnvironmentQualificationPullSmoke(t.Context(), stranger, 30*time.Second); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("unknown address err = %v, want conflict", err)
		}

		leases, err := pulls.LeaseEnvironmentQualificationPullSmoke(t.Context(), caller, 30*time.Second)
		if err != nil || len(leases) != 1 || leases[0].MessageID != messageID || leases[0].Attempt != 1 {
			t.Fatalf("lease = %+v %v", leases, err)
		}
		// PostgreSQL returns jsonb in its own spacing; the value is what is reviewed.
		var payload map[string]string
		if err := json.Unmarshal(leases[0].Payload, &payload); err != nil || len(payload) != 1 || payload["id"] != "smoke" {
			t.Fatalf("leased payload = %s %v", leases[0].Payload, err)
		}
		receipt := leases[0].Receipt
		if again, err := pulls.LeaseEnvironmentQualificationPullSmoke(t.Context(), caller, 30*time.Second); err != nil || len(again) != 0 {
			t.Fatalf("held message leased twice: %+v %v", again, err)
		}
		if _, err := pulls.LeaseEnvironmentQualificationPullSmoke(t.Context(), caller, time.Second); err != nil {
			t.Fatal(err) // an empty lease never validates an unused visibility
		}
		out, err := pulls.SettleEnvironmentQualificationPullSmoke(t.Context(), caller, state.PullSmokeAck, []string{receipt, strings.Repeat("f", 64)}, 0)
		if err != nil || len(out.Succeeded) != 1 || len(out.Failed) != 1 || out.Failed[0].Reason != state.QueueLeaseReasonInvalid {
			t.Fatalf("ack = %+v %v", out, err)
		}
		armed, err := pulls.EnvironmentQualificationPullSmoke(t.Context(), request.ID, request.Attempt, ins.ID, "orders")
		if err != nil || armed.Outcome != state.PullSmokeAcked || armed.SettledAt == nil || armed.Deliveries != 1 || armed.QueueName != "orders" {
			t.Fatalf("settled smoke = %+v %v", armed, err)
		}
		if out, err := pulls.SettleEnvironmentQualificationPullSmoke(t.Context(), caller, state.PullSmokeNack, []string{receipt}, 0); err != nil ||
			len(out.Failed) != 1 || out.Failed[0].Reason != state.QueueLeaseReasonExpired {
			t.Fatalf("a settled smoke changed outcome: %+v %v", out, err)
		}
		if leases, err := pulls.LeaseEnvironmentQualificationPullSmoke(t.Context(), caller, 30*time.Second); err != nil || len(leases) != 0 {
			t.Fatalf("a settled smoke was redelivered: %+v %v", leases, err)
		}
	})
}
