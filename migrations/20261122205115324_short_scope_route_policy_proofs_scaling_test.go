//go:build !no_pg

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// A deployment in a one- or two-character environment must be able to go
// live: every scope column accepts what deployments_scope_shape accepts.
func TestMigrations_20261122205115324ShortScopes(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	for _, name := range []string{
		"deployments_scope_shape",
		"deployment_route_policy_snapshots_scope_v2_check",
		"runtime_instance_config_proofs_scope_v2_check",
		"runtime_environment_scaling_states_scope_v2_check",
	} {
		var def string
		if err := pool.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = $1`, name).Scan(&def); err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(def, "{1,38}") || !strings.Contains(def, "{0,38}") {
			t.Errorf("%s still refuses short scopes: %s", name, def)
		}
	}
	var strict []string
	rows, err := pool.Query(ctx, `SELECT conrelid::regclass::text || '.' || conname FROM pg_constraint
		WHERE contype = 'c' AND conname ~ 'scope' AND pg_get_constraintdef(oid) ~ '\{1,38\}'`)
	if err != nil {
		t.Fatalf("list scope constraints: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		strict = append(strict, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(strict) > 0 {
		t.Errorf("scope constraints still require three characters: %v", strict)
	}
}
