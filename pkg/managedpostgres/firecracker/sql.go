package firecracker

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// The admin batches below run inside the database VM as the superuser
// through schedd's DatabaseAdmin (psql, stop at first error). They are
// composed from validated identifiers and verifiers only; no other input
// reaches the SQL text.

// schemaOwner owns schema public and every object migrations create
// (ADR-461's privilege model).
const schemaOwner = "gregale_schema_owner"

var (
	identifierPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)
	rolePattern       = regexp.MustCompile(`^gregale_(rt|ro|mig|api)_[0-9a-f]{40}$`)
	verifierPattern   = regexp.MustCompile(`^SCRAM-SHA-256\$[0-9]+:[A-Za-z0-9+/=]+\$[A-Za-z0-9+/=]+:[A-Za-z0-9+/=]+$`)
	scopePattern      = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// dataAPISchema is the one schema Data API logins reach (ADR-650).
const dataAPISchema = "api"

// credentialScope names a database's credentials in their role markers,
// "gregale:credential:v1:<scope>:<access>", the same on every role of the
// database. The Data API starter's migrations find the data_api login of
// their own migration login's scope through it.
func credentialScope(resourceID, database string) string {
	sum := sha256.Sum256([]byte("firecracker-scope-v1\x00" + resourceID + "\x00" + database))
	return hex.EncodeToString(sum[:20])
}

// credentialRoleName derives a stable role per database, identity and
// access, so issuing again for the same binding generation rotates the
// same role.
func credentialRoleName(resourceID, identityKey string, access managedpostgres.CredentialAccess) string {
	prefix := "gregale_rt_"
	switch access {
	case managedpostgres.CredentialReadOnly:
		prefix = "gregale_ro_"
	case managedpostgres.CredentialMigration:
		prefix = "gregale_mig_"
	case managedpostgres.CredentialDataAPI:
		prefix = "gregale_api_"
	}
	sum := sha256.Sum256([]byte("firecracker-v1\x00" + resourceID + "\x00" + identityKey + "\x00" + string(access)))
	return prefix + hex.EncodeToString(sum[:20])
}

// initSQL creates the schema owner and the customer database and locks
// down the defaults: customers only reach their own database, and nobody
// but the schema owner creates objects in it. Safe to repeat.
func initSQL(database string) (string, error) {
	if !identifierPattern.MatchString(database) || database == "postgres" {
		return "", fmt.Errorf("firecracker: invalid database name %q", database)
	}
	return fmt.Sprintf(`DO $gregale$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '%[2]s') THEN
    CREATE ROLE %[2]s NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
  END IF;
END $gregale$;
SELECT 'CREATE DATABASE %[1]s OWNER %[2]s' WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = '%[1]s') \gexec
REVOKE CONNECT ON DATABASE postgres FROM PUBLIC;
REVOKE CONNECT ON DATABASE template1 FROM PUBLIC;
\connect %[1]s
BEGIN;
ALTER SCHEMA public OWNER TO %[2]s;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
REVOKE CONNECT, CREATE, TEMPORARY ON DATABASE %[1]s FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE %[2]s REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC;
COMMIT;
`, database, schemaOwner), nil
}

