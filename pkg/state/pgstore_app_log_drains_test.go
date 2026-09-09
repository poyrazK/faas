package state_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgAppLogDrainCRUDAndQuotas(t *testing.T) {
	s, ctx := pgStore(t)
	suffix := uuid.NewString()
	account, err := s.CreateAccount(ctx, "pg-log-drain-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "pg-log-drain-" + suffix})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	created, err := s.CreateAppLogDrain(ctx, state.AppLogDrain{
		AppID: app.ID, AccountID: account.ID, Kind: state.AppLogDrainKindHTTPJSON,
		TargetURL: "https://logs.example/pg-one-" + suffix, AuthHeaderSealed: []byte("sealed"), Enabled: true,
	})
	if err != nil {
		t.Fatalf("CreateAppLogDrain: %v", err)
	}
	if created.ID == "" || created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("created drain missing identity or timestamps: %+v", created)
	}
	if _, err := s.CreateAppLogDrain(ctx, state.AppLogDrain{
		AppID: app.ID, AccountID: account.ID, Kind: state.AppLogDrainKindHTTPJSON,
		TargetURL: created.TargetURL, Enabled: true,
	}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate CreateAppLogDrain = %v, want ErrConflict", err)
	}

	got, err := s.AppLogDrainByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("AppLogDrainByID: %v", err)
	}
	if got.TargetURL != created.TargetURL || string(got.AuthHeaderSealed) != "sealed" {
		t.Fatalf("round-trip drain = %+v", got)
	}
	if _, err := s.AppLogDrainByID(ctx, uuid.NewString()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("AppLogDrainByID missing = %v, want ErrNotFound", err)
	}

	other, err := s.CreateAppLogDrain(ctx, state.AppLogDrain{
		AppID: app.ID, AccountID: account.ID, Kind: state.AppLogDrainKindOTLP,
		TargetURL: "https://logs.example/pg-two-" + suffix, Enabled: false,
	})
	if err != nil {
		t.Fatalf("CreateAppLogDrain second: %v", err)
	}
	rows, err := s.ListAppLogDrainsForApp(ctx, app.ID)
	if err != nil {
		t.Fatalf("ListAppLogDrainsForApp: %v", err)
	}
	if len(rows) != 2 || rows[0].ID != created.ID || rows[1].ID != other.ID {
		t.Fatalf("app drain list = %+v", rows)
	}
	enabled, err := s.ListEnabledAppLogDrains(ctx)
	if err != nil {
		t.Fatalf("ListEnabledAppLogDrains: %v", err)
	}
	if len(enabled) != 1 || enabled[0].ID != created.ID {
		t.Fatalf("enabled drain list = %+v", enabled)
	}

	newKind := state.AppLogDrainKindOTLP
	newTarget := "https://logs.example/pg-updated-" + suffix
	newSecret := []byte("new-sealed")
	newEnabled := false
	updated, err := s.UpdateAppLogDrain(ctx, created.ID, state.UpdateAppLogDrainParams{
		Kind: &newKind, TargetURL: &newTarget, AuthHeaderSealed: &newSecret, Enabled: &newEnabled,
	})
	if err != nil {
		t.Fatalf("UpdateAppLogDrain: %v", err)
	}
	if updated.Kind != newKind || updated.TargetURL != newTarget || string(updated.AuthHeaderSealed) != string(newSecret) || updated.Enabled {
		t.Fatalf("updated drain = %+v", updated)
	}
	conflictTarget := other.TargetURL
	if _, err := s.UpdateAppLogDrain(ctx, created.ID, state.UpdateAppLogDrainParams{TargetURL: &conflictTarget}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate target update = %v, want ErrConflict", err)
	}
	if _, err := s.UpdateAppLogDrain(ctx, uuid.NewString(), state.UpdateAppLogDrainParams{}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("UpdateAppLogDrain missing = %v, want ErrNotFound", err)
	}

	quotaDuplicate := state.AppLogDrain{
		AppID: app.ID, AccountID: account.ID, Kind: state.AppLogDrainKindHTTPJSON,
		TargetURL: newTarget, Enabled: true,
	}
	if _, err := s.CreateAppLogDrainIfUnderQuota(ctx, quotaDuplicate, api.Limits{LogDrainPerApp: 10, LogDrainPerAccount: 10}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate quota create = %v, want ErrConflict", err)
	}
	_, err = s.CreateAppLogDrainIfUnderQuota(ctx, state.AppLogDrain{
		AppID: app.ID, AccountID: account.ID, Kind: state.AppLogDrainKindHTTPJSON,
		TargetURL: "https://logs.example/pg-three-" + suffix, Enabled: true,
	}, api.Limits{LogDrainPerApp: 10, LogDrainPerAccount: 10})
	if err != nil {
		t.Fatalf("CreateAppLogDrainIfUnderQuota success: %v", err)
	}
	_, err = s.CreateAppLogDrainIfUnderQuota(ctx, state.AppLogDrain{
		AppID: app.ID, AccountID: account.ID, Kind: state.AppLogDrainKindHTTPJSON,
		TargetURL: "https://logs.example/pg-four-" + suffix, Enabled: true,
	}, api.Limits{LogDrainPerApp: 3, LogDrainPerAccount: 10})
	var quotaErr *state.AppLogDrainQuotaError
	if !errors.As(err, &quotaErr) || quotaErr.Scope != state.AppLogDrainQuotaScopeApp || quotaErr.Observed != 3 {
		t.Fatalf("app quota error = %v, want app quota observed=3", err)
	}

	otherApp, err := s.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "pg-log-drain-other-" + suffix})
	if err != nil {
		t.Fatalf("CreateApp other: %v", err)
	}
	_, err = s.CreateAppLogDrainIfUnderQuota(ctx, state.AppLogDrain{
		AppID: otherApp.ID, AccountID: account.ID, Kind: state.AppLogDrainKindHTTPJSON,
		TargetURL: "https://logs.example/pg-five-" + suffix, Enabled: true,
	}, api.Limits{LogDrainPerApp: 10, LogDrainPerAccount: 3})
	if !errors.As(err, &quotaErr) || quotaErr.Scope != state.AppLogDrainQuotaScopeAccount || quotaErr.Observed != 3 {
		t.Fatalf("account quota error = %v, want account quota observed=3", err)
	}
	_, err = s.CreateAppLogDrainIfUnderQuota(ctx, state.AppLogDrain{
		AppID: uuid.NewString(), AccountID: account.ID, Kind: state.AppLogDrainKindHTTPJSON,
		TargetURL: "https://logs.example/pg-missing-" + suffix, Enabled: true,
	}, api.Limits{LogDrainPerApp: 10, LogDrainPerAccount: 10})
	if !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing app quota create = %v, want ErrNotFound", err)
	}

	if err := s.DeleteAppLogDrain(ctx, other.ID); err != nil {
		t.Fatalf("DeleteAppLogDrain: %v", err)
	}
	if err := s.DeleteAppLogDrain(ctx, other.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("DeleteAppLogDrain twice = %v, want ErrNotFound", err)
	}
}
