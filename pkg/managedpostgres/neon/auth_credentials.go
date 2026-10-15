package neon

import (
	"context"
	"errors"
	"slices"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// authSchemas are the only schemas an ADR-1190 auth owner may own or create
// in: GoTrue's tables and Gregale's organization and policy tables.
var authSchemas = []string{"auth", "gregale_auth"}

const authOwnerMarker = "gregale:auth-owner:v1"

// authHelperSQL matches an ADR-1190 authorization helper for pg_proc p and
// pg_namespace n: a SECURITY DEFINER function that RLS policies and Data API
// logins call to read the caller's memberships and session. It must live in
// gregale_auth, be owned by that schema's marked auth owner, carry the helper
// marker, and be STABLE or IMMUTABLE SQL or PL/pgSQL. PostgreSQL runs such
// functions read-only (it rejects data-modifying statements in non-volatile
// SQL and PL/pgSQL), so executing one cannot give a read-only login writes.
const authHelperSQL = `(n.nspname = 'gregale_auth' AND p.proowner = n.nspowner
 AND COALESCE(shobj_description(n.nspowner, 'pg_authid'), '') = '` + authOwnerMarker + `'
 AND COALESCE(obj_description(p.oid, 'pg_proc'), '') = 'gregale:auth-helper:v1'
 AND p.provolatile IN ('s', 'i')
 AND p.prolang IN (SELECT oid FROM pg_language WHERE lanname IN ('sql', 'plpgsql')))`

// GoTrue enables RLS on every table it creates, which a DML-only login cannot
// pass. The auth login therefore acts as a dedicated NOLOGIN owner of the
// auth schemas, the way migration logins act as the customer schema owner.
// That owner never joins the customer schema owner and gets no database
// CREATE, so the serving workload cannot reach or alter customer schemas.
func ensureAuthOwner(ctx context.Context, tx pgx.Tx, role credentialRole) error {
	owner := roleIdentifier(role.authOwner)
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, role.authOwner).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		if _, err := tx.Exec(ctx, "CREATE ROLE "+owner+" NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "COMMENT ON ROLE "+owner+" IS '"+authOwnerMarker+"'"); err != nil {
			return err
		}
	}
	info, err := credentialRoleInfo(ctx, tx, role.authOwner)
	if err != nil {
		return err
	}
	if info.login || info.superuser || info.createDB || info.createRole || info.replication || info.bypassRLS || info.inherit || len(info.memberships) != 0 || len(info.configuration) != 0 || info.marker != authOwnerMarker {
		return managedpostgres.ErrConflict
	}
	queries := []string{
		"GRANT " + owner + " TO CURRENT_USER",
		"GRANT CONNECT ON DATABASE " + roleIdentifier(role.database) + " TO " + owner,
		// GoTrue's helper functions must not become PUBLIC-executable; the
		// read-only and Data API verifiers reject such grants database-wide.
		"ALTER DEFAULT PRIVILEGES FOR ROLE " + owner + " REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC",
	}
	for _, query := range queries {
		if _, err := tx.Exec(ctx, query); err != nil {
			return err
		}
	}
	for _, schema := range authSchemas {
		var schemaOwner string
		err := tx.QueryRow(ctx, `SELECT r.rolname::text FROM pg_namespace n JOIN pg_roles r ON r.oid = n.nspowner WHERE n.nspname = $1`, schema).Scan(&schemaOwner)
		if errors.Is(err, pgx.ErrNoRows) {
			if _, err := tx.Exec(ctx, "CREATE SCHEMA "+roleIdentifier(schema)+" AUTHORIZATION "+owner); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		// An existing auth schema belongs to someone else, such as a
		// customer's own table set. Never adopt or alter it.
		if schemaOwner != role.authOwner {
			return managedpostgres.ErrConflict
		}
	}
	return nil
}

func authLoginSettings(role credentialRole) []string {
	return []string{"role=" + role.authOwner, "search_path=auth", "statement_timeout=" + strconv.Itoa(api.AuthStatementTimeoutMS)}
}

// The login's session defaults make every connection act as the auth owner
// and create GoTrue's unqualified schema_migrations table inside auth.
func configureAuthLogin(ctx context.Context, tx pgx.Tx, role credentialRole) error {
	name := roleIdentifier(role.name)
	for _, query := range []string{
		"GRANT " + roleIdentifier(role.authOwner) + " TO " + name,
		"ALTER ROLE " + name + " SET role TO " + roleIdentifier(role.authOwner),
		"ALTER ROLE " + name + " SET search_path TO auth",
		"ALTER ROLE " + name + " SET statement_timeout = " + strconv.Itoa(api.AuthStatementTimeoutMS),
	} {
		if _, err := tx.Exec(ctx, query); err != nil {
			return err
		}
	}
	return nil
}

func sameSettings(observed, expected []string) bool {
	observed, expected = slices.Clone(observed), slices.Clone(expected)
	slices.Sort(observed)
	slices.Sort(expected)
	return slices.Equal(observed, expected)
}

// verifyAuthCredential fails closed when the auth owner or login could reach
// anything outside the auth schemas, or when an unexpected role could act as
// the auth owner. These statements run against the customer database.
func verifyAuthCredential(ctx context.Context, tx pgx.Tx, role credentialRole) error {
	var unsafe bool
	err := tx.QueryRow(ctx, `WITH owner AS (SELECT oid FROM pg_roles WHERE rolname = $1),
 outside AS (SELECT oid FROM pg_namespace WHERE nspname <> ALL($3::text[]) AND nspname !~ '^pg_' AND nspname <> 'information_schema')
 SELECT
 has_database_privilege($1, current_database(), 'CREATE,TEMPORARY')
 OR has_database_privilege($2, current_database(), 'CREATE,TEMPORARY')
 OR EXISTS (SELECT 1 FROM outside n WHERE has_schema_privilege($1, n.oid, 'CREATE') OR has_schema_privilege($2, n.oid, 'CREATE'))
 OR EXISTS (SELECT 1 FROM pg_class c WHERE c.relnamespace IN (SELECT oid FROM outside) AND c.relkind IN ('r','p','v','m','f')
  AND (has_table_privilege($1, c.oid, 'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
  OR has_table_privilege($2, c.oid, 'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')))
 OR EXISTS (SELECT 1 FROM pg_namespace n WHERE n.nspowner = (SELECT oid FROM owner) AND n.nspname <> ALL($3::text[]))
 OR EXISTS (SELECT 1 FROM pg_class c WHERE c.relowner = (SELECT oid FROM owner) AND c.relnamespace IN (SELECT oid FROM outside))
 OR EXISTS (SELECT 1 FROM pg_proc p WHERE p.proowner = (SELECT oid FROM owner) AND p.pronamespace IN (SELECT oid FROM outside))
 OR EXISTS (SELECT 1 FROM pg_type t WHERE t.typowner = (SELECT oid FROM owner) AND t.typnamespace IN (SELECT oid FROM outside))
 OR EXISTS (SELECT 1 FROM pg_shdepend WHERE refclassid = 'pg_authid'::regclass AND refobjid = (SELECT oid FROM pg_roles WHERE rolname = $2) AND deptype = 'o')
 OR EXISTS (SELECT 1 FROM pg_auth_members m JOIN pg_roles member ON member.oid = m.member
  WHERE m.roleid = (SELECT oid FROM owner) AND member.rolname <> current_user AND member.rolname !~ '^gregale_auth_[0-9a-f]{40}$')`,
		role.authOwner, role.name, authSchemas).Scan(&unsafe)
	if err != nil {
		return err
	}
	if unsafe {
		return managedpostgres.ErrConflict
	}
	return nil
}
