package imaged

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestTransitionFailureClosesActiveStage guards the post-build failure seam:
// a rendered imaging error must leave the deployment terminal and move the
// in-flight stage into history instead of leaving the dashboard ticker stuck.
func TestTransitionFailureClosesActiveStage(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	h := newHandler(store)
	acct, err := store.CreateAccount(ctx, "stage-failure@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "stage-failure", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := store.AppendDeploymentStage(ctx, dep.ID, state.StageSourceDownload, state.StageImageBuild, now, ""); err != nil {
		t.Fatal(err)
	}

	if err := h.transitionFailure(ctx, dep.ID, "post-build filesystem full"); err != nil {
		t.Fatalf("transitionFailure: %v", err)
	}
	got, err := store.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.DeployFailed {
		t.Fatalf("status=%q, want %q", got.Status, state.DeployFailed)
	}
	var stages state.StageState
	if err := json.Unmarshal(got.StageState, &stages); err != nil {
		t.Fatal(err)
	}
	if stages.Current != "" {
		t.Fatalf("current stage=%q, want empty after failure", stages.Current)
	}
	if len(stages.History) == 0 {
		t.Fatal("stage history empty after failure")
	}
	last := stages.History[len(stages.History)-1]
	if last.Name != state.StageImageBuild || last.Status != "failed" {
		t.Fatalf("last stage=%+v, want failed image_build", last)
	}
	if last.Reason != "post-build filesystem full" {
		t.Fatalf("failure reason=%q", last.Reason)
	}
}

// TestSnapshotWrittenRedeliveryDoesNotReplayStages pins the successful side of
// the same state machine. A duplicate snapshot notification may be delivered
// after activation, but it must leave the already-closed timeline untouched.
func TestSnapshotWrittenRedeliveryDoesNotReplayStages(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "stage-redelivery@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "stage-redelivery", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(ctx, dep.ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendDeploymentStage(ctx, dep.ID, state.StageSourceDownload, state.StageSnapshotPrepare, time.Now().Add(-time.Second), ""); err != nil {
		t.Fatal(err)
	}
	ops := wire.NewOpsMetrics("imaged_stage_replay_test")
	h := New(store, &fakeNotifier{}, fakePuller{}, &fakeBuilder{}, "./init", t.TempDir(), silentLogger()).WithOpsMetrics(ops)
	n := db.Notification{Channel: db.NotifySnapshotWritten, Payload: `{"deployment_id":"` + dep.ID + `","storage_key":"snap/` + dep.ID + `/mem","mem_bytes":268435456,"vmstate_bytes":40960,"fc_version":"firecracker-1.10"}`}
	h.HandleNotification(ctx, n)
	h.HandleNotification(ctx, n)

	got, err := store.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	var stages state.StageState
	if err := json.Unmarshal(got.StageState, &stages); err != nil {
		t.Fatal(err)
	}
	if stages.Current != "" {
		t.Fatalf("current stage=%q, want empty", stages.Current)
	}
	if len(stages.History) != 3 {
		t.Fatalf("history len=%d, want 3 after one activation and one redelivery", len(stages.History))
	}
	for i, want := range []state.StageName{state.StageSourceDownload, state.StageSnapshotPrepare, state.StageReadiness} {
		if stages.History[i].Name != want {
			t.Fatalf("history[%d]=%q, want %q", i, stages.History[i].Name, want)
		}
	}
	recorder := httptest.NewRecorder()
	ops.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	for _, stage := range []state.StageName{state.StageSnapshotPrepare, state.StageReadiness} {
		want := `imaged_stage_replay_test_deploy_stage_duration_seconds_count{stage="` + string(stage) + `",status="completed"} 1`
		if !strings.Contains(recorder.Body.String(), want) {
			t.Errorf("metric %q missing after activation and redelivery", want)
		}
	}
}
