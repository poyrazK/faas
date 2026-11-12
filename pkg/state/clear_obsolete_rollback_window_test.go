package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Clear-obsolete kept the two newest rows of any status, so failed attempts
// pushed real rollback targets out and their layers were reclaimed, after
// which rollback was refused (deploy review 2026-11-10, D2). Superseded
// deployments in the rollback window now stay whatever failed after them.
func TestClearObsoleteDeployments_KeepsTheRollbackWindow(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		exerciseClearObsoleteKeepsRollbackWindow(t, state.NewMemStore(), context.Background())
	})
	t.Run("postgres", func(t *testing.T) {
		s, ctx := pgStore(t)
		exerciseClearObsoleteKeepsRollbackWindow(t, s, ctx)
	})
}

func exerciseClearObsoleteKeepsRollbackWindow(t *testing.T, s contractTestStore, ctx context.Context) {
	t.Helper()
	acct, err := s.CreateAccount(ctx, uuid.NewString()+"@obsolete-window.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "window-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	create := func() state.Deployment {
		t.Helper()
		d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:window", Status: state.DeployPending})
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond) // distinct created_at, oldest first
		return d
	}
	live := func(d state.Deployment) {
		t.Helper()
		if err := s.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatalf("MarkDeploymentLive: %v", err)
		}
	}
	fail := func(d state.Deployment) {
		t.Helper()
		if _, err := s.SetDeploymentFailed(ctx, d.ID, api.CodeDeployFailed, "attempt failed"); err != nil {
			t.Fatalf("SetDeploymentFailed: %v", err)
		}
	}
	// Oldest to newest: three releases that served, two failed attempts,
	// then the live release. Marking a release live supersedes the one
	// before it.
	s0, s1, s2 := create(), create(), create()
	f1, f2 := create(), create()
	current := create()
	live(s0)
	live(s1)
	live(s2)
	fail(f1)
	fail(f2)
	live(current)

	if _, err := s.ClearObsoleteDeployments(ctx, app.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("ClearObsoleteDeployments: %v", err)
	}
	want := map[string]bool{ // true: cleared
		s0.ID: true,                // the fourth live-or-superseded release: outside the window
		s1.ID: false, s2.ID: false, // rollback targets
		f1.ID:      true,
		f2.ID:      false, // one of the two newest rows
		current.ID: false,
	}
	for id, cleared := range want {
		d, err := s.DeploymentByID(ctx, id)
		if err != nil {
			t.Fatalf("DeploymentByID %s: %v", id, err)
		}
		if got := d.DeletedAt != nil; got != cleared {
			t.Errorf("deployment %s (%s) cleared=%v, want %v", id, d.Status, got, cleared)
		}
	}
}
