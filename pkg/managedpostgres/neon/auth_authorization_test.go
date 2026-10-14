// adr: 1190 — organizations, roles and the shared authorization function, applied through the auth credential.
package neon

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

const authorizationSQLDir = "../../../cmd/gregale/templates/auth/sql"

// The tables and columns of GoTrue's schema that the helpers read. The
// template's end-to-end run applies the real migrations.
const goTrueStub = `CREATE TABLE auth.users (id uuid PRIMARY KEY, email varchar(255), email_confirmed_at timestamptz, banned_until timestamptz, deleted_at timestamptz);
CREATE TABLE auth.sessions (id uuid PRIMARY KEY, user_id uuid NOT NULL REFERENCES auth.users ON DELETE CASCADE, not_after timestamptz);`

type authUser struct{ id, session uuid.UUID }

func installAuthorization(t *testing.T, f *credentialFixture) (credentialRole, *pgx.Conn) {
	t.Helper()
	ctx := context.Background()
	role := f.authRole(t, "")
	if err := f.manager.Ensure(ctx, f.material, role); err != nil {
		t.Fatal(err)
	}
	owner := f.connect(t, role.name)
	executeSQL(t, owner, goTrueStub)
	// The release task applies every file in order; so does the test.
	files, err := filepath.Glob(filepath.Join(authorizationSQLDir, "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatal("no authorization SQL", err)
	}
	sort.Strings(files)
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		executeSQL(t, owner, string(body))
	}
	return role, owner
}

func newAuthUser(t *testing.T, owner *pgx.Conn, email string) authUser {
	t.Helper()
	u := authUser{id: uuid.New(), session: uuid.New()}
	executeSQL(t, owner, `INSERT INTO auth.users (id, email, email_confirmed_at) VALUES ($1, $2, now())`, u.id, email)
	executeSQL(t, owner, `INSERT INTO auth.sessions (id, user_id) VALUES ($1, $2)`, u.session, u.id)
	return u
}

func actAs(ctx context.Context, t *testing.T, conn *pgx.Conn, u authUser) {
	t.Helper()
	if _, err := conn.Exec(ctx, `SELECT set_config('request.jwt.claims', json_build_object('sub', $1::text, 'session_id', $2::text)::text, false)`, u.id.String(), u.session.String()); err != nil {
		t.Fatal(err)
	}
}

func sqlCode(t *testing.T, conn *pgx.Conn, query string, args ...any) (string, string) {
	t.Helper()
	_, err := conn.Exec(context.Background(), query, args...)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected an error from %q, got %v", query, err)
	}
	return pgErr.Code, pgErr.Message
}

func wantSQLError(t *testing.T, conn *pgx.Conn, code, message, query string, args ...any) {
	t.Helper()
	gotCode, gotMessage := sqlCode(t, conn, query, args...)
	if gotCode != code || gotMessage != message {
		t.Fatalf("%q: got %s %q, want %s %q", query, gotCode, gotMessage, code, message)
	}
}

func queryBool(t *testing.T, conn *pgx.Conn, query string, args ...any) bool {
	t.Helper()
	var value bool
	if err := conn.QueryRow(context.Background(), query, args...).Scan(&value); err != nil {
		t.Fatalf("%q: %v", query, err)
	}
	return value
}

func tokenHash(t *testing.T) []byte {
	t.Helper()
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(token[:])
	return sum[:]
}

