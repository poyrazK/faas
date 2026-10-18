package livequery

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/onebox-faas/faas/pkg/livequery/rest"
)

const restSchema = `
CREATE TABLE teams (id bigint PRIMARY KEY, name text);
CREATE TABLE users (id bigint PRIMARY KEY, name text, team_id bigint REFERENCES teams);
CREATE TABLE members (channel_id bigint, user_id text, PRIMARY KEY (channel_id, user_id));
CREATE TABLE messages (id bigint PRIMARY KEY, channel_id bigint NOT NULL, author_id bigint REFERENCES users, body text, data jsonb);
CREATE TABLE comments (id bigint PRIMARY KEY, message_id bigint REFERENCES messages, body text);
CREATE ROLE %[1]s NOLOGIN;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO %[1]s;
ALTER TABLE messages ENABLE ROW LEVEL SECURITY;
CREATE POLICY readable ON messages FOR SELECT TO %[1]s USING (channel_id IN (SELECT channel_id FROM members WHERE user_id = current_setting('request.jwt.claims', true)::json->>'sub'));
`

// adr: 20261016151947 — PostgREST reads compile to SQL the engine maintains, sealed
// and as a session, through writes to the queried relation, to embedded
// relations and to what the policy reads; VerifyEachRound checks each
// result against PostgreSQL.
func TestRESTRequestsStayLive(t *testing.T) {
	role := fmt.Sprintf("lq_rest_%d", rand.Uint32())
	h := newHarness(t, fmt.Sprintf(restSchema, role))
	t.Cleanup(func() { mustExec(t, h.pool, fmt.Sprintf(`DROP OWNED BY %[1]s; DROP ROLE %[1]s`, role)) })
	mustExec(t, h.pool, `INSERT INTO teams VALUES (1, 'red'); INSERT INTO users VALUES (1, 'ann', 1), (2, 'bo', NULL);
		INSERT INTO members VALUES (1, 'alice');
		INSERT INTO messages VALUES (1, 1, 1, 'hi there', '{"k":"x"}'), (2, 2, 2, 'yo', '{"k":"y"}'), (3, 1, 2, 'hey', NULL);
		INSERT INTO comments VALUES (1, 1, 'first'), (2, 1, 'second')`)
	alice := &Session{Role: role, Claims: `{"sub":"alice"}`}
	requests := []string{
		`messages?select=id,body&channel_id=eq.1&order=id.desc&limit=2`,
		`messages?select=id,author:users(name,team:teams(name)),comments(id,body)&channel_id=in.(1,2)`,
		`messages?select=id,k:data->>k&or=(body.ilike.*h*,id.gt.2)&data->>k=not.is.null`,
		`messages?select=id,comments!inner(id)&order=id`,
		`users?select=name,team:teams!team_id(name),messages(id)&name=like.*n*`,
	}
	conn, err := h.pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	for i, target := range requests {
		req, err := rest.Parse(target)
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		c, err := rest.Compile(context.Background(), rest.PG{Conn: conn.Conn()}, "public", req)
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		plan := h.register(fmt.Sprintf("r%d", i), c.SQL, c.Args...)
		h.registerAs(fmt.Sprintf("r%d-alice", i), c.SQL, alice, c.Args...)
		t.Logf("r%d (%s, %s): %s %v", i, plan.Mode, plan.Reason, c.SQL, c.Args)
	}
	h.round()
	if got := h.ids("r1") + "|" + h.ids("r1-alice"); got != "1,2,3,|1,3," {
		t.Fatalf("embeds: %s", got)
	}
	for _, step := range []string{
		`INSERT INTO messages VALUES (4, 1, 1, 'hello', '{"k":"z"}')`,
		`UPDATE teams SET name = 'blue' WHERE id = 1`, // reaches r1 and r4 through embeds
		`INSERT INTO comments VALUES (3, 3, 'third')`, // r3's !inner now admits message 3
		`INSERT INTO members VALUES (2, 'alice')`,     // the policy's table
		`DELETE FROM comments WHERE id = 1; UPDATE users SET team_id = NULL WHERE id = 1`,
		`DELETE FROM comments WHERE message_id = 1; DELETE FROM messages WHERE id = 1`,
	} {
		mustExec(t, h.pool, step)
		h.round()
	}
	if got := h.ids("r3") + "|" + h.ids("r3-alice"); got != "3,|3," {
		t.Fatalf("inner embed after the writes: %s", got)
	}
	if got := h.ids("r1-alice"); got != "2,3,4," {
		t.Fatalf("alice after joining channel 2: %s", got)
	}
}
