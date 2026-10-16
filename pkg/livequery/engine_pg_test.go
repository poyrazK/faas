package livequery

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// client materializes a subscription from deltas, as an SDK would.
type client map[string]string

func (c client) apply(t *testing.T, d Delta) {
	t.Helper()
	if d.Reset {
		for id := range c {
			delete(c, id)
		}
		for _, r := range d.Rows {
			c[r.ID] = encodeValues(r.Values)
		}
		return
	}
	for _, id := range d.Removes {
		if _, ok := c[id]; !ok {
			t.Fatalf("delta removes unknown row %s", id)
		}
		delete(c, id)
	}
	for _, r := range d.Upserts {
		c[r.ID] = encodeValues(r.Values)
	}
}

func (c client) equal(rows []Row) error {
	if len(c) != len(rows) {
		return fmt.Errorf("client holds %d rows, engine %d", len(c), len(rows))
	}
	for _, r := range rows {
		if c[r.ID] != encodeValues(r.Values) {
			return fmt.Errorf("client row %s = %q, engine %q", r.ID, printable(c[r.ID]), printable(encodeValues(r.Values)))
		}
	}
	return nil
}

type harness struct {
	t       *testing.T
	pool    *pgxpool.Pool
	engine  *Engine
	clients map[string]client
	rounds  int
	trace   []string
}

func newHarness(t *testing.T, schema string) *harness {
	t.Helper()
	dsn, pool := openLogicalDB(t)
	mustExec(t, pool, schema)
	mustExec(t, pool, `CREATE PUBLICATION gregale_live FOR ALL TABLES`)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	capture, err := OpenCapture(ctx, dsn, fmt.Sprintf("lq_%d", time.Now().UnixNano()), "gregale_live")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = capture.Close() })
	e := NewEngine(pool, capture)
	e.VerifyEachRound = true
	h := &harness{t: t, pool: pool, engine: e, clients: map[string]client{}}
	if os.Getenv("LIVEQUERY_TRACE") != "" {
		e.Trace = func(line string) { h.trace = append(h.trace, line) }
	}
	return h
}

func (h *harness) register(id, sql string, args ...any) *Plan {
	h.t.Helper()
	p, err := h.engine.Register(context.Background(), id, sql, args...)
	if err != nil {
		h.t.Fatalf("register %s: %v", id, err)
	}
	h.clients[id] = client{}
	return p
}

// round runs one round, applies the deltas to the clients and checks each
// client against the engine (the engine itself is checked against the query
// at the round snapshot by VerifyEachRound).
func (h *harness) round() []Delta {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	deltas, err := h.engine.Round(ctx)
	if err != nil {
		for _, line := range h.trace {
			h.t.Log(line)
		}
		if h.engine.Trace != nil {
			for _, line := range h.engine.DebugDrain(3 * time.Second) {
				h.t.Log(line)
			}
		}
		h.t.Fatal(err)
	}
	h.rounds++
	for _, d := range deltas {
		c := h.clients[d.Query]
		c.apply(h.t, d)
		if err := c.equal(h.engine.Result(d.Query)); err != nil {
			h.t.Fatalf("round %d query %s: %v", h.rounds, d.Query, err)
		}
	}
	return deltas
}

const chatSchema = `
	CREATE TABLE channels (id bigint PRIMARY KEY, name text NOT NULL, archived boolean NOT NULL DEFAULT false);
	CREATE TABLE members (channel_id bigint, user_id bigint, PRIMARY KEY (channel_id, user_id));
	CREATE TABLE messages (id bigint PRIMARY KEY, channel_id bigint, author bigint, body text);`

// adr: 1384 — every maintenance mode stays equal to the query at each round
// snapshot through inserts, key changes, deletes, outer-join match changes
// and truncation.
func TestEngineMaintainsEveryMode(t *testing.T) {
	h := newHarness(t, chatSchema)
	plans := map[string]*Plan{
		"inbox": h.register("inbox", `
			SELECT c.name, m.id AS message, m.body
			FROM members mb JOIN channels c ON c.id = mb.channel_id JOIN messages m ON m.channel_id = c.id
			WHERE mb.user_id = $1 AND NOT c.archived`, int64(7)),
		"empty_channels": h.register("empty_channels", `
			SELECT c.id, c.name FROM channels c LEFT JOIN messages m ON m.channel_id = c.id WHERE m.id IS NULL`),
		"counts": h.register("counts", `
			SELECT c.id, c.name, count(m.id) AS n, max(m.body) AS last, string_agg(m.body, ',' ORDER BY m.id) AS bodies
			FROM channels c LEFT JOIN messages m ON m.channel_id = c.id GROUP BY c.id, c.name HAVING count(m.id) < 3`),
		"latest": h.register("latest", `SELECT id, body FROM messages ORDER BY id DESC LIMIT 2`),
	}
	if plans["inbox"].Mode != ModeLineage || plans["empty_channels"].Mode != ModeLineage ||
		plans["counts"].Mode != ModeGroup || plans["latest"].Mode != ModeRecompute {
		t.Fatalf("unexpected modes: %v %v %v %v", plans["inbox"].Mode, plans["empty_channels"].Mode, plans["counts"].Mode, plans["latest"].Mode)
	}
	h.round()
	for _, step := range []string{
		`INSERT INTO channels VALUES (1, 'general', false), (2, 'random', false), (3, 'empty', false)`,
		`INSERT INTO members VALUES (1, 7), (2, 7), (3, 8)`,
		`INSERT INTO messages VALUES (10, 1, 7, 'hi'), (11, 1, 8, 'yo'), (12, 2, 7, 'hey')`,
		`UPDATE messages SET channel_id = 3 WHERE id = 11`,
		`UPDATE channels SET archived = true WHERE id = 2`,
		`UPDATE messages SET id = 20 WHERE id = 10`,
		`DELETE FROM messages WHERE channel_id = 3`,
		`UPDATE channels SET name = 'main' WHERE id = 1`,
		`INSERT INTO messages VALUES (30, 1, 7, 'a'), (31, 1, 7, 'b'), (32, 1, 7, 'c')`,
		`DELETE FROM members WHERE user_id = 7 AND channel_id = 1`,
		`TRUNCATE messages`,
		`INSERT INTO messages VALUES (40, 2, 7, 'back')`,
	} {
		mustExec(t, h.pool, step)
		h.round()
	}
	// Several transactions in one round are applied atomically.
	mustExec(t, h.pool, `INSERT INTO members VALUES (2, 9)`)
	mustExec(t, h.pool, `UPDATE channels SET archived = false WHERE id = 2`)
	mustExec(t, h.pool, `INSERT INTO messages VALUES (41, 3, 8, 'x')`)
	deltas := h.round()
	if len(deltas[0].Xids) != 3 {
		t.Fatalf("round applied %d transactions, want 3", len(deltas[0].Xids))
	}
	if got := len(h.engine.Result("counts")); got != 3 {
		t.Fatalf("counts rows = %d, want 3", got)
	}
}
