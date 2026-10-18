// adr: 1190 — organizations, roles and the shared authorization function, applied through the auth credential.
package neon

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
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
const goTrueStub = `CREATE TABLE auth.users (id uuid PRIMARY KEY, email varchar(255), email_confirmed_at timestamptz, phone text, phone_confirmed_at timestamptz, banned_until timestamptz, deleted_at timestamptz, raw_app_meta_data jsonb, is_anonymous boolean NOT NULL DEFAULT false, is_sso_user boolean NOT NULL DEFAULT false, role varchar(255), created_at timestamptz DEFAULT now());
CREATE TABLE auth.identities (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES auth.users ON DELETE CASCADE, provider text NOT NULL, provider_id text NOT NULL);
CREATE TABLE auth.sso_providers (id uuid PRIMARY KEY);
CREATE TABLE auth.sso_domains (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), sso_provider_id uuid NOT NULL REFERENCES auth.sso_providers ON DELETE CASCADE, domain text NOT NULL);
CREATE TABLE auth.sessions (id uuid PRIMARY KEY, user_id uuid NOT NULL REFERENCES auth.users ON DELETE CASCADE, not_after timestamptz, aal text, created_at timestamptz DEFAULT now(), updated_at timestamptz DEFAULT now(), refreshed_at timestamp, user_agent text, ip inet, oauth_client_id uuid);
CREATE TABLE auth.audit_log_entries (instance_id uuid, id uuid PRIMARY KEY, payload json, created_at timestamptz, ip_address varchar(64) NOT NULL DEFAULT '');`

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

// TestSQLUserSessionsListAndEndOnlyTheCallersOwn pins sql/0013: a person
// lists their first-party sessions and ends any but the current one.
func TestSQLUserSessionsListAndEndOnlyTheCallersOwn(t *testing.T) {
	f := newCredentialFixture(t)
	ctx := context.Background()
	_, owner := installAuthorization(t, f)
	alice, bob := newAuthUser(t, owner, "alice@example.test"), newAuthUser(t, owner, "bob@example.test")
	phone, client := uuid.New(), uuid.New()
	executeSQL(t, owner, `INSERT INTO auth.sessions (id, user_id, user_agent, ip, refreshed_at) VALUES ($1, $2, 'Mozilla/5.0 (iPhone)', '203.0.113.7', (now() + interval '1 minute') AT TIME ZONE 'UTC')`, phone, alice.id)
	executeSQL(t, owner, `INSERT INTO auth.sessions (id, user_id, oauth_client_id) VALUES ($1, $2, gen_random_uuid())`, client, alice.id)
	actAs(ctx, t, owner, alice)
	rows, err := owner.Query(ctx, `SELECT id, ip, current FROM gregale_auth.list_sessions()`)
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for rows.Next() {
		var id uuid.UUID
		var ip *string
		var current bool
		if err := rows.Scan(&id, &ip, &current); err != nil {
			t.Fatal(err)
		}
		listed = append(listed, fmt.Sprintf("%s %v %v", id, ip != nil && *ip == "203.0.113.7", current))
	}
	rows.Close()
	// Most recently active first; the OAuth client's session is not listed.
	if want := []string{phone.String() + " true false", alice.session.String() + " false true"}; !reflect.DeepEqual(listed, want) {
		t.Fatalf("listed %v, want %v", listed, want)
	}
	wantSQLError(t, owner, "22023", "current_session", `SELECT gregale_auth.end_session($1)`, alice.session)
	wantSQLError(t, owner, "P0002", "not_found", `SELECT gregale_auth.end_session($1)`, bob.session)
	wantSQLError(t, owner, "P0002", "not_found", `SELECT gregale_auth.end_session($1)`, client)
	executeSQL(t, owner, `SELECT gregale_auth.end_session($1)`, phone)
	if queryBool(t, owner, `SELECT EXISTS (SELECT 1 FROM auth.sessions WHERE id = $1)`, phone) || !queryBool(t, owner, `SELECT EXISTS (SELECT 1 FROM auth.sessions WHERE id = $1)`, bob.session) {
		t.Fatal("end_session ended the wrong sessions")
	}
	executeSQL(t, owner, `SELECT set_config('request.jwt.claims', '', false)`)
	wantSQLError(t, owner, "28000", "not_authenticated", `SELECT * FROM gregale_auth.list_sessions()`)
	if err := f.manager.Ensure(ctx, f.material, f.readonly); err != nil {
		t.Fatal(err)
	}
	reader := f.connect(t, f.readonly.name)
	deniedSQL(t, reader, `SELECT * FROM gregale_auth.list_sessions()`)
	deniedSQL(t, reader, `SELECT gregale_auth.end_session('`+phone.String()+`')`)
}

