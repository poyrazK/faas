//go:build !no_pg

package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestHostingOIDCMigrationPreservesManualKeysAndCascadesSessions(t *testing.T) {
	// adr: 202610171028 — additive credential marker and composite trust FK prevent cross-site sessions.
	pool := pgtest.Open(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE TABLE accounts(id uuid PRIMARY KEY);CREATE TABLE hosting_sites(id uuid PRIMARY KEY);
 INSERT INTO accounts VALUES('11111111-1111-4111-8111-111111111111');INSERT INTO hosting_sites VALUES('22222222-2222-4222-8222-222222222222'),('33333333-3333-4333-8333-333333333333');`); err != nil {
		t.Fatal(err)
	}
	old, err := migrations.FS.ReadFile("20261017092513070_hosting_automation_credentials.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, strings.SplitN(string(old), "-- +goose Down", 2)[0]); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO hosting_automation_credentials(id,account_id,site_id,token_hash,label,operations,expires_at) VALUES('44444444-4444-4444-8444-444444444444','11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222',decode(repeat('aa',32),'hex'),'manual',ARRAY['stage'],clock_timestamp()+interval '1 hour');`); err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261017102853765_hosting_oidc_trusts.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	if _, err = pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO hosting_oidc_trusts(id,account_id,site_id,repository_owner_id,repository_id,repository,ref,workflow_ref,event_name) VALUES('55555555-5555-4555-8555-555555555555','11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222','123','456','org/site','refs/heads/main','org/site/.github/workflows/preview.yml@refs/heads/main','push');`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE hosting_automation_credentials SET site_id='33333333-3333-4333-8333-333333333333',oidc_trust_id='55555555-5555-4555-8555-555555555555'`); err == nil {
		t.Fatal("foreign site credential accepted")
	}
	if _, err = pool.Exec(ctx, `INSERT INTO hosting_automation_credentials SELECT '66666666-6666-4666-8666-666666666666',account_id,site_id,decode(repeat('bb',32),'hex'),'GitHub OIDC',operations,created_at,expires_at,'55555555-5555-4555-8555-555555555555' FROM hosting_automation_credentials;`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[1]); err == nil {
		t.Fatal("downgrade discarded live trust")
	}
	if _, err = pool.Exec(ctx, `DELETE FROM hosting_oidc_trusts`); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM hosting_automation_credentials WHERE id='44444444-4444-4444-8444-444444444444'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("manual key lost", count, err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM hosting_automation_credentials WHERE oidc_trust_id IS NOT NULL`).Scan(&count); err != nil || count != 0 {
		t.Fatal("session survived revoke", count, err)
	}
	if _, err = pool.Exec(ctx, parts[1]); err != nil {
		t.Fatal("empty downgrade", err)
	}
}
