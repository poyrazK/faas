package state_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Exercise an actual COMMIT and a second batch, rather than only the empty
// database migration path. Every object lives in pgtest's disposable schema.
func TestPgGitHubPreviewAccountBackfillCommitsBatches(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, "preview-backfill@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	root, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "preview-backfill", RAMMB: 128, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into pr_preview_sets(installation_id,repo_full_name,pr_number,commit_sha,root_app_id,member_app_ids)
 select 42,'octo/backfill',n,repeat('a',40),$1::uuid,array[($1::uuid)::text] from generate_series(1,1001) n`, root.ID); err != nil {
		t.Fatal(err)
	}
	// Restore the old nullable-column state before the batched data migration.
	if _, err := pool.Exec(ctx, `alter table pr_preview_sets drop constraint pr_preview_sets_pkey;
 alter table pr_preview_sets drop constraint pr_preview_sets_account_not_null;
 alter table pr_preview_sets alter column account_id drop not null;
 alter table pr_preview_sets add primary key(installation_id,repo_full_name,pr_number);
 update pr_preview_sets set account_id=null;`); err != nil {
		t.Fatal(err)
	}
	source, err := migrations.FS.ReadFile("20261106170000001_github_preview_accounts_backfill.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(source)
	start := strings.Index(sql, "DO $backfill$")
	end := strings.Index(sql, "-- +goose StatementEnd")
	if start < 0 || end <= start {
		t.Fatal("backfill statement missing")
	}
	if _, err := pool.Exec(ctx, sql[start:end]); err != nil {
		t.Fatalf("batched migration: %v", err)
	}
	var matched, total int
	if err := pool.QueryRow(ctx, `select count(*) filter(where account_id=$1),count(*) from pr_preview_sets`, account.ID).Scan(&matched, &total); err != nil {
		t.Fatal(err)
	}
	if total != 1001 || matched != total {
		t.Fatalf("backfill lost or misattributed rows: %d/%d", matched, total)
	}
}
