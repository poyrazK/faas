// adr: 590
package sched

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestStageExplicitWakeKeepsAcceptedDeploymentPolicy(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 0, false)
	accepted := stageCapacitySettings(t, f, "stage", 3, 0)
	current := stageCapacitySettings(t, f, "stage", 1, 0)
	if accepted.ID == current.ID {
		t.Fatal("fixture did not cut over stage")
	}
	engine := newEngine(t, f.store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	selected, err := engine.resolveWakeEnvironmentForDeployment(WithScope(ctx, "stage"), f.app.ID, nil, accepted.ID)
	if err != nil || selected.deployment.ID != accepted.ID || selected.app.MaxConcurrency != 3 || selected.owner.Scope != "stage" {
		t.Fatalf("accepted stage selection: deployment=%s ceiling=%d scope=%s err=%v", selected.deployment.ID, selected.app.MaxConcurrency, selected.owner.Scope, err)
	}
	for _, scope := range []string{"", "default", "production"} {
		if _, err := engine.resolveWakeEnvironmentForDeployment(WithScope(ctx, scope), f.app.ID, nil, accepted.ID); err == nil {
			t.Fatalf("accepted stage deployment escaped into scope %q", scope)
		}
	}
	if _, err := engine.resolveWakeEnvironmentForDeployment(WithScope(ctx, "stage"), f.app.ID, nil, f.prod.ID); err == nil {
		t.Fatal("production deployment entered stage wake coordinator")
	}
	if err := f.store.MarkDeploymentSuperseded(ctx, accepted.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.resolveWakeEnvironmentForDeployment(WithScope(ctx, "stage"), f.app.ID, nil, accepted.ID); err == nil {
		t.Fatal("retired deployment remained wakeable")
	}
}

func TestStageWakeFanoutUsesDeployedPolicyAndOwnCapacity(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 0, false)
	stageCapacitySettings(t, f, "production", 1, 0)
	stageCapacitySettings(t, f, "stage", 3, 0)
	engine := newEngine(t, f.store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	for _, scope := range []string{"production", "stage", "stage"} {
		result, err := engine.AdmitInstance(ctx, f.app.ID, "", scope, "")
		if err != nil || result.AtCapacity {
			t.Fatalf("seed %s: %+v %v", scope, result, err)
		}
	}
	head, err := f.store.ProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, "stage", f.app.ID)
	if err != nil {
		t.Fatal(err)
	}
	settings := head.Settings
	settings.MaxConcurrency = 1
	if _, err := f.store.PutProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, "stage", f.app.ID, head.Revision, settings); err != nil {
		t.Fatal(err)
	}
	shared := 1
	if _, err := f.store.UpdateApp(ctx, f.app.ID, state.UpdateAppParams{MaxConcurrency: &shared}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		scope           string
		limit, existing int
	}{{"", 1, 1}, {"stage", 3, 2}} {
		for range 2 {
			fanout := engine.wakeFanoutFor(WithScope(ctx, row.scope), f.app.ID)
			if fanout.MaxInFlight != row.limit || fanout.Existing != row.existing || fanout.PerVM != api.MustLimitsFor(api.PlanPro).ConcurrencyPerVMBound {
				t.Fatalf("%s fanout: %+v", row.scope, fanout)
			}
		}
	}
}

type scopedCoordOutcome struct {
	scope string
	out   CoordOutcome
	err   error
}

func waitStageBoots(t *testing.T, started <-chan struct{}, count int) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for range count {
		select {
		case <-started:
		case <-deadline.C:
			t.Fatal("selected environment did not dispatch its own wake capacity")
		}
	}
}

