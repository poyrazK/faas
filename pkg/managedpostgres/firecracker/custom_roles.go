package firecracker

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// Custom group roles (ADR-20261116213000). The admin batches run as the
// database VM's superuser. Every name in them is a validated custom-role
// name (lowercase letters, digits and underscores) or a scope in hex, so
// the SQL text holds no customer-controlled quoting.

const customRoleMarkerPrefix = "gregale:custom-role:v1:"

// sqlstateBypassRLSConflict is raised by the batches' Data API fence.
const sqlstateBypassRLSConflict = "GRB01"

var _ managedpostgres.CustomRoleApplier = (*Provider)(nil)

func sqlTextArray(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, "'"+value+"'")
	}
	return "ARRAY[" + strings.Join(quoted, ",") + "]::text[]"
}

// grantSetSQL grants role to member, SET only (inherit for role-to-role
// memberships), on any PostgreSQL major.
func grantSetSQL(role, member string, inherit bool) string {
	option := "FALSE"
	if inherit {
		option = "TRUE"
	}
	return fmt.Sprintf(`IF current_setting('server_version_num')::int >= 160000 THEN
      EXECUTE format('GRANT %%I TO %%I WITH INHERIT %[3]s, SET TRUE', %[1]s, %[2]s);
    ELSE
      EXECUTE format('GRANT %%I TO %%I', %[1]s, %[2]s);
    END IF;`, role, member, option)
}

func validCustomRoleSpecs(desired []managedpostgres.CustomRoleSpec) error {
	declared := map[string]bool{}
	for _, spec := range desired {
		if err := managedpostgres.ValidateCustomRoleName(spec.Name); err != nil || !identifierPattern.MatchString(spec.Name) {
			return managedpostgres.ErrInvalid
		}
		declared[spec.Name] = true
	}
	for _, spec := range desired {
		for _, parent := range spec.MemberOf {
			if !declared[parent] {
				return managedpostgres.ErrInvalid
			}
		}
		for _, access := range spec.GrantedTo {
			if access != managedpostgres.CredentialMigration && access != managedpostgres.CredentialReadOnly && access != managedpostgres.CredentialReadWrite {
				return managedpostgres.ErrInvalid
			}
		}
		for _, access := range spec.DefaultFor {
			if (access != managedpostgres.CredentialReadOnly && access != managedpostgres.CredentialReadWrite) || !slices.Contains(spec.GrantedTo, access) {
				return managedpostgres.ErrInvalid
			}
		}
	}
	return nil
}

// defaultRoleSQL makes every login whose credential marker is in markers
// start as want (” for none). It only replaces or resets a role setting
// that names a custom role, so the migration login's schema-owner setting
// is never touched.
func defaultRoleSQL(markers []string, want string) string {
	return fmt.Sprintf(`DO $gregale$ DECLARE r record; cur text; BEGIN
  FOR r IN SELECT rolname::text AS name, COALESCE(rolconfig, ARRAY[]::text[]) AS cfg FROM pg_roles
      WHERE rolcanlogin AND COALESCE(shobj_description(oid, 'pg_authid'), '') = ANY(%[1]s) LOOP
    SELECT substr(c, 6) INTO cur FROM unnest(r.cfg) AS c WHERE c LIKE 'role=%%'
      AND EXISTS (SELECT 1 FROM pg_roles p WHERE p.rolname = substr(c, 6) AND COALESCE(shobj_description(p.oid, 'pg_authid'), '') LIKE '%[2]s%%') LIMIT 1;
    IF '%[3]s' <> '' AND cur IS DISTINCT FROM '%[3]s' THEN
      EXECUTE format('ALTER ROLE %%I SET role TO %%I', r.name, '%[3]s');
    ELSIF '%[3]s' = '' AND cur IS NOT NULL THEN
      EXECUTE format('ALTER ROLE %%I RESET role', r.name);
    END IF;
  END LOOP;
END $gregale$;
`, sqlTextArray(markers), customRoleMarkerPrefix, want)
}

