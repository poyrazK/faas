package livequery

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// releaseAndResume releases the database, runs while (commits the engine is
// not connected to see), then resumes on the slot, after dropping it when
// lose is set.
func (h *harness) releaseAndResume(lose bool, while func()) {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if h.engine.Checkpointed {
		// The slot releases only what a durable checkpoint covers.
		if err := h.engine.WriteCheckpoint(io.Discard); err != nil {
			h.t.Fatal(err)
		}
	}
	if err := h.engine.Release(); err != nil {
		h.t.Fatal(err)
	}
	if while != nil {
		while()
	}
	if lose {
		h.dropSlot()
	}
	capture, err := OpenSlot(ctx, h.dsn, h.slot, "gregale_live")
	if err != nil {
		h.t.Fatal(err)
	}
	if capture.Created != lose {
		h.t.Fatalf("slot created = %v after a release that lost it = %v", capture.Created, lose)
	}
	if err := h.engine.Resume(capture); err != nil {
		h.t.Fatal(err)
	}
	h.capture = capture
	h.history = append(h.history, fmt.Sprintf("after round %d: release and resume (slot lost %v)", h.rounds, lose))
}

// writerPool is an application's pool on the harness database, apart from
// the engine's, which a released engine must leave empty.
func (h *harness) writerPool() *pgxpool.Pool {
	h.t.Helper()
	if h.writer == nil {
		pool, err := pgxpool.New(context.Background(), h.dsn)
		if err != nil {
			h.t.Fatal(err)
		}
		h.writer = pool
		h.t.Cleanup(pool.Close)
	}
	return h.writer
}

// slotActive reports whether a walsender holds the harness slot, read on a
// connection of its own so the engine's pool stays empty.
func (h *harness) slotActive() bool {
	h.t.Helper()
	conn, err := pgx.Connect(context.Background(), h.dsn)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var active bool
	if err := conn.QueryRow(context.Background(), `SELECT active FROM pg_replication_slots WHERE slot_name = $1`, h.slot).Scan(&active); err != nil {
		h.t.Fatal(err)
	}
	return active
}

// adr: 20261016151947 — a released engine holds no connection to the database
// (so it can scale to zero) and serves its results from memory: a late
// subscriber to a held result joins without the database. On resume it
// catches up exactly with what committed meanwhile; when the slot was lost
// meanwhile, every result is read again and sent whole.
func TestReleaseAndResume(t *testing.T) {
	h := newHarness(t, chatSchema)
	const feed = `SELECT id, body FROM messages WHERE channel_id = $1 ORDER BY id DESC LIMIT 2`
	const counts = `SELECT channel_id, count(*) AS n FROM messages GROUP BY channel_id`
	h.register("feed", feed, int64(1))
	h.register("counts", counts)
	mustExec(t, h.pool, `INSERT INTO messages VALUES (1, 1, 7, 'a'), (2, 1, 7, 'b'), (3, 2, 8, 'c')`)
	h.round()
	h.round()

	h.releaseAndResume(false, func() {
		if !h.engine.Released() || h.engine.Pending() {
			t.Fatal("the engine does not report itself released")
		}
		if _, err := h.engine.Round(context.Background()); !errors.Is(err, ErrReleased) {
			t.Fatalf("a round while released = %v", err)
		}
		if n := h.pool.Stat().TotalConns(); n != 0 {
			t.Fatalf("the released engine's pool holds %d connections", n)
		}
		if h.slotActive() {
			t.Fatal("the released engine still streams from its slot")
		}
		// A late subscriber to the held result joins from memory.
		if !h.engine.Join("late", feed, nil, int64(1)) {
			t.Fatal("a late subscriber to a held result did not join")
		}
		if h.engine.Join("other", feed, nil, int64(2)) {
			t.Fatal("a subscriber with other arguments joined a held result")
		}
		d, ok := h.engine.Current("late")
		if !ok {
			t.Fatal("no current result for the joined subscriber")
		}
		h.clients["late"] = &client{}
		h.clients["late"].apply(t, d)
		if err := h.clients["late"].equal(h.engine.Result("feed")); err != nil {
			t.Fatalf("joined result: %v", err)
		}
		// Commits the engine is not there to see.
		writer := h.writerPool()
		mustExec(t, writer, `INSERT INTO messages VALUES (4, 1, 7, 'd'), (5, 2, 8, 'e')`)
		mustExec(t, writer, `UPDATE messages SET body = 'b2' WHERE id = 2; DELETE FROM messages WHERE id = 3`)
	})
	if !h.slotActive() {
		t.Fatal("the resumed engine does not stream from its slot")
	}
	h.round() // VerifyEachRound checks every result against PostgreSQL
	if got := h.ids("feed"); got != "2,4," {
		t.Fatalf("feed after resuming = %s", got)
	}
	if err := h.clients["late"].equal(h.engine.Result("late")); err != nil {
		t.Fatalf("joined subscriber after resuming: %v", err)
	}

	// The slot lost while released: everything is read again.
	h.releaseAndResume(true, func() {
		mustExec(t, h.writerPool(), `INSERT INTO messages VALUES (6, 1, 7, 'f')`)
	})
	deltas := h.round()
	resets := 0
	for _, d := range deltas {
		if d.Reset {
			resets++
		}
	}
	if resets != 3 {
		t.Fatalf("after losing the slot %d of 3 subscribers were sent their result whole", resets)
	}
	if got := h.ids("feed"); got != "4,6," {
		t.Fatalf("feed after losing the slot = %s", got)
	}
}
