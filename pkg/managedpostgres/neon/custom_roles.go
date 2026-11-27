package neon

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// Custom group roles (ADR-20261116213000). They run through the platform's
// owner login, a neon_superuser member, and are marked with the database's
// credential scope so only this database's declaration manages them.

const customRoleMarkerPrefix = "gregale:custom-role:v1:"

func customRoleMarker(scope string) string { return customRoleMarkerPrefix + scope }

var _ managedpostgres.CustomRoleApplier = (*Provider)(nil)

// customRoleManager is the SQL half of ApplyCustomRoles; tests substitute it.
type customRoleManager interface {
	ApplyCustomRoles(context.Context, managedpostgres.CredentialMaterial, credentialRole, []managedpostgres.CustomRoleSpec) error
}

// ApplyCustomRoles implements managedpostgres.CustomRoleApplier.
func (p *Provider) ApplyCustomRoles(ctx context.Context, providerResourceID string, desired []managedpostgres.CustomRoleSpec) error {
	for _, spec := range desired {
		if err := managedpostgres.ValidateCustomRoleName(spec.Name); err != nil {
			return err
		}
		for _, parent := range spec.MemberOf {
			if err := managedpostgres.ValidateCustomRoleName(parent); err != nil {
				return err
			}
		}
	}
	manager, ok := p.roles.(customRoleManager)
	if !ok {
		return managedpostgres.ErrCustomRolesUnsupported
	}
	ref, err := parseResourceRef(providerResourceID)
	if err != nil {
		return err
	}
	branchID := ref.branchID
	if branchID == "" {
		branchID, err = p.defaultBranch(ctx, ref.projectID)
	}
	if err != nil {
		return err
	}
	owner, err := p.ownerCredentials(ctx, ref.projectID, branchID)
	if err != nil {
		return err
	}
	// The scope, schema owner and database are those of every credential of
	// this resource; the identity key only names the login, which is unused.
	scope := p.credentialRole(managedpostgres.CredentialRequest{ProviderResourceID: providerResourceID, IdentityKey: "custom-roles", Access: managedpostgres.CredentialMigration})
	err = manager.ApplyCustomRoles(ctx, owner, scope, desired)
	if errors.Is(err, managedpostgres.ErrBypassRLSConflict) || errors.Is(err, managedpostgres.ErrBypassRLSUnavailable) {
		return err
	}
	return credentialSQLError(err)
}

// grantCustomRoleSet grants role to member with SET but without inheritance
// on PostgreSQL 16 and later. Before 16 a membership always allows SET ROLE,
// and Gregale logins are NOINHERIT.
func grantCustomRoleSet(ctx context.Context, tx pgx.Tx, role, member string, inherit bool) error {
	var version int
	if err := tx.QueryRow(ctx, `SELECT current_setting('server_version_num')::int`).Scan(&version); err != nil {
		return err
	}
	grant := "GRANT " + roleIdentifier(role) + " TO " + roleIdentifier(member)
	if version >= 160000 {
		if inherit {
			grant += " WITH INHERIT TRUE, SET TRUE"
		} else {
			grant += " WITH INHERIT FALSE, SET TRUE"
		}
	}
	_, err := tx.Exec(ctx, grant)
	return err
}

// grantCustomRoleExecute lets a custom role run the schema owner's functions
// in public, those the migrations made and those they make later. Stock
// PostgreSQL gives them to PUBLIC; Gregale revokes that, so tools written
// for it (Windmill's set_session_context) need it granted
// (ADR-20261123112326). DROP OWNED in dropCustomRole removes both grants.
func grantCustomRoleExecute(ctx context.Context, tx pgx.Tx, owner, role string) error {
	for _, query := range []string{
		"GRANT EXECUTE ON ALL ROUTINES IN SCHEMA public TO " + roleIdentifier(role),
		"ALTER DEFAULT PRIVILEGES FOR ROLE " + roleIdentifier(owner) + " IN SCHEMA public GRANT EXECUTE ON FUNCTIONS TO " + roleIdentifier(role),
	} {
		if _, err := tx.Exec(ctx, query); err != nil {
			return err
		}
	}
	return nil
}