func TestCoordinatedStageBurstsAndFollowersStayIndependent(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 0, false)
	production := stageCapacitySettings(t, f, "production", 2, 0)
	stage := stageCapacitySettings(t, f, "stage", 2, 0)
	vmm := &fakeVMM{bootStarted: make(chan struct{}, 4), bootRelease: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(vmm.bootRelease) }) }
	t.Cleanup(release)
	engine := newEngine(t, f.store, vmm, &fakeNotifier{}, "1.10.0")
	results := make(chan scopedCoordOutcome, 4)
	wake := func(scope string) {
		out, err := engine.EnsureWakeCapacity(WithScope(ctx, scope), f.app.ID, TriggerGateway, 2)
		results <- scopedCoordOutcome{scope, out, err}
	}
	go wake("")
	go wake("stage")
	waitStageBoots(t, vmm.bootStarted, 4)
	go wake("")
	go wake("stage")
	deadline := time.Now().Add(2 * time.Second)
	for {
		engine.wakeCoord.mu.Lock()
		joined := 0
		for _, calls := range engine.wakeCoord.inflight {
			for _, call := range calls {
				joined += call.waiters
			}
		}
		engine.wakeCoord.mu.Unlock()
		if joined == 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("environment followers did not join their own leaders")
		}
		time.Sleep(time.Millisecond)
	}
	release()
	primary := make(map[string]string)
	for range 4 {
		got := <-results
		if got.err != nil || got.out.Err != nil || got.out.Instance == nil || len(got.out.Additional) != 1 {
			t.Fatalf("%s coordinated outcome: %+v %v", got.scope, got.out, got.err)
		}
		want := stage.ID
		if got.scope == "" {
			want = production.ID
		}
		if got.out.Instance.DeploymentID != want || got.out.Additional[0].DeploymentID != want {
			t.Fatalf("%s borrowed a sibling deployment: %+v", got.scope, got.out)
		}
		if previous, ok := primary[got.scope]; ok && previous != got.out.Instance.InstanceID {
			t.Fatal("same-environment follower did not receive its leader's instance")
		}
		primary[got.scope] = got.out.Instance.InstanceID
	}
	if engine.ledger.ConcurrencyForDeployment(f.app.ID, production.ID) != 2 || engine.ledger.ConcurrencyForDeployment(f.app.ID, stage.ID) != 2 {
		t.Fatal("a sibling environment satisfied desired initial capacity")
	}
	engine.wakeCoord.mu.Lock()
	defer engine.wakeCoord.mu.Unlock()
	if len(engine.wakeCoord.inflight) != 0 {
		t.Fatal("coordinated stage entries leaked")
	}
}

func TestCoordinatedStageReplacementCannotJoinOldBoot(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 0, false)
	if err := f.store.MarkDeploymentSuperseded(ctx, f.stage.ID); err != nil {
		t.Fatal(err)
	}
	f.stage = stageCapacitySettings(t, f, "stage", 2, 0)
	vmm := &fakeVMM{bootStarted: make(chan struct{}, 2), bootRelease: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(vmm.bootRelease) }) }
	t.Cleanup(release)
	engine := newEngine(t, f.store, vmm, &fakeNotifier{}, "1.10.0")
	results := make(chan scopedCoordOutcome, 2)
	wake := func(label string) {
		out, err := engine.EnsureWake(WithScope(ctx, "stage"), f.app.ID, TriggerGateway)
		results <- scopedCoordOutcome{label, out, err}
	}
	go wake("original")
	waitStageBoots(t, vmm.bootStarted, 1)
	f.recreateStage(t.Context(), t)
	settings, err := state.WorkloadSettingsFromApp(f.app)
	if err != nil {
		t.Fatal(err)
	}
	settings.WarmPoolSize = 0
	if _, err := f.store.PutProjectEnvironmentWorkloadSpec(ctx, f.account.ID, f.project.ID, "stage", f.app.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	replacement := stageCapacitySettings(t, f, "stage", 2, 0)
	go wake("replacement")
	waitStageBoots(t, vmm.bootStarted, 1)
	release()
	for range 2 {
		got := <-results
		if got.scope == "original" {
			if got.err == nil && got.out.Err == nil {
				t.Fatal("deleted original environment published a successful boot")
			}
		} else if got.err != nil || got.out.Instance == nil || got.out.Instance.DeploymentID != replacement.ID {
			t.Fatalf("replacement inherited old boot: %+v %v", got.out, got.err)
		}
	}
	if engine.ledger.ConcurrencyForDeployment(f.app.ID, f.stage.ID) != 0 || engine.ledger.ConcurrencyForDeployment(f.app.ID, replacement.ID) != 1 {
		t.Fatal("replacement retained original capacity or lost its own reservation")
	}
}

