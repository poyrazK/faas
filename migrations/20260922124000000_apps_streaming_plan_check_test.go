//go:build !no_pg

// Migration-apply coverage for the ADR-102 streaming entitlement invariant.
// The database must reject both directions that could create a Free account
// with streaming enabled: writing the app flag and downgrading its account.
package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_AppsStreamingPlanCheck(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	const (
		freeAccount = "00000000-0000-0000-0000-000001240000"
		paidAccount = "00000000-0000-0000-0000-000001240001"
		freeApp     = "00000000-0000-0000-0000-000001240010"
		paidApp     = "00000000-0000-0000-0000-000001240011"
	)
	for _, account := range []struct {
		id    string
		email string
		plan  string
	}{
		{freeAccount, "streaming-plan-free@example.com", "free"},
		{paidAccount, "streaming-plan-paid@example.com", "hobby"},
	} {
		if _, err := pool.Exec(ctx, `
			insert into accounts (id, email, plan, created_at)
			values ($1::uuid, $2, $3, now())
			on conflict (id) do update set plan = excluded.plan
		`, account.id, account.email, account.plan); err != nil {
			t.Fatalf("seed account %s: %v", account.plan, err)
		}
	}

	for _, app := range []struct {
		id        string
		accountID string
		slug      string
	}{
		{freeApp, freeAccount, "streaming-plan-free-app"},
		{paidApp, paidAccount, "streaming-plan-paid-app"},
	} {
		if _, err := pool.Exec(ctx, `
			insert into apps (id, account_id, slug, type, ram_mb, max_concurrency,
			                  idle_timeout_s, status, created_at)
			values ($1::uuid, $2::uuid, $3, 'function', 256, 1, 30, 'active', now())
			on conflict (id) do nothing
		`, app.id, app.accountID, app.slug); err != nil {
			t.Fatalf("seed app %s: %v", app.slug, err)
		}
	}

	// The default remains valid for Free apps; the entitlement check is only
	// about the opt-in bit.
	if _, err := pool.Exec(ctx, `update apps set streaming_enabled = true where id = $1::uuid`, paidApp); err != nil {
		t.Fatalf("enable streaming for hobby app: %v", err)
	}

	if _, err := pool.Exec(ctx, `update apps set streaming_enabled = true where id = $1::uuid`, freeApp); err == nil {
		t.Fatal("enabling streaming for a Free app succeeded; want constraint violation")
	}

	if _, err := pool.Exec(ctx, `update accounts set plan = 'free' where id = $1::uuid`, paidAccount); err == nil {
		t.Fatal("downgrading an account with a streaming app succeeded; want constraint violation")
	}

	// Once the app is disabled, the downgrade is valid and the original
	// streaming-enabled row remains usable after an upgrade.
	if _, err := pool.Exec(ctx, `update apps set streaming_enabled = false where id = $1::uuid`, paidApp); err != nil {
		t.Fatalf("disable streaming before downgrade: %v", err)
	}
	if _, err := pool.Exec(ctx, `update accounts set plan = 'free' where id = $1::uuid`, paidAccount); err != nil {
		t.Fatalf("downgrade after disabling streaming: %v", err)
	}
	if _, err := pool.Exec(ctx, `update accounts set plan = 'hobby' where id = $1::uuid`, paidAccount); err != nil {
		t.Fatalf("upgrade account: %v", err)
	}
	if _, err := pool.Exec(ctx, `update apps set streaming_enabled = true where id = $1::uuid`, paidApp); err != nil {
		t.Fatalf("re-enable streaming after upgrade: %v", err)
	}

	// Constraint metadata is part of the migration contract: it must be a
	// validated CHECK, not merely an application trigger.
	var validated bool
	if err := pool.QueryRow(ctx, `
		select convalidated
		  from pg_constraint
		 where conrelid = 'apps'::regclass
		   and conname = 'apps_streaming_enabled_plan_check'
	`).Scan(&validated); err != nil {
		t.Fatalf("read streaming constraint: %v", err)
	}
	if !validated {
		t.Fatal("apps_streaming_enabled_plan_check is not validated")
	}

	// Re-applying the migration must tolerate all of its existing functions,
	// trigger, and constraint objects.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay-safety: second MigrateUp failed: %v", err)
	}
}
