// adr: 20261116213000 — custom Postgres group roles.
package neon

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// customRoleFixture runs the SQL as a non-superuser owner with CREATEROLE
// and BYPASSRLS that owns the database, as Neon's gregale_owner does.
type customRoleFixture struct {
	*credentialFixture
	owner                    string
	wmUser, wmAdmin, wmExtra string
}

func newCustomRoleFixture(t *testing.T) *customRoleFixture {
	t.Helper()
	ctx := context.Background()
	boot := pgtest.Open(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	owner := "test_owner_" + suffix
	database := "gregale_custom_" + suffix
	if _, err := boot.Exec(ctx, "CREATE ROLE "+roleIdentifier(owner)+" LOGIN CREATEROLE BYPASSRLS PASSWORD 'local-fixture-password'"); err != nil {
		t.Fatal(err)
	}
	if _, err := boot.Exec(ctx, "CREATE DATABASE "+roleIdentifier(database)+" OWNER "+roleIdentifier(owner)+" TEMPLATE template0"); err != nil {
		t.Fatal(err)
	}
	config := boot.Config().ConnConfig.Copy()
	config.Database = database
	config.RuntimeParams = map[string]string{}
	provider := &Provider{organizationID: "test-org", databaseName: database}
	request := managedpostgres.CredentialRequest{ProviderResourceID: "test-" + uuid.NewString(), IdentityKey: "binding", Access: managedpostgres.CredentialReadWrite}
	base := &credentialFixture{config: config, runtime: provider.credentialRole(request)}
	request.Access = managedpostgres.CredentialReadOnly
	base.readonly = provider.credentialRole(request)
	request.Access = managedpostgres.CredentialMigration
	base.migration = provider.credentialRole(request)
	f := &customRoleFixture{credentialFixture: base, owner: owner,
		wmUser: "wm_user_" + suffix, wmAdmin: "wm_admin_" + suffix, wmExtra: "wm_extra_" + suffix}
	t.Cleanup(func() {
		if _, err := boot.Exec(ctx, "DROP DATABASE "+roleIdentifier(database)+" WITH (FORCE)"); err != nil {
			t.Errorf("drop isolated database: %v", err)
		}
		for _, name := range append([]string{f.wmAdmin, f.wmUser, f.wmExtra, base.runtime.name, base.readonly.name, base.migration.name, base.runtime.schemaOwner, owner}, base.extraRoles...) {
			if _, err := boot.Exec(ctx, "DROP ROLE IF EXISTS "+roleIdentifier(name)); err != nil {
				t.Errorf("drop isolated role %s: %v", name, err)
			}
		}
	})
	base.admin = base.connect(t, config.User)
	ownerConfig := config.Copy()
	ownerConfig.User, ownerConfig.Password = owner, "local-fixture-password"
	base.manager = &sqlCredentialRoles{connect: func(ctx context.Context, _ string) (*pgx.Conn, error) {
		return pgx.ConnectConfig(ctx, ownerConfig.Copy())
	}}
	base.material = managedpostgres.CredentialMaterial{Username: owner, Password: "test", Database: database, TLSMode: "require", Endpoints: []managedpostgres.Endpoint{{Role: managedpostgres.EndpointDirect, Host: "test.invalid", Port: 5432}}}
	return f
}

func (f *customRoleFixture) windmillRoles() []managedpostgres.CustomRoleSpec {
	return []managedpostgres.CustomRoleSpec{
		{Name: f.wmUser, GrantedTo: []managedpostgres.CredentialAccess{managedpostgres.CredentialMigration, managedpostgres.CredentialReadWrite}},
		{Name: f.wmAdmin, BypassRLS: true, GrantedTo: []managedpostgres.CredentialAccess{managedpostgres.CredentialMigration, managedpostgres.CredentialReadWrite}, MemberOf: []string{f.wmUser}},
	}
}

func (f *customRoleFixture) apply(t *testing.T, desired []managedpostgres.CustomRoleSpec) error {
	t.Helper()
	return f.manager.ApplyCustomRoles(context.Background(), f.material, f.migration, desired)
}

func roleFlags(t *testing.T, conn *pgx.Conn, name string) (login, bypass, inherit bool, marker string) {
	t.Helper()
	err := conn.QueryRow(context.Background(), `SELECT rolcanlogin, rolbypassrls, rolinherit, COALESCE(shobj_description(oid, 'pg_authid'), '')
 FROM pg_roles WHERE rolname = $1`, name).Scan(&login, &bypass, &inherit, &marker)
	if err != nil {
		t.Fatalf("role %s: %v", name, err)
	}
	return login, bypass, inherit, marker
}

// TestCustomRolesRunWindmillStyleMigrations replays what Windmill's
// migrations and user_db transactions do with the roles a superuser would
// otherwise create (init-db-as-superuser.sql).
func TestCustomRolesRunWindmillStyleMigrations(t *testing.T) {
	f := newCustomRoleFixture(t)
	ctx := context.Background()
	if err := f.manager.Ensure(ctx, f.material, f.migration); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Ensure(ctx, f.material, f.runtime); err != nil {
		t.Fatal(err)
	}
	// A function from before the roles were declared (ADR-20261123112326).
	executeSQL(t, f.connect(t, f.migration.name), `CREATE FUNCTION earlier() RETURNS int LANGUAGE sql AS 'SELECT 1'`)
	if err := f.apply(t, f.windmillRoles()); err != nil {
		t.Fatal(err)
	}
	login, bypass, inherit, marker := roleFlags(t, f.db(), f.wmAdmin)
	if login || !bypass || !inherit || marker != customRoleMarker(f.migration.scope) {
		t.Fatalf("admin role login=%v bypass=%v inherit=%v marker=%q", login, bypass, inherit, marker)
	}
	// Idempotent.
	if err := f.apply(t, f.windmillRoles()); err != nil {
		t.Fatal(err)
	}

	migration := f.connect(t, f.migration.name)
	// Windmill's role creation is wrapped in exception handlers; with the
	// roles present it only notices.
	executeSQL(t, migration, `DO $do$ BEGIN CREATE ROLE `+f.wmUser+`; EXCEPTION WHEN OTHERS THEN RAISE NOTICE 'skip: %', SQLERRM; END $do$`)
	executeSQL(t, migration, `CREATE TABLE job (id bigserial PRIMARY KEY, workspace text NOT NULL, owner text NOT NULL)`)
	executeSQL(t, migration, `ALTER TABLE job ENABLE ROW LEVEL SECURITY`)
	executeSQL(t, migration, `CREATE POLICY see_own ON job TO `+f.wmUser+` USING (owner = current_setting('session.user', true))`)
	executeSQL(t, migration, `GRANT USAGE ON SCHEMA public TO `+f.wmUser+`, `+f.wmAdmin)
	executeSQL(t, migration, `GRANT ALL ON ALL TABLES IN SCHEMA public TO `+f.wmUser)
	executeSQL(t, migration, `GRANT ALL ON ALL SEQUENCES IN SCHEMA public TO `+f.wmUser)
	executeSQL(t, migration, `GRANT ALL ON job TO `+f.wmAdmin)
	executeSQL(t, migration, `INSERT INTO job (workspace, owner) VALUES ('w', 'alice'), ('w', 'bob')`)
	executeSQL(t, migration, `DO $do$ BEGIN GRANT `+f.wmAdmin+` TO CURRENT_USER; EXCEPTION WHEN OTHERS THEN RAISE NOTICE 'skip: %', SQLERRM; END $do$`)
	// Migration 20260714230440 (wrap session gucs in rls policies) defines a
	// pg_temp helper: the migration login needs TEMPORARY on the database.
	executeSQL(t, migration, `CREATE FUNCTION pg_temp.wrap_session_gucs(expr text) RETURNS text AS $fn$ BEGIN RETURN expr; END $fn$ LANGUAGE plpgsql`)
	executeSQL(t, migration, `SELECT pg_temp.wrap_session_gucs('x')`)
	executeSQL(t, migration, `DROP FUNCTION pg_temp.wrap_session_gucs(text)`)
	// Windmill's user_db transactions call set_session_context(), which
	// migration 20250412144540 defines without granting it: stock PostgreSQL
	// gives EXECUTE to PUBLIC, which Gregale revokes (ADR-20261123112326).
	executeSQL(t, migration, `CREATE FUNCTION set_session_context(username text) RETURNS void LANGUAGE sql AS $fn$ SELECT set_config('session.user', username, true) $fn$`)

	runtime := f.connect(t, f.runtime.name)
	if !sqlPermissionDenied(ctx, runtime, "CREATE TEMP TABLE runtime_temp (id integer)") {
		t.Fatal("runtime login created a temporary table")
	}
	count := func(role string) int {
		t.Helper()
		tx, err := runtime.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+role); err != nil {
			t.Fatalf("SET LOCAL ROLE %s: %v", role, err)
		}
		if _, err := tx.Exec(ctx, "SELECT set_session_context('alice')"); err != nil {
			t.Fatalf("set_session_context as %s: %v", role, err)
		}
		if _, err := tx.Exec(ctx, "SELECT earlier()"); err != nil {
			t.Fatalf("earlier() as %s: %v", role, err)
		}
		var n int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM job").Scan(&n); err != nil {
			t.Fatalf("select as %s: %v", role, err)
		}
		return n
	}
	if n := count(f.wmUser); n != 1 {
		t.Fatalf("windmill_user saw %d rows through RLS, want 1", n)
	}
	if n := count(f.wmAdmin); n != 2 {
		t.Fatalf("windmill_admin saw %d rows, want 2 (BYPASSRLS)", n)
	}
	// The app's own login runs its functions too.
	if _, err := runtime.Exec(ctx, "SELECT set_session_context('alice'), earlier()"); err != nil {
		t.Fatalf("runtime login cannot run the migrations' functions: %v", err)
	}
	// The login inherits nothing from the roles: RLS has no policy for it.
	var direct int
	if err := runtime.QueryRow(ctx, "SELECT count(*) FROM job").Scan(&direct); err != nil || direct != 0 {
		t.Fatalf("runtime login without SET ROLE saw %d rows (%v), want 0", direct, err)
	}
	// Re-ensuring logins with custom memberships still conforms.
	if err := f.manager.Ensure(ctx, f.material, f.runtime); err != nil {
		t.Fatalf("re-ensure runtime with custom memberships: %v", err)
	}
	if err := f.manager.Ensure(ctx, f.material, f.migration); err != nil {
		t.Fatalf("re-ensure migration with custom memberships: %v", err)
	}
	// A login issued later receives the declared roles at issue time.
	next := f.runtime
	next.name += "_next"
	next.customRoles = []string{f.wmUser, f.wmAdmin}
	f.extraRoles = append(f.extraRoles, next.name)
	if err := f.manager.Ensure(ctx, f.material, next); err != nil {
		t.Fatal(err)
	}
	later := f.connect(t, next.name)
	if _, err := later.Exec(ctx, "SET ROLE "+f.wmAdmin); err != nil {
		t.Fatalf("new login cannot SET ROLE admin: %v", err)
	}
	// A read-only login was not granted the roles.
	if err := f.manager.Ensure(ctx, f.material, f.readonly); err != nil {
		t.Fatal(err)
	}
	reader := f.connect(t, f.readonly.name)
	if _, err := reader.Exec(ctx, "SET ROLE "+f.wmAdmin); err == nil {
		t.Fatal("read-only login could SET ROLE to a role granted only to migration and read_write")
	}
	// PUBLIC stays without EXECUTE: the read-only login gets none.
	if !sqlPermissionDenied(ctx, reader, "SELECT earlier()") {
		t.Fatal("read-only login ran a function of the schema owner")
	}
}

