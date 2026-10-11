package neon

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

const ownerLogin = "gregale_owner"

type credentialRole struct {
	name, schemaOwner, marker, scope, database, legacy string
	access                                             managedpostgres.CredentialAccess
}

type credentialRoleManager interface {
	Ensure(context.Context, managedpostgres.CredentialMaterial, credentialRole) error
	Revoke(context.Context, managedpostgres.CredentialMaterial, credentialRole) error
	RestrictInherited(context.Context, managedpostgres.CredentialMaterial, string) error
}

type sqlCredentialRoles struct {
	// Production always uses pgx.Connect. Tests supply an isolated database.
	connect func(context.Context, string) (*pgx.Conn, error)
}

func (p *Provider) credentialRole(request managedpostgres.CredentialRequest) credentialRole {
	ref, _ := parseResourceRef(request.ProviderResourceID)
	scope := sha256.Sum256([]byte(p.organizationID + "\x00" + request.ProviderResourceID + "\x00" + p.databaseName))
	owner := sha256.Sum256([]byte(p.organizationID + "\x00" + ref.projectID + "\x00" + p.databaseName))
	identity := sha256.Sum256([]byte("sql-v1\x00" + p.organizationID + "\x00" + request.ProviderResourceID + "\x00" + request.IdentityKey + "\x00" + string(request.Access)))
	prefix := "gregale_rt_"
	switch request.Access {
	case managedpostgres.CredentialReadOnly:
		prefix = "gregale_ro_"
	case managedpostgres.CredentialDataAPI:
		prefix = "gregale_api_"
	case managedpostgres.CredentialMigration:
		prefix = "gregale_mig_"
	}
	return credentialRole{name: prefix + hex.EncodeToString(identity[:20]), schemaOwner: "gregale_schema_" + hex.EncodeToString(owner[:16]),
		scope: hex.EncodeToString(scope[:20]), marker: "gregale:credential:v1:" + hex.EncodeToString(scope[:20]) + ":" + string(request.Access),
		database: p.databaseName, access: request.Access, legacy: p.roleName(request.ProviderResourceID, request.IdentityKey)}
}

func (m *sqlCredentialRoles) connection(ctx context.Context, material managedpostgres.CredentialMaterial) (*pgx.Conn, error) {
	dsn, err := probeDSN(material)
	if err != nil {
		return nil, err
	}
	connect := m.connect
	if connect == nil {
		connect = pgx.Connect
	}
	return connect(ctx, dsn)
}

func credentialSQLError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	case errors.Is(err, managedpostgres.ErrConflict):
		return managedpostgres.ErrConflict
	case errors.Is(err, managedpostgres.ErrInvalid):
		return managedpostgres.ErrInvalid
	default:
		return managedpostgres.ErrUnavailable
	}
}

// External PostgreSQL DDL cannot bind identifiers. All names are derived by
// this adapter and quoted with pgx.Identifier; passwords contain hex only.
func roleIdentifier(name string) string { return pgx.Identifier{name}.Sanitize() }

