package liveproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/livequery"
	"github.com/onebox-faas/faas/pkg/livequery/liveproto"
	"github.com/onebox-faas/faas/pkg/livequery/liveserver"
	"github.com/onebox-faas/faas/pkg/livequery/seal"
)

func openDB(t *testing.T) (string, *pgxpool.Pool) {
	t.Helper()
	base := os.Getenv("LIVEQUERY_DATABASE_URL")
	if base == "" {
		t.Skip("LIVEQUERY_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	name := fmt.Sprintf("lqp_%d_%d", os.Getpid(), rand.Uint32())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(base)
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

// trackingListener remembers accepted connections so a test can cut them.
type trackingListener struct {
	net.Listener
	mu    sync.Mutex
	conns []net.Conn
}

func (l *trackingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		l.mu.Lock()
		l.conns = append(l.conns, c)
		l.mu.Unlock()
	}
	return c, err
}

func (l *trackingListener) cut() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, c := range l.conns {
		_ = c.Close()
	}
	l.conns = nil
}

type clock struct {
	mu     sync.Mutex
	offset time.Duration
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().Add(c.offset)
}

func (c *clock) advance(d time.Duration) { c.mu.Lock(); c.offset += d; c.mu.Unlock() }

// client is a realtime connection's live session, read through the
// reference client.
type client struct {
	t       *testing.T
	session *Session
	frames  chan []byte
	results map[string]*liveproto.Result
	errs    []*liveproto.Error
}

func newClient(t *testing.T, p *Proxy, router Router) *client {
	c := &client{t: t, frames: make(chan []byte, 1024), results: map[string]*liveproto.Result{}}
	c.session = p.Attach(router, func(frame []byte) error { c.frames <- append([]byte(nil), frame...); return nil })
	return c
}

func (c *client) send(f liveproto.ClientFrame) {
	data, _ := json.Marshal(f)
	c.session.Receive(context.Background(), data)
}

func (c *client) subscribe(id, sealed string) {
	c.results[id] = &liveproto.Result{}
	c.send(liveproto.ClientFrame{Type: liveproto.TypeSubscribe, Subscription: id, Sealed: sealed})
}

func (c *client) until(what string, cond func() bool) {
	c.t.Helper()
	deadline := time.After(30 * time.Second)
	for !cond() {
		select {
		case data := <-c.frames:
			frame, err := liveproto.ParseServerFrame(data)
			if err != nil {
				c.t.Fatalf("bad frame %s: %v", data, err)
			}
			c.apply(frame)
		case <-deadline:
			c.t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func (c *client) apply(frame any) {
	c.t.Helper()
	switch f := frame.(type) {
	case *liveproto.Progress:
		for _, r := range c.results {
			_ = r.Apply(f)
		}
		return
	case *liveproto.Error:
		c.errs = append(c.errs, f)
		if f.Subscription == "" {
			return
		}
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
		c.t.Fatalf("frame for unknown subscription %q", id)
	}
	if err := r.Apply(frame); err != nil {
		c.t.Fatalf("client refused %#v: %v", frame, err)
	}
}

func (c *client) lastError(id string) *liveproto.Error {
	for i := len(c.errs) - 1; i >= 0; i-- {
		if c.errs[i].Subscription == id {
			return c.errs[i]
		}
	}
	return nil
}

func rows(r *liveproto.Result) string {
	var out []string
	for _, row := range r.Rows() {
		out = append(out, *row.Values[0]+":"+*row.Values[1])
	}
	sort.Strings(out) // unordered results come in row-ID order
	return strings.Join(out, ",")
}

// adr: 1384 — realtimed's proxy carries client subscriptions over one
// upstream per database: frames reach the right client with its own IDs,
// progress reaches every session once, and when the upstream drops the
// subscriptions resume above the versions the clients hold, including one
// whose seal expired meanwhile.
func TestProxyCarriesLiveQueries(t *testing.T) {
	dsn, pool := openDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exec := func(sql string) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	exec(`CREATE TABLE messages (id bigint PRIMARY KEY, channel_id bigint NOT NULL, body text)`)
	if err := livequery.Install(ctx, pool); err != nil {
		t.Fatal(err)
	}
	exec(`CREATE PUBLICATION gregale_live FOR ALL TABLES`)
	capture, err := livequery.OpenCapture(ctx, dsn, fmt.Sprintf("lqp_%d", time.Now().UnixNano()), "gregale_live")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = capture.Close() }()
	engine := livequery.NewEngine(pool, capture)
	engine.VerifyEachRound = true
	text, _ := seal.GenerateSecret()
	secret, _ := seal.ParseSecret(text)
	ring, _ := seal.NewKeyring(seal.Key{Database: "db_1", Secret: secret})
	clk := &clock{}
	hub := liveserver.New(liveserver.Config{Database: "db_1", Keyring: ring, Engine: engine, Now: clk.Now,
		Idle: 5 * time.Millisecond, MaxPerConnection: 1000})
	stopped := make(chan error, 1)
	go func() { stopped <- hub.Run(ctx) }()
	defer func() { cancel(); <-stopped }()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tl := &trackingListener{Listener: ln}
	mux := http.NewServeMux()
	mux.Handle("/live", liveserver.WebSocket(hub, func(*http.Request) bool { return true }))
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(tl) }()
	defer srv.Close()

	router := StaticRouter(map[string]Route{secret.KID: {Database: "db_1", URL: "ws://" + ln.Addr().String() + "/live"}})
	proxy := New(nil, nil)
	defer proxy.Close()
	sealFor := func(channel string, ttl time.Duration) string {
		q := seal.Query{Database: "db_1", SQL: `SELECT id, body FROM messages WHERE channel_id = $1`, Params: []*string{&channel}}
		token, err := secret.Seal(q, clk.Now(), ttl)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}

	a, b := newClient(t, proxy, router), newClient(t, proxy, router)
	a.subscribe("feed", sealFor("1", time.Minute))
	b.subscribe("feed", sealFor("2", time.Minute)) // the same client ID on another session
	b.subscribe("other", sealFor("1", time.Minute))
	a.until("a's result", func() bool { return a.results["feed"].Ready() })
	b.until("b's results", func() bool { return b.results["feed"].Ready() && b.results["other"].Ready() })

	exec(`INSERT INTO messages VALUES (1, 1, 'one'), (2, 2, 'two')`)
	a.until("a's change", func() bool { return rows(a.results["feed"]) == "1:one" })
	b.until("b's changes", func() bool { return rows(b.results["feed"]) == "2:two" && rows(b.results["other"]) == "1:one" })

	// The upstream drops: subscriptions go unavailable, then resume above
	// the versions the clients hold.
	held := a.results["feed"].Version
	tl.cut()
	a.until("unavailable", func() bool { e := a.lastError("feed"); return e != nil && e.Code == liveproto.CodeUnavailable })
	exec(`INSERT INTO messages VALUES (3, 1, 'while cut')`)
	a.until("the resumed result", func() bool {
		r := a.results["feed"]
		return !r.Stale && r.Version > held && rows(r) == "1:one,3:while cut"
	})

	// A seal that expires while the upstream is away: the resubscription is
	// refused, the client renews, and the proxy subscribes again.
	clk.advance(2 * time.Minute)
	tl.cut()
	b.until("expiry", func() bool { e := b.lastError("other"); return e != nil && e.Code == liveproto.CodeSealExpired })
	if b.results["other"].Closed {
		t.Fatal("an expired seal closed the subscription")
	}
	b.send(liveproto.ClientFrame{Type: liveproto.TypeRenew, Subscription: "other", Sealed: sealFor("1", time.Minute)})
	b.until("the renewed result", func() bool {
		r := b.results["other"]
		return !r.Stale && r.Ready() && rows(r) == "1:one,3:while cut"
	})

	// Unsubscribing and closing sessions leave nothing upstream.
	a.send(liveproto.ClientFrame{Type: liveproto.TypeUnsubscribe, Subscription: "feed"})
	b.session.Close()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if n := hub.Subscriptions(ctx); n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("subscriptions left upstream after every client left")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
