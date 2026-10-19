package livequery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/livequery/liveproto"
)

// client is a subscriber as the SDK sees it: every delta travels as JSON
// live frames into the protocol's reference client, which refuses frames
// that break the contract.
type client struct {
	result liveproto.Result
}

func (c *client) apply(t *testing.T, d Delta) {
	t.Helper()
	frames, err := d.Frames("db", c.result.Version, 0)
	if err != nil {
		t.Fatalf("render %s: %v", d.Query, err)
	}
	for _, f := range frames {
		c.deliver(t, f)
	}
}

func (c *client) deliver(t *testing.T, frame any) {
	t.Helper()
	data, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := liveproto.ParseServerFrame(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.result.Apply(parsed); err != nil {
		t.Fatalf("client refused %s: %v", data, err)
	}
}

// equal compares the client with the engine: in order for ordered queries,
// by row ID otherwise.
func (c *client) equal(rows []Row) error {
	got := c.result.Rows()
	if len(got) != len(rows) {
		return fmt.Errorf("client holds %d rows, engine %d", len(got), len(rows))
	}
	for i, r := range rows {
		if got[i].ID != r.ID || encodeValues(got[i].Values) != encodeValues(r.Values) {
			return fmt.Errorf("client row %d = %s %q, engine %s %q", i, got[i].ID, printable(encodeValues(got[i].Values)), r.ID, printable(encodeValues(r.Values)))
		}
	}
	return nil
}

type harness struct {
	t        *testing.T
	pool     *pgxpool.Pool
	dsn      string
	slot     string
	capture  *Capture
	engine   *Engine
	clients  map[string]*client
	rounds   int
	restarts int
	releases int
	trace    []string
	// history notes restarts and schema changes by round, for failures.
	history []string
	// writer is an application pool apart from the engine's (writerPool).
	writer *pgxpool.Pool
}

func newHarness(t *testing.T, schema string) *harness {
	t.Helper()
	dsn, pool := openLogicalDB(t)
	mustExec(t, pool, schema)
	// Creating a slot waits for a consistent point, which can take until
	// the next running-transactions record (every 15 s) on a busy server.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := Install(ctx, pool); err != nil {
		t.Fatal(err)
	}
	mustExec(t, pool, `CREATE PUBLICATION gregale_live FOR ALL TABLES`)
	h := &harness{t: t, pool: pool, dsn: dsn, slot: fmt.Sprintf("lq_%d", time.Now().UnixNano()), clients: map[string]*client{}}
	// Registered before the slot exists, so a failed setup cannot leak it
	// (a leaked slot holds WAL and blocks dropping the database).
	t.Cleanup(func() {
		if h.capture != nil {
			_ = h.capture.Close()
		}
		h.dropSlot()
	})
	capture, err := OpenSlot(ctx, dsn, h.slot, "gregale_live")
	if err != nil {
		t.Fatal(err)
	}
	h.capture = capture
	h.engine = NewEngine(pool, capture)
	h.engine.VerifyEachRound = true
	if os.Getenv("LIVEQUERY_TRACE") != "" {
		h.engine.Trace = func(line string) { h.trace = append(h.trace, line) }
	}
	return h
}

// dropSlot drops the harness slot once its walsender lets go of it.
func (h *harness) dropSlot() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		_, err := h.pool.Exec(ctx, `SELECT pg_drop_replication_slot(slot_name) FROM pg_replication_slots WHERE slot_name = $1`, h.slot)
		if err == nil || ctx.Err() != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// restart checkpoints the engine, stops it and restores a new one from the
// checkpoint over the slot, as livequeryd does across a park; with lose,
// the slot is dropped in between, as when its WAL is gone.
func (h *harness) restart(lose bool) { h.restartWith(lose, nil) }

