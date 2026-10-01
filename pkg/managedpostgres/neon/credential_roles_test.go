package neon

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

type fakeCredentialRoles struct {
	ensures, revokes, restrictions int
	err                            error
}

func (f *fakeCredentialRoles) Ensure(context.Context, managedpostgres.CredentialMaterial, credentialRole) error {
	f.ensures++
	return f.err
}
func (f *fakeCredentialRoles) Revoke(context.Context, managedpostgres.CredentialMaterial, credentialRole) error {
	f.revokes++
	return f.err
}
func (f *fakeCredentialRoles) RestrictInherited(context.Context, managedpostgres.CredentialMaterial, string) error {
	f.restrictions++
	return f.err
}

type credentialFixture struct {
	admin              *pgx.Conn
	config             *pgx.ConnConfig
	manager            *sqlCredentialRoles
	material           managedpostgres.CredentialMaterial
	runtime, migration credentialRole
	extraRoles         []string
}

// Unlike pgtest.Open's isolated schema, these tests need a private database:
// PostgreSQL PUBLIC privileges and default ACLs are database-wide.
func newCredentialFixture(t *testing.T) *credentialFixture {
	t.Helper()
	ctx := context.Background()
	boot := pgtest.Open(t)
	database := "gregale_roles_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := boot.Exec(ctx, "CREATE DATABASE "+roleIdentifier(database)+" TEMPLATE template0"); err != nil {
		t.Fatal(err)
	}
	config := boot.Config().ConnConfig.Copy()
	config.Database = database
	config.RuntimeParams = map[string]string{}
	provider := &Provider{organizationID: "test-org", databaseName: database}
	request := managedpostgres.CredentialRequest{ProviderResourceID: "test-" + uuid.NewString(), IdentityKey: "binding", Access: managedpostgres.CredentialReadWrite}
	f := &credentialFixture{config: config, runtime: provider.credentialRole(request)}
	request.Access = managedpostgres.CredentialMigration
	f.migration = provider.credentialRole(request)
	t.Cleanup(func() {
		_, err := boot.Exec(ctx, "DROP DATABASE "+roleIdentifier(database)+" WITH (FORCE)")
		if err != nil {
			t.Errorf("drop isolated database: %v", err)
		}
		for _, name := range append([]string{f.runtime.name, f.migration.name, f.runtime.schemaOwner, f.runtime.legacy}, f.extraRoles...) {
			if _, err := boot.Exec(ctx, "DROP ROLE IF EXISTS "+roleIdentifier(name)); err != nil {
				t.Errorf("drop isolated role: %v", err)
			}
		}
	})
	f.admin = f.connect(t, config.User)
	f.manager = &sqlCredentialRoles{connect: func(ctx context.Context, _ string) (*pgx.Conn, error) { return pgx.ConnectConfig(ctx, config.Copy()) }}
	f.material = managedpostgres.CredentialMaterial{Username: ownerLogin, Password: "test", Database: database, TLSMode: "require", Endpoints: []managedpostgres.Endpoint{{Role: managedpostgres.EndpointDirect, Host: "test.invalid", Port: 5432}}}
	return f
}

