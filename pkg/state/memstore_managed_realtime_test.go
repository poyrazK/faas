package state

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

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

func TestMemStoreManagedRealtimeEndpointListAllIncludesDisabled(t *testing.T) {
	m, ctx, acct, app := realtimeFixture(t)
	first, err := m.CreateManagedRealtimeEndpointIfUnderQuota(ctx, ManagedRealtimeEndpoint{
		AccountID: acct.ID, AppID: app.ID, CallbackURL: "https://example.com/first", Enabled: true,
	}, 10, 10)
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := m.CreateManagedRealtimeEndpointIfUnderQuota(ctx, ManagedRealtimeEndpoint{
		AccountID: acct.ID, AppID: app.ID, CallbackURL: "https://example.com/second", Enabled: false,
	}, 10, 10)
	if err != nil {
		t.Fatalf("create second: %v", err)
	}

	rows, err := m.ListManagedRealtimeEndpoints(ctx)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("list all returned %d rows, want 2", len(rows))
	}
	seen := map[string]bool{}
	for _, row := range rows {
		seen[row.ID] = true
	}
	if !seen[first.ID] || !seen[second.ID] {
		t.Fatalf("list all ids = %v, want %s and %s", seen, first.ID, second.ID)
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

func TestMemStoreManagedRealtimeDrainWorkerClaimRetryAndFinish(t *testing.T) {
	m, ctx, acct, app := realtimeFixture(t)
	op, err := m.CreateManagedRealtimeDrainOperation(ctx, ManagedRealtimeDrainOperationInput{
		AccountID: acct.ID, AppID: app.ID, EndpointID: "endpoint-1", Reason: "deploy",
		Matched: 2, ConnectionIDs: []string{"conn-a", "conn-b"}, Limit: 10,
	})
	if err != nil {
		t.Fatalf("create operation: %v", err)
	}
	claims, err := m.ClaimManagedRealtimeDrainOperations(ctx, 1, time.Minute)
	if err != nil || len(claims) != 1 || claims[0].Operation.Attempts != 1 {
		t.Fatalf("claim = %+v, %v", claims, err)
	}
	result, _ := json.Marshal(map[string]any{"operation_id": op.ID, "status": "running"})
	if err := m.RetryManagedRealtimeDrainOperation(ctx, op.ID, claims[0].ClaimToken, []string{"conn-b"}, result, 1, 0, 0, time.Now().UTC().Add(-time.Second), "owner offline"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	claims, err = m.ClaimManagedRealtimeDrainOperations(ctx, 1, time.Minute)
	if err != nil || len(claims) != 1 || len(claims[0].Operation.ConnectionIDs) != 1 || claims[0].Operation.ConnectionIDs[0] != "conn-b" {
		t.Fatalf("reclaim = %+v, %v", claims, err)
	}
	result, _ = json.Marshal(map[string]any{"operation_id": op.ID, "status": "completed"})
	if err := m.FinishManagedRealtimeDrainOperation(ctx, op.ID, claims[0].ClaimToken, ManagedRealtimeDrainOperationCompleted, result, 2, 0, 0); err != nil {
		t.Fatalf("finish: %v", err)
	}
	got, err := m.GetManagedRealtimeDrainOperation(ctx, acct.ID, "endpoint-1", op.ID)
	if err != nil || got.Status != ManagedRealtimeDrainOperationCompleted || len(got.ConnectionIDs) != 0 || got.Closed != 2 {
		t.Fatalf("finished operation = %+v, %v", got, err)
	}
}