func TestCustomRolesRevokeAndDrop(t *testing.T) {
	f := newCustomRoleFixture(t)
	ctx := context.Background()
	if err := f.manager.Ensure(ctx, f.material, f.migration); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Ensure(ctx, f.material, f.runtime); err != nil {
		t.Fatal(err)
	}
	roles := append(f.windmillRoles(), managedpostgres.CustomRoleSpec{Name: f.wmExtra, GrantedTo: []managedpostgres.CredentialAccess{managedpostgres.CredentialReadWrite}})
	if err := f.apply(t, roles); err != nil {
		t.Fatal(err)
	}
	// The extra role owns a table; dropping it hands the table to the schema owner.
	migration := f.connect(t, f.migration.name)
	executeSQL(t, migration, "GRANT CREATE ON SCHEMA public TO "+f.wmExtra)
	executeSQL(t, f.db(), "SET ROLE "+f.wmExtra)
	executeSQL(t, f.db(), "CREATE TABLE extra_owned (id int)")
	executeSQL(t, f.db(), "RESET ROLE")

	// No longer granted to read_write: revoked from the runtime login.
	roles[0].GrantedTo = []managedpostgres.CredentialAccess{managedpostgres.CredentialMigration}
	if err := f.apply(t, roles[:2]); err != nil {
		t.Fatal(err)
	}
	if member := directMember(t, f.db(), f.wmUser, f.runtime.name); member {
		t.Fatal("revoked grant still makes the runtime login a direct member")
	}
	if member := directMember(t, f.db(), f.wmUser, f.migration.name); !member {
		t.Fatal("migration login lost its grant")
	}
	var exists bool
	if err := f.db().QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, f.wmExtra).Scan(&exists); err != nil || exists {
		t.Fatalf("undeclared role still exists (%v)", err)
	}
	var owner string
	if err := f.db().QueryRow(ctx, `SELECT tableowner FROM pg_tables WHERE tablename = 'extra_owned'`).Scan(&owner); err != nil || owner != f.migration.schemaOwner {
		t.Fatalf("extra_owned owner %q, want %q (%v)", owner, f.migration.schemaOwner, err)
	}
	// Dropping everything.
	if err := f.apply(t, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.db().QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname IN ($1, $2))`, f.wmUser, f.wmAdmin).Scan(&exists); err != nil || exists {
		t.Fatalf("roles survived an empty declaration (%v)", err)
	}
}

func TestCustomRolesRefuseForeignRolesAndDataAPIDatabases(t *testing.T) {
	f := newCustomRoleFixture(t)
	ctx := context.Background()
	if err := f.manager.Ensure(ctx, f.material, f.migration); err != nil {
		t.Fatal(err)
	}
	// A role that is not a custom role is never taken over.
	executeSQL(t, f.db(), "CREATE ROLE "+f.wmExtra+" NOLOGIN")
	if err := f.apply(t, []managedpostgres.CustomRoleSpec{{Name: f.wmExtra}}); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("foreign role adopted: %v", err)
	}
	executeSQL(t, f.db(), "DROP ROLE "+f.wmExtra)
	// A Data API login fences BYPASSRLS roles.
	dataAPI := "gregale_api_" + strings.Repeat("a", 40)
	f.extraRoles = append(f.extraRoles, dataAPI)
	executeSQL(t, f.db(), "CREATE ROLE "+dataAPI+" LOGIN")
	executeSQL(t, f.db(), "COMMENT ON ROLE "+dataAPI+" IS 'gregale:credential:v1:"+f.migration.scope+":data_api'")
	if err := f.apply(t, f.windmillRoles()); !errors.Is(err, managedpostgres.ErrBypassRLSConflict) {
		t.Fatalf("BYPASSRLS role on a Data API database: %v", err)
	}
	// Without BYPASSRLS the declaration applies.
	if err := f.apply(t, []managedpostgres.CustomRoleSpec{{Name: f.wmUser, GrantedTo: []managedpostgres.CredentialAccess{managedpostgres.CredentialMigration}}}); err != nil {
		t.Fatal(err)
	}
	executeSQL(t, f.db(), "DROP ROLE "+dataAPI)
	// And a Data API login is refused once a BYPASSRLS role exists.
	if err := f.apply(t, f.windmillRoles()); err != nil {
		t.Fatal(err)
	}
	api := f.runtime
	api.access = managedpostgres.CredentialDataAPI
	if err := ensureFenceOnly(ctx, f, api); !errors.Is(err, managedpostgres.ErrBypassRLSConflict) {
		t.Fatalf("Data API login on a BYPASSRLS database: %v", err)
	}
}

func ensureFenceOnly(ctx context.Context, f *customRoleFixture, role credentialRole) error {
	conn, err := f.manager.connection(ctx, f.material)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return ensureLoginCustomRoles(ctx, tx, role)
}

func TestCustomRolesWithoutBypassRLSOwnerAreUnavailable(t *testing.T) {
	f := newCustomRoleFixture(t)
	ctx := context.Background()
	executeSQL(t, f.db(), "ALTER ROLE "+f.owner+" NOBYPASSRLS")
	if err := f.manager.Ensure(ctx, f.material, f.migration); err != nil {
		t.Fatal(err)
	}
	if err := f.apply(t, f.windmillRoles()); !errors.Is(err, managedpostgres.ErrBypassRLSUnavailable) {
		t.Fatalf("got %v, want ErrBypassRLSUnavailable", err)
	}
}

func (f *customRoleFixture) db() *pgx.Conn { return f.credentialFixture.admin }

func directMember(t *testing.T, conn *pgx.Conn, role, member string) bool {
	t.Helper()
	var exists bool
	if err := conn.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_auth_members m JOIN pg_roles r ON r.oid = m.roleid
 JOIN pg_roles u ON u.oid = m.member WHERE r.rolname = $1 AND u.rolname = $2)`, role, member).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	return exists
}

