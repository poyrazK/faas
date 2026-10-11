// adr: 650 — schema isolation, RLS and backend qualification for Data API bindings.
package neon

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestDataAPICapabilityRequiresExplicitBackendOptIn(t *testing.T) {
	config := testBackend()
	p, err := New(config, func(string) string { return "test-only" })
	if err != nil {
		t.Fatal(err)
	}
	if p.Capabilities().SupportsCredentialAccess(managedpostgres.CredentialDataAPI) == nil {
		t.Fatal("data API advertised by default")
	}
	config.DataAPIEnabled = true
	p, err = New(config, func(string) string { return "test-only" })
	if err != nil || p.Capabilities().SupportsCredentialAccess(managedpostgres.CredentialDataAPI) != nil {
		t.Fatal("explicit opt-in rejected", err)
	}
}

func TestSQLDataAPICredentialsRestrictSchemaAndEnforceRLS(t *testing.T) {
	f := newCredentialFixture(t)
	ctx := context.Background()
	role := f.runtime
	role.name = strings.Replace(role.name, "gregale_rt_", "gregale_api_", 1)
	role.marker = strings.TrimSuffix(role.marker, ":read_write") + ":data_api"
	role.access = managedpostgres.CredentialDataAPI
	f.extraRoles = append(f.extraRoles, role.name)
	if err := f.manager.Ensure(ctx, f.material, role); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Ensure(ctx, f.material, role); err != nil {
		t.Fatalf("idempotent issuance: %v", err)
	}
	if err := f.manager.Ensure(ctx, f.material, f.migration); err != nil {
		t.Fatal(err)
	}
	owner := f.connect(t, f.migration.name)
	probe := f.connect(t, role.name)
	if err := verifyDataAPIProbe(ctx, probe, owner, role.schemaOwner, "live_contract"); err != nil {
		t.Fatalf("qualification SQL contract: %v", err)
	}
	_ = probe.Close(ctx)
	_, err := owner.Exec(ctx, `CREATE TABLE public.private_notes(id int);
 CREATE TABLE api.notes(id int GENERATED ALWAYS AS IDENTITY PRIMARY KEY, subject text NOT NULL, body text NOT NULL);
 ALTER TABLE api.notes ENABLE ROW LEVEL SECURITY;
 CREATE POLICY own_notes ON api.notes USING(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub') WITH CHECK(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub');`)
	if err != nil {
		t.Fatal(err)
	}
	conn := f.connect(t, role.name)
	if !sqlPermissionDenied(ctx, conn, "SELECT * FROM public.private_notes") {
		t.Fatal("data API can access private public-schema data")
	}
	if !sqlPermissionDenied(ctx, conn, "CREATE TABLE api.forbidden(id int)") {
		t.Fatal("data API can perform DDL")
	}
	if !sqlPermissionDenied(ctx, conn, "SET ROLE "+roleIdentifier(role.schemaOwner)) {
		t.Fatal("data API can become migration owner")
	}
	if _, err = conn.Exec(ctx, `SELECT set_config('request.jwt.claims','{"sub":"alice"}',false); INSERT INTO api.notes(subject,body) VALUES ('alice','hello')`); err != nil {
		t.Fatal(err)
	}
	if !sqlPermissionDenied(ctx, conn, "INSERT INTO api.notes(subject,body) VALUES ('bob','forbidden')") {
		t.Fatal("cross-user insert passed RLS")
	}
	if _, err = conn.Exec(ctx, `SELECT set_config('request.jwt.claims','{"sub":"bob"}',false)`); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = conn.QueryRow(ctx, "SELECT count(*) FROM api.notes").Scan(&count); err != nil || count != 0 {
		t.Fatalf("cross-user SELECT count=%d: %v", count, err)
	}
	if err = f.manager.Revoke(ctx, f.material, role); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "SELECT 1"); err == nil {
		t.Fatal("retired session still usable")
	}
}
