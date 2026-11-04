package apidsource

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261021221906 — a build enqueued with BuildNotBefore is not claimable until
// then, both of two deferred syncs stay listed for apid to cancel (a
// building deployment is not superseded by the newer one), and starting one
// early re-notifies builderd.
func TestEnqueue_DeferredBuildCoalesces(t *testing.T) {
	ctx := context.Background()
	st := state.NewMemStore()
	app := mustSeedApp(t, st)
	notif := &recordingNotifier{}
	enqueue := func(notBefore time.Time) EnqueueResult {
		t.Helper()
		srcPath, srcBytes := stageSource(t, t.TempDir())
		res, err := Enqueue(ctx, st, notif, EnqueueParams{
			AppID: app.ID, Kind: state.DeploymentKindTarball,
			SourcePath: srcPath, SourceBytes: srcBytes,
			LogSpool: t.TempDir(), Log: quietLogger(), BuildNotBefore: notBefore,
		})
		if err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		return res
	}
	first := enqueue(time.Now().Add(time.Hour))
	if _, err := st.ClaimQueuedBuild(ctx, first.BuildID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deferred build was claimable: %v", err)
	}
	second := enqueue(time.Now().Add(time.Hour))
	if ids, err := st.DeferredDeployments(ctx, app.ID); err != nil || len(ids) != 2 {
		t.Fatalf("DeferredDeployments = %v, %v; want both syncs", ids, err)
	}
	build, found, err := st.StartDeferredBuild(ctx, second.DeploymentID)
	if err != nil || !found {
		t.Fatalf("StartDeferredBuild = %v %v", found, err)
	}
	before := notif.callCount()
	NotifyBuildQueued(ctx, notif, quietLogger(), build, app.ID, "")
	if notif.callCount() != before+1 {
		t.Fatal("starting a deferred build did not notify builderd")
	}
	if claimed, err := st.ClaimQueuedBuild(ctx, second.BuildID); err != nil || claimed.Status != state.BuildRunning {
		t.Fatalf("started build claim = %+v, %v", claimed, err)
	}
}