func sessionRole(t *testing.T, conn *pgx.Conn) string {
	t.Helper()
	var role string
	if err := conn.QueryRow(context.Background(), "SELECT current_user::text").Scan(&role); err != nil {
		t.Fatal(err)
	}
	return role
}

// TestCustomRoleDefaultFor checks amendment 2: read_write logins start as
// the declared default role (Windmill's windmill_admin), keep conforming
// on re-issue, and go back to themselves when the default is removed.
func TestCustomRoleDefaultFor(t *testing.T) {
	f := newCustomRoleFixture(t)
	ctx := context.Background()
	for _, role := range []credentialRole{f.migration, f.runtime, f.readonly} {
		if err := f.manager.Ensure(ctx, f.material, role); err != nil {
			t.Fatal(err)
		}
	}
	roles := f.windmillRoles()
	roles[1].DefaultFor = []managedpostgres.CredentialAccess{managedpostgres.CredentialReadWrite}
	if err := f.apply(t, roles); err != nil {
		t.Fatal(err)
	}
	migration := f.connect(t, f.migration.name)
	executeSQL(t, migration, `CREATE TABLE audit (id bigserial PRIMARY KEY, username text NOT NULL)`)
	executeSQL(t, migration, `ALTER TABLE audit ENABLE ROW LEVEL SECURITY`)
	executeSQL(t, migration, `CREATE POLICY own ON audit TO `+f.wmUser+` USING (username = current_setting('session.user', true))`)
	executeSQL(t, migration, `GRANT ALL ON audit TO `+f.wmUser+`, `+f.wmAdmin)
	executeSQL(t, migration, `GRANT ALL ON SEQUENCE audit_id_seq TO `+f.wmUser+`, `+f.wmAdmin)

	// Windmill's own connection: starts as windmill_admin and writes past RLS.
	runtime := f.connect(t, f.runtime.name)
	if got := sessionRole(t, runtime); got != f.wmAdmin {
		t.Fatalf("read_write login starts as %q, want %q", got, f.wmAdmin)
	}
	executeSQL(t, runtime, `INSERT INTO audit (username) VALUES ('anyone')`)
	// Its user transactions still switch down to windmill_user.
	tx, err := runtime.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+f.wmUser); err != nil {
		t.Fatalf("default-role session cannot SET LOCAL ROLE windmill_user: %v", err)
	}
	var visible int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM audit").Scan(&visible); err != nil || visible != 0 {
		t.Fatalf("windmill_user saw %d audit rows (%v), want 0 through RLS", visible, err)
	}
	_ = tx.Rollback(ctx)
	// The read-only login is not a default: it starts as itself.
	if got := sessionRole(t, f.connect(t, f.readonly.name)); got != f.readonly.name {
		t.Fatalf("read_only login starts as %q", got)
	}
	// The migration login keeps starting as the schema owner.
	if got := sessionRole(t, migration); got != f.migration.schemaOwner {
		t.Fatalf("migration login starts as %q", got)
	}

	// Re-issue conforms and keeps the setting; a login issued later gets it.
	f.runtime.customRoles = []string{f.wmAdmin, f.wmUser}
	f.runtime.defaultCustomRole = f.wmAdmin
	if err := f.manager.Ensure(ctx, f.material, f.runtime); err != nil {
		t.Fatalf("re-ensure login with a default role: %v", err)
	}
	next := f.runtime
	next.name += "_next"
	f.extraRoles = append(f.extraRoles, next.name)
	if err := f.manager.Ensure(ctx, f.material, next); err != nil {
		t.Fatal(err)
	}
	if got := sessionRole(t, f.connect(t, next.name)); got != f.wmAdmin {
		t.Fatalf("login issued later starts as %q, want %q", got, f.wmAdmin)
	}

	// Removing the default resets every login.
	roles[1].DefaultFor = nil
	if err := f.apply(t, roles); err != nil {
		t.Fatal(err)
	}
	if got := sessionRole(t, f.connect(t, f.runtime.name)); got != f.runtime.name {
		t.Fatalf("after removing the default the login starts as %q", got)
	}
	f.runtime.defaultCustomRole = ""
	if err := f.manager.Ensure(ctx, f.material, f.runtime); err != nil {
		t.Fatalf("re-ensure after removing the default: %v", err)
	}
	// Dropping the role while it is a default leaves no dangling setting.
	roles[1].DefaultFor = []managedpostgres.CredentialAccess{managedpostgres.CredentialReadWrite}
	if err := f.apply(t, roles); err != nil {
		t.Fatal(err)
	}
	if err := f.apply(t, roles[:1]); err != nil {
		t.Fatal(err)
	}
	if got := sessionRole(t, f.connect(t, next.name)); got != next.name {
		t.Fatalf("after dropping the default role the login starts as %q", got)
	}
}
