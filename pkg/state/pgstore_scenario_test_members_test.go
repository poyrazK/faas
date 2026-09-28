//go:build !no_pg

package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreScenarioTestMembers(t *testing.T) {
	store, ctx := pgStore(t)
	account, err := store.CreateAccount(ctx, "scenario-members-pg@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().Add(time.Hour)
	create := func(slug string) state.App {
		t.Helper()
		app, createErr := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: slug,
			RAMMB: 128, Status: state.AppActive, PreviewOfSlug: slug,
			PreviewPrState: state.PreviewPrStateOpen, PreviewExpiresAt: &expiry})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return app
	}
	caller, worker := create("scenario-pg-caller"), create("scenario-pg-worker")
	runID := "0123456789abcdef0123456789abcdef"
	if err := store.RegisterScenarioTestMembers(ctx, account.ID, runID, []state.ScenarioTestMember{
		{Workload: "caller", AppID: caller.ID}, {Workload: "worker", AppID: worker.ID},
	}); err != nil {
		t.Fatal(err)
	}
	member, err := store.ScenarioTestMemberByApp(ctx, caller.ID)
	if err != nil || member.RunID != runID || member.Workload != "caller" {
		t.Fatalf("member = (%+v, %v)", member, err)
	}
	target, err := store.ScenarioTestAppByWorkload(ctx, account.ID, runID, "worker")
	if err != nil || target.ID != worker.ID {
		t.Fatalf("target = (%+v, %v)", target, err)
	}
	if _, err := store.ScenarioTestAppByWorkload(ctx, account.ID, runID, "missing"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing target = %v", err)
	}
	if err := store.DeleteScenarioTestMembers(ctx, account.ID, runID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("active deletion = %v", err)
	}
	for _, app := range []state.App{caller, worker} {
		if _, err := store.SoftDeleteAppCascade(ctx, app.ID); err != nil {
			t.Fatal(err)
		}
	}
	if removed, err := store.PruneScenarioTestMembers(ctx, 1); err != nil || removed != 2 {
		t.Fatalf("prune finished run = (%d, %v)", removed, err)
	}
}