// customRolesSQL makes the database's custom roles exactly desired.
func customRolesSQL(database, scope string, desired []managedpostgres.CustomRoleSpec) (string, error) {
	if !identifierPattern.MatchString(database) || !scopePattern.MatchString(scope) || validCustomRoleSpecs(desired) != nil {
		return "", fmt.Errorf("firecracker: invalid custom role parameters")
	}
	marker := customRoleMarkerPrefix + scope
	credential := "gregale:credential:v1:" + scope + ":"
	var b strings.Builder
	fmt.Fprintf(&b, `\connect %[1]s
BEGIN;
SELECT pg_advisory_xact_lock(hashtextextended('gregale-credentials', 0));
`, database)
	if slices.ContainsFunc(desired, func(spec managedpostgres.CustomRoleSpec) bool { return spec.BypassRLS }) {
		fmt.Fprintf(&b, `DO $gregale$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolcanlogin AND COALESCE(shobj_description(oid, 'pg_authid'), '') IN ('%[1]sdata_api', '%[1]sauth')) THEN
    RAISE EXCEPTION 'bypassrls custom role on a data api database' USING ERRCODE = '%[2]s';
  END IF;
END $gregale$;
`, credential, sqlstateBypassRLSConflict)
	}
	names := make([]string, 0, len(desired))
	for _, spec := range desired {
		names = append(names, spec.Name)
		bypass := "NOBYPASSRLS"
		if spec.BypassRLS {
			bypass = "BYPASSRLS"
		}
		fmt.Fprintf(&b, `DO $gregale$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '%[1]s') THEN
    CREATE ROLE %[1]s NOLOGIN INHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION %[2]s;
  ELSIF COALESCE(shobj_description((SELECT oid FROM pg_roles WHERE rolname = '%[1]s'), 'pg_authid'), '') NOT LIKE '%[3]s%%' THEN
    RAISE EXCEPTION 'role %[1]s is not a custom role';
  END IF;
END $gregale$;
ALTER ROLE %[1]s NOLOGIN INHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION %[2]s CONNECTION LIMIT -1 PASSWORD NULL;
ALTER ROLE %[1]s RESET ALL;
COMMENT ON ROLE %[1]s IS '%[4]s';
GRANT EXECUTE ON ALL ROUTINES IN SCHEMA public TO %[1]s;
ALTER DEFAULT PRIVILEGES FOR ROLE %[5]s IN SCHEMA public GRANT EXECUTE ON FUNCTIONS TO %[1]s;
`, spec.Name, bypass, customRoleMarkerPrefix, marker, schemaOwner)
	}
	for _, spec := range desired {
		var markers, children []string
		for _, access := range spec.GrantedTo {
			markers = append(markers, credential+string(access))
			if access == managedpostgres.CredentialReadWrite {
				markers = append(markers, credential+string(managedpostgres.CredentialSession))
			}
			if access == managedpostgres.CredentialReadOnly {
				markers = append(markers, credential+string(managedpostgres.CredentialReplicaOnly))
			}
		}
		for _, other := range desired {
			if slices.Contains(other.MemberOf, spec.Name) {
				children = append(children, other.Name)
			}
		}
		fmt.Fprintf(&b, `DO $gregale$ DECLARE r record; p text; BEGIN
  FOR r IN SELECT parent.rolname::text AS name FROM pg_auth_members m JOIN pg_roles parent ON parent.oid = m.roleid
      JOIN pg_roles member ON member.oid = m.member WHERE member.rolname = '%[1]s' AND NOT (parent.rolname::text = ANY(%[2]s)) LOOP
    EXECUTE format('REVOKE %%I FROM %%I', r.name, '%[1]s');
  END LOOP;
  FOREACH p IN ARRAY %[2]s LOOP
    IF NOT EXISTS (SELECT 1 FROM pg_auth_members m JOIN pg_roles parent ON parent.oid = m.roleid JOIN pg_roles member ON member.oid = m.member
        WHERE member.rolname = '%[1]s' AND parent.rolname = p) THEN
    %[5]s
    END IF;
  END LOOP;
  FOR r IN SELECT rolname::text AS name FROM pg_roles WHERE rolcanlogin AND COALESCE(shobj_description(oid, 'pg_authid'), '') = ANY(%[3]s)
      AND NOT EXISTS (SELECT 1 FROM pg_auth_members m JOIN pg_roles parent ON parent.oid = m.roleid WHERE parent.rolname = '%[1]s' AND m.member = pg_roles.oid) LOOP
    %[6]s
  END LOOP;
  FOR r IN SELECT member.rolname::text AS name, COALESCE(shobj_description(member.oid, 'pg_authid'), '') AS marker FROM pg_auth_members m
      JOIN pg_roles parent ON parent.oid = m.roleid JOIN pg_roles member ON member.oid = m.member WHERE parent.rolname = '%[1]s' LOOP
    IF (r.marker LIKE 'gregale:credential:v1:%%' AND NOT (r.marker = ANY(%[3]s)))
       OR (r.marker LIKE '%[7]s%%' AND NOT (r.name = ANY(%[4]s))) THEN
      EXECUTE format('REVOKE %%I FROM %%I', '%[1]s', r.name);
    END IF;
  END LOOP;
END $gregale$;
`, spec.Name, sqlTextArray(spec.MemberOf), sqlTextArray(markers), sqlTextArray(children),
			grantSetSQL("p", "'"+spec.Name+"'", true), grantSetSQL("'"+spec.Name+"'", "r.name", false), customRoleMarkerPrefix)
	}
	for _, access := range []managedpostgres.CredentialAccess{managedpostgres.CredentialReadWrite, managedpostgres.CredentialReadOnly} {
		want := ""
		for _, spec := range desired {
			if slices.Contains(spec.DefaultFor, access) {
				want = spec.Name
			}
		}
		markers := []string{credential + string(access)}
		if access == managedpostgres.CredentialReadWrite {
			markers = append(markers, credential+string(managedpostgres.CredentialSession))
		}
		if access == managedpostgres.CredentialReadOnly {
			markers = append(markers, credential+string(managedpostgres.CredentialReplicaOnly))
		}
		b.WriteString(defaultRoleSQL(markers, want))
	}
	fmt.Fprintf(&b, `DO $gregale$ DECLARE r record; BEGIN
  FOR r IN SELECT rolname::text AS name FROM pg_roles WHERE COALESCE(shobj_description(oid, 'pg_authid'), '') = '%[1]s'
      AND NOT (rolname::text = ANY(%[2]s)) ORDER BY rolname LOOP
    EXECUTE format('REASSIGN OWNED BY %%I TO %%I', r.name, '%[3]s');
    EXECUTE format('DROP OWNED BY %%I', r.name);
    EXECUTE format('DROP ROLE %%I', r.name);
  END LOOP;
END $gregale$;
COMMIT;
`, marker, sqlTextArray(names), schemaOwner)
	return b.String(), nil
}

