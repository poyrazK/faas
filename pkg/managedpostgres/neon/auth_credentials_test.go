// adr: 1190 — auth-schema ownership, customer isolation and backend qualification for auth bindings.
package neon

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestAuthCapabilityRequiresExplicitBackendOptIn(t *testing.T) {
	config := testBackend()
	p, err := New(config, func(string) string { return "test-only" })
	if err != nil {
		t.Fatal(err)
	}
	if p.Capabilities().SupportsCredentialAccess(managedpostgres.CredentialAuth) == nil {
		t.Fatal("auth advertised by default")
	}
	if _, err := p.IssueCredentials(context.Background(), managedpostgres.CredentialRequest{ProviderResourceID: "project-1/br-1", IdentityKey: "k", IdempotencyKey: "k", Access: managedpostgres.CredentialAuth}); !errors.Is(err, managedpostgres.ErrUnsupported) {
		t.Fatalf("auth issued without opt-in: %v", err)
	}
	config.AuthEnabled = true
	p, err = New(config, func(string) string { return "test-only" })
	if err != nil || p.Capabilities().SupportsCredentialAccess(managedpostgres.CredentialAuth) != nil {
		t.Fatal("explicit opt-in rejected", err)
	}
}

func (f *credentialFixture) authRole(t *testing.T, suffix string) credentialRole {
	t.Helper()
	role := f.runtime
	role.name = strings.Replace(role.name, "gregale_rt_", "gregale_auth_", 1)
	role.name = role.name[:len(role.name)-len(suffix)] + suffix
	role.marker = strings.TrimSuffix(role.marker, ":read_write") + ":auth"
	role.access = managedpostgres.CredentialAuth
	f.extraRoles = append(f.extraRoles, role.name, role.authOwner)
	return role
}

func TestSQLAuthCredentialsOwnAuthSchemasAndIsolateCustomerData(t *testing.T) {
	f := newCredentialFixture(t)
	ctx := context.Background()
	role := f.authRole(t, "")
	if err := f.manager.Ensure(ctx, f.material, role); err != nil {
		t.Fatal(err)
	}
	var before, after string
	if err := f.admin.QueryRow(ctx, `SELECT rolpassword FROM pg_authid WHERE rolname=$1`, role.name).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Ensure(ctx, f.material, role); err != nil {
		t.Fatalf("idempotent issuance: %v", err)
	}
	if err := f.admin.QueryRow(ctx, `SELECT rolpassword FROM pg_authid WHERE rolname=$1`, role.name).Scan(&after); err != nil || before == "" || before != after {
		t.Fatal("auth retry changed the password", err)
	}
	for _, schema := range authSchemas {
		var owner string
		if err := f.admin.QueryRow(ctx, `SELECT nspowner::regrole::text FROM pg_namespace WHERE nspname=$1`, schema).Scan(&owner); err != nil || owner != role.authOwner {
			t.Fatalf("schema %s owned by %q: %v", schema, owner, err)
		}
	}
	if err := f.manager.Ensure(ctx, f.material, f.migration); err != nil {
		t.Fatal(err)
	}
	migration := f.connect(t, f.migration.name)
	conn := f.connect(t, role.name)
	if err := verifyAuthProbe(ctx, conn, migration, role.authOwner, role.schemaOwner, "auth_contract"); err != nil {
		t.Fatalf("qualification SQL contract: %v", err)
	}
	// GoTrue creates helper functions; the owner's default ACL keeps them
	// from PUBLIC so read-only and Data API logins still pass verification.
	executeSQL(t, conn, `CREATE FUNCTION auth.uid() RETURNS uuid LANGUAGE sql STABLE AS $$ SELECT nullif(current_setting('request.jwt.claim.sub', true), '')::uuid $$`)
	executeSQL(t, conn, `CREATE FUNCTION auth.definer_probe() RETURNS int LANGUAGE sql SECURITY DEFINER AS $$ SELECT 1 $$`)
	if err := f.manager.Ensure(ctx, f.material, f.readonly); err != nil {
		t.Fatalf("read-only issuance after auth install: %v", err)
	}
	api := f.runtime
	api.name = strings.Replace(api.name, "gregale_rt_", "gregale_api_", 1)
	api.marker = strings.TrimSuffix(api.marker, ":read_write") + ":data_api"
	api.access = managedpostgres.CredentialDataAPI
	f.extraRoles = append(f.extraRoles, api.name)
	if err := f.manager.Ensure(ctx, f.material, api); err != nil {
		t.Fatalf("data API issuance after auth install: %v", err)
	}
	reader := f.connect(t, f.readonly.name)
	deniedSQL(t, reader, `SELECT * FROM auth.auth_contract`)
	deniedSQL(t, migration, `SELECT * FROM auth.auth_contract`)

	next := f.authRole(t, "f0")
	if err := f.manager.Ensure(ctx, f.material, next); err != nil {
		t.Fatal(err)
	}
	replacement := f.connect(t, next.name)
	if err := f.manager.Revoke(ctx, f.material, role); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "SELECT 1"); err == nil {
		t.Fatal("retired auth session survived")
	}
	var count int
	if err := replacement.QueryRow(ctx, `SELECT count(*) FROM auth.auth_contract`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("rotation lost auth data: %d %v", count, err)
	}
	if err := f.manager.Revoke(ctx, f.material, role); err != nil {
		t.Fatal("auth revoke replay", err)
	}
}

func TestSQLAuthCredentialRejectsOwnershipDrift(t *testing.T) {
	for _, kind := range []string{"foreign auth schema", "public create", "owned customer table", "extra member"} {
		t.Run(kind, func(t *testing.T) {
			f := newCredentialFixture(t)
			ctx := context.Background()
			role := f.authRole(t, "")
			switch kind {
			case "foreign auth schema":
				// A customer's own auth schema must never be adopted.
				if err := f.manager.Ensure(ctx, f.material, f.migration); err != nil {
					t.Fatal(err)
				}
				executeSQL(t, f.connect(t, f.migration.name), `CREATE SCHEMA auth`)
			default:
				if err := f.manager.Ensure(ctx, f.material, role); err != nil {
					t.Fatal(err)
				}
				owner := roleIdentifier(role.authOwner)
				switch kind {
				case "public create":
					executeSQL(t, f.admin, "GRANT CREATE ON SCHEMA public TO "+owner)
				case "owned customer table":
					executeSQL(t, f.admin, "CREATE TABLE public.adopted (id int); ALTER TABLE public.adopted OWNER TO "+owner)
				case "extra member":
					if err := f.manager.Ensure(ctx, f.material, f.migration); err != nil {
						t.Fatal(err)
					}
					executeSQL(t, f.admin, "GRANT "+owner+" TO "+roleIdentifier(f.migration.name))
				}
			}
			if err := f.manager.Ensure(ctx, f.material, role); !errors.Is(err, managedpostgres.ErrConflict) {
				t.Fatalf("expected conflict, got %v", err)
			}
		})
	}
}