func (f *credentialFixture) connect(t *testing.T, name string) *pgx.Conn {
	t.Helper()
	config := f.config.Copy()
	config.User = name
	conn, err := pgx.ConnectConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

func executeSQL(t *testing.T, conn *pgx.Conn, query string, args ...any) {
	t.Helper()
	if _, err := conn.Exec(context.Background(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func deniedSQL(t *testing.T, conn *pgx.Conn, query string) {
	t.Helper()
	_, err := conn.Exec(context.Background(), query)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("expected permission denial for %q; got %v", query, err)
	}
}

func TestSQLCredentialPrivilegesAndRotationPreserveData(t *testing.T) {
	f := newCredentialFixture(t)
	ctx := context.Background()
	if err := f.manager.Ensure(ctx, f.material, f.migration); err != nil {
		t.Fatal(err)
	}
	migration := f.connect(t, f.migration.name)
	var current, session string
	if err := migration.QueryRow(ctx, `SELECT current_user, session_user`).Scan(&current, &session); err != nil {
		t.Fatal(err)
	}
	if current != f.migration.schemaOwner || session != f.migration.name {
		t.Fatalf("migration owner=%s login=%s", current, session)
	}
	executeSQL(t, migration, `SET ROLE NONE`)
	deniedSQL(t, migration, `CREATE TABLE public.owned_by_login (id integer)`)
	executeSQL(t, migration, "SET ROLE "+roleIdentifier(f.migration.schemaOwner))
	executeSQL(t, migration, `CREATE TABLE public.items (id serial PRIMARY KEY, tenant integer NOT NULL, value text); INSERT INTO public.items (tenant,value) VALUES (1,'visible'),(2,'hidden'); ALTER TABLE public.items ENABLE ROW LEVEL SECURITY; CREATE POLICY tenant_one ON public.items USING (tenant=1) WITH CHECK (tenant=1)`)
	if err := f.manager.Ensure(ctx, f.material, f.runtime); err != nil {
		t.Fatal(err)
	}
	var before, after string
	if err := f.admin.QueryRow(ctx, `SELECT rolpassword FROM pg_authid WHERE rolname=$1`, f.runtime.name).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Ensure(ctx, f.material, f.runtime); err != nil {
		t.Fatal(err)
	}
	if err := f.admin.QueryRow(ctx, `SELECT rolpassword FROM pg_authid WHERE rolname=$1`, f.runtime.name).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before == "" || before != after {
		t.Fatal("credential retry changed the password")
	}
	runtime := f.connect(t, f.runtime.name)
	var count int
	if err := runtime.QueryRow(ctx, `SELECT count(*) FROM public.items`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("RLS visible rows=%d err=%v", count, err)
	}
	executeSQL(t, runtime, `INSERT INTO public.items (tenant,value) VALUES (1,'new'); UPDATE public.items SET value='updated' WHERE tenant=1; DELETE FROM public.items WHERE value='updated' AND id<>1`)
	executeSQL(t, migration, `CREATE TABLE public.future_items (id serial PRIMARY KEY, value text)`)
	executeSQL(t, runtime, `INSERT INTO public.future_items (value) VALUES ('survives rotation')`)
	for _, query := range []string{`CREATE TABLE public.forbidden (id integer)`, `CREATE TEMP TABLE forbidden (id integer)`, `CREATE SCHEMA forbidden`, `TRUNCATE public.items`, `ALTER TABLE public.items ADD COLUMN forbidden text`, `CREATE ROLE forbidden`, `SET row_security = off; SELECT * FROM public.items`, "SET ROLE " + roleIdentifier(f.runtime.schemaOwner), `INSERT INTO public.items (tenant,value) VALUES (2,'forbidden')`} {
		deniedSQL(t, runtime, query)
		// Restore this setting after PostgreSQL correctly rejects a bypass attempt.
		executeSQL(t, runtime, `SET row_security=on`)
	}
	if err := f.manager.Revoke(ctx, f.material, f.migration); err != nil {
		t.Fatal(err)
	}
	executeSQL(t, runtime, `UPDATE public.future_items SET value='still exists'`)
	if err := f.manager.Revoke(ctx, f.material, f.runtime); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Exec(ctx, `SELECT 1`); err == nil {
		t.Fatal("revocation left an authenticated session alive")
	}
	if err := f.manager.Revoke(ctx, f.material, f.runtime); err != nil {
		t.Fatal(err)
	}
	if err := f.admin.QueryRow(ctx, `SELECT count(*) FROM public.future_items`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("rotation lost application data: %d %v", count, err)
	}
}

func TestSQLCredentialRevokeRefusesOwnedObjects(t *testing.T) {
	f := newCredentialFixture(t)
	ctx := context.Background()
	if err := f.manager.Ensure(ctx, f.material, f.runtime); err != nil {
		t.Fatal(err)
	}
	executeSQL(t, f.admin, `CREATE TABLE public.preserved (id integer)`)
	executeSQL(t, f.admin, "ALTER TABLE public.preserved OWNER TO "+roleIdentifier(f.runtime.name))
	if err := f.manager.Revoke(ctx, f.material, f.runtime); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("owned object revoke=%v", err)
	}
	var login, exists bool
	if err := f.admin.QueryRow(ctx, `SELECT rolcanlogin, to_regclass('public.preserved') IS NOT NULL FROM pg_roles WHERE rolname=$1`, f.runtime.name).Scan(&login, &exists); err != nil {
		t.Fatal(err)
	}
	if login || !exists {
		t.Fatalf("login=%v table preserved=%v", login, exists)
	}
}

func TestSQLCredentialRejectsPrivilegeDriftAndPublicDefiners(t *testing.T) {
	for _, kind := range []string{"admin flag", "membership", "schema create", "public definer"} {
		t.Run(kind, func(t *testing.T) {
			f := newCredentialFixture(t)
			ctx := context.Background()
			if kind == "public definer" {
				executeSQL(t, f.admin, `CREATE FUNCTION public.unsafe() RETURNS integer LANGUAGE SQL SECURITY DEFINER AS 'SELECT 1'`)
			} else {
				if err := f.manager.Ensure(ctx, f.material, f.runtime); err != nil {
					t.Fatal(err)
				}
				if kind == "admin flag" {
					executeSQL(t, f.admin, "ALTER ROLE "+roleIdentifier(f.runtime.name)+" BYPASSRLS")
				} else if kind == "schema create" {
					executeSQL(t, f.admin, "GRANT CREATE ON SCHEMA public TO "+roleIdentifier(f.runtime.name))
				} else {
					executeSQL(t, f.admin, "GRANT "+roleIdentifier(f.runtime.schemaOwner)+" TO "+roleIdentifier(f.runtime.name))
				}
			}
			if err := f.manager.Ensure(ctx, f.material, f.runtime); !errors.Is(err, managedpostgres.ErrConflict) {
				t.Fatalf("unsafe role accepted: %v", err)
			}
		})
	}
}

func TestCredentialPrivilegeProbeUsesActualSQLPermissions(t *testing.T) {
	f := newCredentialFixture(t)
	ctx := context.Background()
	if err := f.manager.Ensure(ctx, f.material, f.migration); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Ensure(ctx, f.material, f.runtime); err != nil {
		t.Fatal(err)
	}
	runtime := f.connect(t, f.runtime.name)
	migration := f.connect(t, f.migration.name)
	if err := verifyCredentialPrivileges(ctx, runtime, migration, f.migration, "gregale_probe_test"); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Revoke(ctx, f.material, f.migration); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := runtime.QueryRow(ctx, `SELECT count(*) FROM public.gregale_probe_test`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("migration retirement lost data: %d %v", count, err)
	}
}

func TestSQLCredentialRestoreDisablesInheritedLogins(t *testing.T) {
	f := newCredentialFixture(t)
	ctx := context.Background()
	if err := f.manager.Ensure(ctx, f.material, f.runtime); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Ensure(ctx, f.material, f.migration); err != nil {
		t.Fatal(err)
	}
	// A target binding has a distinct scope, even though it shares the schema owner.
	source := f.runtime
	f.runtime.name = "gregale_rt_" + strings.ReplaceAll(uuid.NewString()+uuid.NewString(), "-", "")[:40]
	f.runtime.scope = strings.Repeat("a", 40)
	f.runtime.marker = "gregale:credential:v1:" + f.runtime.scope + ":read_write"
	f.extraRoles = append(f.extraRoles, source.name)
	if err := f.manager.Ensure(ctx, f.material, f.runtime); err != nil {
		t.Fatal(err)
	}
	inherited := f.connect(t, source.name)
	if err := f.manager.RestrictInherited(ctx, f.material, f.runtime.scope); err != nil {
		t.Fatal(err)
	}
	var login bool
	for _, name := range []string{source.name, f.migration.name} {
		if err := f.admin.QueryRow(ctx, `SELECT rolcanlogin FROM pg_roles WHERE rolname=$1`, name).Scan(&login); err != nil || login {
			t.Fatalf("inherited role still enabled: %v", err)
		}
	}
	if _, err := inherited.Exec(ctx, `SELECT 1`); err == nil {
		t.Fatal("inherited session survived")
	}
	if err := f.admin.QueryRow(ctx, `SELECT rolcanlogin FROM pg_roles WHERE rolname=$1`, f.runtime.name).Scan(&login); err != nil || !login {
		t.Fatalf("target role disabled: %v", err)
	}
}

func TestLegacyCredentialRetirementPreservesOwnedData(t *testing.T) {
	f := newCredentialFixture(t)
	ctx := context.Background()
	executeSQL(t, f.admin, "CREATE ROLE "+roleIdentifier(f.runtime.legacy)+" LOGIN CREATEDB CREATEROLE BYPASSRLS")
	executeSQL(t, f.admin, `CREATE TABLE public.legacy_data (id integer)`)
	executeSQL(t, f.admin, "ALTER TABLE public.legacy_data OWNER TO "+roleIdentifier(f.runtime.legacy))
	legacy := f.connect(t, f.runtime.legacy)
	if err := f.manager.Revoke(ctx, f.material, f.runtime); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("legacy ownership retirement=%v", err)
	}
	if _, err := legacy.Exec(ctx, `SELECT 1`); err == nil {
		t.Fatal("legacy session survived retirement")
	}
	var login, preserved bool
	if err := f.admin.QueryRow(ctx, `SELECT rolcanlogin,to_regclass('public.legacy_data') IS NOT NULL FROM pg_roles WHERE rolname=$1`, f.runtime.legacy).Scan(&login, &preserved); err != nil || login || !preserved {
		t.Fatalf("legacy login=%v preserved=%v err=%v", login, preserved, err)
	}
	executeSQL(t, f.admin, "ALTER TABLE public.legacy_data OWNER TO "+roleIdentifier(f.config.User))
	if err := f.manager.Revoke(ctx, f.material, f.runtime); err != nil {
		t.Fatal(err)
	}
}