// TestSQLActiveOrganizationIsChosenByAMemberForTheirSession pins sql/0015:
// a session may act in an organization its user belongs to, and the choice
// goes with the session or the organization.
func TestSQLActiveOrganizationIsChosenByAMemberForTheirSession(t *testing.T) {
	f := newCredentialFixture(t)
	ctx := context.Background()
	_, owner := installAuthorization(t, f)
	alice, bob := newAuthUser(t, owner, "alice@example.test"), newAuthUser(t, owner, "bob@example.test")
	actAs(ctx, t, owner, alice)
	var org uuid.UUID
	if err := owner.QueryRow(ctx, `SELECT id FROM gregale_auth.create_organization('acme', 'Acme')`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	var slug, role string
	if err := owner.QueryRow(ctx, `SELECT slug, role FROM gregale_auth.set_active_organization($1)`, org).Scan(&slug, &role); err != nil || slug != "acme" || role != "owner" {
		t.Fatalf("set = %q %q, %v", slug, role, err)
	}
	active := func(u authUser) bool {
		return queryBool(t, owner, `SELECT EXISTS (SELECT 1 FROM gregale_auth.session_organizations WHERE session_id = $1)`, u.session)
	}
	executeSQL(t, owner, `SELECT gregale_auth.set_active_organization($1)`, org)
	if !active(alice) {
		t.Fatal("choosing again lost the choice")
	}
	actAs(ctx, t, owner, bob)
	wantSQLError(t, owner, "P0002", "not_found", `SELECT gregale_auth.set_active_organization($1)`, org)
	actAs(ctx, t, owner, alice)
	executeSQL(t, owner, `SELECT gregale_auth.set_active_organization(NULL)`)
	if active(alice) {
		t.Fatal("NULL did not clear the choice")
	}
	executeSQL(t, owner, `SELECT gregale_auth.set_active_organization($1)`, org)
	executeSQL(t, owner, `DELETE FROM auth.sessions WHERE id = $1`, alice.session)
	if active(alice) {
		t.Fatal("an ended session kept its organization")
	}
	executeSQL(t, owner, `SELECT set_config('request.jwt.claims', '', false)`)
	wantSQLError(t, owner, "28000", "not_authenticated", `SELECT gregale_auth.set_active_organization($1)`, org)
	if err := f.manager.Ensure(ctx, f.material, f.readonly); err != nil {
		t.Fatal(err)
	}
	deniedSQL(t, f.connect(t, f.readonly.name), `SELECT gregale_auth.set_active_organization('`+org.String()+`')`)
}

// TestSQLLifecycleEventsRecordUserChangesOnlyWhenEnabled pins the outbox the
// Auth app publishes (ADR-1190): nothing while off, one row per change while
// on, and never an address or metadata.
func TestSQLLifecycleEventsRecordUserChangesOnlyWhenEnabled(t *testing.T) {
	f := newCredentialFixture(t)
	ctx := context.Background()
	_, owner := installAuthorization(t, f)
	newAuthUser(t, owner, "before@example.test")
	var queued int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM gregale_auth.event_outbox`).Scan(&queued); err != nil || queued != 0 {
		t.Fatalf("recorded %d events while off: %v", queued, err)
	}
	executeSQL(t, owner, `UPDATE gregale_auth.event_settings SET enabled = true`)
	id, session := uuid.New(), uuid.New()
	executeSQL(t, owner, `INSERT INTO auth.users (id, email, raw_app_meta_data) VALUES ($1, 'carol@example.test', '{"provider":"google","secret":"x"}')`, id)
	executeSQL(t, owner, `UPDATE auth.users SET email_confirmed_at = now() WHERE id = $1`, id)
	executeSQL(t, owner, `INSERT INTO auth.sessions (id, user_id, aal) VALUES ($1, $2, 'aal1')`, session, id)
	executeSQL(t, owner, `UPDATE auth.users SET banned_until = now() + interval '1 day' WHERE id = $1`, id)
	executeSQL(t, owner, `UPDATE auth.users SET email = 'carol2@example.test' WHERE id = $1`, id)
	executeSQL(t, owner, `UPDATE auth.users SET banned_until = NULL WHERE id = $1`, id)
	executeSQL(t, owner, `DELETE FROM auth.users WHERE id = $1`, id)
	rows, err := owner.Query(ctx, `SELECT type, user_id, data::text FROM gregale_auth.event_outbox ORDER BY occurred_at, ctid`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var kind, data string
		var user uuid.UUID
		if err := rows.Scan(&kind, &user, &data); err != nil {
			t.Fatal(err)
		}
		if user != id || strings.Contains(data, "@") || strings.Contains(data, "secret") {
			t.Fatalf("event %s for %s carries %s", kind, user, data)
		}
		got = append(got, kind+" "+data)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`auth.user.created {"provider": "google", "anonymous": false}`,
		`auth.user.confirmed {"channel": "email"}`,
		`auth.user.signed_in {"aal": "aal1", "session_id": "` + session.String() + `"}`,
		`auth.user.banned`,
		`auth.user.unbanned {}`,
		`auth.user.deleted {}`,
	}
	if len(got) != len(want) {
		t.Fatalf("events = %q", got)
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Fatalf("event %d = %q, want %q", i, got[i], want[i])
		}
	}
	// A phone sign-up is confirmed by its code; the number is never recorded.
	executeSQL(t, owner, `DELETE FROM gregale_auth.event_outbox`)
	phone := uuid.New()
	executeSQL(t, owner, `INSERT INTO auth.users (id, phone, raw_app_meta_data) VALUES ($1, '447700900123', '{"provider":"phone"}')`, phone)
	executeSQL(t, owner, `UPDATE auth.users SET phone_confirmed_at = now() WHERE id = $1`, phone)
	executeSQL(t, owner, `UPDATE auth.users SET phone_confirmed_at = now() WHERE id = $1`, phone)
	var phoneEvents string
	if err := owner.QueryRow(ctx, `SELECT string_agg(type || ' ' || data::text, '; ' ORDER BY occurred_at, ctid) FROM gregale_auth.event_outbox WHERE user_id = $1`, phone).Scan(&phoneEvents); err != nil {
		t.Fatal(err)
	}
	if phoneEvents != `auth.user.created {"provider": "phone", "anonymous": false}; auth.user.confirmed {"channel": "phone"}` {
		t.Fatalf("phone events = %s", phoneEvents)
	}
	// Only the auth credential reaches the outbox.
	if err := f.manager.Ensure(ctx, f.material, f.runtime); err != nil {
		t.Fatal(err)
	}
	if code, _ := sqlCode(t, f.connect(t, f.runtime.name), `SELECT count(*) FROM gregale_auth.event_outbox`); code != "42501" {
		t.Fatalf("runtime credential read the outbox: %s", code)
	}
}

func TestSQLOrganizationSSOJoinsLinkedProvidersAndBoundsInvitations(t *testing.T) {
	f := newCredentialFixture(t)
	ctx := context.Background()
	_, owner := installAuthorization(t, f)
	alice := newAuthUser(t, owner, "alice@example.test")
	actAs(ctx, t, owner, alice)
	var org uuid.UUID
	if err := owner.QueryRow(ctx, `SELECT id FROM gregale_auth.create_organization('acme', 'Acme')`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	provider := uuid.New()
	executeSQL(t, owner, `INSERT INTO auth.sso_providers (id) VALUES ($1)`, provider)
	executeSQL(t, owner, `INSERT INTO auth.sso_domains (sso_provider_id, domain) VALUES ($1, 'Acme.com')`, provider)
	ssoUser := func(email string) authUser {
		u := newAuthUser(t, owner, email)
		executeSQL(t, owner, `UPDATE auth.users SET is_sso_user = true WHERE id = $1`, u.id)
		executeSQL(t, owner, `INSERT INTO auth.identities (user_id, provider, provider_id) VALUES ($1, $2, $3)`, u.id, "sso:"+provider.String(), email)
		return u
	}
	member := func(u authUser) string {
		var role string
		if err := owner.QueryRow(ctx, `SELECT coalesce((SELECT role FROM gregale_auth.memberships WHERE org_id = $1 AND user_id = $2), '')`, org, u.id).Scan(&role); err != nil {
			t.Fatal(err)
		}
		return role
	}
	// A user who signed in before the link joins when it is made.
	early := ssoUser("early@acme.com")
	var joined int
	if err := owner.QueryRow(ctx, `SELECT gregale_auth.link_sso_organization($1, $2, 'member')`, provider, org).Scan(&joined); err != nil || joined != 1 || member(early) != "member" {
		t.Fatalf("link joined %d (%v), early member %q", joined, err, member(early))
	}
	// Later users join on their first SSO sign-in; other providers do not.
	later := ssoUser("later@acme.com")
	other := newAuthUser(t, owner, "oauth@acme.com")
	executeSQL(t, owner, `INSERT INTO auth.identities (user_id, provider, provider_id) VALUES ($1, 'google', 'g1')`, other.id)
	if member(later) != "member" || member(other) != "" {
		t.Fatalf("later %q, google user %q", member(later), member(other))
	}
	if code, _ := sqlCode(t, owner, `SELECT gregale_auth.link_sso_organization($1, $2, 'owner')`, provider, org); code != "23514" {
		t.Fatalf("linking with the owner role: %s", code)
	}
	// An SSO user accepts invitations only for its provider's domains.
	actAs(ctx, t, owner, alice)
	inside, outside := tokenHash(t), tokenHash(t)
	executeSQL(t, owner, `SELECT gregale_auth.invite($1, 'eve@acme.com', 'member', $2, 3600)`, org, inside)
	executeSQL(t, owner, `SELECT gregale_auth.invite($1, 'eve@globex.com', 'member', $2, 3600)`, org, outside)
	executeSQL(t, owner, `DELETE FROM gregale_auth.sso_organizations`)
	claimed := ssoUser("eve@globex.com")
	actAs(ctx, t, owner, claimed)
	wantSQLError(t, owner, "42501", "invitation_email_mismatch", `SELECT gregale_auth.accept_invitation($1)`, outside)
	eve := ssoUser("eve@acme.com")
	actAs(ctx, t, owner, eve)
	executeSQL(t, owner, `SELECT gregale_auth.accept_invitation($1)`, inside)
	// Deleting the provider removes its link; members stay.
	executeSQL(t, owner, `SELECT gregale_auth.link_sso_organization($1, $2, 'member')`, provider, org)
	executeSQL(t, owner, `DELETE FROM auth.sso_providers WHERE id = $1`, provider)
	var links int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM gregale_auth.sso_organizations`).Scan(&links); err != nil || links != 0 || member(later) != "member" {
		t.Fatalf("links %d (%v), later %q", links, err, member(later))
	}
	if err := f.manager.Ensure(ctx, f.material, f.runtime); err != nil {
		t.Fatal(err)
	}
	if code, _ := sqlCode(t, f.connect(t, f.runtime.name), `SELECT gregale_auth.link_sso_organization(gen_random_uuid(), gen_random_uuid(), 'member')`); code != "42501" {
		t.Fatalf("runtime credential linked a provider: %s", code)
	}
}

