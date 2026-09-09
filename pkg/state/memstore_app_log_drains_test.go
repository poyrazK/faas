package state

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func appLogDrainFixture(t *testing.T) (*MemStore, context.Context, Account, App) {
	t.Helper()
	ctx := context.Background()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "log-drain-"+newID()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "log-drain-" + newID()})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	return store, ctx, account, app
}

func TestMemStoreAppLogDrainCRUDAndQuota(t *testing.T) {
	store, ctx, account, app := appLogDrainFixture(t)
	created, err := store.CreateAppLogDrain(ctx, AppLogDrain{
		AppID: app.ID, AccountID: account.ID, Kind: AppLogDrainKindHTTPJSON,
		TargetURL: "https://logs.example/one", AuthHeaderSealed: []byte("sealed"), Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateAppLogDrain: %v", err)
	}
	if created.ID == "" || created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("CreateAppLogDrain did not stamp identity and timestamps: %+v", created)
	}

	got, err := store.AppLogDrainByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("AppLogDrainByID: %v", err)
	}
	if got.TargetURL != created.TargetURL || got.Kind != created.Kind || string(got.AuthHeaderSealed) != "sealed" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	got.AuthHeaderSealed[0] = 'x'
	untouched, err := store.AppLogDrainByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("AppLogDrainByID after mutation: %v", err)
	}
	if string(untouched.AuthHeaderSealed) != "sealed" {
		t.Fatalf("AppLogDrainByID returned aliased secret: %q", untouched.AuthHeaderSealed)
	}

	disabled, err := store.CreateAppLogDrain(ctx, AppLogDrain{
		AppID: app.ID, AccountID: account.ID, Kind: AppLogDrainKindOTLP,
		TargetURL: "https://logs.example/two", Enabled: false,
	})
	if err != nil {
		t.Fatalf("CreateAppLogDrain disabled: %v", err)
	}
	rows, err := store.ListAppLogDrainsForApp(ctx, app.ID)
	if err != nil {
		t.Fatalf("ListAppLogDrainsForApp: %v", err)
	}
	if len(rows) != 2 || rows[0].ID != created.ID || rows[1].ID != disabled.ID {
		t.Fatalf("app drain list = %+v, want created order", rows)
	}
	enabled, err := store.ListEnabledAppLogDrains(ctx)
	if err != nil {
		t.Fatalf("ListEnabledAppLogDrains: %v", err)
	}
	if len(enabled) != 1 || enabled[0].ID != created.ID {
		t.Fatalf("enabled drain list = %+v, want only %q", enabled, created.ID)
	}

	newKind := AppLogDrainKindOTLP
	newTarget := "https://logs.example/updated"
	newSecret := []byte("new-sealed")
	newEnabled := false
	updated, err := store.UpdateAppLogDrain(ctx, created.ID, UpdateAppLogDrainParams{
		Kind: &newKind, TargetURL: &newTarget, AuthHeaderSealed: &newSecret, Enabled: &newEnabled,
	})
	if err != nil {
		t.Fatalf("UpdateAppLogDrain: %v", err)
	}
	if updated.Kind != newKind || updated.TargetURL != newTarget || string(updated.AuthHeaderSealed) != string(newSecret) || updated.Enabled {
		t.Fatalf("updated drain = %+v", updated)
	}
	if got, err := store.ListEnabledAppLogDrains(ctx); err != nil {
		t.Fatalf("ListEnabledAppLogDrains after update: %v", err)
	} else if len(got) != 0 {
		t.Fatalf("enabled drains after disabling = %+v, want empty", got)
	}

	conflictTarget := disabled.TargetURL
	if _, err := store.UpdateAppLogDrain(ctx, created.ID, UpdateAppLogDrainParams{TargetURL: &conflictTarget}); !errors.Is(err, ErrConflict) {
		t.Fatalf("UpdateAppLogDrain duplicate target = %v, want ErrConflict", err)
	}
	if _, err := store.UpdateAppLogDrain(ctx, "missing", UpdateAppLogDrainParams{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateAppLogDrain missing = %v, want ErrNotFound", err)
	}
	if _, err := store.AppLogDrainByID(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AppLogDrainByID missing = %v, want ErrNotFound", err)
	}

	if err := store.DeleteAppLogDrain(ctx, disabled.ID); err != nil {
		t.Fatalf("DeleteAppLogDrain: %v", err)
	}
	if err := store.DeleteAppLogDrain(ctx, disabled.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteAppLogDrain twice = %v, want ErrNotFound", err)
	}

	limits := api.Limits{LogDrainPerApp: 1, LogDrainPerAccount: 10}
	if _, err := store.CreateAppLogDrainIfUnderQuota(ctx, AppLogDrain{
		AppID: app.ID, AccountID: account.ID, Kind: AppLogDrainKindHTTPJSON, TargetURL: newTarget,
	}, limits); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate CreateAppLogDrainIfUnderQuota = %v, want ErrConflict", err)
	}
	_, err = store.CreateAppLogDrainIfUnderQuota(ctx, AppLogDrain{
		AppID: app.ID, AccountID: account.ID, Kind: AppLogDrainKindHTTPJSON, TargetURL: "https://logs.example/three",
	}, limits)
	if err == nil {
		t.Fatal("app quota unexpectedly allowed a second drain")
	}
	var appQuotaErr *AppLogDrainQuotaError
	if !errors.As(err, &appQuotaErr) || appQuotaErr.Scope != AppLogDrainQuotaScopeApp || appQuotaErr.Observed != 1 {
		t.Fatalf("app quota error = %v, want app quota with observed=1", err)
	}

	otherApp, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "log-drain-other-" + newID()})
	if err != nil {
		t.Fatalf("CreateApp other: %v", err)
	}
	if _, err := store.CreateAppLogDrainIfUnderQuota(ctx, AppLogDrain{
		AppID: otherApp.ID, AccountID: account.ID, Kind: AppLogDrainKindHTTPJSON, TargetURL: "https://logs.example/four",
	}, api.Limits{LogDrainPerApp: 10, LogDrainPerAccount: 1}); err == nil {
		t.Fatal("account quota unexpectedly allowed a second drain")
	} else {
		var quotaErr *AppLogDrainQuotaError
		if !errors.As(err, &quotaErr) || quotaErr.Scope != AppLogDrainQuotaScopeAccount || quotaErr.Observed != 1 {
			t.Fatalf("account quota error = %v, want account quota with observed=1", err)
		}
	}
	if _, err := store.CreateAppLogDrainIfUnderQuota(ctx, AppLogDrain{
		AppID: "missing", AccountID: account.ID, Kind: AppLogDrainKindHTTPJSON, TargetURL: "https://logs.example/missing",
	}, api.Limits{LogDrainPerApp: 10, LogDrainPerAccount: 10}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing app quota create = %v, want ErrNotFound", err)
	}
}
