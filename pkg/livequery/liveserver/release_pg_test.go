package liveserver

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/livequery"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// slotActive reports whether a walsender streams the fixture's slot, read
// on a connection of its own.
func (f *fixture) slotActive() bool {
	f.t.Helper()
	conn, err := pgx.Connect(context.Background(), f.dsn)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var active bool
	if err := conn.QueryRow(context.Background(), `SELECT active FROM pg_replication_slots WHERE slot_name = $1`, f.slot).Scan(&active); err != nil {
		f.t.Fatal(err)
	}
	return active
}

// adr: 20261016151947 — scale to zero: a hub with no transaction to apply for
// ReleaseAfter releases its database, holding no connection to it, while
// subscribers keep their results and new subscribers to a held result join
// without it. It resumes, catching up exactly, when the activity probe
// reports the database running, when a subscription needs the database, or
// when Wake is called.
func TestHubReleasesAnIdleDatabase(t *testing.T) {
	var awake atomic.Bool
	cfg := Config{ReleaseAfter: time.Minute, AwakeEvery: 20 * time.Millisecond,
		Awake:      func(context.Context) (bool, error) { return awake.Load(), nil },
		Checkpoint: filepath.Join(t.TempDir(), "checkpoint")}
	f := openFixture(t, schema, cfg, nil, true)
	var reconnects atomic.Int32
	f.sync(func() {
		reconnect := f.hub.cfg.Reconnect
		f.hub.cfg.Reconnect = func(ctx context.Context) (*livequery.Capture, error) {
			reconnects.Add(1)
			return reconnect(ctx)
		}
	})
	release := func() {
		t.Helper()
		f.clock.advance(70 * time.Second)
		waitFor(t, "the hub to release the database", f.hub.Released)
	}

	feed := `SELECT id, body FROM messages WHERE channel_id = $1 ORDER BY id DESC LIMIT 2`
	c := f.dial()
	c.subscribe("feed", f.seal(f.secret, "db_1", feed, 5*time.Minute, "1"))
	c.until("the initial result", func() bool { return c.results["feed"].Ready() })
	xid := f.write(`INSERT INTO messages VALUES (1, 1, 'a')`)
	c.until("the first message", func() bool { return c.results["feed"].Sees(xid) })

	release()
	if n := f.pool.Stat().TotalConns(); n != 0 {
		t.Fatalf("the released hub's pool holds %d connections", n)
	}
	if f.slotActive() {
		t.Fatal("the released hub still streams from its slot")
	}
	// A subscriber to the held result is served without the database.
	d := f.dial()
	d.subscribe("same", f.seal(f.secret, "db_1", feed, 5*time.Minute, "1"))
	d.until("the held result", func() bool { return d.results["same"].Ready() })
	if got := values(d.results["same"]); got != "1:a" {
		t.Fatalf("held result %q", got)
	}
	if !f.hub.Released() || reconnects.Load() != 0 {
		t.Fatalf("serving a held result reconnected (released %v, reconnects %d)", f.hub.Released(), reconnects.Load())
	}

	// A commit while released streams once the probe reports the database
	// running.
	xid = f.write(`INSERT INTO messages VALUES (2, 1, 'b')`)
	time.Sleep(100 * time.Millisecond)
	if c.results["feed"].Sees(xid) {
		t.Fatal("a released hub delivered a commit")
	}
	awake.Store(true)
	c.until("the commit made while released", func() bool { return c.results["feed"].Sees(xid) })
	d.until("the commit made while released", func() bool { return d.results["same"].Sees(xid) })
	if got := values(c.results["feed"]); got != "2:b,1:a" {
		t.Fatalf("after resuming %q", got)
	}
	if f.hub.Released() || reconnects.Load() != 1 {
		t.Fatalf("after the probe: released %v, reconnects %d", f.hub.Released(), reconnects.Load())
	}
	awake.Store(false)

	// A subscription that needs the database resumes the hub.
	release()
	c.subscribe("other", f.seal(f.secret, "db_1", feed, 5*time.Minute, "2"))
	c.until("a result the hub did not hold", func() bool { return c.results["other"].Ready() })
	if f.hub.Released() || reconnects.Load() != 2 {
		t.Fatalf("after a new subscription: released %v, reconnects %d", f.hub.Released(), reconnects.Load())
	}

	// Wake resumes the hub.
	release()
	f.hub.Wake(context.Background())
	waitFor(t, "Wake to resume the hub", func() bool { return !f.hub.Released() })
	if reconnects.Load() != 3 {
		t.Fatalf("after Wake: reconnects %d", reconnects.Load())
	}
	xid = f.write(`INSERT INTO messages VALUES (3, 1, 'c')`)
	c.until("a commit after Wake", func() bool { return c.results["feed"].Sees(xid) })
}

// adr: 20261016151947 — a hub that may release must be able to reconnect.
func TestHubReleaseNeedsReconnect(t *testing.T) {
	h := New(Config{ReleaseAfter: time.Minute})
	if err := h.Run(context.Background()); !errors.Is(err, errReleaseConfig) {
		t.Fatalf("Run = %v, want %v", err, errReleaseConfig)
	}
}