func TestSQLSCIMLinksProvisionedPeopleOnFirstSignIn(t *testing.T) {
	f := newCredentialFixture(t)
	ctx := context.Background()
	_, owner := installAuthorization(t, f)
	alice := newAuthUser(t, owner, "alice@example.test")
	actAs(ctx, t, owner, alice)
	var org uuid.UUID
	if err := owner.QueryRow(ctx, `SELECT id FROM gregale_auth.create_organization('acme', 'Acme')`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	provider := uuid.New()
	executeSQL(t, owner, `INSERT INTO auth.sso_providers (id) VALUES ($1)`, provider)
	executeSQL(t, owner, `SELECT gregale_auth.link_sso_organization($1, $2, 'member')`, provider, org)
	executeSQL(t, owner, `INSERT INTO gregale_auth.scim_users (provider_id, user_name, active) VALUES ($1, 'Ada@Acme.com', true), ($1, 'gone@acme.com', false)`, provider)
	signIn := func(email string) uuid.UUID {
		id := uuid.New()
		executeSQL(t, owner, `INSERT INTO auth.users (id, email, email_confirmed_at, is_sso_user) VALUES ($1, $2, now(), true)`, id, email)
		executeSQL(t, owner, `INSERT INTO auth.identities (user_id, provider, provider_id) VALUES ($1, $2, $3)`, id, "sso:"+provider.String(), email)
		return id
	}
	state := func(user uuid.UUID) (linked, banned, member bool) {
		if err := owner.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM gregale_auth.scim_users WHERE user_id = $1),
			coalesce((SELECT banned_until > now() FROM auth.users WHERE id = $1), false),
			EXISTS (SELECT 1 FROM gregale_auth.memberships WHERE user_id = $1 AND org_id = $2)`, user, org).Scan(&linked, &banned, &member); err != nil {
			t.Fatal(err)
		}
		return
	}
	// An active provisioned person is linked by email and joins.
	if linked, banned, member := state(signIn("ada@acme.com")); !linked || banned || !member {
		t.Fatalf("active person: linked %v banned %v member %v", linked, banned, member)
	}
	// A deprovisioned person is linked, banned and joins nothing.
	if linked, banned, member := state(signIn("gone@acme.com")); !linked || !banned || member {
		t.Fatalf("deprovisioned person: linked %v banned %v member %v", linked, banned, member)
	}
	// Someone the identity provider never provisioned is unaffected.
	if linked, banned, member := state(signIn("walk-in@acme.com")); linked || banned || !member {
		t.Fatalf("unprovisioned person: linked %v banned %v member %v", linked, banned, member)
	}
	if code, _ := sqlCode(t, owner, `INSERT INTO gregale_auth.scim_users (provider_id, user_name) VALUES ($1, 'ADA@acme.com')`, provider); code != "23505" {
		t.Fatalf("duplicate user name: %s", code)
	}
	executeSQL(t, owner, `DELETE FROM auth.sso_providers WHERE id = $1`, provider)
	var left int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM gregale_auth.scim_users`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("records left after the provider was removed: %d %v", left, err)
	}
}

