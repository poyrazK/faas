package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/promotion"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func projectRollbackFixture(t *testing.T, s state.Store, pinned bool) (state.App, state.Deployment, state.Deployment, state.ProjectEnvironmentPromotion) {
	t.Helper()
	ctx := t.Context()
	a, err := s.CreateAccount(ctx, uuid.NewString()+"@review2.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "review2-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	if pinned {
		manifest := app.Manifest
		manifest.RevisionPinTTLSeconds = 300
		app, err = s.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest})
		if err != nil {
			t.Fatal(err)
		}
	}
	p := state.ProjectEnvironmentPromotion{ID: uuid.NewString(), ToEnvironment: "default"}
	create := func(digest, reason string) state.Deployment {
		d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "default", Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat(digest, 64), Reason: reason, Status: state.DeployBuilding})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.SetDeploymentRootfs(ctx, d.ID, "/test/"+d.ID, "test/"+d.ID, 4096); err != nil {
			t.Fatal(err)
		}
		if err = s.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
		return d
	}
	old := create("a", "")
	current := create("b", "environment promotion/"+p.ID)
	return app, old, current, p
}

func TestProjectRollbackPinnedBaselineReceivesTrafficMem(t *testing.T) {
	projectRollbackPinned(t, state.NewMemStore())
}
func TestProjectRollbackPinnedBaselineReceivesTrafficPG(t *testing.T) {
	s, _ := pgStore(t)
	projectRollbackPinned(t, s)
}
func projectRollbackPinned(t *testing.T, s state.Store) {
	app, old, current, p := projectRollbackFixture(t, s, true)
	prior, err := s.DeploymentByID(t.Context(), old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if prior.Status != state.DeployLive || prior.TrafficPercent != 0 {
		t.Fatalf("fixture did not retain a live baseline: %+v", prior)
	}
	status, _, err := promotion.RollbackWorkload(t.Context(), s, p, state.ProjectEnvironmentPromotionWorkload{PreviousTargetDeploymentID: old.ID, TargetDeploymentID: current.ID}, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	live, err := s.LiveDeploymentForScope(t.Context(), app.ID, "default")
	if err != nil || live.ID != old.ID || live.TrafficPercent != 100 {
		t.Fatalf("rollback reports %s but baseline still has no traffic: live=%s percent=%d expected=%s err=%v", status, live.ID, live.TrafficPercent, old.ID, err)
	}
}

type concurrentProjectRollbackEdit struct {
	MemStore *state.MemStore
	previous string
	onRead   func()
}

func (s *concurrentProjectRollbackEdit) DeploymentByID(ctx context.Context, id string) (state.Deployment, error) {
	d, err := s.MemStore.DeploymentByID(ctx, id)
	if id == s.previous && s.onRead != nil {
		f := s.onRead
		s.onRead = nil
		f()
	}
	return d, err
}
func TestProjectRollbackKeepsNewerRelease(t *testing.T) {
	mem := state.NewMemStore()
	app, old, current, p := projectRollbackFixture(t, mem, false)
	newest, err := mem.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("c", 64), Status: state.DeployBuilding})
	if err != nil {
		t.Fatal(err)
	}
	fake := &concurrentProjectRollbackEdit{MemStore: mem, previous: old.ID, onRead: func() {
		if err := mem.MarkDeploymentLive(t.Context(), newest.ID); err != nil {
			t.Fatal(err)
		}
	}}
	status, _, err := promotion.RollbackWorkload(t.Context(), statetest.NewMem(fake), p, state.ProjectEnvironmentPromotionWorkload{PreviousTargetDeploymentID: old.ID, TargetDeploymentID: current.ID}, app.ID)
	live, readErr := mem.LiveDeploymentForScope(t.Context(), app.ID, "default")
	if err == nil || readErr != nil || live.ID != newest.ID {
		t.Fatalf("concurrent newer release was overwritten: status=%s err=%v live=%s expected=%s readErr=%v", status, err, live.ID, newest.ID, readErr)
	}
}

func TestProjectRollbackPreservesBindingPolicyMem(t *testing.T) {
	projectRollbackBindingPolicy(t, state.NewMemStore())
}
func TestProjectRollbackPreservesBindingPolicyPG(t *testing.T) {
	s, _ := pgStore(t)
	projectRollbackBindingPolicy(t, s)
}
func projectRollbackBindingPolicy(t *testing.T, s state.Store) {
	app, old, current, p := projectRollbackFixture(t, s, true)
	zero := int64(0)
	if _, err := s.(state.BindingReleasePolicyStore).SetBindingReleasePolicy(t.Context(), app.AccountID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := promotion.RollbackWorkload(t.Context(), s, p, state.ProjectEnvironmentPromotionWorkload{PreviousTargetDeploymentID: old.ID, TargetDeploymentID: current.ID}, app.ID); !state.IsBindingReleaseRequired(err) {
		t.Fatalf("project rollback bypassed binding policy: %v", err)
	}
	baseline, err := s.DeploymentByID(t.Context(), old.ID)
	if err != nil || baseline.Status != state.DeployLive || baseline.TrafficPercent != 0 {
		t.Fatalf("refused rollback changed its baseline: %+v %v", baseline, err)
	}
	live, err := s.LiveDeploymentForScope(t.Context(), app.ID, "default")
	if err != nil || live.ID != current.ID {
		t.Fatalf("refused rollback changed traffic: %+v %v", live, err)
	}
}

func TestProjectRollbackLatePromotionFailureIsAtomicPG(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	app, old, current, p := projectRollbackFixture(t, s, true)
	productionMonitorExec(t, pool, "CREATE FUNCTION reject_rollback_activation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected rollback activation failure'; END $$")
	productionMonitorExec(t, pool, "CREATE TRIGGER reject_rollback_activation BEFORE UPDATE ON deployments FOR EACH ROW WHEN (OLD.status='snapshotting' AND NEW.status='live' AND NEW.traffic_percent=100) EXECUTE FUNCTION reject_rollback_activation()")
	if _, _, err := promotion.RollbackWorkload(t.Context(), s, p, state.ProjectEnvironmentPromotionWorkload{PreviousTargetDeploymentID: old.ID, TargetDeploymentID: current.ID}, app.ID); err == nil || errors.Is(err, state.ErrCheckedRollbackChanged) {
		t.Fatalf("late promotion error was not exercised: %v", err)
	}
	baseline, err := s.DeploymentByID(t.Context(), old.ID)
	if err != nil || baseline.Status != state.DeployLive || baseline.TrafficPercent != 0 {
		t.Fatalf("failed promotion changed its baseline: %+v %v", baseline, err)
	}
	live, err := s.LiveDeploymentForScope(t.Context(), app.ID, "default")
	if err != nil || live.ID != current.ID || live.TrafficPercent != 100 {
		t.Fatalf("failed promotion changed serving traffic: %+v %v", live, err)
	}
	if marked, err := s.DeploymentRollbackPrepared(t.Context(), old.ID); err != nil || marked {
		t.Fatalf("failed synchronous restore leaked a prepared marker: %v %v", marked, err)
	}
}
