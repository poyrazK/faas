package sched_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// Deploy-path review P12: Prime held the app lock for its whole cold boot, so
// a request to the app's parked serving release waited for the new release's
// boot to finish.
func TestWakeOfTheServingReleaseDoesNotWaitForAPrimeBoot(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	acct, err := mem.CreateAccount(ctx, "primelock@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := mem.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "primelock", RAMMB: 128, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mem.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:serving", Status: state.DeployLive}); err != nil {
		t.Fatal(err)
	}
	next, err := mem.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:next", Status: state.DeploySnapshotting})
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.SetDeploymentRootfs(ctx, next.ID, "/test/"+next.ID, "test/"+next.ID, 4096); err != nil {
		t.Fatal(err)
	}
	if _, err := mem.AppendDeploymentStage(ctx, next.ID, state.StageSourceDownload, state.StageSnapshotPrepare, time.Now().UTC(), ""); err != nil {
		t.Fatal(err)
	}

	vmm := &sched.FakeVMM{}
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	vmm.SetColdBootHook(func() {
		first := false
		once.Do(func() { first = true })
		if first { // the prime's boot
			close(started)
			<-release
		}
	})
	e := sched.XNewEngine(t, mem, vmm, &sched.FakeNotifier{}, "1.10.0")

	primed := make(chan error, 1)
	go func() { primed <- e.Prime(ctx, app.ID, next.ID) }()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("prime never reached its boot")
	}

	woke := make(chan error, 1)
	go func() {
		_, err := e.Wake(ctx, app.ID, "", "", sched.TriggerGateway)
		woke <- err
	}()
	select {
	case err := <-woke:
		if err != nil {
			t.Fatalf("Wake: %v", err)
		}
	case <-time.After(5 * time.Second):
		close(release)
		<-primed
		t.Fatal("a wake of the serving release waited for the prime's boot")
	}
	close(release)
	if err := <-primed; err != nil {
		t.Fatalf("Prime: %v", err)
	}
}
