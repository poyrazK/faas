package state

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func realtimeFixture(t *testing.T) (*MemStore, context.Context, Account, App) {
	t.Helper()
	ctx := context.Background()
	m := NewMemStore()
	acct, err := m.CreateAccount(ctx, "realtime-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, App{AccountID: acct.ID, Slug: "realtime-" + uuid.NewString(), Status: AppActive, RAMMB: 512})
	if err != nil {
		t.Fatal(err)
	}
	return m, ctx, acct, app
}

func TestMemStoreManagedRealtimeEndpointLifecycle(t *testing.T) {
	m, ctx, acct, app := realtimeFixture(t)
	created, err := m.CreateManagedRealtimeEndpointIfUnderQuota(ctx, ManagedRealtimeEndpoint{
		AccountID: acct.ID, AppID: app.ID, CallbackURL: "https://example.com",
		ConnectPath: "/connect", MessagePath: "/message", DisconnectPath: "/disconnect",
		CallbackAuthTokenSealed: []byte("callback"), AuthTokenSealed: []byte("client"), Enabled: true,
	}, 2, 10)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := m.ManagedRealtimeEndpointByID(ctx, created.ID)
	if err != nil || got.CallbackURL != created.CallbackURL {
		t.Fatalf("read: got=%+v err=%v", got, err)
	}
	enabled := false
	updated, err := m.UpdateManagedRealtimeEndpoint(ctx, created.ID, UpdateManagedRealtimeEndpointParams{Enabled: &enabled})
	if err != nil || updated.Enabled {
		t.Fatalf("update: got=%+v err=%v", updated, err)
	}
	if err := m.DeleteManagedRealtimeEndpoint(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := m.ManagedRealtimeEndpointByID(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("read after delete = %v, want ErrNotFound", err)
	}
}

func TestMemStoreManagedRealtimeEndpointQuotaCountsDisabledRows(t *testing.T) {
	m, ctx, acct, app := realtimeFixture(t)
	first, err := m.CreateManagedRealtimeEndpointIfUnderQuota(ctx, ManagedRealtimeEndpoint{AccountID: acct.ID, AppID: app.ID, Enabled: false}, 1, 10)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err = m.CreateManagedRealtimeEndpointIfUnderQuota(ctx, ManagedRealtimeEndpoint{AccountID: acct.ID, AppID: app.ID, Enabled: true}, 1, 10)
	var quota *ManagedRealtimeEndpointQuotaError
	if !errors.As(err, &quota) || quota.Scope != ManagedRealtimeEndpointQuotaScopeApp {
		t.Fatalf("second create err=%v, want app quota", err)
	}
	if err := m.DeleteManagedRealtimeEndpoint(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
}
