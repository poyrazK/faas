package state

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestScenarioTestExpiryGuardsAndBounds(t *testing.T) {
	// adr: 984 — expiry never removes a live namespace or guesses a legacy deadline.
	ctx := context.Background()
	past, future := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	for _, tc := range []struct {
		name        string
		expiry      *time.Time
		status      AppStatus
		missing     bool
		wantState   string
		wantRemoved int
	}{
		{"expired-live", &past, AppActive, false, "expired", 0},
		{"future-deleted", &future, AppDeleted, false, "pending", 0},
		{"unknown-deleted", nil, AppDeleted, false, "unknown", 0},
		{"missing-app", &past, AppDeleted, true, "expired", 0},
		{"expired-deleted", &past, AppDeleted, false, "expired", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMemStore()
			if !tc.missing {
				store.apps["app"] = App{ID: "app", Status: tc.status}
			}
			store.scenarioTestMembers = map[string]ScenarioTestMember{"app": {AccountID: "account", RunID: "run", Workload: "api", AppID: "app", RegistrationExpiresAt: tc.expiry}}
			status, err := store.ScenarioTestRegistrationStatus(ctx, "account", "run")
			if err != nil || status.ExpiryState != tc.wantState {
				t.Fatalf("status: %+v %v", status, err)
			}
			if n, err := store.PruneScenarioTestMembers(ctx, 1); err != nil || n != tc.wantRemoved {
				t.Fatalf("prune: %d %v", n, err)
			}
		})
	}
	store := NewMemStore()
	store.scenarioTestMembers = map[string]ScenarioTestMember{}
	for run := 0; run < 3; run++ {
		for app := 0; app < 2; app++ {
			id := fmt.Sprintf("app-%d-%d", run, app)
			store.apps[id] = App{ID: id, Status: AppDeleted}
			store.scenarioTestMembers[id] = ScenarioTestMember{AccountID: "account", RunID: fmt.Sprint(run), Workload: fmt.Sprint(app), AppID: id, RegistrationExpiresAt: &past}
		}
	}
	if n, err := store.PruneScenarioTestMembers(ctx, 0); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if n, err := store.PruneScenarioTestMembers(ctx, 1); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	if len(store.scenarioTestMembers) != 4 {
		t.Fatal("run limit split or exceeded")
	}
	if n, err := store.PruneScenarioTestMembers(ctx, 10); err != nil || n != 4 {
		t.Fatal(n, err)
	}
	if n, err := store.PruneScenarioTestMembers(ctx, 10); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}

func TestScenarioTestExpiryCapturedFromMemberLeases(t *testing.T) {
	// adr: 984 — caller metadata cannot extend registration lifetime.
	ctx := context.Background()
	store := NewMemStore()
	early, late := time.Now().UTC().Add(time.Hour), time.Now().UTC().Add(2*time.Hour)
	for id, expiry := range map[string]time.Time{"api": early, "worker": late} {
		expiry := expiry
		store.apps[id] = App{ID: id, AccountID: "account", Status: AppActive, PreviewOfSlug: id, PreviewPrState: PreviewPrStateOpen, PreviewExpiresAt: &expiry}
	}
	forged := late.Add(24 * time.Hour)
	run := "0123456789abcdef0123456789abcdef"
	if err := store.RegisterScenarioTestMembers(ctx, "account", run, []ScenarioTestMember{{Workload: "api", AppID: "api", RegistrationExpiresAt: &forged}, {Workload: "worker", AppID: "worker"}}); err != nil {
		t.Fatal(err)
	}
	status, err := store.ScenarioTestRegistrationStatus(ctx, "account", run)
	if err != nil || status.ExpiresAt == nil || !status.ExpiresAt.Equal(late) {
		t.Fatal(status, err)
	}
	app := store.apps["worker"]
	app.PreviewExpiresAt = &forged
	store.apps["worker"] = app
	status, err = store.ScenarioTestRegistrationStatus(ctx, "account", run)
	if err != nil || !status.ExpiresAt.Equal(late) {
		t.Fatal("deadline changed with preview renewal", status, err)
	}
	if err := store.DeleteScenarioTestMembers(ctx, "account", run); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}