func (m *sqlCredentialRoles) Ensure(ctx context.Context, material managedpostgres.CredentialMaterial, role credentialRole) (err error) {
	defer func() { err = credentialSQLError(err) }()
	conn, err := m.connection(ctx, material)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Schema initialization and role creation are serialized across replicas.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, role.schemaOwner); err != nil {
		return err
	}
	if err := initializeCredentialSchema(ctx, tx, role); err != nil {
		return err
	}
	info, err := credentialRoleInfo(ctx, tx, role.name)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		var entropy [32]byte
		if _, err := rand.Read(entropy[:]); err != nil {
			return err
		}
		password := "Ga!" + hex.EncodeToString(entropy[:])
		if _, err := tx.Exec(ctx, "CREATE ROLE "+roleIdentifier(role.name)+" LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '"+password+"'"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "COMMENT ON ROLE "+roleIdentifier(role.name)+" IS '"+role.marker+"'"); err != nil {
			return err
		}
		if role.access == managedpostgres.CredentialMigration {
			if _, err := tx.Exec(ctx, "GRANT "+roleIdentifier(role.schemaOwner)+" TO "+roleIdentifier(role.name)); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, "ALTER ROLE "+roleIdentifier(role.name)+" SET role TO "+roleIdentifier(role.schemaOwner)); err != nil {
				return err
			}
		}
		if role.access == managedpostgres.CredentialDataAPI {
			if _, err := tx.Exec(ctx, "ALTER ROLE "+roleIdentifier(role.name)+" SET statement_timeout = "+strconv.Itoa(api.DataAPIQueryTimeoutMS)); err != nil {
				return err
			}
		}
		info, err = credentialRoleInfo(ctx, tx, role.name)
		if err != nil {
			return err
		}
	}
	if !info.matches(role) {
		return managedpostgres.ErrConflict
	}
	if role.access == managedpostgres.CredentialReadWrite || role.access == managedpostgres.CredentialReadOnly {
		var unsafePrivileges bool
		if err := tx.QueryRow(ctx, `SELECT has_database_privilege($1, $2, 'CREATE') OR has_database_privilege($1, $2, 'TEMPORARY')
 OR has_schema_privilege($1, 'public', 'CREATE') OR EXISTS (SELECT 1 FROM pg_shdepend WHERE refclassid='pg_authid'::regclass
 AND refobjid=(SELECT oid FROM pg_roles WHERE rolname=$1) AND deptype='o')`, role.name, role.database).Scan(&unsafePrivileges); err != nil {
			return err
		}
		if unsafePrivileges {
			return managedpostgres.ErrConflict
		}
		name := roleIdentifier(role.name)
		owner := roleIdentifier(role.schemaOwner)
		queries := []string{
			"GRANT CONNECT ON DATABASE " + roleIdentifier(role.database) + " TO " + name,
			"GRANT USAGE ON SCHEMA public TO " + name,
			"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO " + name,
			"GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO " + name,
			"ALTER DEFAULT PRIVILEGES FOR ROLE " + owner + " IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO " + name,
			"ALTER DEFAULT PRIVILEGES FOR ROLE " + owner + " IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO " + name,
		}
		if role.access == managedpostgres.CredentialReadOnly {
			queries = []string{
				"GRANT CONNECT ON DATABASE " + roleIdentifier(role.database) + " TO " + name,
				"GRANT USAGE ON SCHEMA public TO " + name,
				"GRANT SELECT ON ALL TABLES IN SCHEMA public TO " + name,
				"GRANT SELECT ON ALL SEQUENCES IN SCHEMA public TO " + name,
				"ALTER DEFAULT PRIVILEGES FOR ROLE " + owner + " IN SCHEMA public GRANT SELECT ON TABLES TO " + name,
				"ALTER DEFAULT PRIVILEGES FOR ROLE " + owner + " IN SCHEMA public GRANT SELECT ON SEQUENCES TO " + name,
			}
			if err := verifyReadOnlyRole(ctx, tx, role.name); err != nil {
				return err
			}
		}
		for _, query := range queries {
			if _, err := tx.Exec(ctx, query); err != nil {
				return err
			}
		}
		// GRANT may emit only a warning when an older object's owner differs.
		// Never deliver a credential that silently lacks its promised data access.
		var missingPrivileges bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname='public' AND
 ((c.relkind IN ('r','p','v','m','f') AND (NOT has_table_privilege($1,c.oid,'SELECT') OR
 (NOT $2 AND (NOT has_table_privilege($1,c.oid,'INSERT') OR NOT has_table_privilege($1,c.oid,'UPDATE') OR NOT has_table_privilege($1,c.oid,'DELETE')))))
 OR (c.relkind='S' AND (NOT has_sequence_privilege($1,c.oid,'SELECT') OR (NOT $2 AND NOT has_sequence_privilege($1,c.oid,'USAGE'))))))`, role.name, role.access == managedpostgres.CredentialReadOnly).Scan(&missingPrivileges); err != nil {
			return err
		}
		if missingPrivileges {
			return managedpostgres.ErrConflict
		}
	}
	if role.access == managedpostgres.CredentialDataAPI {
		if err := ensureDataAPICredential(ctx, tx, role); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// Read-only is a privilege contract, not a client-overridable transaction setting.
// Reject observed direct, column, PUBLIC, function, and future-object write grants.
// These statements run against the external customer database, not Gregale's catalog.
func verifyReadOnlyRole(ctx context.Context, tx pgx.Tx, name string) error {
	var unsafe bool
	err := tx.QueryRow(ctx, `SELECT
 EXISTS (SELECT 1 FROM pg_namespace n WHERE n.nspname !~ '^pg_' AND n.nspname <> 'information_schema'
 AND has_schema_privilege($1,n.oid,'CREATE'))
 OR EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname !~ '^pg_' AND n.nspname <> 'information_schema' AND
 ((c.relkind IN ('r','p','v','m','f') AND (has_table_privilege($1,c.oid,'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
 OR has_any_column_privilege($1,c.oid,'INSERT,UPDATE,REFERENCES')))
 OR (c.relkind='S' AND has_sequence_privilege($1,c.oid,'USAGE,UPDATE'))))
 OR EXISTS (SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
 WHERE n.nspname !~ '^pg_' AND n.nspname <> 'information_schema' AND p.prosecdef AND has_function_privilege($1,p.oid,'EXECUTE'))
 OR EXISTS (SELECT 1 FROM pg_default_acl d, LATERAL aclexplode(d.defaclacl) a
 WHERE a.grantee IN (0,(SELECT oid FROM pg_roles WHERE rolname=$1))
 AND ((d.defaclobjtype='r' AND a.privilege_type IN ('INSERT','UPDATE','DELETE','TRUNCATE','REFERENCES','TRIGGER'))
 OR (d.defaclobjtype='S' AND a.privilege_type IN ('USAGE','UPDATE'))))`, name).Scan(&unsafe)
	if err != nil {
		return err
	}
	if unsafe {
		return managedpostgres.ErrConflict
	}
	return nil
}

type sqlRoleInfo struct {
	login, superuser, createDB, createRole, replication, bypassRLS bool
	inherit, inheritedMembership                                   bool
	marker                                                         string
	memberships, configuration                                     []string
}

func credentialRoleInfo(ctx context.Context, tx pgx.Tx, name string) (sqlRoleInfo, error) {
	var info sqlRoleInfo
	err := tx.QueryRow(ctx, `SELECT rolcanlogin, rolsuper, rolcreatedb, rolcreaterole, rolreplication, rolbypassrls, rolinherit,
 COALESCE(shobj_description(r.oid, 'pg_authid'), ''), COALESCE(rolconfig, ARRAY[]::text[]),
 ARRAY(SELECT parent.rolname::text FROM pg_auth_members m JOIN pg_roles parent ON parent.oid = m.roleid WHERE m.member = r.oid ORDER BY parent.rolname),
 EXISTS (SELECT 1 FROM pg_auth_members m WHERE m.member=r.oid AND COALESCE((to_jsonb(m)->>'inherit_option')::boolean, false))
 FROM pg_roles r WHERE rolname = $1`, name).Scan(&info.login, &info.superuser, &info.createDB, &info.createRole, &info.replication, &info.bypassRLS,
		&info.inherit, &info.marker, &info.configuration, &info.memberships, &info.inheritedMembership)
	return info, err
}

func (i sqlRoleInfo) matches(role credentialRole) bool {
	if !i.login || i.superuser || i.createDB || i.createRole || i.replication || i.bypassRLS || i.inherit || i.inheritedMembership || i.marker != role.marker {
		return false
	}
	if role.access == managedpostgres.CredentialMigration {
		return len(i.memberships) == 1 && i.memberships[0] == role.schemaOwner && len(i.configuration) == 1 && i.configuration[0] == "role="+role.schemaOwner
	}
	if role.access == managedpostgres.CredentialDataAPI {
		return len(i.memberships) == 0 && len(i.configuration) == 1 && i.configuration[0] == "statement_timeout="+strconv.Itoa(api.DataAPIQueryTimeoutMS)
	}
	return len(i.memberships) == 0 && len(i.configuration) == 0
}

func initializeCredentialSchema(ctx context.Context, tx pgx.Tx, role credentialRole) error {
	owner := roleIdentifier(role.schemaOwner)
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, role.schemaOwner).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		if _, err := tx.Exec(ctx, "CREATE ROLE "+owner+" NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "COMMENT ON ROLE "+owner+" IS 'gregale:schema-owner:v1'"); err != nil {
			return err
		}
	}
	info, err := credentialRoleInfo(ctx, tx, role.schemaOwner)
	if err != nil {
		return err
	}
	if info.login || info.superuser || info.createDB || info.createRole || info.replication || info.bypassRLS || info.inherit || len(info.memberships) != 0 || len(info.configuration) != 0 || info.marker != "gregale:schema-owner:v1" {
		return managedpostgres.ErrConflict
	}
	queries := []string{
		"GRANT " + owner + " TO CURRENT_USER",
		"GRANT CONNECT, CREATE ON DATABASE " + roleIdentifier(role.database) + " TO " + owner,
		"ALTER SCHEMA public OWNER TO " + owner,
		"REVOKE CREATE ON SCHEMA public FROM PUBLIC",
		"REVOKE CREATE, TEMPORARY ON DATABASE " + roleIdentifier(role.database) + " FROM PUBLIC",
		"ALTER DEFAULT PRIVILEGES FOR ROLE " + owner + " REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC",
		"ALTER DEFAULT PRIVILEGES REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC",
	}
	for _, query := range queries {
		if _, err := tx.Exec(ctx, query); err != nil {
			return err
		}
	}
	// A pre-existing SECURITY DEFINER function exposed to PUBLIC would bypass
	// the intended runtime boundary. Reject it rather than modify customer code.
	var unsafeFunction bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace,
 LATERAL aclexplode(COALESCE(p.proacl, acldefault('f', p.proowner))) a
 WHERE n.nspname = 'public' AND p.prosecdef AND a.grantee = 0 AND a.privilege_type = 'EXECUTE')`).Scan(&unsafeFunction); err != nil {
		return err
	}
	if unsafeFunction {
		return managedpostgres.ErrConflict
	}
	return nil
}

