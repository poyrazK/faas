package livequery

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestBenchFirstResult times each step between a subscription and its first
// result: creating or reopening the slot, compiling (the process's first
// compile loads the SQL parser), the first round, the marker's trip through
// the slot, and creating a slot while another transaction runs. It runs only
// with LIVEQUERY_BENCH=1.
func TestBenchFirstResult(t *testing.T) {
	if os.Getenv("LIVEQUERY_BENCH") == "" {
		t.Skip("set LIVEQUERY_BENCH=1 to run the benchmark")
	}
	ctx := context.Background()
	dsn, pool := openLogicalDB(t)
	mustExec(t, pool, `CREATE TABLE items (id int PRIMARY KEY, v text NOT NULL);
		INSERT INTO items SELECT g, 'v' || g FROM generate_series(1, 1000) g`)
	if err := Install(ctx, pool); err != nil {
		t.Fatal(err)
	}
	mustExec(t, pool, `CREATE PUBLICATION gregale_live FOR ALL TABLES`)
	slot := fmt.Sprintf("measure_%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `SELECT pg_drop_replication_slot($1)`, slot)
	})
	step := func(name string, f func()) time.Duration {
		start := time.Now()
		f()
		d := time.Since(start)
		t.Logf("%-48s %8.1f ms", name, float64(d.Microseconds())/1000)
		return d
	}
	var capture *Capture
	var err error
	step("create slot (idle server)", func() {
		if capture, err = OpenSlot(ctx, dsn, slot, "gregale_live"); err != nil {
			t.Fatal(err)
		}
	})
	engine := NewEngine(pool, capture)
	step("register: compile + first query", func() {
		if _, err := engine.Register(ctx, "s1", "SELECT id, v FROM items WHERE id < $1", 100); err != nil {
			t.Fatal(err)
		}
	})
	measureRound(t, engine, step, "first round (initial read)")
	measureRound(t, engine, step, "idle round")
	measureMarker(t, engine, step)
	step("register: second query, same table", func() {
		if _, err := engine.Register(ctx, "s2", "SELECT count(*) FROM items WHERE id > $1", 10); err != nil {
			t.Fatal(err)
		}
	})
	measureRound(t, engine, step, "round with the second query")
	step("register: joining an existing query", func() {
		if _, err := engine.Register(ctx, "s3", "SELECT id, v FROM items WHERE id < $1", 100); err != nil {
			t.Fatal(err)
		}
	})
	measureRound(t, engine, step, "round serving the joiner")

	// A hub restart: the slot exists.
	_ = engine.Release()
	step("reopen existing slot", func() {
		if capture, err = OpenSlot(ctx, dsn, slot, "gregale_live"); err != nil {
			t.Fatal(err)
		}
	})
	engine = NewEngine(pool, capture)
	step("register after reopen", func() {
		if _, err := engine.Register(ctx, "s1", "SELECT id, v FROM items WHERE id < $1", 100); err != nil {
			t.Fatal(err)
		}
	})
	measureRound(t, engine, step, "first round after reopen")
	_ = engine.Release()

	// A busy server: another transaction holds an xid while a new slot is
	// created.
	other, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close(ctx) }()
	tx, err := other.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "SELECT pg_current_xact_id()"); err != nil {
		t.Fatal(err)
	}
	busy := slot + "_busy"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `SELECT pg_drop_replication_slot($1)`, busy)
	})
	go func() {
		time.Sleep(3 * time.Second)
		_ = tx.Commit(ctx)
	}()
	step("create slot while a transaction runs 3s", func() {
		c, err := OpenSlot(ctx, dsn, busy, "gregale_live")
		if err != nil {
			t.Fatal(err)
		}
		_ = c.Close() //nolint:contextcheck // Close bounds its own shutdown
	})
}

func measureRound(t *testing.T, e *Engine, step func(string, func()) time.Duration, name string) {
	step(name, func() {
		if _, err := e.Round(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
}

// measureMarker times one marker's trip through the slot.
func measureMarker(t *testing.T, e *Engine, step func(string, func()) time.Duration) {
	marker := "measure." + strconv.FormatInt(time.Now().UnixNano(), 10)
	step("marker round trip through the slot", func() {
		if _, err := e.pool.Exec(context.Background(), "SELECT pg_logical_emit_message(false, $1, $2), pg_current_xact_id()", MarkerPrefix, marker); err != nil {
			t.Fatal(err)
		}
		if _, err := e.drain(context.Background(), marker); err != nil {
			t.Fatal(err)
		}
	})
	marker += ".sync"
	step("marker round trip, synchronous commit forced", func() {
		if _, err := e.pool.Exec(context.Background(), "SELECT set_config('synchronous_commit', 'on', true), pg_logical_emit_message(false, $1, $2), pg_current_xact_id()", MarkerPrefix, marker); err != nil {
			t.Fatal(err)
		}
		if _, err := e.drain(context.Background(), marker); err != nil {
			t.Fatal(err)
		}
	})
}