type customRoleRow struct {
	name, marker string
	login        bool
}

func customRolesByMarker(ctx context.Context, tx pgx.Tx) (map[string]customRoleRow, error) {
	rows, err := tx.Query(ctx, `SELECT rolname::text, COALESCE(shobj_description(oid, 'pg_authid'), ''), rolcanlogin
 FROM pg_roles WHERE COALESCE(shobj_description(oid, 'pg_authid'), '') LIKE $1`, customRoleMarkerPrefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]customRoleRow{}
	for rows.Next() {
		var row customRoleRow
		if err := rows.Scan(&row.name, &row.marker, &row.login); err != nil {
			return nil, err
		}
		out[row.name] = row
	}
	return out, rows.Err()
}

// scopeLogins lists the live logins of one access mode of the scope.
func scopeLogins(ctx context.Context, tx pgx.Tx, scope string, access managedpostgres.CredentialAccess) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT rolname::text FROM pg_roles WHERE rolcanlogin AND rolname LIKE 'gregale\_%'
 AND COALESCE(shobj_description(oid, 'pg_authid'), '') = $1 ORDER BY rolname`, "gregale:credential:v1:"+scope+":"+string(access))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// roleMembers lists the members of role with their credential markers.
func roleMembers(ctx context.Context, tx pgx.Tx, role string) (map[string]string, error) {
	rows, err := tx.Query(ctx, `SELECT member.rolname::text, COALESCE(shobj_description(member.oid, 'pg_authid'), '')
 FROM pg_auth_members m JOIN pg_roles parent ON parent.oid = m.roleid JOIN pg_roles member ON member.oid = m.member
 WHERE parent.rolname = $1`, role)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name, marker string
		if err := rows.Scan(&name, &marker); err != nil {
			return nil, err
		}
		out[name] = marker
	}
	return out, rows.Err()
}

// rolesOf lists the roles role is a direct member of.
func rolesOf(ctx context.Context, tx pgx.Tx, role string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT parent.rolname::text FROM pg_auth_members m JOIN pg_roles parent ON parent.oid = m.roleid
 JOIN pg_roles member ON member.oid = m.member WHERE member.rolname = $1`, role)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// accessLogins are the credential markers that receive a role granted to access.
func accessLogins(access managedpostgres.CredentialAccess) []managedpostgres.CredentialAccess {
	if access == managedpostgres.CredentialReadWrite {
		return []managedpostgres.CredentialAccess{managedpostgres.CredentialReadWrite, managedpostgres.CredentialSession}
	}
	return []managedpostgres.CredentialAccess{access}
}

// bypassRLSFenced reports a live Data API or Auth login of the scope.
func bypassRLSFenced(ctx context.Context, tx pgx.Tx, scope string) (bool, error) {
	var fenced bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolcanlogin
 AND COALESCE(shobj_description(oid, 'pg_authid'), '') IN ($1, $2))`,
		"gregale:credential:v1:"+scope+":"+string(managedpostgres.CredentialDataAPI),
		"gregale:credential:v1:"+scope+":"+string(managedpostgres.CredentialAuth)).Scan(&fenced)
	return fenced, err
}

// scopeHasBypassRLSRole reports a BYPASSRLS custom role of the scope.
func scopeHasBypassRLSRole(ctx context.Context, tx pgx.Tx, scope string) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolbypassrls
 AND COALESCE(shobj_description(oid, 'pg_authid'), '') = $1)`, customRoleMarker(scope)).Scan(&exists)
	return exists, err
}

