package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestScenarioTestMembersFenceAndCleanup(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "scenario-members@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().Add(time.Hour)
	create := func(slug string) App {
		t.Helper()
		app, createErr := store.CreateApp(ctx, App{AccountID: account.ID, Slug: slug, RAMMB: 128,
			Status: AppActive, PreviewOfSlug: slug, PreviewPrState: PreviewPrStateOpen,
			PreviewExpiresAt: &expiry})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return app
	}
	apiApp, worker := create("dev-scenario-api"), create("dev-scenario-worker")
	runID := "0123456789abcdef0123456789abcdef"
	if err := store.RegisterScenarioTestMembers(ctx, account.ID, runID, []ScenarioTestMember{
		{Workload: "api", AppID: apiApp.ID}, {Workload: "worker", AppID: worker.ID},
	}); err != nil {
		t.Fatal(err)
	}
	member, err := store.ScenarioTestMemberByApp(ctx, apiApp.ID)
	if err != nil || member.RunID != runID || member.Workload != "api" {
		t.Fatalf("caller membership = (%+v, %v)", member, err)
	}
	resolved, err := store.ScenarioTestAppByWorkload(ctx, account.ID, runID, "worker")
	if err != nil || resolved.ID != worker.ID {
		t.Fatalf("worker = (%+v, %v)", resolved, err)
	}
	if _, err := store.ScenarioTestAppByWorkload(ctx, account.ID, runID, "production"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing sibling = %v", err)
	}
	if err := store.DeleteScenarioTestMembers(ctx, account.ID, runID); !errors.Is(err, ErrConflict) {
		t.Fatalf("premature cleanup = %v", err)
	}
	if _, err := store.SoftDeleteAppCascade(ctx, worker.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ScenarioTestAppByWorkload(ctx, account.ID, runID, "worker"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted sibling = %v", err)
	}
	if _, err := store.ScenarioTestMemberByApp(ctx, apiApp.ID); err != nil {
		t.Fatalf("caller fence after sibling deletion: %v", err)
	}
	if _, err := store.SoftDeleteAppCascade(ctx, apiApp.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteScenarioTestMembers(ctx, account.ID, runID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ScenarioTestMemberByApp(ctx, apiApp.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("membership after cleanup = %v", err)
	}
	second := create("dev-scenario-second-run")
	otherRunID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := store.RegisterScenarioTestMembers(ctx, account.ID, otherRunID, []ScenarioTestMember{{Workload: "second", AppID: second.ID}}); err != nil {
		t.Fatal(err)
	}
	if removed, err := store.PruneScenarioTestMembers(ctx, 10); err != nil || removed != 0 {
		t.Fatalf("prune active run = (%d, %v)", removed, err)
	}
	if _, err := store.SoftDeleteAppCascade(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	if removed, err := store.PruneScenarioTestMembers(ctx, 10); err != nil || removed != 1 {
		t.Fatalf("prune deleted run = (%d, %v)", removed, err)
	}
}
