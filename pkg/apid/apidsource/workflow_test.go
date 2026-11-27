package apidsource

import (
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowEnqueueRecoversAcceptedWithoutRepublishing(t *testing.T) {
	ctx := t.Context()
	m := state.NewMemStore()
	app := mustSeedApp(t, m)
	path, n := stageSource(t, t.TempDir())
	be := newMemBackend()
	notif := &recordingNotifier{}
	p := EnqueueParams{AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: path, SourceBytes: n, LogSpool: t.TempDir(), Log: quietLogger(), OperationID: "migration/service/1"}
	p.ExpectedSourceSHA256 = strings.Repeat("0", 64)
	if _, err := enqueueWithSourceStorage(ctx, m, notif, p, be); err == nil {
		t.Fatal("wrong reviewed digest was accepted")
	}
	deploymentID, _ := OperationIDs(p.OperationID, app.ID)
	if _, err := m.DeploymentByID(ctx, deploymentID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("digest mismatch published a deployment: %v", err)
	}
	var err error
	p.ExpectedSourceSHA256, err = hashSourceFile(path)
	if err != nil {
		t.Fatal(err)
	}
	first, err := enqueueWithSourceStorage(ctx, m, notif, p, be)
	if err != nil {
		t.Fatal(err)
	}
	be.putErr = errors.New("must not publish again")
	p.SourcePath = "/missing-after-restart"
	second, err := enqueueWithSourceStorage(ctx, m, notif, p, be)
	if err != nil || second != first {
		t.Fatalf("replay duplicated or lost accepted work: %+v %v", second, err)
	}
	p.ExpectedSourceSHA256 = strings.Repeat("0", 64)
	if _, err := enqueueWithSourceStorage(ctx, m, notif, p, be); err == nil {
		t.Fatal("replay ignored the reviewed digest")
	}
	if _, err = m.ClaimNextQueuedBuild(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = m.ClaimNextQueuedBuild(ctx); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("duplicate build queued")
	}
}
func TestWorkflowEnqueueRecoversDeploymentWithoutOverwritingSource(t *testing.T) {
	ctx := t.Context()
	m := state.NewMemStore()
	app := mustSeedApp(t, m)
	be := newMemBackend()
	be.putErr = errors.New("immutable published source must be reused")
	dep, build := OperationIDs("migration/service/1", app.ID)
	if _, err := m.CreateDeployment(ctx, state.Deployment{ID: dep, AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployPending, SourceSHA256: "original-checksum"}); err != nil {
		t.Fatal(err)
	}
	p := EnqueueParams{AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: "/missing-after-restart", LogSpool: t.TempDir(), Log: quietLogger(), OperationID: "migration/service/1"}
	out, err := enqueueWithSourceStorage(ctx, m, &recordingNotifier{}, p, be)
	if err != nil || out.DeploymentID != dep || out.BuildID != build {
		t.Fatalf("partial acceptance recovery: %+v %v", out, err)
	}
	saved, err := m.DeploymentByID(ctx, dep)
	if err != nil || saved.SourceSHA256 != "original-checksum" {
		t.Fatal("immutable input changed")
	}
}