func (m *sqlCredentialRoles) ApplyCustomRoles(ctx context.Context, material managedpostgres.CredentialMaterial, scope credentialRole, desired []managedpostgres.CustomRoleSpec) (err error) {
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
	// The credential lock: logins are issued under it, so the Data API/Auth
	// fence below and the one in Ensure cannot both pass.
	if _, err := tx.Exec(ctx, `SELECT pg_catalog.pg_advisory_xact_lock(pg_catalog.hashtextextended($1, 0))`, scope.schemaOwner); err != nil {
		return err
	}
	if err := initializeCredentialSchema(ctx, tx, scope); err != nil {
		return err
	}
	marker := customRoleMarker(scope.scope)
	existing, err := customRolesByMarker(ctx, tx)
	if err != nil {
		return err
	}
	wantBypass := slices.ContainsFunc(desired, func(spec managedpostgres.CustomRoleSpec) bool { return spec.BypassRLS })
	if wantBypass {
		fenced, err := bypassRLSFenced(ctx, tx, scope.scope)
		if err != nil {
			return err
		}
		if fenced {
			return managedpostgres.ErrBypassRLSConflict
		}
		var canBypass bool
		if err := tx.QueryRow(ctx, `SELECT rolbypassrls OR rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&canBypass); err != nil {
			return err
		}
		if !canBypass {
			return managedpostgres.ErrBypassRLSUnavailable
		}
	}
	declared := map[string]managedpostgres.CustomRoleSpec{}
	for _, spec := range desired {
		declared[spec.Name] = spec
	}
	// Create or correct every declared role. A role marked for another scope
	// is a copy a branch or restore carried over from its source; declaring it
	// here adopts it, data grants included.
	for _, spec := range desired {
		name := roleIdentifier(spec.Name)
		bypass := "NOBYPASSRLS"
		if spec.BypassRLS {
			bypass = "BYPASSRLS"
		}
		row, exists := existing[spec.Name]
		if !exists {
			var taken bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, spec.Name).Scan(&taken); err != nil {
				return err
			}
			if taken {
				// Someone else's role: never take it over.
				return managedpostgres.ErrConflict
			}
			if _, err := tx.Exec(ctx, "CREATE ROLE "+name+" NOLOGIN INHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION "+bypass); err != nil {
				return err
			}
		} else {
			if row.marker != marker && !strings.HasPrefix(row.marker, customRoleMarkerPrefix) {
				return managedpostgres.ErrConflict
			}
			if err := correctCustomRole(ctx, tx, spec); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, "COMMENT ON ROLE "+name+" IS '"+marker+"'"); err != nil {
			return err
		}
		if err := grantCustomRoleExecute(ctx, tx, scope.schemaOwner, spec.Name); err != nil {
			return err
		}
	}
	for _, spec := range desired {
		// Parent memberships, inherited, among this scope's custom roles.
		current, err := rolesOf(ctx, tx, spec.Name)
		if err != nil {
			return err
		}
		for _, parent := range spec.MemberOf {
			if _, ok := declared[parent]; !ok {
				return managedpostgres.ErrInvalid
			}
			if !slices.Contains(current, parent) {
				if err := grantCustomRoleSet(ctx, tx, parent, spec.Name, true); err != nil {
					return err
				}
			}
		}
		for _, parent := range current {
			if !slices.Contains(spec.MemberOf, parent) {
				if _, err := tx.Exec(ctx, "REVOKE "+roleIdentifier(parent)+" FROM "+roleIdentifier(spec.Name)); err != nil {
					return err
				}
			}
		}
		// Logins of the granted access modes, SET only.
		wanted := map[string]bool{}
		for _, access := range spec.GrantedTo {
			for _, loginAccess := range accessLogins(access) {
				logins, err := scopeLogins(ctx, tx, scope.scope, loginAccess)
				if err != nil {
					return err
				}
				for _, login := range logins {
					wanted[login] = true
				}
			}
		}
		members, err := roleMembers(ctx, tx, spec.Name)
		if err != nil {
			return err
		}
		for login := range wanted {
			if _, ok := members[login]; !ok {
				if err := grantCustomRoleSet(ctx, tx, spec.Name, login, false); err != nil {
					return err
				}
			}
		}
		for member, memberMarker := range members {
			child, isCustom := declared[member]
			switch {
			case wanted[member]:
			case isCustom && slices.Contains(child.MemberOf, spec.Name):
			case strings.HasPrefix(memberMarker, "gregale:credential:v1:") || strings.HasPrefix(memberMarker, customRoleMarkerPrefix) || memberMarker == "":
				// The owner login keeps the membership PostgreSQL gave it on
				// CREATE ROLE; everything else not declared is revoked.
				var isOwner bool
				if err := tx.QueryRow(ctx, `SELECT $1 = current_user::text`, member).Scan(&isOwner); err != nil {
					return err
				}
				if isOwner {
					continue
				}
				if _, err := tx.Exec(ctx, "REVOKE "+roleIdentifier(spec.Name)+" FROM "+roleIdentifier(member)); err != nil {
					return err
				}
			}
		}
	}
	// Default roles: every login of an access mode starts as the role
	// declared its default, or as itself.
	for _, access := range []managedpostgres.CredentialAccess{managedpostgres.CredentialReadWrite, managedpostgres.CredentialReadOnly} {
		want := ""
		for _, spec := range desired {
			if slices.Contains(spec.DefaultFor, access) && slices.Contains(spec.GrantedTo, access) {
				want = spec.Name
			}
		}
		for _, loginAccess := range accessLogins(access) {
			logins, err := scopeLogins(ctx, tx, scope.scope, loginAccess)
			if err != nil {
				return err
			}
			for _, login := range logins {
				if err := setLoginDefaultRole(ctx, tx, login, want, existing); err != nil {
					return err
				}
			}
		}
	}
	// Drop this scope's roles that are no longer declared, children first.
	var removed []string
	for name, row := range existing {
		if row.marker == marker {
			if _, ok := declared[name]; !ok {
				removed = append(removed, name)
			}
		}
	}
	slices.Sort(removed)
	for _, name := range removed {
		if err := dropCustomRole(ctx, tx, scope, name); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// correctCustomRole resets an existing custom role to its declaration.
// PostgreSQL 16 checks every attribute named in ALTER ROLE against the
// caller's own, even an unchanged one, so only differences are altered. The
// powers the owner itself lacks can only be set by someone else: refuse.
func correctCustomRole(ctx context.Context, tx pgx.Tx, spec managedpostgres.CustomRoleSpec) error {
	// A NOLOGIN role's password is unusable, so only attributes are compared
	// (pg_authid, which shows it, is superuser-only).
	var login, superuser, createDB, createRole, replication, bypass, inherit bool
	var limit, settings int
	if err := tx.QueryRow(ctx, `SELECT rolcanlogin, rolsuper, rolcreatedb, rolcreaterole, rolreplication, rolbypassrls, rolinherit,
 rolconnlimit, cardinality(COALESCE(rolconfig, ARRAY[]::text[])) FROM pg_roles WHERE rolname = $1`, spec.Name).Scan(&login, &superuser, &createDB, &createRole, &replication, &bypass, &inherit, &limit, &settings); err != nil {
		return err
	}
	if superuser || createDB || createRole || replication {
		return managedpostgres.ErrConflict
	}
	name := roleIdentifier(spec.Name)
	var changes []string
	if login {
		changes = append(changes, "NOLOGIN")
	}
	if !inherit {
		changes = append(changes, "INHERIT")
	}
	if bypass != spec.BypassRLS {
		if spec.BypassRLS {
			changes = append(changes, "BYPASSRLS")
		} else {
			changes = append(changes, "NOBYPASSRLS")
		}
	}
	if limit != -1 {
		changes = append(changes, "CONNECTION LIMIT -1")
	}
	if len(changes) > 0 {
		if _, err := tx.Exec(ctx, "ALTER ROLE "+name+" "+strings.Join(changes, " ")); err != nil {
			return err
		}
	}
	if settings > 0 {
		if _, err := tx.Exec(ctx, "ALTER ROLE "+name+" RESET ALL"); err != nil {
			return err
		}
	}
	return nil
}

// dropCustomRole hands what the role owns to the schema owner, removes its
// grants and drops it.
func dropCustomRole(ctx context.Context, tx pgx.Tx, scope credentialRole, name string) error {
	role := roleIdentifier(name)
	// DROP OWNED needs SET membership (PostgreSQL 16+ gives the creating
	// CREATEROLE owner only ADMIN); DROP ROLE removes this grant again.
	if _, err := tx.Exec(ctx, "GRANT "+role+" TO CURRENT_USER"); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "REASSIGN OWNED BY "+role+" TO "+roleIdentifier(scope.schemaOwner)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "DROP OWNED BY "+role); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, "DROP ROLE "+role)
	return err
}

// ensureLoginCustomRoles grants a freshly ensured login the declared roles
// that already exist, and fences Data API and Auth logins from a database
// with a BYPASSRLS custom role. It runs inside Ensure's transaction.
func ensureLoginCustomRoles(ctx context.Context, tx pgx.Tx, role credentialRole) error {
	if role.access == managedpostgres.CredentialDataAPI || role.access == managedpostgres.CredentialAuth {
		bypass, err := scopeHasBypassRLSRole(ctx, tx, role.scope)
		if err != nil {
			return err
		}
		if bypass {
			return managedpostgres.ErrBypassRLSConflict
		}
		return nil
	}
	existing, err := customRolesByMarker(ctx, tx)
	if err != nil {
		return err
	}
	current, err := rolesOf(ctx, tx, role.name)
	if err != nil {
		return err
	}
	marker := customRoleMarker(role.scope)
	for _, name := range role.customRoles {
		if managedpostgres.ValidateCustomRoleName(name) != nil {
			return managedpostgres.ErrInvalid
		}
		row, ok := existing[name]
		if !ok || row.marker != marker || slices.Contains(current, name) {
			continue
		}
		if err := grantCustomRoleSet(ctx, tx, name, role.name, false); err != nil {
			return err
		}
	}
	// The default role is set once it exists and is granted; until then
	// the next custom-role apply sets it.
	want := ""
	if row, ok := existing[role.defaultCustomRole]; ok && row.marker == marker && slices.Contains(role.customRoles, role.defaultCustomRole) {
		want = role.defaultCustomRole
	}
	return setLoginDefaultRole(ctx, tx, role.name, want, existing)
}

// loginDefaultRole is the custom role a login's role setting names, or ""
// when it has none.
func loginDefaultRole(ctx context.Context, tx pgx.Tx, login string, existing map[string]customRoleRow) (string, error) {
	var settings []string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(rolconfig, ARRAY[]::text[]) FROM pg_roles WHERE rolname = $1`, login).Scan(&settings); err != nil {
		return "", err
	}
	for _, setting := range settings {
		if name, ok := strings.CutPrefix(setting, "role="); ok {
			if _, custom := existing[name]; custom {
				return name, nil
			}
		}
	}
	return "", nil
}

// setLoginDefaultRole makes want ("" for none) the custom role login starts
// as. It only touches a role setting that names a custom role.
func setLoginDefaultRole(ctx context.Context, tx pgx.Tx, login, want string, existing map[string]customRoleRow) error {
	current, err := loginDefaultRole(ctx, tx, login, existing)
	if err != nil || current == want {
		return err
	}
	if want == "" {
		_, err = tx.Exec(ctx, "ALTER ROLE "+roleIdentifier(login)+" RESET role")
		return err
	}
	_, err = tx.Exec(ctx, "ALTER ROLE "+roleIdentifier(login)+" SET role TO "+roleIdentifier(want))
	return err
}
