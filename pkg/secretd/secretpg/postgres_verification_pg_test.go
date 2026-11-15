//go:build !no_pg

package secretpg_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/secretd"
	"github.com/onebox-faas/faas/pkg/secretd/secretpg"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresVerificationStoreFencesLeaseAndTarget(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	schema, err := os.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	// Use the actual audit migration, alongside the existing SQL projections.
	projection, _, ok := strings.Cut(string(schema), "CREATE TABLE secret_verification_audit (")
	if !ok {
		t.Fatal("missing audit projection")
	}
	if _, err := pool.Exec(ctx, projection); err != nil {
		t.Fatal(err)
	}
	audit, err := migrations.FS.ReadFile("20261114190000002_secret_verification_audit.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(audit), "-- +goose Down")
	if !ok {
		t.Fatal("missing audit rollback")
	}
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	account, app, target, cutover, member := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	_, err = pool.Exec(ctx, `INSERT INTO managed_postgres_databases(id,account_id,state,backend_id,backend_fingerprint,provider_resource_id,desired_generation,postgres_major) VALUES($1,$2,'ready','backend','fingerprint','resource',1,17)`, target, account)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO managed_postgres_cutovers(id,account_id,app_id,target_database_id,target_backend_id,target_backend_fingerprint,target_resource_id,target_generation,state,lease_token,lease_until) VALUES($1,$2,$3,$4,'backend','fingerprint','resource',1,'verifying','lease',now()+interval '1 hour')`, cutover, account, app, target)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO managed_postgres_cutover_credentials(id,cutover_id,environment_key,access,state,provider_identity_id,credential_ref,ciphertext,kid,value_hash) VALUES($1,$2,'DATABASE_URL','read_write','sealed','role','ref','cipher','kid','hash')`, member, cutover)
	if err != nil {
		t.Fatal(err)
	}
	store := secretpg.New(pool, state.NewPgStore(pool))
	get := func(a, c, m, l string) error { _, err := store.PostgresCutoverCredential(ctx, a, c, m, l); return err }
	if value, err := store.PostgresCutoverCredential(ctx, account, cutover, member, "lease"); err != nil || value.AppID != app || value.Major != 17 || value.Kid != "kid" {
		t.Fatalf("valid staged read: %+v %v", value, err)
	}
	for _, ids := range [][4]string{{uuid.NewString(), cutover, member, "lease"}, {account, cutover, uuid.NewString(), "lease"}, {account, cutover, member, "old"}} {
		if err := get(ids[0], ids[1], ids[2], ids[3]); !errors.Is(err, secretd.ErrUnknownPostgresCredential) {
			t.Fatal("foreign/stale request passed", err)
		}
	}
	for _, tc := range []struct{ name, change, restore string }{
		{"cancelled", "UPDATE managed_postgres_cutovers SET state='cancelling'", "UPDATE managed_postgres_cutovers SET state='verifying'"},
		{"lease_expired", "UPDATE managed_postgres_cutovers SET lease_until=now()-interval '1 second'", "UPDATE managed_postgres_cutovers SET lease_until=now()+interval '1 hour'"},
		{"revoked", "UPDATE managed_postgres_cutover_credentials SET state='revoked'", "UPDATE managed_postgres_cutover_credentials SET state='sealed'"},
		{"target_state", "UPDATE managed_postgres_databases SET state='updating'", "UPDATE managed_postgres_databases SET state='ready'"},
		{"target_generation", "UPDATE managed_postgres_databases SET desired_generation=2", "UPDATE managed_postgres_databases SET desired_generation=1"},
		{"target_backend", "UPDATE managed_postgres_databases SET backend_id='other'", "UPDATE managed_postgres_databases SET backend_id='backend'"},
		{"target_fingerprint", "UPDATE managed_postgres_databases SET backend_fingerprint='other'", "UPDATE managed_postgres_databases SET backend_fingerprint='fingerprint'"},
		{"target_resource", "UPDATE managed_postgres_databases SET provider_resource_id='other'", "UPDATE managed_postgres_databases SET provider_resource_id='resource'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, tc.change); err != nil {
				t.Fatal(err)
			}
			if err := get(account, cutover, member, "lease"); !errors.Is(err, secretd.ErrUnknownPostgresCredential) {
				t.Errorf("changed target/lease admitted: %v", err)
			}
			if _, err := pool.Exec(ctx, tc.restore); err != nil {
				t.Fatal(err)
			}
		})
	}
	if err := store.RecordPostgresVerification(ctx, account, app, cutover, member); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM secret_verification_audit WHERE cutover_id=$1`, cutover).Scan(&count); err != nil || count != 1 {
		t.Fatalf("audit count=%d err=%v", count, err)
	}
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresVerificationQueriesOnMigratedSchema(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := secretpg.New(pool, state.NewPgStore(pool))
	account, app, cutover, member := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := store.PostgresCutoverCredential(t.Context(), account, cutover, member, "absent"); !errors.Is(err, secretd.ErrUnknownPostgresCredential) {
		t.Fatalf("query against production schema: %v", err)
	}
	if err := store.RecordPostgresVerification(t.Context(), account, app, cutover, member); err != nil {
		t.Fatal("audit against production schema", err)
	}
}
