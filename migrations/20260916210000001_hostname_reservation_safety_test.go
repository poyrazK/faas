//go:build !no_pg

package migrations_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestHostnameReservationSafetyMigration(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	accountID := seedAccount(t, ctx, pool)
	reserved := []string{
		"account", "admin", "api", "assets", "billing", "cdn", "console",
		"dashboard", "docs", "help", "login", "logout", "mail", "ns",
		"operations", "security", "signup", "static", "status", "support", "www",
	}
	for _, slug := range reserved {
		_, err := pool.Exec(ctx, `
			insert into apps (account_id,slug,type,ram_mb,max_concurrency,idle_timeout_s,status,created_at)
			values ($1,$2,'function',128,1,30,'active',now())`, accountID, slug)
		if err == nil {
			t.Errorf("reserved slug %q was inserted directly", slug)
			continue
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" || !strings.Contains(pgErr.Message, "reserved") {
			t.Errorf("reserved slug %q error = %v, want reserved check violation", slug, err)
		}
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (account_id,slug,type,ram_mb,max_concurrency,idle_timeout_s,status,created_at)
		values ($1,'status-page','function',128,1,30,'active',now())`, accountID); err != nil {
		t.Fatalf("near-miss slug rejected: %v", err)
	}

	// The migration policy keeps a hypothetical legacy collision operable long
	// enough to rename it away; it only blocks fresh allocation or a rename to
	// a reserved value.
	if _, err := pool.Exec(ctx, `alter table apps disable trigger apps_reserved_slug_allocation_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (account_id,slug,type,ram_mb,max_concurrency,idle_timeout_s,status,created_at)
		values ($1,'status','function',128,1,30,'active',now())`, accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `alter table apps enable trigger apps_reserved_slug_allocation_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update apps set slug=slug where slug='status'`); err != nil {
		t.Fatalf("legacy reserved row cannot remain operable: %v", err)
	}
	if _, err := pool.Exec(ctx, `update apps set slug='status-migrated' where slug='status'`); err != nil {
		t.Fatalf("legacy reserved row cannot be renamed away: %v", err)
	}
}
