package liveserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/livequery"
	"github.com/onebox-faas/faas/pkg/livequery/liveproto"
	"github.com/onebox-faas/faas/pkg/livequery/seal"
)

// openDB creates a throwaway database on LIVEQUERY_DATABASE_URL, which must
// run with wal_level=logical.
func openDB(t *testing.T) (string, *pgxpool.Pool) {
	t.Helper()
	base := os.Getenv("LIVEQUERY_DATABASE_URL")
	if base == "" {
		t.Skip("LIVEQUERY_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	var level string
	if err := admin.QueryRow(ctx, "SHOW wal_level").Scan(&level); err != nil || level != "logical" {
		t.Skipf("wal_level is %q, need logical", level)
	}
	name := fmt.Sprintf("lqs_%d_%d", os.Getpid(), rand.Uint32())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		if admin, err := pgx.Connect(context.Background(), base); err == nil {
			_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
			_ = admin.Close(context.Background())
		}
	})
	return u.String(), pool
}

type testLog struct{ t *testing.T }

func (l testLog) Write(p []byte) (int, error) {
	l.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}

// clock is the hub's time, which tests move forward to expire seals.
type clock struct {
	mu     sync.Mutex
	offset time.Duration
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().Add(c.offset)
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.offset += d
	c.mu.Unlock()
}

type fixture struct {
	t      *testing.T
	pool   *pgxpool.Pool
	hub    *Hub
	engine *livequery.Engine
	srv    *httptest.Server
	secret seal.Secret
	other  seal.Secret
	clock  *clock
}

func newFixture(t *testing.T, schema string, cfg Config) *fixture {
	return newFixtureWith(t, schema, cfg, nil)
}

