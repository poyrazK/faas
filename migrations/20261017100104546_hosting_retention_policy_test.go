//go:build !no_pg

package migrations_test

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestHostingRetentionMigrationGivesExistingReleasesFreshIdleWindow(t *testing.T) {
	// adr: 202610171001 — rollout must not interpret all old uploads as immediately abandoned.
	pool := pgtest.Open(t)
	ctx := t.Context()
	raw, err := migrations.FS.ReadFile("20261017100104546_hosting_retention_policy.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	if _, err = pool.Exec(ctx, `CREATE TABLE hosting_releases(id uuid PRIMARY KEY);CREATE TABLE hosting_sites(id uuid PRIMARY KEY);
 INSERT INTO hosting_sites VALUES('11111111-1111-4111-8111-111111111111');INSERT INTO hosting_releases SELECT id FROM hosting_sites;`); err != nil {
		t.Fatal(err)
	}
	before := time.Now().Add(-time.Second)
	if _, err = pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	var at time.Time
	if err = pool.QueryRow(ctx, `SELECT last_activity_at FROM hosting_releases`).Scan(&at); err != nil || at.Before(before) {
		t.Fatal("backfill erased idle window", at, err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO hosting_retention_policies(site_id,revision,enabled,pending_age_seconds,staged_age_seconds) SELECT id,1,true,86400,86400 FROM hosting_sites`); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{`UPDATE hosting_retention_policies SET pending_age_seconds=1`, `UPDATE hosting_retention_policies SET revision=0`, `UPDATE hosting_releases SET last_activity_at='infinity'`} {
		if _, err = pool.Exec(ctx, invalid); err == nil {
			t.Fatal("constraint missing", invalid)
		}
	}
	if _, err = pool.Exec(ctx, parts[1]); err == nil {
		t.Fatal("active policy lost on downgrade")
	}
	if _, err = pool.Exec(ctx, `UPDATE hosting_retention_policies SET enabled=false`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[1]); err != nil {
		t.Fatal("disabled downgrade", err)
	}
}
