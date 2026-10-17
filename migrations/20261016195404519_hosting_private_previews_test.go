//go:build !no_pg

package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestHostingPreviewMigrationFencesAndDowngrade(t *testing.T) {
	// adr: 202610161954 — preserve references and queue a fresh canonical permission head.
	pool := pgtest.Open(t)
	ctx := t.Context()
	raw, err := migrations.FS.ReadFile("20261016195404519_hosting_private_previews.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	if _, err = pool.Exec(ctx, `CREATE TABLE accounts(id uuid PRIMARY KEY); CREATE TABLE hosting_sites(id uuid PRIMARY KEY); CREATE TABLE hosting_releases(id uuid PRIMARY KEY);
 CREATE TABLE hosting_route_publications(generation bigint,next_attempt_at timestamptz,pending_since timestamptz,last_attempt_at timestamptz,retry_count integer,last_error_code text);
 INSERT INTO hosting_route_publications VALUES(5,now()+interval '6 hours',NULL,NULL,3,'publication_unavailable');
 INSERT INTO accounts VALUES('11111111-1111-4111-8111-111111111111'); INSERT INTO hosting_sites SELECT id FROM accounts; INSERT INTO hosting_releases SELECT id FROM accounts;`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	var generation int
	var pending bool
	if err = pool.QueryRow(ctx, `SELECT generation,pending_since IS NOT NULL AND retry_count=0 AND last_error_code='' FROM hosting_route_publications`).Scan(&generation, &pending); err != nil || generation != 6 || !pending {
		t.Fatal("permission repair not queued", generation, pending, err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO hosting_previews(id,account_id,site_id,release_id,hostname,origin_id,expires_at) SELECT id,id,id,id,'p-'||id::text||'.hosting.example',repeat('a',64),now()+interval '1 hour' FROM accounts`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[1]); err == nil {
		t.Fatal("downgrade dropped referenced preview")
	}
	for _, query := range []string{`DELETE FROM hosting_releases`, `DELETE FROM hosting_sites`, `UPDATE hosting_previews SET generation=2`, `UPDATE hosting_previews SET published_generation=1`, `UPDATE hosting_previews SET last_error_code='secret provider response'`} {
		if _, err = pool.Exec(ctx, query); err == nil {
			t.Fatal("missing constraint", query)
		}
	}
	if _, err = pool.Exec(ctx, `DELETE FROM hosting_previews`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[1]); err != nil {
		t.Fatal("drained downgrade", err)
	}
}
