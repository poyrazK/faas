//go:build !no_pg

package state_test

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgPlatformTenantInvocationLifecycle(t *testing.T) {
	store, _, ctx := tenantInvocationPGStore(t)
	testPlatformTenantInvocationLifecycle(t, store, ctx)
}

func TestPgPlatformTenantInvocationIdentityImmutable(t *testing.T) {
	store, pool, ctx := tenantInvocationPGStore(t)
	account, err := store.CreateAccount(ctx, "immutable-"+uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "immutable-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	accountID, appID := account.ID, app.ID
	tenant, _, err := store.CreatePlatformTenant(ctx, accountID, "immutable", "Immutable", 250)
	if err != nil {
		t.Fatal(err)
	}
	inv, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: appID, AccountID: accountID, PlatformTenantID: tenant.ID, Source: state.InvocationAsyncInvoke, Method: "POST", Path: "/", DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	for _, replacement := range []any{nil, uuid.NewString()} {
		if _, err := pool.Exec(ctx, `UPDATE invocations SET platform_tenant_id = $2 WHERE id = $1`, inv.ID, replacement); err == nil {
			t.Fatal("database allowed tenant identity mutation")
		}
	}
	read, err := store.InvocationByID(ctx, inv.ID)
	if err != nil || read.PlatformTenantID != tenant.ID {
		t.Fatalf("persisted tenant: %+v %v", read, err)
	}
}

// Kept self-contained so this focused acceptance can run without compiling
// every unrelated state test on development machines with limited disk.
func tenantInvocationPGStore(t *testing.T) (*state.PgStore, *pgxpool.Pool, context.Context) {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return state.NewPgStore(pool), pool, ctx
}