// customRoleLoginSQL returns the batch parts around credentialSQL for one
// login: before it, the fence that refuses a Data API login on a database
// with a BYPASSRLS custom role (psql stops at the first error, so no login is
// created); after it, the grants of the declared roles that already exist.
func customRoleLoginSQL(database, role, scope string, access managedpostgres.CredentialAccess, customRoles []string, defaultRole string) (string, string, error) {
	if !identifierPattern.MatchString(database) || !rolePattern.MatchString(role) || !scopePattern.MatchString(scope) {
		return "", "", fmt.Errorf("firecracker: invalid custom role login parameters")
	}
	marker := customRoleMarkerPrefix + scope
	if access == managedpostgres.CredentialDataAPI || access == managedpostgres.CredentialAuth {
		return fmt.Sprintf(`DO $gregale$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolbypassrls AND COALESCE(shobj_description(oid, 'pg_authid'), '') = '%[1]s') THEN
    RAISE EXCEPTION 'data api login on a database with a bypassrls custom role' USING ERRCODE = '%[2]s';
  END IF;
END $gregale$;
`, marker, sqlstateBypassRLSConflict), "", nil
	}
	if len(customRoles) == 0 {
		return "", "", nil
	}
	for _, name := range customRoles {
		if managedpostgres.ValidateCustomRoleName(name) != nil || !identifierPattern.MatchString(name) {
			return "", "", fmt.Errorf("firecracker: invalid custom role %q", name)
		}
	}
	if defaultRole != "" && !slices.Contains(customRoles, defaultRole) {
		return "", "", fmt.Errorf("firecracker: default role %q is not granted", defaultRole)
	}
	// The default role is set once it exists; until then the next
	// custom-role apply sets it.
	setDefault := fmt.Sprintf(`DO $gregale$ BEGIN
  IF '%[1]s' <> '' AND EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '%[1]s' AND COALESCE(shobj_description(oid, 'pg_authid'), '') = '%[2]s') THEN
    EXECUTE format('ALTER ROLE %%I SET role TO %%I', '%[3]s', '%[1]s');
  END IF;
END $gregale$;
`, defaultRole, marker, role)
	return "", fmt.Sprintf(`\connect %[1]s
DO $gregale$ DECLARE n text; BEGIN
  FOREACH n IN ARRAY %[2]s LOOP
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = n AND COALESCE(shobj_description(oid, 'pg_authid'), '') = '%[3]s')
       AND NOT EXISTS (SELECT 1 FROM pg_auth_members m JOIN pg_roles parent ON parent.oid = m.roleid JOIN pg_roles member ON member.oid = m.member
           WHERE parent.rolname = n AND member.rolname = '%[4]s') THEN
    %[5]s
    END IF;
  END LOOP;
END $gregale$;
`, database, sqlTextArray(customRoles), marker, role, grantSetSQL("n", "'"+role+"'", false)) + setDefault, nil
}

// ApplyCustomRoles implements managedpostgres.CustomRoleApplier.
func (p *Provider) ApplyCustomRoles(ctx context.Context, providerResourceID string, desired []managedpostgres.CustomRoleSpec) error {
	if providerResourceID == "" {
		return managedpostgres.ErrInvalid
	}
	batch, err := customRolesSQL(p.cfg.Database, credentialScope(providerResourceID, p.cfg.Database), desired)
	if err != nil {
		return managedpostgres.ErrInvalid
	}
	return p.admin(ctx, providerResourceID, batch)
}
