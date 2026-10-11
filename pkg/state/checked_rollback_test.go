package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func checkedRollbackFixture(t *testing.T, s state.Store, service bool) (state.Account, state.App, state.Deployment, state.Deployment) {
	t.Helper()
	return checkedRollbackFixtureContext(t.Context(), t, s, service)
}
func checkedRollbackFixtureContext(ctx context.Context, t *testing.T, s state.Store, service bool) (state.Account, state.App, state.Deployment, state.Deployment) {
	t.Helper()
	a, err := s.CreateAccount(ctx, uuid.NewString()+"@rollback.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	manifest := state.AppManifest{}
	if service {
		manifest.ExecutionMode = api.ExecutionModeService
		manifest.ServiceReplicas = &state.ServiceReplicas{Min: 1, Max: 3, Desired: 2}
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "rollback-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 5, Manifest: manifest})
	if err != nil {
		t.Fatal(err)
	}
	create := func(createCtx context.Context) state.Deployment {
		d, err := s.CreateDeployment(createCtx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("1", 64), Scope: "default", Status: state.DeployPending, TrafficPercent: 100})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.SetDeploymentRootfs(createCtx, d.ID, "/test/"+d.ID, "test/"+d.ID, 4096); err != nil {
			t.Fatal(err)
		}
		if err = s.MarkDeploymentLive(createCtx, d.ID); err != nil {
			t.Fatal(err)
		}
		d, err = s.DeploymentByID(createCtx, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	target, current := create(ctx), create(ctx)
	return a, app, target, current
}
func TestCheckedRollbackMem(t *testing.T) {
	checkedRollbackSuite(t, state.NewMemStore(), state.DefaultLocalNodeName, false)
}
func TestCheckedRollbackPG(t *testing.T) {
	s, ctx := pgStore(t)
	checkedRollbackSuite(t, s, resolveDefaultLocal(t, ctx, s), false)
}
func TestCheckedRollbackServiceMem(t *testing.T) {
	checkedRollbackSuite(t, state.NewMemStore(), state.DefaultLocalNodeName, true)
}
func TestCheckedRollbackServicePG(t *testing.T) {
	s, ctx := pgStore(t)
	checkedRollbackSuite(t, s, resolveDefaultLocal(t, ctx, s), true)
}
func checkedRollbackSuite(t *testing.T, s state.Store, node string, service bool) {
	t.Helper()
	ctx := t.Context()
	a, app, target, current := checkedRollbackFixture(t, s, service)
	store := s.(state.CheckedRollbackStore)
	if _, err := store.CreateCheckedRollback(ctx, a.ID, app.ID, target.ID, uuid.NewString(), "wrong current"); !errors.Is(err, state.ErrCheckedRollbackChanged) {
		t.Fatalf("wrong pair accepted: %v", err)
	}
	operation, err := store.CreateCheckedRollback(ctx, a.ID, app.ID, target.ID, current.ID, "restore previous release")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCheckedRollback(ctx, a.ID, app.ID, target.ID, current.ID, "duplicate"); !errors.Is(err, state.ErrConflict) && !errors.Is(err, state.ErrNoRollbackTarget) && !errors.Is(err, state.ErrRollbackTargetAlreadyLive) {
		t.Fatalf("duplicate accepted: %v", err)
	}
	untouched := func() {
		t.Helper()
		got, _ := s.DeploymentByID(ctx, current.ID)
		old, _ := s.DeploymentByID(ctx, target.ID)
		if got.Status != state.DeployLive || got.TrafficPercent != 100 || old.TrafficPercent != 0 {
			t.Fatalf("rollback changed current before checked cutover: %+v %+v", got, old)
		}
	}
	untouched()
	if _, err := store.CommitCheckedRollback(ctx, operation); !errors.Is(err, state.ErrCheckedRollbackChanged) {
		t.Fatalf("cutover before readiness: %v", err)
	}
	readinessStarted := time.Now().UTC()
	if err = s.MarkDeploymentLive(ctx, target.ID); err != nil {
		t.Fatal(err)
	}
	if service {
		ready, readErr := s.DeploymentByID(ctx, target.ID)
		if readErr != nil || ready.RolloutStartedAt == nil || ready.RolloutStartedAt.Before(readinessStarted) {
			t.Fatalf("historical service readiness reused the old rollout clock: %+v %v", ready, readErr)
		}
	}
	operation, err = store.GetCheckedRollback(ctx, a.ID, app.ID, operation.ID)
	if err != nil || operation.Status != "ready" {
		t.Fatalf("readiness receipt: %+v %v", operation, err)
	}
	if _, err := s.UpdateDeploymentTraffic(ctx, target.ID, 100); !errors.Is(err, state.ErrCheckedRollbackRequired) {
		t.Fatalf("generic routing bypassed operation under off policy: %v", err)
	}
	untouched()
	zero := int64(0)
	if _, err := s.(state.BindingReleasePolicyStore).SetBindingReleasePolicy(ctx, a.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitCheckedRollback(ctx, operation); !state.IsBindingReleaseRequired(err) {
		t.Fatalf("unchecked cutover accepted: %v", err)
	}
	fence := func(recipient state.Deployment) state.BindingPromotionFence {
		f := bindingPromotionFence(t, s.(state.BindingPromotionStore), a, app, recipient)
		f.PolicyRevision = 1
		f.MaxVerificationAge = api.DefaultBindingVerificationAge
		return f
	}
	if _, err := store.CommitCheckedRollback(state.WithBindingReleaseFences(ctx, []state.BindingPromotionFence{fence(current)}), operation); !state.IsBindingReleaseRequired(err) {
		t.Fatalf("current evidence accepted for rollback target: %v", err)
	}
	f := fence(target)
	f.ValidUntil = time.Now().Add(-time.Second)
	if _, err := store.CommitCheckedRollback(state.WithBindingReleaseFences(ctx, []state.BindingPromotionFence{f}), operation); !errors.Is(err, state.ErrBindingPromotionExpired) {
		t.Fatalf("expired evidence accepted: %v", err)
	}
	f = fence(target)
	if err = s.UpsertAppEnv(ctx, a.ID, app.ID, "ROLLBACK_CONFIG", "rotated"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitCheckedRollback(state.WithBindingReleaseFences(ctx, []state.BindingPromotionFence{f}), operation); !errors.Is(err, state.ErrBindingPromotionChanged) {
		t.Fatalf("changed credentials accepted: %v", err)
	}
	untouched()
	if service {
		if _, err := store.CommitCheckedRollback(state.WithBindingReleaseFences(ctx, []state.BindingPromotionFence{fence(target)}), operation); !errors.Is(err, state.ErrServiceRolloutNotReady) {
			t.Fatalf("unready service routed: %v", err)
		}
		for range 2 {
			if _, err := s.CreateInstanceWithMode(ctx, app.ID, target.ID, "running", 128, node, uuid.NewString(), "service"); err != nil {
				t.Fatal(err)
			}
		}
	}
	committed, err := store.CommitCheckedRollback(state.WithBindingReleaseFences(ctx, []state.BindingPromotionFence{fence(target)}), operation)
	if err != nil {
		t.Fatal(err)
	}
	want := "complete"
	if service {
		want = "routing"
	}
	if committed.Status != want || committed.AuditID == "" {
		t.Fatalf("invalid routing receipt: %+v", committed)
	}
	if err := store.UpdateCheckedRollback(ctx, operation, "blocked", "late", nil); !errors.Is(err, state.ErrCheckedRollbackChanged) {
		t.Fatalf("late blocker replaced commit: %v", err)
	}
	got, _ := s.DeploymentByID(ctx, target.ID)
	if got.TrafficPercent != 100 {
		t.Fatalf("target did not receive traffic: %+v", got)
	}
	if service {
		if err := store.UpdateCheckedRollback(ctx, committed, "complete", "", nil); !errors.Is(err, state.ErrCheckedRollbackChanged) {
			t.Fatalf("reported completion before handoff: %v", err)
		}
		if err = s.UpsertAppEnv(ctx, a.ID, app.ID, "ROLLBACK_CONFIG", "rotated after routing"); err != nil {
			t.Fatal(err)
		}
		if _, err = s.FinalizeServiceRollout(ctx, target.ID); err != nil {
			t.Fatalf("fresh grant incorrectly required for no-gain cleanup: %v", err)
		}
		if err = store.UpdateCheckedRollback(ctx, committed, "complete", "", nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CommitCheckedRollback(ctx, operation); !errors.Is(err, state.ErrCheckedRollbackChanged) {
		t.Fatalf("replayed operation routed again: %v", err)
	}
}
func TestCheckedRollbackConcurrentDeploymentMem(t *testing.T) {
	checkedRollbackConcurrentSuite(t, state.NewMemStore())
}
func TestCheckedRollbackConcurrentDeploymentPG(t *testing.T) {
	s, _ := pgStore(t)
	checkedRollbackConcurrentSuite(t, s)
}
func checkedRollbackConcurrentSuite(t *testing.T, s state.Store) {
	t.Helper()
	ctx := t.Context()
	a, app, target, current := checkedRollbackFixture(t, s, false)
	store := s.(state.CheckedRollbackStore)
	operation, err := store.CreateCheckedRollback(ctx, a.ID, app.ID, target.ID, current.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkDeploymentLive(ctx, target.ID); err != nil {
		t.Fatal(err)
	}
	operation, _ = store.GetCheckedRollback(ctx, a.ID, app.ID, operation.ID)
	newer, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: state.DeployPending, ImageDigest: "sha256:newer", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.MarkDeploymentLive(ctx, newer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitCheckedRollback(ctx, operation); !errors.Is(err, state.ErrCheckedRollbackChanged) {
		t.Fatalf("overwrote newer release: %v", err)
	}
	if err := store.UpdateCheckedRollback(ctx, operation, "failed", "rollback_deployment_changed", nil); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkDeploymentLive(ctx, target.ID); !errors.Is(err, state.ErrCheckedRollbackRequired) {
		t.Fatalf("late image completion activated failed rollback: %v", err)
	}
	got, _ := s.DeploymentByID(ctx, newer.ID)
	if got.TrafficPercent != 100 {
		t.Fatalf("newer release lost traffic: %+v", got)
	}
}
func TestCheckedRollbackReadOwnershipMem(t *testing.T) {
	s := state.NewMemStore()
	a, app, target, current := checkedRollbackFixture(t, s, false)
	var store state.CheckedRollbackStore = s
	r, err := store.CreateCheckedRollback(t.Context(), a.ID, app.ID, target.ID, current.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.GetCheckedRollback(t.Context(), uuid.NewString(), app.ID, r.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account read: %v", err)
	}
	if _, err = store.GetCheckedRollback(t.Context(), a.ID, uuid.NewString(), r.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-app read: %v", err)
	}
}
