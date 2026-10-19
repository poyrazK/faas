package livequery

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"
)

// adr: 20261016151947 — an engine can hand compilation to a remote compiler, so the
// SQL parser never starts beside the database: plans survive the trip
// unchanged, every maintenance mode runs from them, schema changes recompile
// remotely, and a compiler outage fails the round instead of closing
// subscriptions.
func TestRemoteCompiler(t *testing.T) {
	h := newHarness(t, chatSchema)
	srv := httptest.NewServer(CompilerHandler(h.pool))
	defer func() { srv.Close() }()
	h.engine.Compiler = RemoteCompiler(srv.URL, nil)
	queries := map[string]string{
		"inbox": `SELECT c.name, m.id AS message, m.body FROM members mb JOIN channels c ON c.id = mb.channel_id
			JOIN messages m ON m.channel_id = c.id WHERE mb.user_id = $1 AND NOT c.archived`,
		"empty":  `SELECT c.id, c.name FROM channels c LEFT JOIN messages m ON m.channel_id = c.id WHERE m.id IS NULL`,
		"counts": `SELECT channel_id, count(*) AS n, sum(author) AS s, min(body) AS first FROM messages GROUP BY channel_id`,
		"feed":   `SELECT id, body FROM messages ORDER BY id DESC LIMIT 2`,
		"member": `SELECT id, name FROM channels WHERE EXISTS (SELECT 1 FROM members WHERE channel_id = channels.id)`,
		"bodies": `SELECT string_agg(body, ',' ORDER BY id) AS all_bodies FROM messages`,
		"all":    `SELECT * FROM messages`,
	}
	ctx := context.Background()
	conn, err := h.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for id, sql := range queries {
		local, err := Compile(ctx, conn.Conn(), sql)
		if err != nil {
			t.Fatal(err)
		}
		remote, err := h.engine.Compiler(ctx, sql)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if local.Fingerprint() != remote.Fingerprint() {
			t.Fatalf("%s: the plan changed in transit", id)
		}
		if id == "inbox" {
			h.register(id, sql, int64(7))
		} else {
			h.register(id, sql)
		}
	}
	conn.Release()
	if _, err := h.engine.Compiler(ctx, `SELECT id, now() FROM messages`); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsupported query: %v", err)
	}
	if _, err := h.engine.Compiler(ctx, `SELECT nope FROM messages`); err == nil || errors.Is(err, ErrUnsupported) || errors.Is(err, ErrCompilerUnavailable) {
		t.Fatalf("invalid query: %v", err)
	}

	h.round()
	mustExec(t, h.pool, `INSERT INTO channels VALUES (1, 'general', false), (2, 'random', false), (3, 'quiet', false)`)
	mustExec(t, h.pool, `INSERT INTO members VALUES (1, 7), (2, 7)`)
	mustExec(t, h.pool, `INSERT INTO messages VALUES (10, 1, 7, 'hi'), (11, 2, 8, 'yo'), (12, 1, 7, 'again')`)
	h.round()
	mustExec(t, h.pool, `ALTER TABLE messages ADD COLUMN edited boolean NOT NULL DEFAULT false`)
	if got := outcomes(h.round()); got != "all=reset bodies=- counts=- empty=- feed=- inbox=- member=-" {
		t.Fatalf("schema change through the remote compiler: %s", got)
	}

	// The compiler goes away during a schema change: the round fails and is
	// retried; no subscription is closed for it.
	srv.Close()
	mustExec(t, h.pool, `ALTER TABLE messages ADD COLUMN pinned boolean NOT NULL DEFAULT false`)
	rctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if _, err := h.engine.Round(rctx); !errors.Is(err, ErrCompilerUnavailable) {
		t.Fatalf("round without a compiler: %v", err)
	}
	if n := h.engine.Subscriptions(); n != len(queries) {
		t.Fatalf("%d subscriptions after the outage, want %d", n, len(queries))
	}
	srv = httptest.NewServer(CompilerHandler(h.pool))
	h.engine.Compiler = RemoteCompiler(srv.URL, nil)
	mustExec(t, h.pool, `INSERT INTO messages (id, channel_id, author, body) VALUES (13, 2, 9, 'back')`)
	h.round()
	if got := len(h.engine.Result("all")); got != 4 {
		t.Fatalf("all holds %d rows after the compiler returned, want 4", got)
	}
}
