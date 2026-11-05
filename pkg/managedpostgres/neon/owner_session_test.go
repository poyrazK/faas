package neon

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestOwnerDSNPinsSearchPath(t *testing.T) {
	material := managedpostgres.CredentialMaterial{Username: ownerLogin, Password: "secret", Database: "app", TLSMode: "require",
		Endpoints: []managedpostgres.Endpoint{{Role: managedpostgres.EndpointPooled, Host: "pooled.invalid", Port: 5432}, {Role: managedpostgres.EndpointDirect, Host: "direct.invalid", Port: 5432}}}
	var dialed string
	roles := &sqlCredentialRoles{connect: func(_ context.Context, dsn string) (*pgx.Conn, error) {
		dialed = dsn
		return nil, errors.New("not dialing")
	}}
	if _, err := roles.connection(context.Background(), material); err == nil {
		t.Fatal("connection succeeded without a server")
	}
	config, err := pgx.ParseConfig(dialed)
	if err != nil {
		t.Fatal(err)
	}
	if config.RuntimeParams["search_path"] != ownerSearchPath || config.Host != "direct.invalid" || config.User != ownerLogin {
		t.Fatalf("owner session = host %s user %s params %v; want the direct endpoint with search_path %q", config.Host, config.User, config.RuntimeParams, ownerSearchPath)
	}
}

// dsnRuntimeConnect dials the fixture database with the startup parameters
// the owner DSN carries, so a test exercises the session the provider opens.
func dsnRuntimeConnect(config *pgx.ConnConfig) func(context.Context, string) (*pgx.Conn, error) {
	return func(ctx context.Context, dsn string) (*pgx.Conn, error) {
		parsed, err := pgx.ParseConfig(dsn)
		if err != nil {
			return nil, err
		}
		c := config.Copy()
		for key, value := range parsed.RuntimeParams {
			c.RuntimeParams[key] = value
		}
		return pgx.ConnectConfig(ctx, c)
	}
}

