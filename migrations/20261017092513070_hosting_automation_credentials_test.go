//go:build !no_pg

package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestHostingAutomationMigrationPreservesIntentsAndRejectsDowngrade(t *testing.T) {
	// adr: 202610170925 — catalog cleanup cannot erase the retry ledger; downgrade cannot silently forget it.
	pool := pgtest.Open(t)
	ctx := t.Context()
	raw, err := migrations.FS.ReadFile("20261017092513070_hosting_automation_credentials.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	if _, err = pool.Exec(ctx, `CREATE TABLE accounts(id uuid PRIMARY KEY);CREATE TABLE hosting_sites(id uuid PRIMARY KEY);
 INSERT INTO accounts VALUES('11111111-1111-4111-8111-111111111111');INSERT INTO hosting_sites SELECT id FROM accounts;`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO hosting_automation_credentials(id,account_id,site_id,token_hash,label,operations,expires_at) SELECT id,id,id,decode(repeat('aa',32),'hex'),'CI',ARRAY['stage','preview'],clock_timestamp()+interval '1 hour' FROM accounts`
	if _, err = pool.Exec(ctx, insert); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`UPDATE hosting_automation_credentials SET operations=ARRAY['promote']`, `UPDATE hosting_automation_credentials SET token_hash=decode('aa','hex')`, `UPDATE hosting_automation_credentials SET expires_at='infinity'`, `UPDATE hosting_automation_credentials SET label=''`} {
		if _, err = pool.Exec(ctx, bad); err == nil {
			t.Fatal("constraint missing", bad)
		}
	}
	if _, err = pool.Exec(ctx, parts[1]); err == nil {
		t.Fatal("credential lost on downgrade")
	}
	if _, err = pool.Exec(ctx, `DELETE FROM hosting_automation_credentials;
 INSERT INTO hosting_ci_runs(site_id,id,release_id,preview_id,fingerprint,preview_expires_at) SELECT id,id,id,id,repeat('a',64),clock_timestamp()+interval '1 hour' FROM hosting_sites`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[1]); err == nil {
		t.Fatal("retry identity lost on downgrade")
	}
	if _, err = pool.Exec(ctx, `DELETE FROM hosting_sites`); err != nil {
		t.Fatal("ledger blocked final site purge", err)
	}
	if _, err = pool.Exec(ctx, parts[1]); err != nil {
		t.Fatal("drained downgrade", err)
	}
}