func TestSQLAuthorizationMembershipsPoliciesAndRevocation(t *testing.T) {
	f := newCredentialFixture(t)
	ctx := context.Background()
	_, owner := installAuthorization(t, f)
	alice, bob, carol := newAuthUser(t, owner, "alice@example.test"), newAuthUser(t, owner, "bob@example.test"), newAuthUser(t, owner, "carol@example.test")

	actAs(ctx, t, owner, alice)
	var org uuid.UUID
	if err := owner.QueryRow(ctx, `SELECT id FROM gregale_auth.create_organization('acme', 'Acme')`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	wantSQLError(t, owner, "23505", "slug_taken", `SELECT gregale_auth.create_organization('acme', 'Again')`)
	wantSQLError(t, owner, "22023", "invalid_organization", `SELECT gregale_auth.create_organization('Bad Slug', 'x')`)
	bobInvite, carolInvite := tokenHash(t), tokenHash(t)
	executeSQL(t, owner, `SELECT gregale_auth.invite($1, ' Bob@Example.test ', 'member', $2, 3600)`, org, bobInvite)
	executeSQL(t, owner, `SELECT gregale_auth.invite($1, 'carol@example.test', 'member', $2, 3600)`, org, carolInvite)

	// Invitations bind to the invited, confirmed address, and work once.
	actAs(ctx, t, owner, bob)
	wantSQLError(t, owner, "42501", "invitation_email_mismatch", `SELECT gregale_auth.accept_invitation($1)`, carolInvite)
	executeSQL(t, owner, `SELECT gregale_auth.accept_invitation($1)`, bobInvite)
	wantSQLError(t, owner, "P0002", "invitation_invalid", `SELECT gregale_auth.accept_invitation($1)`, bobInvite)
	wantSQLError(t, owner, "42501", "forbidden", `SELECT gregale_auth.invite($1, 'dave@example.test', 'member', $2, 3600)`, org, tokenHash(t))
	wantSQLError(t, owner, "42501", "forbidden", `SELECT gregale_auth.list_invitations($1)`, org)
	var members int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM gregale_auth.list_members($1)`, org).Scan(&members); err != nil || members != 2 {
		t.Fatalf("members visible to a member: %d %v", members, err)
	}

	// The helpers answer for the caller's own session, through a read-only
	// login that the credential verifier still accepts.
	if err := f.manager.Ensure(ctx, f.material, f.readonly); err != nil {
		t.Fatalf("read-only issuance next to authorization helpers: %v", err)
	}
	reader := f.connect(t, f.readonly.name)
	actAs(ctx, t, reader, bob)
	resource := "org/" + org.String() + "/doc/42"
	if !queryBool(t, reader, `SELECT gregale_auth.has_permission($1, 'org:read')`, org) || queryBool(t, reader, `SELECT gregale_auth.has_permission($1, 'members:manage')`, org) {
		t.Fatal("member permissions wrong")
	}
	if queryBool(t, reader, `SELECT gregale_auth.authorize('read', $1)`, resource) {
		t.Fatal("authorize allowed an action no policy grants")
	}
	actAs(ctx, t, owner, alice)
	executeSQL(t, owner, `SELECT gregale_auth.apply_authz($1)`, `{"roles":{"editor":["doc:write","org:read"]},"policies":[{"action":"read","resource":"doc","permission":"org:read"},{"action":"write","resource":"doc","permission":"doc:write"}]}`)
	if !queryBool(t, reader, `SELECT gregale_auth.authorize('read', $1)`, resource) || queryBool(t, reader, `SELECT gregale_auth.authorize('write', $1)`, resource) {
		t.Fatal("policy evaluation wrong for a member")
	}
	for _, malformed := range []string{"org/" + org.String() + "/doc", "org/not-a-uuid/doc/1", "team/" + org.String() + "/doc/1", ""} {
		if queryBool(t, reader, `SELECT gregale_auth.authorize('read', $1)`, malformed) {
			t.Fatalf("authorize allowed malformed resource %q", malformed)
		}
	}
	actAs(ctx, t, reader, carol)
	if queryBool(t, reader, `SELECT gregale_auth.authorize('read', $1)`, resource) {
		t.Fatal("non-member authorized")
	}
	deniedSQL(t, reader, `SELECT * FROM gregale_auth.memberships`)
	deniedSQL(t, reader, `SELECT gregale_auth.create_organization('evil', 'Evil')`)

	// Role changes take effect on the next check; so does revocation.
	executeSQL(t, owner, `SELECT gregale_auth.set_member_role($1, $2, 'editor')`, org, bob.id)
	actAs(ctx, t, reader, bob)
	if !queryBool(t, reader, `SELECT gregale_auth.authorize('write', $1)`, resource) {
		t.Fatal("role change not effective")
	}
	wantSQLError(t, owner, "23503", "role_in_use", `SELECT gregale_auth.apply_authz('{}')`)
	wantSQLError(t, owner, "22023", "owner_role_fixed", `SELECT gregale_auth.apply_authz('{"roles":{"owner":["org:read"]}}')`)
	executeSQL(t, owner, `DELETE FROM auth.sessions WHERE id = $1`, bob.session)
	if queryBool(t, reader, `SELECT gregale_auth.has_permission($1, 'org:read')`, org) {
		t.Fatal("revoked session kept its permissions")
	}
	executeSQL(t, owner, `INSERT INTO auth.sessions (id, user_id) VALUES ($1, $2)`, bob.session, bob.id)
	executeSQL(t, owner, `UPDATE auth.users SET banned_until = now() + interval '1 day' WHERE id = $1`, bob.id)
	if queryBool(t, reader, `SELECT gregale_auth.has_permission($1, 'org:read')`, org) {
		t.Fatal("banned user kept its permissions")
	}
	executeSQL(t, owner, `UPDATE auth.users SET banned_until = NULL WHERE id = $1`, bob.id)

	// Ownership rules: no last-owner removal, only owners touch owners.
	wantSQLError(t, owner, "23514", "last_owner", `SELECT gregale_auth.set_member_role($1, $2, 'member')`, org, alice.id)
	wantSQLError(t, owner, "23514", "last_owner", `SELECT gregale_auth.remove_member($1, $2)`, org, alice.id)
	actAs(ctx, t, owner, bob)
	wantSQLError(t, owner, "42501", "forbidden", `SELECT gregale_auth.remove_member($1, $2)`, org, alice.id)
	executeSQL(t, owner, `SELECT gregale_auth.remove_member($1, $2)`, org, bob.id)
	actAs(ctx, t, owner, carol)
	wantSQLError(t, owner, "42501", "forbidden", `SELECT gregale_auth.list_members($1)`, org)
	executeSQL(t, owner, `SELECT set_config('request.jwt.claims', '{}', false)`)
	wantSQLError(t, owner, "28000", "not_authenticated", `SELECT gregale_auth.list_organizations()`)
}

func TestSQLAuthorizationHelpersServeDataAPIRowLevelSecurity(t *testing.T) {
	f := newCredentialFixture(t)
	ctx := context.Background()
	_, owner := installAuthorization(t, f)
	alice, bob := newAuthUser(t, owner, "alice@example.test"), newAuthUser(t, owner, "bob@example.test")
	actAs(ctx, t, owner, alice)
	var org uuid.UUID
	if err := owner.QueryRow(ctx, `SELECT id FROM gregale_auth.create_organization('acme', 'Acme')`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	api := f.runtime
	api.name = strings.Replace(api.name, "gregale_rt_", "gregale_api_", 1)
	api.marker = strings.TrimSuffix(api.marker, ":read_write") + ":data_api"
	api.access = managedpostgres.CredentialDataAPI
	f.extraRoles = append(f.extraRoles, api.name)
	if err := f.manager.Ensure(ctx, f.material, api); err != nil {
		t.Fatalf("data API issuance next to authorization helpers: %v", err)
	}
	if err := f.manager.Ensure(ctx, f.material, f.migration); err != nil {
		t.Fatal(err)
	}
	executeSQL(t, f.connect(t, f.migration.name), `CREATE TABLE api.docs (id int PRIMARY KEY, org_id uuid NOT NULL, body text);
ALTER TABLE api.docs ENABLE ROW LEVEL SECURITY;
CREATE POLICY org_read ON api.docs USING (gregale_auth.authorize('read', 'org/' || org_id || '/doc/' || id));
INSERT INTO api.docs VALUES (1, '`+org.String()+`', 'secret plans');`)
	executeSQL(t, owner, `SELECT gregale_auth.apply_authz('{"policies":[{"action":"read","resource":"doc","permission":"org:read"}]}')`)
	reader := f.connect(t, api.name)
	count := func(u authUser) int {
		actAs(ctx, t, reader, u)
		var n int
		if err := reader.QueryRow(ctx, `SELECT count(*) FROM api.docs`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count(alice) != 1 || count(bob) != 0 {
		t.Fatal("row-level security through authorize() is wrong")
	}
}

func TestSQLReadOnlyVerifierRejectsUnmarkedOrWritableDefiners(t *testing.T) {
	for _, tc := range []struct{ name, create string }{
		{"volatile helper", `CREATE FUNCTION gregale_auth.sneaky() RETURNS int LANGUAGE sql VOLATILE SECURITY DEFINER AS 'SELECT 1'; COMMENT ON FUNCTION gregale_auth.sneaky() IS 'gregale:auth-helper:v1'`},
		{"unmarked helper", `CREATE FUNCTION gregale_auth.sneaky() RETURNS int LANGUAGE sql STABLE SECURITY DEFINER AS 'SELECT 1'; COMMENT ON FUNCTION gregale_auth.sneaky() IS 'something else'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCredentialFixture(t)
			_, owner := installAuthorization(t, f)
			executeSQL(t, owner, tc.create+`; GRANT EXECUTE ON FUNCTION gregale_auth.sneaky() TO PUBLIC`)
			if err := f.manager.Ensure(context.Background(), f.material, f.readonly); !errors.Is(err, managedpostgres.ErrConflict) {
				t.Fatalf("read-only issuance accepted %s: %v", tc.name, err)
			}
		})
	}
}