// The tenant's migration login owns schema public. A better-matching
// overload planted there must not run in the owner's session.
func TestOwnerSessionIgnoresTenantOverloadsInPublic(t *testing.T) {
	f := newCredentialFixture(t)
	ctx := context.Background()
	for _, role := range []credentialRole{f.runtime, f.migration} {
		if err := f.manager.Ensure(ctx, f.material, role); err != nil {
			t.Fatal(err)
		}
	}
	// RestrictInherited calls shobj_description(oid, 'pg_authid'); for an
	// untyped literal PostgreSQL prefers a (oid, text) overload over
	// pg_catalog's (oid, name).
	if _, err := f.admin.Exec(ctx, `CREATE FUNCTION public.shobj_description(oid, text) RETURNS text LANGUAGE plpgsql
 AS $$BEGIN RAISE EXCEPTION 'tenant code ran as %', current_user; END$$`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.admin.Exec(ctx, `ALTER FUNCTION public.shobj_description(oid, text) OWNER TO `+roleIdentifier(f.runtime.schemaOwner)); err != nil {
		t.Fatal(err)
	}
	// Control: the planted overload does run in a session with the default
	// search_path, so the assertion below is meaningful.
	plain := &sqlCredentialRoles{connect: func(ctx context.Context, _ string) (*pgx.Conn, error) { return pgx.ConnectConfig(ctx, f.config.Copy()) }}
	if err := plain.RestrictInherited(ctx, f.material, f.runtime.scope); err == nil {
		t.Fatal("control: the planted overload did not run with the default search_path")
	}
	pinned := &sqlCredentialRoles{connect: dsnRuntimeConnect(f.config)}
	if err := pinned.RestrictInherited(ctx, f.material, f.runtime.scope); err != nil {
		t.Fatalf("owner session ran the tenant's overload: %v", err)
	}
}

// A restored branch keeps the source's live-query login in its catalog; the
// restore disables it, and keeps the target scope's own live login.
func TestSQLCredentialRestoreDisablesInheritedLiveLogin(t *testing.T) {
	f := newCredentialFixture(t)
	ctx := context.Background()
	if err := f.manager.Ensure(ctx, f.material, f.runtime); err != nil {
		t.Fatal(err)
	}
	source := strings.ReplaceAll(uuid.NewString()+uuid.NewString(), "-", "")[:40]
	for _, scope := range []string{source, f.runtime.scope} {
		name := "gregale_live_" + scope
		f.extraRoles = append(f.extraRoles, name)
		if _, err := f.admin.Exec(ctx, "CREATE ROLE "+roleIdentifier(name)+" LOGIN"); err != nil {
			t.Fatal(err)
		}
		if _, err := f.admin.Exec(ctx, "COMMENT ON ROLE "+roleIdentifier(name)+" IS 'gregale:live:v1:"+scope+"'"); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.manager.RestrictInherited(ctx, f.material, f.runtime.scope); err != nil {
		t.Fatal(err)
	}
	for scope, want := range map[string]bool{source: false, f.runtime.scope: true} {
		var login bool
		if err := f.admin.QueryRow(ctx, `SELECT rolcanlogin FROM pg_roles WHERE rolname=$1`, "gregale_live_"+scope).Scan(&login); err != nil || login != want {
			t.Fatalf("live login for scope %s: login=%v err=%v, want login=%v", scope, login, err, want)
		}
	}
}

// The provider runs refresh-rotation DML as the schema owner. Anything the
// tenant can attach to those tables that runs code on DELETE or UPDATE (a
// trigger, a rule, a cascading foreign key, a constraint or index calling its
// own function) could RESET ROLE back to the owner login, so the table check
// refuses it.
func TestRefreshRotationTablesRefuseTenantCode(t *testing.T) {
	f := newCredentialFixture(t)
	ctx := context.Background()
	if err := f.manager.Ensure(ctx, f.material, f.migration); err != nil {
		t.Fatal(err)
	}
	owner := roleIdentifier(f.migration.schemaOwner)
	install := `CREATE SCHEMA gregale_data_api AUTHORIZATION ` + owner + `;
SET ROLE ` + owner + `;
CREATE TABLE gregale_data_api.refresh_rotation_bindings(
 binding_id uuid NOT NULL,scope text NOT NULL,version integer NOT NULL,source_role text NOT NULL,sql_deadline boolean NOT NULL,
 paused boolean NOT NULL,token_hash text,PRIMARY KEY(binding_id,scope),
 CHECK(version=1),CHECK((paused AND token_hash ~ '^[a-f0-9]{64}$' AND token_hash IS NOT NULL) OR (NOT paused AND token_hash IS NULL)));
CREATE TABLE gregale_data_api.refresh_rotation_members(
 role_name text PRIMARY KEY,role_oid oid NOT NULL,binding_id uuid NOT NULL,scope text NOT NULL,fingerprint text NOT NULL,catalog_witness text NOT NULL,rpcs oid[] NOT NULL,
 FOREIGN KEY(binding_id,scope) REFERENCES gregale_data_api.refresh_rotation_bindings(binding_id,scope),
 CHECK(fingerprint ~ '^[a-f0-9]{64}$'),CHECK(catalog_witness ~ '^[a-f0-9]{64}$'));
CREATE FUNCTION public.tenant_fn(text) RETURNS boolean LANGUAGE sql IMMUTABLE AS 'SELECT true';
CREATE FUNCTION public.tenant_trigger() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN RETURN NEW; END';
RESET ROLE;`
	if _, err := f.admin.Exec(ctx, install); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, ddl string
	}{
		{"installer tables", ""},
		{"trigger", `CREATE TRIGGER t BEFORE UPDATE ON gregale_data_api.refresh_rotation_bindings FOR EACH ROW EXECUTE FUNCTION public.tenant_trigger()`},
		{"rule", `CREATE RULE r AS ON DELETE TO gregale_data_api.refresh_rotation_members DO ALSO SELECT 1`},
		{"check calling a tenant function", `ALTER TABLE gregale_data_api.refresh_rotation_bindings ADD CONSTRAINT c CHECK (public.tenant_fn(source_role))`},
		{"index calling a tenant function", `CREATE INDEX i ON gregale_data_api.refresh_rotation_bindings (public.tenant_fn(source_role))`},
		{"foreign key from a tenant table", `CREATE TABLE public.child(b uuid, s text, FOREIGN KEY (b,s) REFERENCES gregale_data_api.refresh_rotation_bindings ON DELETE CASCADE)`},
		{"cascading member key", `ALTER TABLE gregale_data_api.refresh_rotation_members DROP CONSTRAINT refresh_rotation_members_binding_id_scope_fkey,
 ADD FOREIGN KEY(binding_id,scope) REFERENCES gregale_data_api.refresh_rotation_bindings(binding_id,scope) ON DELETE CASCADE`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := f.admin.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+owner); err != nil {
				t.Fatal(err)
			}
			if tc.ddl != "" {
				if _, err := tx.Exec(ctx, tc.ddl); err != nil {
					t.Fatal(err)
				}
			}
			err = verifyRefreshRotationTables(ctx, tx, f.migration.schemaOwner)
			switch {
			case tc.ddl == "" && err != nil:
				t.Fatalf("installer tables refused: %v", err)
			case tc.ddl != "" && !errors.Is(err, managedpostgres.ErrConflict):
				t.Fatalf("verify = %v, want ErrConflict", err)
			}
		})
	}
}
