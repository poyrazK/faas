package deploywatchdog_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/deploywatchdog"
	"github.com/onebox-faas/faas/pkg/state"
)

// A deployment whose build sat in the queue, or whose pipeline lost a
// notification, stayed in flight forever: the CLI's --timeout is
// client-side only. The watchdog fails it once its bound passes and leaves
// deployments still within their bounds alone.
func TestSweepFailsOnlyStuckDeployments(t *testing.T) {
	ctx := context.Background()
	s := state.NewMemStore()
	acct, err := s.CreateAccount(ctx, "watchdog@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "watchdog-app", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	queued, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateBuild(ctx, queued.ID, state.DeploymentKindTarball, 10, ""); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	clock := now
	w := &deploywatchdog.Watchdog{Store: s, Now: func() time.Time { return clock }}
	if n, err := w.Sweep(ctx); err != nil || n != 0 {
		t.Fatalf("sweep within bounds failed %d deployments (err=%v)", n, err)
	}
	clock = now.Add(time.Duration(api.DeploymentQueuedBuildTimeoutSeconds)*time.Second + time.Minute)
	if n, err := w.Sweep(ctx); err != nil || n != 1 {
		t.Fatalf("sweep past the queue bound failed %d deployments (err=%v), want 1", n, err)
	}
	got, err := s.DeploymentByID(ctx, queued.ID)
	if err != nil || got.Status != state.DeployFailed || got.ErrorCode != api.CodeDeploymentQueueTimeout || !strings.Contains(got.Error, "builder queue") {
		t.Fatalf("queued deployment = status %q code %q error %q (err=%v)", got.Status, got.ErrorCode, got.Error, err)
	}
	build, err := s.BuildByDeployment(ctx, queued.ID)
	if err != nil || build.Status != state.BuildCancelled {
		t.Fatalf("queued build = %+v (err=%v), want cancelled", build, err)
	}
	if n, err := w.Sweep(ctx); err != nil || n != 0 {
		t.Fatalf("second sweep failed %d deployments (err=%v), want none", n, err)
	}
}

func TestFailureMessagesNameTheBound(t *testing.T) {
	code, message := deploywatchdog.Failure(state.StuckDeployment{Reason: state.StuckDeploymentStalled, Status: state.DeployImaging})
	if code != api.CodeDeploymentStalled || !strings.Contains(message, "3 hours") || !strings.Contains(message, "imaging") {
		t.Fatalf("stall failure = %q %q", code, message)
	}
	code, message = deploywatchdog.Failure(state.StuckDeployment{Reason: state.StuckDeploymentQueued})
	if code != api.CodeDeploymentQueueTimeout || !strings.Contains(message, "6 hours") {
		t.Fatalf("queue failure = %q %q", code, message)
	}
}