func (m *sqlCredentialRoles) Revoke(ctx context.Context, material managedpostgres.CredentialMaterial, role credentialRole) (err error) {
	defer func() { err = credentialSQLError(err) }()
	conn, err := m.connection(ctx, material)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	if err := revokeCredentialRole(ctx, conn, role); err != nil {
		return err
	}
	if role.legacy != "" {
		legacy := role
		legacy.name = role.legacy
		legacy.marker = ""
		return revokeCredentialRole(ctx, conn, legacy)
	}
	return nil
}

func revokeCredentialRole(ctx context.Context, conn *pgx.Conn, role credentialRole) error {

	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, role.schemaOwner); err != nil {
		return err
	}
	info, err := credentialRoleInfo(ctx, tx, role.name)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if role.marker != "" && info.marker != role.marker {
		return managedpostgres.ErrConflict
	}
	if _, err := tx.Exec(ctx, "ALTER ROLE "+roleIdentifier(role.name)+" NOLOGIN"); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if err := terminateCredentialSessions(ctx, conn, role.name); err != nil {
		return err
	}
	tx, err = conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, role.schemaOwner); err != nil {
		return err
	}
	var ownsObjects bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_shdepend WHERE refclassid = 'pg_authid'::regclass
 AND refobjid = (SELECT oid FROM pg_roles WHERE rolname = $1) AND deptype = 'o')`, role.name).Scan(&ownsObjects); err != nil {
		return err
	}
	if ownsObjects {
		return managedpostgres.ErrConflict
	}
	// PostgreSQL 16+ auto-grants ADMIN but not SET to a non-superuser
	// CREATEROLE owner. DROP OWNED requires SET membership. This grant is
	// confined to the guarded retirement transaction: DROP ROLE removes it,
	// and any failure rolls it back. Plain GRANT also supports PostgreSQL 14/15.
	if _, err := tx.Exec(ctx, "GRANT "+roleIdentifier(role.name)+" TO CURRENT_USER"); err != nil {
		return err
	}
	// With object ownership excluded, DROP OWNED removes grants/default ACL
	// references only. No application table can be removed by credential rotation.
	if _, err := tx.Exec(ctx, "DROP OWNED BY "+roleIdentifier(role.name)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "DROP ROLE "+roleIdentifier(role.name)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (m *sqlCredentialRoles) RestrictInherited(ctx context.Context, material managedpostgres.CredentialMaterial, scope string) (err error) {
	defer func() { err = credentialSQLError(err) }()
	conn, err := m.connection(ctx, material)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	rows, err := conn.Query(ctx, `SELECT rolname::text FROM pg_roles WHERE rolcanlogin
 AND rolname ~ '^gregale_(rt_|ro_|mig_)?[0-9a-f]{40}$'
 AND COALESCE(shobj_description(oid, 'pg_authid'), '') NOT LIKE $1`, "gregale:credential:v1:"+scope+":%")
	if err != nil {
		return err
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, name := range names {
		if !strings.HasPrefix(name, "gregale_") {
			return managedpostgres.ErrInvalid
		}
		if _, err := conn.Exec(ctx, "ALTER ROLE "+roleIdentifier(name)+" NOLOGIN"); err != nil {
			return err
		}
		if err := terminateCredentialSessions(ctx, conn, name); err != nil {
			return err
		}
	}
	return nil
}

func terminateCredentialSessions(ctx context.Context, conn *pgx.Conn, name string) error {
	var terminated bool
	if err := conn.QueryRow(ctx, `SELECT COALESCE(bool_and(pg_terminate_backend(pid)),true) FROM pg_stat_activity WHERE usename=$1 AND pid<>pg_backend_pid()`, name).Scan(&terminated); err != nil {
		return err
	}
	if !terminated {
		return managedpostgres.ErrUnavailable
	}
	return nil
}