// newFixtureWith runs prepare on the engine before the hub starts.
func newFixtureWith(t *testing.T, schema string, cfg Config, prepare func(*livequery.Engine)) *fixture {
	t.Helper()
	dsn, pool := openDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := pool.Exec(ctx, schema); err != nil {
		t.Fatal(err)
	}
	if err := livequery.Install(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE PUBLICATION gregale_live FOR ALL TABLES`); err != nil {
		t.Fatal(err)
	}
	capture, err := livequery.OpenCapture(ctx, dsn, fmt.Sprintf("lqs_%d", time.Now().UnixNano()), "gregale_live")
	if err != nil {
		t.Fatal(err)
	}
	engine := livequery.NewEngine(pool, capture)
	engine.VerifyEachRound = true
	f := &fixture{t: t, pool: pool, engine: engine, clock: &clock{}}
	f.secret = mustSecret(t)
	f.other = mustSecret(t)
	ring, err := seal.NewKeyring(seal.Key{Database: "db_1", Secret: f.secret}, seal.Key{Database: "db_other", Secret: f.other})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Database, cfg.Keyring, cfg.Engine, cfg.Now, cfg.Idle = "db_1", ring, engine, f.clock.Now, 5*time.Millisecond
	cfg.Logger = slog.New(slog.NewTextHandler(testLog{t}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	if prepare != nil {
		prepare(engine)
	}
	f.hub = New(cfg)
	stopped := make(chan error, 1)
	go func() { stopped <- f.hub.Run(ctx) }()
	mux := http.NewServeMux()
	mux.Handle("/live", WebSocket(f.hub, func(*http.Request) bool { return true }))
	f.srv = httptest.NewServer(mux)
	t.Cleanup(func() {
		f.srv.Close()
		cancel()
		if err := <-stopped; err != nil {
			t.Errorf("hub: %v", err)
		}
		_ = capture.Close()
	})
	return f
}

func mustSecret(t *testing.T) seal.Secret {
	t.Helper()
	text, err := seal.GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	s, err := seal.ParseSecret(text)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (f *fixture) seal(secret seal.Secret, db, sql string, ttl time.Duration, params ...string) string {
	f.t.Helper()
	q := seal.Query{Database: db, SQL: sql}
	for _, p := range params {
		q.Params = append(q.Params, &p)
	}
	token, err := secret.Seal(q, f.clock.Now(), ttl)
	if err != nil {
		f.t.Fatal(err)
	}
	return token
}

func (f *fixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.pool.Exec(context.Background(), sql, args...); err != nil {
		f.t.Fatalf("%s: %v", sql, err)
	}
}

// write runs sql and returns its transaction ID.
func (f *fixture) write(sql string) uint64 {
	f.t.Helper()
	tx, err := f.pool.Begin(context.Background())
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), sql); err != nil {
		f.t.Fatalf("%s: %v", sql, err)
	}
	var xid string
	if err := tx.QueryRow(context.Background(), "SELECT pg_current_xact_id()::text").Scan(&xid); err != nil {
		f.t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		f.t.Fatal(err)
	}
	n, _ := strconv.ParseUint(xid, 10, 64)
	return n
}

// client is a browser: every frame goes through the reference client.
type client struct {
	t       *testing.T
	ws      *websocket.Conn
	frames  chan any
	results map[string]*liveproto.Result
	errors  []*liveproto.Error
}

func (f *fixture) dial() *client {
	f.t.Helper()
	ws, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(f.srv.URL, "http")+"/live", nil)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		f.t.Fatal(err)
	}
	c := &client{t: f.t, ws: ws, frames: make(chan any, 1024), results: map[string]*liveproto.Result{}}
	go func() {
		defer close(c.frames)
		for {
			_, data, err := ws.ReadMessage()
			if err != nil {
				return
			}
			frame, err := liveproto.ParseServerFrame(data)
			if err != nil {
				f.t.Errorf("server sent a bad frame %s: %v", data, err)
				return
			}
			c.frames <- frame
		}
	}()
	f.t.Cleanup(func() { _ = ws.Close() })
	return c
}

func (c *client) send(frame liveproto.ClientFrame) {
	c.t.Helper()
	data, _ := json.Marshal(frame)
	if err := c.ws.WriteMessage(websocket.TextMessage, data); err != nil {
		c.t.Fatal(err)
	}
}

func (c *client) subscribe(id, sealed string) {
	c.t.Helper()
	c.results[id] = &liveproto.Result{}
	c.send(liveproto.ClientFrame{Type: liveproto.TypeSubscribe, Subscription: id, Sealed: sealed})
}

// until applies frames until cond holds.
func (c *client) until(what string, cond func() bool) {
	c.t.Helper()
	deadline := time.After(20 * time.Second)
	for !cond() {
		select {
		case frame, ok := <-c.frames:
			if !ok {
				c.t.Fatalf("connection closed waiting for %s", what)
			}
			c.apply(frame)
		case <-deadline:
			c.t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func (c *client) apply(frame any) {
	c.t.Helper()
	if e, ok := frame.(*liveproto.Error); ok {
		c.errors = append(c.errors, e)
		if e.Subscription == "" {
			return
		}
	}
	if p, ok := frame.(*liveproto.Progress); ok {
		for _, r := range c.results {
			if err := r.Apply(p); err != nil && !r.Closed {
				c.t.Fatalf("progress refused: %v", err)
			}
		}
		return
	}
	var id string
	switch f := frame.(type) {
	case *liveproto.Rows:
		id = f.Subscription
	case *liveproto.Changes:
		id = f.Subscription
	case *liveproto.Renewed:
		id = f.Subscription
	case *liveproto.Error:
		id = f.Subscription
	}
	r := c.results[id]
	if r == nil {
		c.t.Fatalf("frame for unknown subscription %q: %#v", id, frame)
	}
	if err := r.Apply(frame); err != nil {
		c.t.Fatalf("client refused %#v: %v", frame, err)
	}
}

func (c *client) lastError(id string) *liveproto.Error {
	for i := len(c.errors) - 1; i >= 0; i-- {
		if c.errors[i].Subscription == id {
			return c.errors[i]
		}
	}
	return nil
}

func values(r *liveproto.Result) string {
	var out []string
	for _, row := range r.Rows() {
		var vs []string
		for _, v := range row.Values {
			if v == nil {
				vs = append(vs, "NULL")
			} else {
				vs = append(vs, *v)
			}
		}
		out = append(out, strings.Join(vs, ":"))
	}
	return strings.Join(out, ",")
}

const schema = `CREATE TABLE messages (id bigint PRIMARY KEY, channel_id bigint NOT NULL, body text);
	CREATE TABLE reads (id bigint PRIMARY KEY, n int)`

// adr: 1384 — a client subscribes with a seal over a WebSocket and holds the
// query's result through changes, progress, expiry and renewal, with every
// frame checked by the reference client.
func TestHubServesSealedQueries(t *testing.T) {
	f := newFixture(t, schema, Config{})
	c := f.dial()
	feed := `SELECT id, body FROM messages WHERE channel_id = $1 ORDER BY id DESC LIMIT 2`
	c.subscribe("feed", f.seal(f.secret, "db_1", feed, time.Minute, "1"))
	c.until("the initial result", func() bool { return c.results["feed"].Ready() })
	if got := values(c.results["feed"]); got != "" {
		t.Fatalf("initial rows %q", got)
	}

	xid := f.write(`INSERT INTO messages VALUES (1, 1, 'a'), (2, 1, 'b'), (3, 1, 'c'), (4, 2, 'other')`)
	c.until("the first messages", func() bool { return c.results["feed"].Sees(xid) })
	if got := values(c.results["feed"]); got != "3:c,2:b" {
		t.Fatalf("feed %q", got)
	}
	// A write the query does not read still resolves awaitTxId.
	xid = f.write(`INSERT INTO reads VALUES (1, 1)`)
	c.until("progress past an unrelated write", func() bool { return c.results["feed"].Sees(xid) })

	// The seal lapses: the subscription goes stale and gets nothing.
	f.clock.advance(2 * time.Minute)
	c.until("staleness", func() bool { return c.results["feed"].Stale })
	if e := c.lastError("feed"); e.Code != liveproto.CodeSealExpired || e.Closed {
		t.Fatalf("expiry error %+v", e)
	}
	staleXid := f.write(`INSERT INTO messages VALUES (5, 1, 'while stale')`)
	// Renewing with a different query is refused; the same query resumes
	// with the whole current result.
	c.send(liveproto.ClientFrame{Type: liveproto.TypeRenew, Subscription: "feed",
		Sealed: f.seal(f.secret, "db_1", feed, time.Minute, "2")})
	c.until("the mismatch", func() bool { e := c.lastError("feed"); return e != nil && e.Code == liveproto.CodeRenewMismatch })
	version := c.results["feed"].Version
	c.send(liveproto.ClientFrame{Type: liveproto.TypeRenew, Subscription: "feed",
		Sealed: f.seal(f.secret, "db_1", feed, time.Minute, "1")})
	// The renewal sends the result as of the last round; the write made
	// while stale arrives with its round, as an ordinary change if that round
	// comes after the renewal.
	c.until("the renewed result", func() bool {
		r := c.results["feed"]
		return !r.Stale && r.Version > version && r.Sees(staleXid)
	})
	if got := values(c.results["feed"]); got != "5:while stale,3:c" {
		t.Fatalf("after renewal %q", got)
	}

	// Subscriptions the server refuses.
	for _, tc := range []struct {
		id, sealed, code string
	}{
		{"forged", "gls1.0123456789abcdef.AAAA", liveproto.CodeSealRejected},
		{"elsewhere", f.seal(f.other, "db_other", `SELECT id FROM messages`, time.Minute), liveproto.CodeForbidden},
		{"clock", f.seal(f.secret, "db_1", `SELECT id, now() FROM messages`, time.Minute), liveproto.CodeUnsupported},
		{"missing", f.seal(f.secret, "db_1", `SELECT id FROM nope`, time.Minute), liveproto.CodeInvalidQuery},
	} {
		c.subscribe(tc.id, tc.sealed)
		c.until(tc.id+" refused", func() bool { return c.results[tc.id].Closed })
		if e := c.lastError(tc.id); e.Code != tc.code {
			t.Fatalf("%s: code %s, want %s (%s)", tc.id, e.Code, tc.code, e.Message)
		}
	}

	// A second client of the same query gets the result whole; dropping a
	// table closes both.
	d := f.dial()
	d.subscribe("same", f.seal(f.secret, "db_1", feed, time.Minute, "1"))
	d.until("the shared result", func() bool { return d.results["same"].Ready() })
	if got := values(d.results["same"]); got != "5:while stale,3:c" {
		t.Fatalf("second client %q", got)
	}
	f.exec(`DROP TABLE messages`)
	c.until("the close", func() bool { return c.results["feed"].Closed })
	d.until("the close", func() bool { return d.results["same"].Closed })
	if e := d.lastError("same"); e.Code != liveproto.CodeSchemaChanged {
		t.Fatalf("drop: %+v", e)
	}
	f.waitSubscriptions(0, "after every query closed")
}

// dropConnections ends every session from the server side, as a realtimed
// restart or a network failure would.
func (f *fixture) dropConnections() {
	f.sync(func() {
		for sess := range f.hub.sessions {
			f.hub.detach(sess)
		}
	})
}

// sync runs fn on the hub's loop.
func (f *fixture) sync(fn func()) {
	done := make(chan struct{})
	f.hub.do(context.Background(), func(context.Context) { fn(); close(done) })
	<-done
}

// adr: 1384 — connections are bounded: per-connection subscriptions, and a
// connection that closes takes its subscriptions with it.
func TestHubLimitsAndDisconnects(t *testing.T) {
	f := newFixture(t, schema, Config{MaxPerConnection: 2})
	c := f.dial()
	for i := range 3 {
		c.subscribe(fmt.Sprint("q", i), f.seal(f.secret, "db_1", `SELECT id FROM messages WHERE channel_id = $1`, time.Minute, fmt.Sprint(i)))
	}
	c.until("the limit", func() bool { return c.results["q2"].Closed })
	if e := c.lastError("q2"); e.Code != liveproto.CodeLimit || e.Limit != 2 || e.Observed != 3 {
		t.Fatalf("limit error %+v", e)
	}
	c.until("the results", func() bool { return c.results["q0"].Ready() && c.results["q1"].Ready() })
	c.send(liveproto.ClientFrame{Type: liveproto.TypeUnsubscribe, Subscription: "q0"})
	f.waitSubscriptions(1, "after unsubscribing one of two")

	// A result too large to send whole is refused with its size, so it can
	// never close a realtime connection as a slow consumer.
	f.exec(`INSERT INTO messages SELECT g, 9, repeat('x', 200000) FROM generate_series(1, 12) g`)
	c.subscribe("big", f.seal(f.secret, "db_1", `SELECT id, body FROM messages WHERE channel_id = $1`, time.Minute, "9"))
	c.until("the size refusal", func() bool { return c.results["big"].Closed })
	if e := c.lastError("big"); e.Code != liveproto.CodeLimit || e.Limit != api.LiveQueryResultBytesMax || e.Observed <= e.Limit {
		t.Fatalf("size error %+v", e)
	}
	_ = c.ws.Close()
	f.waitSubscriptions(0, "after the connection closed")
}

// waitSubscriptions waits for the engine to hold n subscriptions: client
// frames reach the hub asynchronously.
func (f *fixture) waitSubscriptions(n int, when string) {
	f.t.Helper()
	f.waitSubscriptionsWithin(n, when, 10*time.Second)
}

func (f *fixture) waitSubscriptionsWithin(n int, when string, within time.Duration) {
	f.t.Helper()
	deadline := time.Now().Add(within)
	for {
		var got int
		f.sync(func() { got = f.engine.Subscriptions() })
		if got == n {
			return
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("%d subscriptions %s, want %d", got, when, n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// adr: 1384 — subscriptions restored from a checkpoint keep their results
// warm for reconnecting clients, then are released.
func TestHubReleasesRestoredSubscriptions(t *testing.T) {
	feed := `SELECT id, body FROM messages WHERE channel_id = $1`
	f := newFixtureWith(t, schema+`; INSERT INTO messages VALUES (1, 1, 'warm')`, Config{RestoredGrace: 3 * time.Second},
		func(e *livequery.Engine) {
			if _, err := e.Register(context.Background(), "restored", feed, "1"); err != nil {
				t.Fatal(err)
			}
		})
	c := f.dial()
	c.subscribe("feed", f.seal(f.secret, "db_1", feed, time.Minute, "1"))
	c.until("the warm result", func() bool { return c.results["feed"].Ready() })
	if got := values(c.results["feed"]); got != "1:warm" {
		t.Fatalf("joined result %q", got)
	}
	f.waitSubscriptionsWithin(1, "after the grace period", 30*time.Second)
	f.sync(func() {
		if ids := f.engine.SubscriptionIDs(); len(ids) != 1 || ids[0] == "restored" {
			t.Errorf("subscriptions %v", ids)
		}
	})
}