func TestStagePrewarmTargetsOwnCapacity(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 0, false)
	production := stageCapacitySettings(t, f, "production", 2, 0)
	stage := stageCapacitySettings(t, f, "stage", 3, 0)
	engine := newEngine(t, f.store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	for range 2 {
		if result, err := engine.AdmitInstance(ctx, f.app.ID, "", "stage", ""); err != nil || result.AtCapacity {
			t.Fatalf("seed stage: %+v %v", result, err)
		}
	}
	if admitted, err := engine.Prewarm(ctx, f.app.ID, 2); err != nil || admitted != 2 {
		t.Fatalf("stage replicas satisfied production prewarm: admitted=%d %v", admitted, err)
	}
	if admitted, err := engine.Prewarm(WithScope(ctx, "stage"), f.app.ID, 3); err != nil || admitted != 1 {
		t.Fatalf("production replicas satisfied stage prewarm: admitted=%d %v", admitted, err)
	}
	for _, row := range []struct {
		scope  string
		target int
	}{{"", 2}, {"stage", 3}} {
		if admitted, err := engine.Prewarm(WithScope(ctx, row.scope), f.app.ID, row.target); err != nil || admitted != 0 {
			t.Fatalf("%s prewarm duplicated capacity: admitted=%d %v", row.scope, admitted, err)
		}
	}
	if engine.ledger.ConcurrencyForDeployment(f.app.ID, production.ID) != 2 || engine.ledger.ConcurrencyForDeployment(f.app.ID, stage.ID) != 3 || engine.ledger.Concurrency(f.app.ID) != api.MustLimitsFor(api.PlanPro).MaxConcurrency {
		t.Fatal("prewarm did not preserve environment targets within the shared plan")
	}
}

type stageWakeSwitchVerifier struct {
	once sync.Once
	hook func() error
	err  error
}

func (v *stageWakeSwitchVerifier) Verify(context.Context, string, string) error {
	v.once.Do(func() { v.err = v.hook() })
	return v.err
}

func TestStageWakeBurstDoesNotAdoptAnotherDeployment(t *testing.T) {
	ctx := t.Context()
	f := seedStageSnapshotPolicy(t, 0, false)
	stage := stageCapacitySettings(t, f, "stage", 3, 0)
	var replacement state.Deployment
	verifier := &stageWakeSwitchVerifier{hook: func() error {
		var err error
		replacement, err = f.store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: "stage", Kind: state.DeploymentKindImage, Status: state.DeployLive})
		if err != nil {
			return err
		}
		return f.store.SetDeploymentRootfs(ctx, replacement.ID, "/local/replacement.ext4", "apps/replacement.ext4", 4096)
	}}
	vmm := &fakeVMM{}
	engine := newEngine(t, f.store, vmm, &fakeNotifier{}, "1.10.0").WithVerifier(verifier)
	out, err := engine.EnsureWakeCapacity(WithScope(ctx, "stage"), f.app.ID, TriggerGateway, 3)
	if err != nil || out.Instance == nil || out.Instance.DeploymentID != stage.ID || len(out.Additional) != 0 {
		t.Fatalf("burst changed deployed generation: %+v %v", out, err)
	}
	if vmm.coldBoots != 1 || engine.ledger.ConcurrencyForDeployment(f.app.ID, replacement.ID) != 0 {
		t.Fatal("burst continuations admitted the replacement deployment")
	}
}

func TestWakeCoordForgetEvictsAllAppDeployments(t *testing.T) {
	coord := newWakeCoord()
	var calls []*wakeCoordCall
	for _, key := range []string{"app", "app\x00deployment:prod", "app\x00deployment:stage"} {
		call, _, err := coord.Enter(key, WakeFanout{})
		if err != nil {
			t.Fatal(err)
		}
		calls = append(calls, call)
	}
	other, _, _ := coord.Enter("app-sibling\x00deployment:prod", WakeFanout{})
	coord.Forget("app")
	for _, call := range calls {
		if out := call.Await(t.Context()); !errors.Is(out.Err, ErrAppDeleted) {
			t.Fatalf("forgotten deployment: %+v", out)
		}
	}
	coord.mu.Lock()
	if len(coord.inflight) != 1 || other.completed {
		t.Error("app deletion forgot a sibling app or retained its own deployment")
	}
	coord.mu.Unlock()
	other.Complete(CoordOutcome{})
	coord.Release("app-sibling\x00deployment:prod", other)
}