func TestSQLSelfServiceOrganizationSSONeedsVerifiedDomainsAndPermission(t *testing.T) {
	f := newCredentialFixture(t)
	ctx := context.Background()
	_, owner := installAuthorization(t, f)
	alice, bob := newAuthUser(t, owner, "alice@acme.com"), newAuthUser(t, owner, "bob@globex.com")
	var acme, globex uuid.UUID
	actAs(ctx, t, owner, alice)
	if err := owner.QueryRow(ctx, `SELECT id FROM gregale_auth.create_organization('acme', 'Acme')`).Scan(&acme); err != nil {
		t.Fatal(err)
	}
	actAs(ctx, t, owner, bob)
	if err := owner.QueryRow(ctx, `SELECT id FROM gregale_auth.create_organization('globex', 'Globex')`).Scan(&globex); err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 32)
	// Only someone with sso:manage in the organization manages its domains.
	wantSQLError(t, owner, "42501", "forbidden", `SELECT gregale_auth.add_org_domain($1, 'acme.com', $2)`, acme, token)
	actAs(ctx, t, owner, alice)
	executeSQL(t, owner, `SELECT gregale_auth.add_org_domain($1, 'Acme.com', $2)`, acme, token)
	wantSQLError(t, owner, "22023", "invalid_domain", `SELECT gregale_auth.add_org_domain($1, 'localhost', $2)`, acme, token)
	wantSQLError(t, owner, "42501", "domain_not_verified", `SELECT gregale_auth.check_org_sso($1, NULL, ARRAY['acme.com'], 'member')`, acme)
	executeSQL(t, owner, `SELECT gregale_auth.verify_org_domain($1, 'acme.com')`, acme)
	executeSQL(t, owner, `SELECT gregale_auth.check_org_sso($1, NULL, ARRAY['acme.com'], 'member')`, acme)
	wantSQLError(t, owner, "22023", "unknown_role", `SELECT gregale_auth.check_org_sso($1, NULL, ARRAY['acme.com'], 'owner')`, acme)
	// A domain verified by one organization cannot be claimed by another.
	actAs(ctx, t, owner, bob)
	wantSQLError(t, owner, "23505", "domain_taken", `SELECT gregale_auth.add_org_domain($1, 'acme.com', $2)`, globex, token)
	wantSQLError(t, owner, "42501", "forbidden", `SELECT gregale_auth.list_org_domains($1)`, acme)
	wantSQLError(t, owner, "42501", "forbidden", `SELECT gregale_auth.check_org_sso($1, NULL, ARRAY['acme.com'], 'member')`, acme)
	wantSQLError(t, owner, "42501", "domain_not_verified", `SELECT gregale_auth.check_org_sso($1, NULL, ARRAY['acme.com'], 'member')`, globex)
	// A domain an organization's provider serves cannot be removed.
	actAs(ctx, t, owner, alice)
	provider := uuid.New()
	executeSQL(t, owner, `INSERT INTO auth.sso_providers (id) VALUES ($1)`, provider)
	executeSQL(t, owner, `INSERT INTO auth.sso_domains (sso_provider_id, domain) VALUES ($1, 'acme.com')`, provider)
	executeSQL(t, owner, `SELECT gregale_auth.link_sso_organization($1, $2, 'member')`, provider, acme)
	wantSQLError(t, owner, "23503", "domain_in_use", `SELECT gregale_auth.remove_org_domain($1, 'acme.com')`, acme)
	actAs(ctx, t, owner, bob)
	wantSQLError(t, owner, "42501", "forbidden", `SELECT gregale_auth.check_org_sso($1, $2, NULL, NULL)`, acme, provider)
	wantSQLError(t, owner, "P0002", "not_found", `SELECT gregale_auth.check_org_sso($1, $2, NULL, NULL)`, globex, provider)
	if err := f.manager.Ensure(ctx, f.material, f.runtime); err != nil {
		t.Fatal(err)
	}
	if code, _ := sqlCode(t, f.connect(t, f.runtime.name), `SELECT gregale_auth.list_org_domains(gen_random_uuid())`); code != "42501" {
		t.Fatalf("runtime credential called the organization SSO functions: %s", code)
	}
}

