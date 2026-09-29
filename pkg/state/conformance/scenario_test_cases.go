package conformance

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A test service name must never resolve outside its run, and a failed
// teardown must keep the namespace fenced until the apps have been deleted.
func testScenarioTestNamespace(t *testing.T, fx *Fixture) {
	s, ctx, accountID := fx.Store, fx.Ctx, fx.Account.ID
	limits := api.MustLimitsFor(api.PlanPro)
	expiry := time.Now().UTC().Add(time.Hour)
	newPreview := func() state.App {
		t.Helper()
		app, err := s.CreateAppIfUnderQuota(ctx, state.App{
			AccountID: accountID, Slug: "scenario-" + uuid.NewString()[:8],
			Type: state.AppTypeApp, Runtime: "node22", RAMMB: limits.RAMMB,
			MaxConcurrency: limits.MaxConcurrency, IdleTimeoutS: limits.IdleTimeoutS,
			PreviewOfSlug: fx.App.Slug, PreviewPrState: state.PreviewPrStateOpen,
			PreviewExpiresAt: &expiry,
		}, limits)
		if err != nil {
			t.Fatalf("CreateAppIfUnderQuota(preview): %v", err)
		}
		return app
	}
	app := newPreview()
	runID := strings.Repeat("a", 32)
	member := state.ScenarioTestMember{Workload: "worker", AppID: app.ID}
	if err := s.RegisterScenarioTestMembers(ctx, accountID, runID, []state.ScenarioTestMember{{Workload: "primary", AppID: fx.App.ID}}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("register production app = %v, want ErrNotFound", err)
	}
	if err := s.RegisterScenarioTestMembers(ctx, accountID, runID, []state.ScenarioTestMember{member}); err != nil {
		t.Fatalf("RegisterScenarioTestMembers: %v", err)
	}
	if got, err := s.ScenarioTestMemberByApp(ctx, app.ID); err != nil || got.AccountID != accountID || got.RunID != runID || got.Workload != "worker" {
		t.Fatalf("ScenarioTestMemberByApp = (%+v, %v)", got, err)
	}
	if got, err := s.ScenarioTestAppByWorkload(ctx, accountID, runID, "worker"); err != nil || got.ID != app.ID {
		t.Fatalf("ScenarioTestAppByWorkload = (%+v, %v)", got, err)
	}
	for _, scope := range [][2]string{{"other-account", runID}, {accountID, strings.Repeat("b", 32)}} {
		if _, err := s.ScenarioTestAppByWorkload(ctx, scope[0], scope[1], "worker"); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("cross-scope lookup %v = %v, want ErrNotFound", scope, err)
		}
	}
	if err := s.RegisterScenarioTestMembers(ctx, accountID, runID, []state.ScenarioTestMember{member}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate registration = %v, want ErrConflict", err)
	}
	if err := s.DeleteScenarioTestMembers(ctx, accountID, runID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("delete active namespace = %v, want ErrConflict", err)
	}
	if n, err := s.PruneScenarioTestMembers(ctx, 1); err != nil || n != 0 {
		t.Fatalf("prune active namespace = (%d, %v), want (0, nil)", n, err)
	}
	if err := s.DeleteApp(ctx, app.ID); err != nil {
		t.Fatalf("DeleteApp: %v", err)
	}
	if _, err := s.ScenarioTestAppByWorkload(ctx, accountID, runID, "worker"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("lookup deleted preview = %v, want ErrNotFound", err)
	}
	if n, err := s.PruneScenarioTestMembers(ctx, 1); err != nil || n != 1 {
		t.Fatalf("prune deleted namespace = (%d, %v), want (1, nil)", n, err)
	}
	if _, err := s.ScenarioTestMemberByApp(ctx, app.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("lookup pruned member = %v, want ErrNotFound", err)
	}

	second := newPreview()
	secondRun := strings.Repeat("c", 32)
	if err := s.RegisterScenarioTestMembers(ctx, accountID, secondRun, []state.ScenarioTestMember{{Workload: "worker", AppID: second.ID}}); err != nil {
		t.Fatalf("register second namespace: %v", err)
	}
	if err := s.DeleteApp(ctx, second.ID); err != nil {
		t.Fatalf("DeleteApp(second): %v", err)
	}
	if err := s.DeleteScenarioTestMembers(ctx, accountID, secondRun); err != nil {
		t.Fatalf("delete drained namespace: %v", err)
	}
	if _, err := s.ScenarioTestMemberByApp(ctx, second.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("lookup deleted member = %v, want ErrNotFound", err)
	}
}
