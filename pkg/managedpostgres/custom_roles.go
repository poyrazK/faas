package managedpostgres

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

// Custom group roles (ADR-20261116213000). A customer declares NOLOGIN roles
// on one managed database and grants them to that database's own Gregale
// logins; the platform's admin path creates them. Tools such as Windmill,
// whose migrations expect group roles a superuser made beforehand, then run
// with ordinary Gregale credentials.

// CustomRoleState is the apply state of one declared role.
type CustomRoleState string

const (
	// CustomRolePending is declared but not yet applied to the database.
	CustomRolePending CustomRoleState = "pending"
	// CustomRoleReady matches the database as of the last apply.
	CustomRoleReady CustomRoleState = "ready"
	// CustomRoleDeleting is removed from the declaration; the row goes away
	// once the role has been dropped from the database.
	CustomRoleDeleting CustomRoleState = "deleting"
	// CustomRoleDeleted is never stored: a delete reports it once the role
	// has been dropped and its row removed.
	CustomRoleDeleted CustomRoleState = "deleted"
)

// CustomRolesMax bounds the declared roles of one database.
const CustomRolesMax = 16

// CustomRole is one declared group role of a database.
type CustomRole struct {
	AccountID  string
	DatabaseID string
	Name       string
	// BypassRLS is the only role attribute a customer may ask for.
	BypassRLS bool
	// GrantedTo lists the access modes whose logins may SET ROLE to it:
	// migration, read_write (session logins too) and read_only.
	GrantedTo []CredentialAccess
	// MemberOf lists other custom roles of the same database this role is a
	// member of, with inheritance (Windmill grants windmill_user to
	// windmill_admin).
	MemberOf []string
	// DefaultFor lists the access modes (read_write, read_only; a subset of
	// GrantedTo) whose logins start every session as this role, as if each
	// session began with SET ROLE. At most one role per access mode of a
	// database. Tools whose own connection must bypass row-level security
	// (Windmill) use it with a BYPASSRLS role; poolers refuse a role set in
	// the connection string.
	DefaultFor []CredentialAccess
	State      CustomRoleState
	// Revision increases with every change, so an apply only settles the
	// declaration it read.
	Revision     int64
	LastError    string
	AttemptCount int
	RetryAt      time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// CustomRoleSpec is the provider's view of one desired role.
type CustomRoleSpec struct {
	Name       string
	BypassRLS  bool
	GrantedTo  []CredentialAccess
	MemberOf   []string
	DefaultFor []CredentialAccess
}

// CustomRoleApplier is an optional provider capability. ApplyCustomRoles
// makes the database's custom roles exactly desired: it creates and alters
// the declared roles, grants them to every existing login of their access
// modes and to their parent roles, revokes grants no longer declared, and
// drops custom roles of this database that are no longer declared after
// reassigning what they own to the schema owner. It must be idempotent and
// refuse (ErrBypassRLSConflict) a BYPASSRLS role while the database holds a
// Data API or Auth login, under the same lock that issues those logins.
type CustomRoleApplier interface {
	ApplyCustomRoles(ctx context.Context, providerResourceID string, desired []CustomRoleSpec) error
}

// ErrBypassRLSConflict refuses BYPASSRLS on a database that uses Data API or
// Auth, and those features on a database that holds a BYPASSRLS role: both
// rely on row-level security.
var ErrBypassRLSConflict = fmt.Errorf("%w: BYPASSRLS custom roles cannot coexist with Data API or Auth on one database", pgerrors.ErrConflict)

// ErrBypassRLSUnavailable means the backend cannot create BYPASSRLS roles on
// this database (for example a Neon project from before 2023-08-15).
var ErrBypassRLSUnavailable = fmt.Errorf("%w: this database cannot create BYPASSRLS roles", pgerrors.ErrUnsupported)

// ErrCustomRoleDefaultTaken refuses a DefaultFor access mode another
// declared role is already the default role of.
var ErrCustomRoleDefaultTaken = fmt.Errorf("%w: another custom role is already the default role of that access mode", pgerrors.ErrConflict)

// ErrCustomRolesUnsupported means the database's backend has no custom-role
// support.
var ErrCustomRolesUnsupported = fmt.Errorf("%w: this database's backend does not support custom roles", pgerrors.ErrUnsupported)

var customRoleNamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// reservedCustomRolePrefixes belong to PostgreSQL, Gregale and the providers.
var reservedCustomRolePrefixes = []string{"gregale", "pg_", "neon", "cloudsql", "rds", "azure", "supabase", "cron_"}

// reservedCustomRoleNames are PostgreSQL keywords and roles every cluster has.
var reservedCustomRoleNames = []string{"public", "postgres", "none", "current_user", "current_role", "session_user", "user", "all", "authenticator", "anon", "authenticated", "service_role"}

// ValidateCustomRoleName explains a refused role name.
func ValidateCustomRoleName(name string) error {
	if !customRoleNamePattern.MatchString(name) {
		return fmt.Errorf("%w: role name %q must start with a lowercase letter or underscore and use at most 63 lowercase letters, digits and underscores", ErrInvalid, name)
	}
	for _, prefix := range reservedCustomRolePrefixes {
		if strings.HasPrefix(name, prefix) {
			return fmt.Errorf("%w: role name %q uses the reserved prefix %q", ErrInvalid, name, prefix)
		}
	}
	if slices.Contains(reservedCustomRoleNames, name) {
		return fmt.Errorf("%w: role name %q is reserved", ErrInvalid, name)
	}
	return nil
}

// customRoleGrantable lists the access modes a custom role can be granted to.
var customRoleGrantable = []CredentialAccess{CredentialMigration, CredentialReadOnly, CredentialReadWrite}

// NormalizeCustomRoleGrants validates, deduplicates and orders access modes.
// session is accepted as read_write, whose privileges it carries.
func NormalizeCustomRoleGrants(grants []CredentialAccess) ([]CredentialAccess, error) {
	seen := map[CredentialAccess]bool{}
	for _, grant := range grants {
		grant = grant.Privileges()
		if !slices.Contains(customRoleGrantable, grant) {
			return nil, fmt.Errorf("%w: custom roles can be granted to migration, read_write and read_only logins, not %q", ErrInvalid, grant)
		}
		seen[grant] = true
	}
	out := make([]CredentialAccess, 0, len(seen))
	for grant := range seen {
		out = append(out, grant)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// normalizeNames validates, deduplicates and orders role names.
func normalizeNames(names []string) ([]string, error) {
	seen := map[string]bool{}
	for _, name := range names {
		if err := ValidateCustomRoleName(name); err != nil {
			return nil, err
		}
		seen[name] = true
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// grantsTo reports whether logins of access receive the role.
func (r CustomRole) grantsTo(access CredentialAccess) bool {
	return slices.Contains(r.GrantedTo, access.Privileges())
}

func (r CustomRole) spec() CustomRoleSpec {
	return CustomRoleSpec{Name: r.Name, BypassRLS: r.BypassRLS, GrantedTo: slices.Clone(r.GrantedTo), MemberOf: slices.Clone(r.MemberOf), DefaultFor: slices.Clone(r.DefaultFor)}
}

// customRoleDefaultable lists the access modes a custom role can be the
// default role of. A migration login already starts as the schema owner.
var customRoleDefaultable = []CredentialAccess{CredentialReadOnly, CredentialReadWrite}

// NormalizeCustomRoleDefaults validates, deduplicates and orders the access
// modes a role is the default of; each must be one it is granted to.
func NormalizeCustomRoleDefaults(defaults, grants []CredentialAccess) ([]CredentialAccess, error) {
	seen := map[CredentialAccess]bool{}
	for _, access := range defaults {
		access = access.Privileges()
		if !slices.Contains(customRoleDefaultable, access) {
			return nil, fmt.Errorf("%w: a custom role can be the default role of read_write and read_only logins, not %q", ErrInvalid, access)
		}
		if !slices.Contains(grants, access) {
			return nil, fmt.Errorf("%w: a custom role can be the default role only of logins it is granted to; grant it to %q first", ErrInvalid, access)
		}
		seen[access] = true
	}
	out := make([]CredentialAccess, 0, len(seen))
	for access := range seen {
		out = append(out, access)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// customRoleDefaultTaken names another declared role that is already the
// default of one of role's DefaultFor access modes.
func customRoleDefaultTaken(roles []CustomRole, role CustomRole) string {
	for _, other := range roles {
		if other.State == CustomRoleDeleting || other.Name == role.Name {
			continue
		}
		for _, access := range role.DefaultFor {
			if slices.Contains(other.DefaultFor, access) {
				return other.Name
			}
		}
	}
	return ""
}

// CustomRoleDefaultFor names the declared role logins of access start as,
// or "" for none.
func CustomRoleDefaultFor(roles []CustomRole, access CredentialAccess) string {
	for _, role := range roles {
		if role.State != CustomRoleDeleting && role.grantsTo(access) && slices.Contains(role.DefaultFor, access.Privileges()) {
			return role.Name
		}
	}
	return ""
}

// DesiredCustomRoles is the declaration a provider applies: every role not
// being deleted, ordered so parents come before members.
func DesiredCustomRoles(roles []CustomRole) []CustomRoleSpec {
	byName := map[string]CustomRole{}
	for _, role := range roles {
		if role.State != CustomRoleDeleting {
			byName[role.Name] = role
		}
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]CustomRoleSpec, 0, len(names))
	visited := map[string]bool{}
	var visit func(string)
	visit = func(name string) {
		role, ok := byName[name]
		if !ok || visited[name] {
			return
		}
		visited[name] = true
		for _, parent := range role.MemberOf {
			visit(parent)
		}
		out = append(out, role.spec())
	}
	for _, name := range names {
		visit(name)
	}
	return out
}

// CustomRoleGrants is a sorted, comma-separated list of custom role names.
// It is a string so CredentialRequest stays comparable.
type CustomRoleGrants string

// NewCustomRoleGrants sorts and joins role names.
func NewCustomRoleGrants(names ...string) CustomRoleGrants {
	sorted := slices.Clone(names)
	sort.Strings(sorted)
	return CustomRoleGrants(strings.Join(slices.Compact(sorted), ","))
}

// Names splits the list; empty yields none.
func (g CustomRoleGrants) Names() []string {
	if g == "" {
		return nil
	}
	return strings.Split(string(g), ",")
}

// CustomRoleGrantsFor names the declared roles a login of access receives.
func CustomRoleGrantsFor(roles []CustomRole, access CredentialAccess) CustomRoleGrants {
	var out []string
	for _, role := range roles {
		if role.State != CustomRoleDeleting && role.grantsTo(access) {
			out = append(out, role.Name)
		}
	}
	return NewCustomRoleGrants(out...)
}

// customRoleCycle reports whether making name a member of parents would make
// a role a member of itself.
func customRoleCycle(roles []CustomRole, name string, parents []string) bool {
	edges := map[string][]string{}
	for _, role := range roles {
		if role.State != CustomRoleDeleting && role.Name != name {
			edges[role.Name] = role.MemberOf
		}
	}
	edges[name] = parents
	visiting := map[string]bool{}
	done := map[string]bool{}
	var walk func(string) bool
	walk = func(node string) bool {
		if visiting[node] {
			return true
		}
		if done[node] {
			return false
		}
		visiting[node] = true
		for _, next := range edges[node] {
			if walk(next) {
				return true
			}
		}
		visiting[node] = false
		done[node] = true
		return false
	}
	return walk(name)
}

// CustomRoleDatabase names one database whose declaration needs applying.
type CustomRoleDatabase struct {
	AccountID  string
	DatabaseID string
}

// CustomRoleStore persists declarations. Save and Delete serialize with
// binding reservations on the database row, so the Data API/Auth fence and
// the role limit hold under concurrency.
type CustomRoleStore interface {
	ListCustomRoles(ctx context.Context, accountID, databaseID string) ([]CustomRole, error)
	// SaveCustomRole inserts or replaces one role as pending. It refuses
	// with ErrBypassRLSConflict a BYPASSRLS role on a database with a live
	// Data API or Auth binding, ErrQuotaExceeded past limit roles,
	// ErrInvalid a MemberOf name that is not a declared role, and
	// ErrCustomRoleDefaultTaken a DefaultFor access mode another role is
	// the default of.
	SaveCustomRole(ctx context.Context, role CustomRole, limit int, now time.Time) (CustomRole, error)
	// DeleteCustomRole marks the role deleting. A role another role is a
	// member of is refused with ErrConflict.
	DeleteCustomRole(ctx context.Context, accountID, databaseID, name string, now time.Time) (CustomRole, error)
	// FinishCustomRoleApply settles the revisions an apply read: pending
	// rows at those revisions become ready, deleting rows are removed.
	FinishCustomRoleApply(ctx context.Context, accountID, databaseID string, applied map[string]int64, now time.Time) error
	// FailCustomRoleApply records a failed apply on every unsettled row.
	FailCustomRoleApply(ctx context.Context, accountID, databaseID, code string, retryAt, now time.Time) error
	// DueCustomRoleDatabases lists databases with unsettled rows due now.
	DueCustomRoleDatabases(ctx context.Context, limit int, now time.Time) ([]CustomRoleDatabase, error)
	// HasBypassRLSCustomRole reports a declared BYPASSRLS role.
	HasBypassRLSCustomRole(ctx context.Context, accountID, databaseID string) (bool, error)
}
