package state

import (
	"errors"
	"sort"
	"testing"
)

func TestMemStoreAppLogDrainHealthEdgeCases(t *testing.T) {
	store, ctx, account, app := appLogDrainFixture(t)
	if err := store.UpsertAppLogDrainHealth(ctx, AppLogDrainHealth{DrainID: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpsertAppLogDrainHealth missing = %v, want ErrNotFound", err)
	}

	first, err := store.CreateAppLogDrain(ctx, AppLogDrain{
		AppID: app.ID, AccountID: account.ID, Kind: AppLogDrainKindHTTPJSON,
		TargetURL: "https://logs.example/edge-first",
	})
	if err != nil {
		t.Fatalf("CreateAppLogDrain first: %v", err)
	}
	second, err := store.CreateAppLogDrain(ctx, AppLogDrain{
		AppID: app.ID, AccountID: account.ID, Kind: AppLogDrainKindHTTPJSON,
		TargetURL: "https://logs.example/edge-second",
	})
	if err != nil {
		t.Fatalf("CreateAppLogDrain second: %v", err)
	}
	otherApp, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "log-drain-health-other-" + newID()})
	if err != nil {
		t.Fatalf("CreateApp other: %v", err)
	}
	other, err := store.CreateAppLogDrain(ctx, AppLogDrain{
		AppID: otherApp.ID, AccountID: account.ID, Kind: AppLogDrainKindHTTPJSON,
		TargetURL: "https://logs.example/edge-other",
	})
	if err != nil {
		t.Fatalf("CreateAppLogDrain other: %v", err)
	}

	if err := store.UpsertAppLogDrainHealth(ctx, AppLogDrainHealth{DrainID: first.ID}); err != nil {
		t.Fatalf("UpsertAppLogDrainHealth default: %v", err)
	}
	firstHealth, err := store.AppLogDrainHealthByDrainID(ctx, first.ID)
	if err != nil {
		t.Fatalf("AppLogDrainHealthByDrainID default: %v", err)
	}
	if firstHealth.Status != "unknown" || firstHealth.UpdatedAt.IsZero() {
		t.Fatalf("default health fields = %+v, want unknown status and timestamp", firstHealth)
	}
	if err := store.UpsertAppLogDrainHealth(ctx, AppLogDrainHealth{DrainID: second.ID, Status: "healthy"}); err != nil {
		t.Fatalf("UpsertAppLogDrainHealth second: %v", err)
	}
	if err := store.UpsertAppLogDrainHealth(ctx, AppLogDrainHealth{DrainID: other.ID, Status: "degraded"}); err != nil {
		t.Fatalf("UpsertAppLogDrainHealth other: %v", err)
	}

	rows, err := store.ListAppLogDrainHealthForApp(ctx, app.ID)
	if err != nil {
		t.Fatalf("ListAppLogDrainHealthForApp app: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("app health rows = %+v, want two rows", rows)
	}
	gotIDs := []string{rows[0].DrainID, rows[1].DrainID}
	wantIDs := []string{first.ID, second.ID}
	sort.Strings(wantIDs)
	if gotIDs[0] != wantIDs[0] || gotIDs[1] != wantIDs[1] {
		t.Fatalf("app health ids = %v, want sorted %v", gotIDs, wantIDs)
	}

	rows, err = store.ListAppLogDrainHealthForApp(ctx, otherApp.ID)
	if err != nil {
		t.Fatalf("ListAppLogDrainHealthForApp other: %v", err)
	}
	if len(rows) != 1 || rows[0].DrainID != other.ID {
		t.Fatalf("other app health rows = %+v, want %q", rows, other.ID)
	}
}
