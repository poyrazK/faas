//go:build !no_pg

package migrations_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestBindingPromotionRevisionTriggersWatchExistingColumns(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	defer pool.Close()
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `SELECT c.relname,t.tgargs FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema() AND t.tgname='binding_promotion_revision' ORDER BY c.relname`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	args := map[string][]string{}
	for rows.Next() {
		var table string
		var raw []byte
		if err := rows.Scan(&table, &raw); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
		args[table] = strings.Split(strings.TrimRight(string(raw), "\x00"), ",")
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(tables) != 23 {
		t.Fatalf("revision triggers=%d: %v", len(tables), tables)
	}
	for _, table := range tables {
		for _, column := range args[table] {
			var exists bool
			if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1 AND column_name=$2)`, table, column).Scan(&exists); err != nil || !exists {
				t.Errorf("trigger watches absent column %s.%s: %v", table, column, err)
			}
		}
	}
}

func TestBindingPromotionRevisionMutationRollbackAndMigrationRoundTrip(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	defer pool.Close()
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	acct := seedAccount(t, ctx, pool)
	app, other := seedApp(t, ctx, pool, acct), seedApp(t, ctx, pool, acct)
	revision := func(id string) string {
		t.Helper()
		var revision string
		if err := pool.QueryRow(ctx, `SELECT epoch::text||':'||revision::text FROM app_binding_promotion_revisions WHERE app_id=$1`, id).Scan(&revision); err != nil {
			t.Fatal(err)
		}
		return revision
	}
	before, unrelated := revision(app), revision(other)
	if _, err := pool.Exec(ctx, `INSERT INTO app_envs(account_id,app_id,key,value) VALUES($1,$2,'FEATURE','first')`, acct, app); err != nil {
		t.Fatal(err)
	}
	if revision(app) == before || revision(other) != unrelated {
		t.Fatal("env mutation did not invalidate only its app")
	}
	before = revision(app)
	if _, err := pool.Exec(ctx, `UPDATE app_envs SET updated_at=now(),value=value WHERE app_id=$1`, app); err != nil {
		t.Fatal(err)
	}
	if revision(app) != before {
		t.Fatal("irrelevant timestamp invalidated observations")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE app_envs SET value='rolled-back' WHERE app_id=$1`, app); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if revision(app) != before {
		t.Fatal("rolled-back config changed revision")
	}
	if _, err := pool.Exec(ctx, `UPDATE accounts SET plan='hobby' WHERE id=$1`, acct); err != nil {
		t.Fatal(err)
	}
	if revision(app) == before || revision(other) == unrelated {
		t.Fatal("account policy did not invalidate bound apps")
	}
	before = revision(app)
	dependency, err := os.ReadFile("20261004190339147_service_binding_dependency_revision.sql")
	if err != nil {
		t.Fatal(err)
	}
	dependencyUp, dependencyDown, ok := strings.Cut(string(dependency), "-- +goose Down")
	if !ok {
		t.Fatal("missing service dependency rollback")
	}
	if _, err := pool.Exec(ctx, dependencyDown); err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile("20261002175413928_binding_promotion_revision.sql")
	if err != nil {
		t.Fatal(err)
	}
	adoption, err := os.ReadFile("20261002212247902_binding_application_adoption.sql")
	if err != nil {
		t.Fatal(err)
	}
	processGenerations, err := os.ReadFile("20261003113828429_runtime_secret_process_generations.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, processGenerationsDown, ok := strings.Cut(string(processGenerations), "-- +goose Down")
	if !ok {
		t.Fatal("missing runtime secret process generations rollback")
	}
	if _, err := pool.Exec(ctx, processGenerationsDown); err != nil {
		t.Fatal(err)
	}
	adoptionUp, adoptionDown, ok := strings.Cut(string(adoption), "-- +goose Down")
	if !ok {
		t.Fatal("missing adoption rollback")
	}
	if _, err := pool.Exec(ctx, adoptionDown); err != nil {
		t.Fatal(err)
	}
	latest, err := os.ReadFile("20261002193101619_outbound_binding_verification.sql")
	if err != nil {
		t.Fatal(err)
	}
	latestUp, latestDown, ok := strings.Cut(string(latest), "-- +goose Down")
	if !ok {
		t.Fatal("missing outbound rollback")
	}
	if _, err := pool.Exec(ctx, latestDown); err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(source), "-- +goose Down")
	if !ok {
		t.Fatal("missing rollback")
	}
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	var value string
	if err := pool.QueryRow(ctx, `SELECT value FROM app_envs WHERE app_id=$1 AND key='FEATURE'`, app).Scan(&value); err != nil || value != "first" {
		t.Fatalf("rollback destroyed config: %q %v", value, err)
	}
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	if revision(app) == before {
		t.Fatal("migration reinstallation reused a prior revision token")
	}
	if _, err := pool.Exec(ctx, latestUp); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, adoptionUp); err != nil {
		t.Fatal(err)
	}
	processGenerationsUp, _, ok := strings.Cut(string(processGenerations), "-- +goose Down")
	if !ok {
		t.Fatal("missing runtime secret process generations migration")
	}
	if _, err := pool.Exec(ctx, processGenerationsUp); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, dependencyUp); err != nil {
		t.Fatal(err)
	}
}