// credentialSQL creates or re-keys a login role with a SCRAM verifier (the
// password itself never enters the VM), marks it with the database's
// credential scope and grants its access level. Runtime, read-only and
// Data API roles must end without any way to create objects; the closing
// assertion fails the batch (SQLSTATE P0001) otherwise.
func credentialSQL(database, role, scope string, access managedpostgres.CredentialAccess, verifier string) (string, error) {
	if !identifierPattern.MatchString(database) || !rolePattern.MatchString(role) || !scopePattern.MatchString(scope) || !verifierPattern.MatchString(verifier) {
		return "", fmt.Errorf("firecracker: invalid credential parameters")
	}
	var b strings.Builder
	fmt.Fprintf(&b, `BEGIN;
SELECT pg_advisory_xact_lock(hashtextextended('gregale-credentials', 0));
DO $gregale$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '%[1]s') THEN
    ALTER ROLE %[1]s WITH LOGIN PASSWORD '%[2]s';
  ELSE
    CREATE ROLE %[1]s LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '%[2]s';
  END IF;
END $gregale$;
COMMENT ON ROLE %[1]s IS 'gregale:credential:v1:%[4]s:%[5]s';
`, role, verifier, database, scope, access)
	if access == managedpostgres.CredentialDataAPI {
		// Its only settings, the Data API's statement and lock deadlines: the
		// Data API starter refuses a login with others.
		fmt.Fprintf(&b, "ALTER ROLE %s RESET ALL;\nALTER ROLE %[1]s SET statement_timeout = %d;\nALTER ROLE %[1]s SET lock_timeout = %d;\n",
			role, api.DataAPIQueryTimeoutMS, api.DataAPILockTimeoutMS)
	}
	fmt.Fprintf(&b, `COMMIT;
\connect %[2]s
BEGIN;
GRANT CONNECT ON DATABASE %[2]s TO %[1]s;
`, role, database)
	switch access {
	case managedpostgres.CredentialMigration:
		fmt.Fprintf(&b, "GRANT %[2]s TO %[1]s;\nALTER ROLE %[1]s SET role TO %[2]s;\n", role, schemaOwner)
	case managedpostgres.CredentialReadOnly:
		fmt.Fprintf(&b, `GRANT USAGE ON SCHEMA public TO %[1]s;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO %[1]s;
GRANT SELECT ON ALL SEQUENCES IN SCHEMA public TO %[1]s;
ALTER DEFAULT PRIVILEGES FOR ROLE %[2]s IN SCHEMA public GRANT SELECT ON TABLES TO %[1]s;
ALTER DEFAULT PRIVILEGES FOR ROLE %[2]s IN SCHEMA public GRANT SELECT ON SEQUENCES TO %[1]s;
`, role, schemaOwner)
	case managedpostgres.CredentialReadWrite:
		fmt.Fprintf(&b, `GRANT USAGE ON SCHEMA public TO %[1]s;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO %[1]s;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO %[1]s;
ALTER DEFAULT PRIVILEGES FOR ROLE %[2]s IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %[1]s;
ALTER DEFAULT PRIVILEGES FOR ROLE %[2]s IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO %[1]s;
`, role, schemaOwner)
	case managedpostgres.CredentialDataAPI:
		fmt.Fprintf(&b, `DO $gregale$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = '%[3]s') THEN
    CREATE SCHEMA %[3]s AUTHORIZATION %[2]s;
  ELSIF (SELECT nspowner FROM pg_namespace WHERE nspname = '%[3]s') <> (SELECT oid FROM pg_roles WHERE rolname = '%[2]s') THEN
    RAISE EXCEPTION 'schema %[3]s has another owner';
  END IF;
END $gregale$;
REVOKE ALL ON SCHEMA %[3]s FROM PUBLIC;
GRANT USAGE ON SCHEMA %[3]s TO %[1]s;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA %[3]s TO %[1]s;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA %[3]s TO %[1]s;
ALTER DEFAULT PRIVILEGES FOR ROLE %[2]s IN SCHEMA %[3]s GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO %[1]s;
ALTER DEFAULT PRIVILEGES FOR ROLE %[2]s IN SCHEMA %[3]s GRANT USAGE, SELECT ON SEQUENCES TO %[1]s;
`, role, schemaOwner, dataAPISchema)
	default:
		return "", managedpostgres.ErrUnsupported
	}
	if access == managedpostgres.CredentialDataAPI {
		// Data API logins reach the api schema's rows and nothing else:
		// no memberships, no object creation, no rows outside api, no
		// owned objects and no SECURITY DEFINER function to run.
		fmt.Fprintf(&b, `DO $gregale$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_auth_members m JOIN pg_roles r ON r.oid = m.member WHERE r.rolname = '%[1]s')
     OR has_database_privilege('%[1]s', current_database(), 'CREATE,TEMPORARY')
     OR EXISTS (SELECT 1 FROM pg_namespace n WHERE n.nspname !~ '^pg_' AND n.nspname <> 'information_schema' AND has_schema_privilege('%[1]s', n.oid, 'CREATE'))
     OR EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname NOT IN ('%[2]s', 'information_schema') AND n.nspname !~ '^pg_'
                AND c.relkind IN ('r', 'p', 'v', 'm', 'f') AND has_table_privilege('%[1]s', c.oid, 'SELECT,INSERT,UPDATE,DELETE'))
     OR EXISTS (SELECT 1 FROM pg_shdepend WHERE refclassid = 'pg_authid'::regclass AND refobjid = (SELECT oid FROM pg_roles WHERE rolname = '%[1]s') AND deptype = 'o')
     OR EXISTS (SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace WHERE n.nspname IN ('%[2]s', 'public') AND p.prosecdef AND has_function_privilege('%[1]s', p.oid, 'EXECUTE')) THEN
    RAISE EXCEPTION 'data api role reaches beyond schema %[2]s';
  END IF;
END $gregale$;
`, role, dataAPISchema)
	}
	if access != managedpostgres.CredentialMigration {
		fmt.Fprintf(&b, `DO $gregale$ BEGIN
  IF has_database_privilege('%[1]s', '%[2]s', 'CREATE') OR has_database_privilege('%[1]s', '%[2]s', 'TEMPORARY')
     OR has_schema_privilege('%[1]s', 'public', 'CREATE') THEN
    RAISE EXCEPTION 'credential role can create objects';
  END IF;
END $gregale$;
`, role, database)
	}
	b.WriteString("COMMIT;\n")
	return b.String(), nil
}

// revokeSQL disables a role's login and ends its sessions. The role is
// kept so objects it might own keep an owner.
func revokeSQL(role string) (string, error) {
	if !rolePattern.MatchString(role) {
		return "", fmt.Errorf("firecracker: invalid role %q", role)
	}
	return fmt.Sprintf(`DO $gregale$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '%[1]s') THEN
    ALTER ROLE %[1]s NOLOGIN;
  END IF;
END $gregale$;
SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = '%[1]s';
`, role), nil
}
