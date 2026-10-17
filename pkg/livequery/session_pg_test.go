package livequery

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

// rlsSchema is a chat whose messages each reader sees only for channels they
// belong to. %[1]s is the readers' role.
const rlsSchema = `
CREATE TABLE members (channel_id bigint, user_id text, PRIMARY KEY (channel_id, user_id));
CREATE TABLE messages (id bigint PRIMARY KEY, channel_id bigint NOT NULL, body text);
CREATE SCHEMA auth;
CREATE FUNCTION auth.uid() RETURNS text LANGUAGE sql STABLE AS $$ select current_setting('request.jwt.claims', true)::json->>'sub' $$;
CREATE ROLE %[1]s NOLOGIN;
GRANT USAGE ON SCHEMA auth TO %[1]s;
GRANT SELECT ON messages, members TO %[1]s;
ALTER TABLE messages ENABLE ROW LEVEL SECURITY;
CREATE POLICY readable ON messages FOR SELECT TO %[1]s USING (channel_id IN (SELECT channel_id FROM members WHERE user_id = auth.uid()));
`

func (h *harness) registerAs(id, sql string, s *Session, args ...any) {
	h.t.Helper()
	if _, err := h.engine.RegisterAs(context.Background(), id, sql, s, args...); err != nil {
		h.t.Fatalf("register %s: %v", id, err)
	}
	h.clients[id] = &client{}
}

// ids lists a result's first column, sorted, each followed by a comma.
func (h *harness) ids(id string) string {
	var vals []string
	for _, r := range h.engine.Result(id) {
		vals = append(vals, *r.Values[0]+",")
	}
	slices.Sort(vals)
	return strings.Join(vals, "")
}

// adr: 1384 — RLS mode: each session reads through PostgreSQL's row-level
// security, so a change to what a policy reads (membership) changes a
// result although no row the query reads changed; an opaque policy is
// re-read on every change; a revoked grant closes only that session's
// queries. VerifyEachRound checks every result against PostgreSQL as the
// same role with the same claims.
func TestSessionsReadThroughRowLevelSecurity(t *testing.T) {
	role := fmt.Sprintf("lq_reader_%d", rand.Uint32())
	h := newHarness(t, fmt.Sprintf(rlsSchema, role))
	t.Cleanup(func() { mustExec(t, h.pool, fmt.Sprintf(`DROP OWNED BY %[1]s; DROP ROLE %[1]s`, role)) })
	alice := &Session{Role: role, Claims: `{"sub":"alice"}`}
	bob := &Session{Role: role, Claims: `{"sub":"bob"}`}
	const feed = `SELECT id, body FROM messages`
	mustExec(t, h.pool, `INSERT INTO messages VALUES (1, 1, 'one'), (2, 2, 'two'); INSERT INTO members VALUES (1, 'alice')`)
	h.registerAs("alice", feed, alice)
	h.registerAs("alice2", feed, alice) // shares alice's group
	h.registerAs("bob", feed, bob)
	h.register("all", feed) // the engine's own login: no policy applies
	h.round()
	if got := h.ids("alice") + "|" + h.ids("bob") + "|" + h.ids("all"); got != "1,||1,2," {
		t.Fatalf("initial results %s", got)
	}
	if n := h.engine.Sessions(); n != 2 || h.engine.Queries() != 1 {
		t.Fatalf("%d session groups, %d queries", n, h.engine.Queries())
	}

	// Membership is not a table the query reads, but it decides what bob
	// sees.
	mustExec(t, h.pool, `INSERT INTO members VALUES (1, 'bob'), (2, 'bob')`)
	if got := outcomes(h.round()); got != "alice2=- alice=- all=- bob=changed" {
		t.Fatalf("bob joins: %s", got)
	}
	mustExec(t, h.pool, `DELETE FROM members WHERE user_id = 'alice'`)
	if got := outcomes(h.round()); got != "alice2=changed alice=changed all=- bob=-" {
		t.Fatalf("alice leaves: %s", got)
	}
	mustExec(t, h.pool, `INSERT INTO messages VALUES (3, 2, 'three')`)
	if got := outcomes(h.round()); got != "alice2=- alice=- all=changed bob=changed" {
		t.Fatalf("a message: %s", got)
	}
	if got := h.ids("bob"); got != "1,2,3," {
		t.Fatalf("bob sees %s", got)
	}

	// A policy calling plpgsql is opaque: any change re-reads its sessions.
	mustExec(t, h.pool, fmt.Sprintf(`CREATE FUNCTION is_member(c bigint) RETURNS boolean LANGUAGE plpgsql STABLE AS
		$$ BEGIN RETURN EXISTS (SELECT 1 FROM members WHERE channel_id = c AND user_id = auth.uid()); END $$;
		DROP POLICY readable ON messages;
		CREATE POLICY readable ON messages FOR SELECT TO %s USING (is_member(channel_id))`, role))
	h.round()
	mustExec(t, h.pool, `INSERT INTO members VALUES (2, 'alice')`)
	if got := outcomes(h.round()); got != "alice2=changed alice=changed all=- bob=-" {
		t.Fatalf("alice joins through an opaque policy: %s", got)
	}
	if got := h.ids("alice"); got != "2,3," {
		t.Fatalf("alice sees %s", got)
	}

	// A revoked grant closes the sessions' queries, not the round.
	mustExec(t, h.pool, fmt.Sprintf(`REVOKE SELECT ON messages FROM %s`, role))
	mustExec(t, h.pool, `INSERT INTO messages VALUES (4, 1, 'four')`)
	if got := outcomes(h.round()); got != "alice2=closed:invalid_query alice=closed:invalid_query all=changed bob=closed:invalid_query" {
		t.Fatalf("revoked: %s", got)
	}
	if h.engine.Sessions() != 0 || h.engine.Subscriptions() != 1 {
		t.Fatalf("%d sessions, %d subscriptions left", h.engine.Sessions(), h.engine.Subscriptions())
	}
}