func TestSQLOAuthSessionResourcesEndWithTheirSessions(t *testing.T) {
	f := newCredentialFixture(t)
	_, owner := installAuthorization(t, f)
	alice, bob := newAuthUser(t, owner, "alice@example.test"), newAuthUser(t, owner, "bob@example.test")
	client := uuid.New()
	// The custom access token hook records a client session's resource.
	for _, u := range []authUser{alice, bob} {
		executeSQL(t, owner, `INSERT INTO gregale_auth.oauth_session_resources (session_id, client_id, resource) VALUES ($1, $2, 'https://mcp.example.test/mcp')`, u.session, client)
	}
	executeSQL(t, owner, `INSERT INTO gregale_auth.oauth_session_resources (session_id, client_id, resource) VALUES ($1, $2, NULL)`, uuid.New(), client)
	if code, _ := sqlCode(t, owner, `INSERT INTO gregale_auth.oauth_session_resources (session_id, client_id, resource) VALUES ($1, $2, '')`, uuid.New(), client); code != "23514" {
		t.Fatalf("an empty resource = %s, want a check violation", code)
	}
	remembered := func(u authUser) bool {
		return queryBool(t, owner, `SELECT EXISTS (SELECT 1 FROM gregale_auth.oauth_session_resources WHERE session_id = $1)`, u.session)
	}
	executeSQL(t, owner, `DELETE FROM auth.sessions WHERE id = $1`, alice.session)
	executeSQL(t, owner, `DELETE FROM auth.users WHERE id = $1`, bob.id)
	if remembered(alice) || remembered(bob) {
		t.Fatal("an ended session kept its resource")
	}
}