func TestSQLSubjectAuthorizationForRealtimeIsOwnerOnlyAndUserLevel(t *testing.T) {
	f := newCredentialFixture(t)
	ctx := context.Background()
	_, owner := installAuthorization(t, f)
	alice, bob := newAuthUser(t, owner, "alice@example.test"), newAuthUser(t, owner, "bob@example.test")
	actAs(ctx, t, owner, alice)
	var org uuid.UUID
	if err := owner.QueryRow(ctx, `SELECT id FROM gregale_auth.create_organization('acme', 'Acme')`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	executeSQL(t, owner, `SELECT gregale_auth.apply_authz('{"policies":[{"action":"realtime:read","resource":"room","permission":"org:read"}]}')`)
	executeSQL(t, owner, `SELECT set_config('request.jwt.claims', '', false)`)
	resource := "org/" + org.String() + "/room/lobby"
	check := func(u authUser) bool {
		return queryBool(t, owner, `SELECT gregale_auth.authorize_subject($1, 'realtime:read', $2)`, u.id, resource)
	}
	if !check(alice) || check(bob) {
		t.Fatal("subject authorization wrong for member and non-member")
	}
	if queryBool(t, owner, `SELECT gregale_auth.authorize_subject($1, 'realtime:write', $2)`, alice.id, resource) {
		t.Fatal("an action without a policy was allowed")
	}
	// Signing out everywhere or a ban ends realtime grants at the next check.
	executeSQL(t, owner, `UPDATE auth.users SET banned_until = now() + interval '1 hour' WHERE id = $1`, alice.id)
	if check(alice) {
		t.Fatal("banned user authorized")
	}
	executeSQL(t, owner, `UPDATE auth.users SET banned_until = NULL WHERE id = $1`, alice.id)
	executeSQL(t, owner, `DELETE FROM auth.sessions WHERE user_id = $1`, alice.id)
	if check(alice) {
		t.Fatal("user without a live session authorized")
	}
	// The function trusts its subject argument, so no other login may call it.
	if err := f.manager.Ensure(ctx, f.material, f.readonly); err != nil {
		t.Fatal(err)
	}
	reader := f.connect(t, f.readonly.name)
	deniedSQL(t, reader, `SELECT gregale_auth.authorize_subject('`+bob.id.String()+`', 'realtime:read', '`+resource+`')`)
	deniedSQL(t, reader, `SELECT gregale_auth.user_active('`+bob.id.String()+`')`)
}
