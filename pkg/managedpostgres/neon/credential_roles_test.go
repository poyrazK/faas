// adr: 592 — portable reader permissions and capability discovery.
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
	admin                        *pgx.Conn
	config                       *pgx.ConnConfig
	manager                      *sqlCredentialRoles
	material                     managedpostgres.CredentialMaterial
	runtime, readonly, migration credentialRole
	extraRoles                   []string
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
	request.Access = managedpostgres.CredentialReadOnly
	f.readonly = provider.credentialRole(request)
	request.Access = managedpostgres.CredentialMigration
	f.migration = provider.credentialRole(request)
	t.Cleanup(func() {
		_, err := boot.Exec(ctx, "DROP DATABASE "+roleIdentifier(database)+" WITH (FORCE)")
		if err != nil {
			t.Errorf("drop isolated database: %v", err)
		}
		for _, name := range append([]string{f.runtime.name, f.readonly.name, f.migration.name, f.runtime.schemaOwner, f.runtime.legacy}, f.extraRoles...) {
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

func TestSQLReadOnlyCredentialsConformAndRotateWithoutDataLoss(t *testing.T) {
	f := newCredentialFixture(t)
	ctx := context.Background()
	if err := f.manager.Ensure(ctx, f.material, f.migration); err != nil {
		t.Fatal(err)
	}
	writer := f.connect(t, f.migration.name)
	if err := managedpostgres.PrepareReadOnlyCredentialProbe(ctx, writer, "readonly_contract"); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Ensure(ctx, f.material, f.readonly); err != nil {
		t.Fatal(err)
	}
	var before, after string
	if err := f.admin.QueryRow(ctx, `SELECT rolpassword FROM pg_authid WHERE rolname=$1`, f.readonly.name).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Ensure(ctx, f.material, f.readonly); err != nil {
		t.Fatal(err)
	}
	if err := f.admin.QueryRow(ctx, `SELECT rolpassword FROM pg_authid WHERE rolname=$1`, f.readonly.name).Scan(&after); err != nil || before == "" || before != after {
		t.Fatal("read-only retry changed the password", err)
	}
	reader := f.connect(t, f.readonly.name)
	if err := managedpostgres.VerifyReadOnlyCredentialProbe(ctx, reader, writer, "readonly_contract"); err != nil {
		t.Fatal(err)
	}
	role := f.readonly
	role.name += "_next"
	f.extraRoles = append(f.extraRoles, role.name)
	if err := f.manager.Ensure(ctx, f.material, role); err != nil {
		t.Fatal(err)
	}
	replacement := f.connect(t, role.name)
	if err := f.manager.Revoke(ctx, f.material, f.readonly); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Exec(ctx, "SELECT 1"); err == nil {
		t.Fatal("retired read-only session survived")
	}
	var count int
	if err := replacement.QueryRow(ctx, `SELECT count(*) FROM public.readonly_contract_future`).Scan(&count); err != nil || count != 1 {
		t.Fatal("rotation lost readable application data", err)
	}
	deniedSQL(t, replacement, `UPDATE public.readonly_contract_future SET value='forbidden'`)
	if err := f.manager.Revoke(ctx, f.material, f.readonly); err != nil {
		t.Fatal("read-only revoke replay", err)
	}
}

func TestSQLReadOnlyCredentialRejectsPrivilegeDrift(t *testing.T) {
	for _, kind := range []string{"column write", "public write", "sequence usage", "future write", "definer grant", "other schema", "pg prefix schema", "membership", "owned object"} {
		t.Run(kind, func(t *testing.T) {
			f := newCredentialFixture(t)
			ctx := context.Background()
			if err := f.manager.Ensure(ctx, f.material, f.readonly); err != nil {
				t.Fatal(err)
			}
			executeSQL(t, f.admin, `CREATE TABLE public.readonly_drift (value text); CREATE SEQUENCE public.readonly_seq`)
			name := roleIdentifier(f.readonly.name)
			switch kind {
			case "column write":
				executeSQL(t, f.admin, "GRANT UPDATE(value) ON public.readonly_drift TO "+name)
			case "public write":
				executeSQL(t, f.admin, "GRANT INSERT ON public.readonly_drift TO PUBLIC")
			case "sequence usage":
				executeSQL(t, f.admin, "GRANT USAGE ON SEQUENCE public.readonly_seq TO "+name)
			case "future write":
				executeSQL(t, f.admin, "ALTER DEFAULT PRIVILEGES FOR ROLE "+roleIdentifier(f.readonly.schemaOwner)+" IN SCHEMA public GRANT INSERT ON TABLES TO "+name)
			case "definer grant":
				executeSQL(t, f.admin, "CREATE FUNCTION public.readonly_definer() RETURNS integer LANGUAGE SQL SECURITY DEFINER AS 'SELECT 1'; REVOKE EXECUTE ON FUNCTION public.readonly_definer() FROM PUBLIC; GRANT EXECUTE ON FUNCTION public.readonly_definer() TO "+name)
			case "other schema":
				executeSQL(t, f.admin, "CREATE SCHEMA extra; CREATE TABLE extra.writable(value text); GRANT USAGE ON SCHEMA extra TO "+name+"; GRANT INSERT ON extra.writable TO "+name)
			case "pg prefix schema":
				executeSQL(t, f.admin, "CREATE SCHEMA pgcustomer; GRANT CREATE ON SCHEMA pgcustomer TO "+name)
			case "membership":
				executeSQL(t, f.admin, "GRANT "+roleIdentifier(f.readonly.schemaOwner)+" TO "+name)
			case "owned object":
				executeSQL(t, f.admin, "ALTER TABLE public.readonly_drift OWNER TO "+name)
			}
			if err := f.manager.Ensure(ctx, f.material, f.readonly); !errors.Is(err, managedpostgres.ErrConflict) {
				t.Fatalf("unsafe read-only credential accepted: %v", err)
			}
		})
	}
}

func (f *credentialFixture) connect(t *testing.T, name string) *pgx.Conn {
	t.Helper()
	config := f.config.Copy()
	config.User = name
	if name != f.config.User {
		// Neon supplies role passwords through its API. The local fixture has
		// no password API, so install a known test password before exercising
		// the login under both trust and SCRAM authentication.
		config.Password = "local-fixture-password"
		executeSQL(t, f.admin, "ALTER ROLE "+roleIdentifier(name)+" PASSWORD 'local-fixture-password'")
	}
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

// Neon owners have CREATEROLE without SUPERUSER. On PostgreSQL 16+, the
// automatic ADMIN membership does not permit DROP OWNED without SET access.
func TestSQLCredentialRetirementByNonSuperuserPreservesData(t *testing.T) {
	for _, access := range []managedpostgres.CredentialAccess{managedpostgres.CredentialReadWrite, managedpostgres.CredentialReadOnly, managedpostgres.CredentialMigration} {
		t.Run(string(access), func(t *testing.T) {
			f := newCredentialFixture(t)
			ctx := context.Background()
			owner := "gregale_control_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			f.extraRoles = append(f.extraRoles, owner)
			executeSQL(t, f.admin, "CREATE ROLE "+roleIdentifier(owner)+" LOGIN CREATEROLE NOSUPERUSER PASSWORD 'local-owner-password'")
			executeSQL(t, f.admin, "GRANT pg_signal_backend TO "+roleIdentifier(owner))
			executeSQL(t, f.admin, "ALTER DATABASE "+roleIdentifier(f.config.Database)+" OWNER TO "+roleIdentifier(owner))
			executeSQL(t, f.admin, "ALTER SCHEMA public OWNER TO "+roleIdentifier(owner))
			config := f.config.Copy()
			config.User, config.Password = owner, "local-owner-password"
			f.manager.connect = func(ctx context.Context, _ string) (*pgx.Conn, error) {
				return pgx.ConnectConfig(ctx, config.Copy())
			}
			controller, err := pgx.ConnectConfig(ctx, config.Copy())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = controller.Close(ctx) }()
			var superuser, createRole bool
			if err := controller.QueryRow(ctx, `SELECT rolsuper, rolcreaterole FROM pg_roles WHERE rolname=current_user`).Scan(&superuser, &createRole); err != nil || superuser || !createRole {
				t.Fatalf("fixture permissions superuser=%v createrole=%v err=%v", superuser, createRole, err)
			}
			if err := f.manager.Ensure(ctx, f.material, f.migration); err != nil {
				t.Fatal("create migration role", err)
			}
			migration := f.connect(t, f.migration.name)
			executeSQL(t, migration, `CREATE TABLE public.retirement_data(value text); INSERT INTO public.retirement_data VALUES ('preserved')`)
			role := f.migration
			if access == managedpostgres.CredentialReadWrite {
				role = f.runtime
			} else if access == managedpostgres.CredentialReadOnly {
				role = f.readonly
			}
			if err := f.manager.Ensure(ctx, f.material, role); err != nil {
				t.Fatal("create credential", err)
			}
			retired := f.connect(t, role.name)
			replacementRole := role
			replacementRole.name += "_next"
			f.extraRoles = append(f.extraRoles, replacementRole.name)
			if err := f.manager.Ensure(ctx, f.material, replacementRole); err != nil {
				t.Fatal("create replacement", err)
			}
			replacement := f.connect(t, replacementRole.name)
			if err := f.manager.Revoke(ctx, f.material, role); err != nil {
				t.Fatal("non-superuser retirement", err)
			}
			if _, err := retired.Exec(ctx, "SELECT 1"); err == nil {
				t.Fatal("retired session survived")
			}
			var value string
			if err := replacement.QueryRow(ctx, `SELECT value FROM public.retirement_data`).Scan(&value); err != nil || value != "preserved" {
				t.Fatalf("replacement data value=%q err=%v", value, err)
			}
			var exists bool
			if err := f.admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, role.name).Scan(&exists); err != nil || exists {
				t.Fatalf("retired role exists=%v err=%v", exists, err)
			}
			if err := f.manager.Revoke(ctx, f.material, role); err != nil {
				t.Fatal("retirement replay", err)
			}
		})
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
	if err := f.manager.Ensure(ctx, f.material, f.readonly); err != nil {
		t.Fatal(err)
	}
	inheritedReader := f.connect(t, f.readonly.name)
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
	for _, name := range []string{source.name, f.readonly.name, f.migration.name} {
		if err := f.admin.QueryRow(ctx, `SELECT rolcanlogin FROM pg_roles WHERE rolname=$1`, name).Scan(&login); err != nil || login {
			t.Fatalf("inherited role still enabled: %v", err)
		}
	}
	if _, err := inherited.Exec(ctx, `SELECT 1`); err == nil {
		t.Fatal("inherited session survived")
	}
	if _, err := inheritedReader.Exec(ctx, `SELECT 1`); err == nil {
		t.Fatal("inherited read-only session survived")
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