// restartWith runs down, when set, while the engine is stopped: after the
// checkpoint and before the restore.
func (h *harness) restartWith(lose bool, down func()) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var buf bytes.Buffer
	if err := h.engine.WriteCheckpoint(&buf); err != nil {
		h.t.Fatal(err)
	}
	_ = h.capture.Close()
	if down != nil {
		down()
	}
	if lose {
		h.dropSlot()
	}
	capture, err := OpenSlot(ctx, h.dsn, h.slot, "gregale_live")
	if err != nil {
		h.t.Fatal(err)
	}
	if capture.Created != lose {
		h.t.Fatalf("slot created = %v after a restart that lost it = %v", capture.Created, lose)
	}
	e, err := RestoreEngine(ctx, h.pool, capture, &buf)
	if err != nil {
		h.t.Fatal(err)
	}
	old := h.engine
	e.VerifyEachRound, e.Trace, e.StatementHook, e.Stats = old.VerifyEachRound, old.Trace, old.StatementHook, old.Stats
	h.engine, h.capture = e, capture
	h.restarts++
	h.history = append(h.history, fmt.Sprintf("after round %d: restart (slot lost %v)", h.rounds, lose))
}

func (h *harness) register(id, sql string, args ...any) *Plan {
	h.t.Helper()
	p, err := h.engine.Register(context.Background(), id, sql, args...)
	if err != nil {
		h.t.Fatalf("register %s: %v", id, err)
	}
	h.clients[id] = &client{}
	return p
}

// round runs one round, applies the deltas to the clients and checks each
// client against the engine (the engine itself is checked against the query
// at the round snapshot by VerifyEachRound).
func (h *harness) round() []Delta {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	recompiles := h.engine.Stats.Recompiles
	deltas, err := h.engine.Round(ctx)
	if n := h.engine.Stats.Recompiles - recompiles; n > 0 {
		h.history = append(h.history, fmt.Sprintf("round %d: %d recompiles", h.rounds+1, n))
	}
	if err != nil {
		h.t.Logf("history: %v", h.history)
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
		if d.Closed != nil {
			if !c.result.Closed {
				h.t.Fatalf("round %d query %s: closed delta did not close the client", h.rounds, d.Query)
			}
			continue
		}
		if err := c.equal(h.engine.Result(d.Query)); err != nil {
			h.t.Fatalf("round %d query %s: %v", h.rounds, d.Query, err)
		}
	}
	if len(deltas) == 0 {
		return deltas
	}
	// The round's progress frame: every client now sees every transaction
	// the round applied, whether or not its rows changed (awaitTxId).
	for _, c := range h.clients {
		if c.result.Closed {
			continue
		}
		c.deliver(h.t, ProgressFrame("db", deltas[0].Snapshot))
		for _, xid := range deltas[0].Xids {
			if !c.result.Sees(xid) {
				h.t.Fatalf("round %d: client does not see applied transaction %s at %s", h.rounds, strconv.FormatUint(xid, 10), c.result.Snapshot)
			}
		}
	}
	return deltas
}

const chatSchema = `
	CREATE TABLE channels (id bigint PRIMARY KEY, name text NOT NULL, archived boolean NOT NULL DEFAULT false);
	CREATE TABLE members (channel_id bigint, user_id bigint, PRIMARY KEY (channel_id, user_id));
	CREATE TABLE messages (id bigint PRIMARY KEY, channel_id bigint, author bigint, body text);`

// adr: 20261016151947 — every maintenance mode stays equal to the query at each round
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
		"top_channels": h.register("top_channels", `
			SELECT channel_id, count(*) AS n FROM messages GROUP BY channel_id ORDER BY n DESC, channel_id LIMIT 2`),
	}
	if plans["inbox"].Mode != ModeLineage || plans["empty_channels"].Mode != ModeLineage ||
		plans["counts"].Mode != ModeGroup || plans["latest"].Order == nil || plans["top_channels"].Mode != ModeRecompute {
		t.Fatalf("unexpected modes: %v %v %v %v %v", plans["inbox"].Mode, plans["empty_channels"].Mode, plans["counts"].Mode,
			plans["latest"].Order, plans["top_channels"].Mode)
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