// adr: 1384 — the policy walk finds tables read through policies, SQL
// functions (by probing a BEGIN ATOMIC copy), views and other tables'
// policies, and marks other languages opaque.
func TestPolicyDependencies(t *testing.T) {
	role := fmt.Sprintf("lq_reader_%d", rand.Uint32())
	_, pool := openLogicalDB(t)
	mustExec(t, pool, fmt.Sprintf(rlsSchema, role))
	t.Cleanup(func() { mustExec(t, pool, fmt.Sprintf(`DROP OWNED BY %[1]s; DROP ROLE %[1]s`, role)) })
	mustExec(t, pool, `
		CREATE TABLE teams (id bigint PRIMARY KEY, owner text);
		CREATE TABLE notes (id bigint PRIMARY KEY, channel_id bigint);
		CREATE VIEW owners AS SELECT owner FROM teams;
		CREATE FUNCTION in_team(c bigint) RETURNS boolean LANGUAGE sql STABLE AS $$ select exists (select 1 from owners where owner = auth.uid()) $$;
		ALTER TABLE notes ENABLE ROW LEVEL SECURITY;
		CREATE POLICY team ON notes USING (in_team(channel_id));
		ALTER TABLE members ENABLE ROW LEVEL SECURITY;
		CREATE POLICY own ON members USING (user_id = auth.uid())`)
	oid := func(table string) uint32 {
		var o uint32
		if err := pool.QueryRow(context.Background(), `SELECT $1::regclass::oid`, table).Scan(&o); err != nil {
			t.Fatal(err)
		}
		return o
	}
	read := func(table string) policyDeps {
		t.Helper()
		conn, err := pool.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Release()
		deps, err := readPolicyDeps(context.Background(), conn.Conn(), map[uint32]bool{oid(table): true})
		if err != nil {
			t.Fatal(err)
		}
		return deps
	}
	if d := read("messages"); d.opaque || len(d.tables) != 1 || !d.tables[oid("members")] {
		t.Fatalf("messages depend on %v (opaque %v), want members", d.tables, d.opaque)
	}
	if d := read("notes"); d.opaque || len(d.tables) != 1 || !d.tables[oid("teams")] {
		t.Fatalf("notes depend on %v (opaque %v), want teams through a function and a view", d.tables, d.opaque)
	}
	mustExec(t, pool, `CREATE FUNCTION opaque(c bigint) RETURNS boolean LANGUAGE plpgsql STABLE AS $$ BEGIN RETURN true; END $$;
		CREATE POLICY more ON notes USING (opaque(channel_id))`)
	if d := read("notes"); !d.opaque {
		t.Fatal("a plpgsql policy function is not opaque")
	}
	if d := read("teams"); d.opaque || len(d.tables) != 0 {
		t.Fatalf("a table without policies depends on %v", d.tables)
	}
}

// adr: 1384 — live queries never read as a role that bypasses row-level
// security, nor as one the engine's login may not become.
func TestSessionsRefusePrivilegedRoles(t *testing.T) {
	role := fmt.Sprintf("lq_reader_%d", rand.Uint32())
	h := newHarness(t, fmt.Sprintf(rlsSchema, role))
	t.Cleanup(func() { mustExec(t, h.pool, fmt.Sprintf(`DROP OWNED BY %[1]s; DROP ROLE %[1]s`, role)) })
	mustExec(t, h.pool, fmt.Sprintf(`ALTER ROLE %s BYPASSRLS`, role))
	_, err := h.engine.RegisterAs(context.Background(), "q", `SELECT id FROM messages`, &Session{Role: role, Claims: `{}`})
	if err == nil || !strings.Contains(err.Error(), "bypasses row-level security") {
		t.Fatalf("a BYPASSRLS role: %v", err)
	}
	if _, err := h.engine.RegisterAs(context.Background(), "q", `SELECT id FROM messages`, &Session{Role: "no_such_role_lq", Claims: `{}`}); err == nil {
		t.Fatal("a missing role was accepted")
	}
}