func TestSQLAnonymousUsersCannotOwnOrganizationsAndIdleOnesArePruned(t *testing.T) {
	f := newCredentialFixture(t)
	ctx := context.Background()
	_, owner := installAuthorization(t, f)
	guest, person := newAuthUser(t, owner, ""), newAuthUser(t, owner, "ada@example.test")
	executeSQL(t, owner, `UPDATE auth.users SET email = NULL, is_anonymous = true WHERE id = $1`, guest.id)
	actAs(ctx, t, owner, guest)
	wantSQLError(t, owner, "42501", "anonymous_user", `SELECT gregale_auth.create_organization('guests', 'Guests')`)
	actAs(ctx, t, owner, person)
	executeSQL(t, owner, `SELECT gregale_auth.create_organization('acme', 'Acme')`)
	// The template's own pruning statement, run with 30 days.
	source, err := os.ReadFile(filepath.Join(authorizationSQLDir, "..", "retention.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	statement := regexp.MustCompile("(?s)const anonymousStatement = `([^`]+)`").FindSubmatch(source)
	if statement == nil {
		t.Fatal("no anonymousStatement in retention.mjs")
	}
	idle, active, refreshed := newAuthUser(t, owner, ""), newAuthUser(t, owner, ""), newAuthUser(t, owner, "")
	for _, u := range []authUser{guest, idle, active, refreshed} {
		executeSQL(t, owner, `UPDATE auth.users SET email = NULL, is_anonymous = true, created_at = now() - interval '40 days' WHERE id = $1`, u.id)
		executeSQL(t, owner, `UPDATE auth.sessions SET created_at = now() - interval '40 days', updated_at = now() - interval '40 days' WHERE user_id = $1`, u.id)
	}
	executeSQL(t, owner, `UPDATE auth.users SET created_at = now() - interval '40 days' WHERE id = $1`, person.id)
	executeSQL(t, owner, `UPDATE auth.sessions SET updated_at = now() - interval '2 days' WHERE user_id = $1`, active.id)
	executeSQL(t, owner, `UPDATE auth.sessions SET refreshed_at = (now() - interval '1 day') AT TIME ZONE 'UTC' WHERE user_id = $1`, refreshed.id)
	executeSQL(t, owner, `DELETE FROM auth.sessions WHERE user_id = $1`, guest.id)
	executeSQL(t, owner, string(statement[1]), 30, 1000)
	exists := func(u authUser) bool {
		return queryBool(t, owner, `SELECT EXISTS (SELECT 1 FROM auth.users WHERE id = $1)`, u.id)
	}
	if exists(guest) || exists(idle) || !exists(active) || !exists(refreshed) || !exists(person) {
		t.Fatalf("after pruning: guest %v idle %v active %v refreshed %v person %v", exists(guest), exists(idle), exists(active), exists(refreshed), exists(person))
	}
}
