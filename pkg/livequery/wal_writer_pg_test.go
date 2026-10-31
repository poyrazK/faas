package livequery

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// adr: 20261016151947 — a database tuned with synchronous_commit = off still
// gets prompt rounds: the round marker commits synchronously, so it reaches
// the slot without waiting for the WAL writer (wal_writer_delay).
func TestRoundsDoNotWaitForTheWALWriter(t *testing.T) {
	dsn, pool := openLogicalDB(t)
	ctx := context.Background()
	var delay time.Duration
	var delayText, name string
	if err := pool.QueryRow(ctx, "SELECT current_setting('wal_writer_delay'), current_database()").Scan(&delayText, &name); err != nil {
		t.Fatal(err)
	}
	if delay, _ = time.ParseDuration(delayText); delay < 100*time.Millisecond {
		t.Skipf("wal_writer_delay is %s; a round waiting for it would not stand out", delayText)
	}
	mustExec(t, pool, `ALTER DATABASE "`+name+`" SET synchronous_commit = off`)
	pool.Close()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var setting string
	if err := pool.QueryRow(ctx, "SHOW synchronous_commit").Scan(&setting); err != nil || setting != "off" {
		t.Fatalf("synchronous_commit = %q, %v; want off for this test", setting, err)
	}
	mustExec(t, pool, `CREATE TABLE items (id int PRIMARY KEY, v text NOT NULL); INSERT INTO items VALUES (1, 'a')`)
	if err := Install(ctx, pool); err != nil {
		t.Fatal(err)
	}
	mustExec(t, pool, `CREATE PUBLICATION gregale_live FOR ALL TABLES`)
	capture, err := OpenCapture(ctx, dsn, "lq_wal_writer", "gregale_live")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = capture.Close() })
	engine := NewEngine(pool, capture)
	if _, err := engine.Register(ctx, "s1", "SELECT id, v FROM items"); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Round(ctx); err != nil {
		t.Fatal(err)
	}
	var took []time.Duration
	for range 7 {
		start := time.Now()
		if _, err := engine.Round(ctx); err != nil {
			t.Fatal(err)
		}
		took = append(took, time.Since(start))
	}
	slices.Sort(took)
	if median := took[len(took)/2]; median >= delay/2 {
		t.Fatalf("idle rounds take a median %s with wal_writer_delay %s: the marker waits for the WAL writer (all: %v)", median, delay, took)
	}
}
