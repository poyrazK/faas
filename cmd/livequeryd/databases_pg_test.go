package main

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
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/livequery/liveproto"
	"github.com/onebox-faas/faas/pkg/livequery/liveserver"
	"github.com/onebox-faas/faas/pkg/livequery/seal"
)

// adr: 20261016151947 — one process serves several databases. Each has its
// own hub, slot, route and checkpoint; a subscription sees only its own
// database's rows, a seal for another database is refused, and a database
// that cannot be opened is retried while the others serve.
func TestServeSeveralDatabases(t *testing.T) {
	base := os.Getenv("LIVEQUERY_DATABASE_URL")
	if base == "" {
		t.Skip("LIVEQUERY_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	var level string
	if err := admin.QueryRow(ctx, "SHOW wal_level").Scan(&level); err != nil || level != "logical" {
		t.Skipf("wal_level is %q, need logical", level)
	}
	suffix := fmt.Sprintf("%d_%d", os.Getpid(), rand.Uint32())
	dir := t.TempDir()
	dsnFile := func(name string) string {
		u, err := url.Parse(base)
		if err != nil {
			t.Fatal(err)
		}
		u.Path = "/" + name
		path := filepath.Join(dir, name+".dsn")
		if err := os.WriteFile(path, []byte(u.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	dbNames := map[string]string{"db_a": "lqm_a_" + suffix, "db_b": "lqm_b_" + suffix}
	for id, name := range dbNames {
		if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			// A slot's walsender may outlive its connection for a moment;
			// the database cannot be dropped while the slot exists.
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			for ctx.Err() == nil {
				var left int
				_ = admin.QueryRow(ctx, `SELECT count(*) FROM pg_replication_slots WHERE database = $1`, name).Scan(&left)
				if left == 0 {
					break
				}
				_, _ = admin.Exec(ctx, `SELECT pg_drop_replication_slot(slot_name) FROM pg_replication_slots WHERE database = $1 AND NOT active`, name)
				time.Sleep(100 * time.Millisecond)
			}
			if _, err := admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
				t.Logf("drop %s: %v", name, err)
			}
		})
		conn, err := pgx.Connect(ctx, strings.Replace(base, "/postgres", "/"+name, 1))
		if err != nil {
			t.Fatal(err)
		}
		_, err = conn.Exec(ctx, `CREATE TABLE items (id int PRIMARY KEY, name text NOT NULL);
			INSERT INTO items VALUES (1, '`+id+`')`)
		_ = conn.Close(ctx)
		if err != nil {
			t.Fatal(err)
		}
	}
	config, _ := json.Marshal(map[string]any{"databases": []map[string]any{
		{"id": "db_a", "database_url_file": dsnFile(dbNames["db_a"])},
		{"id": "db_b", "database_url_file": dsnFile(dbNames["db_b"])},
		{"id": "db_c", "database_url_file": dsnFile("lqm_missing_" + suffix)},
	}})
	configPath := filepath.Join(dir, "databases.json")
	if err := os.WriteFile(configPath, config, 0o600); err != nil {
		t.Fatal(err)
	}
	checkpoints := t.TempDir()
	dbs, err := loadDatabases(configPath, checkpoints)
	if err != nil {
		t.Fatal(err)
	}

	secretA, secretB := testSecret(t), testSecret(t)
	ring, err := seal.NewKeyring(seal.Key{Database: "db_a", Secret: secretA}, seal.Key{Database: "db_b", Secret: secretB})
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(t.Output(), nil))
	routes := newRoutes([]string{"db_a", "db_b", "db_c"})
	set := liveserver.NewMetricsSet(prometheus.NewRegistry())
	runCtx, stop := context.WithCancel(ctx)
	var wg sync.WaitGroup
	for _, db := range dbs {
		deps := hubDeps{keyring: ring, log: log.With("database", db.ID)}
		metrics := set.Database(db.ID)
		wg.Go(func() { supervise(runCtx, db, deps, metrics, routes) })
	}
	srv := httptest.NewServer(realtimedMux(routes, func(h *liveserver.Hub) http.Handler {
		return liveserver.WebSocket(h, func(*http.Request) bool { return true })
	}))
	stopped := false
	shutdown := func() {
		if !stopped {
			stopped = true
			srv.Close()
			stop()
			wg.Wait()
		}
	}
	defer shutdown()

	waitUntil(t, "db_a and db_b running, db_c retried", func() bool {
		st := routes.status()
		return st[0].Running && st[1].Running && !st[2].Running && st[2].Restarts >= 1
	})
	if resp, err := http.Get(srv.URL + "/db_c/live"); err != nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("GET /db_c/live = %v, %v; want 503 while it cannot be opened", resp, err)
	} else {
		_ = resp.Body.Close()
	}

	query := "SELECT id, name FROM items"
	a := dialLive(t, srv.URL+"/db_a/live")
	a.subscribe("s1", sealFor(t, secretA, "db_a", query))
	a.until("db_a's row", func(r *liveproto.Result) bool { return r.Ready() && names(r) == "db_a" })
	conn, err := pgx.Connect(ctx, strings.Replace(base, "/postgres", "/"+dbNames["db_a"], 1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO items VALUES (2, 'db_a again')`); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(ctx)
	a.until("db_a's insert", func(r *liveproto.Result) bool { return names(r) == "db_a,db_a again" })

	b := dialLive(t, srv.URL+"/db_b/live")
	b.subscribe("other", sealFor(t, secretA, "db_a", query))
	b.untilError("other")
	b.subscribe("s1", sealFor(t, secretB, "db_b", query))
	b.until("db_b's row", func(r *liveproto.Result) bool { return r.Ready() && names(r) == "db_b" })

	shutdown()
	for id, want := range map[string]bool{"db_a": true, "db_b": true, "db_c": false} {
		_, err := os.Stat(filepath.Join(checkpoints, id+".checkpoint"))
		if got := err == nil; got != want {
			t.Errorf("%s checkpoint written = %v, want %v", id, got, want)
		}
	}
}

func testSecret(t *testing.T) seal.Secret {
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

func sealFor(t *testing.T, secret seal.Secret, database, sql string) string {
	t.Helper()
	token, err := secret.Seal(seal.Query{Database: database, SQL: sql}, time.Now(), 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// names lists a result's name column in id order.
func names(r *liveproto.Result) string {
	rows := r.Rows()
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if len(row.Values) == 2 && row.Values[1] != nil {
			out = append(out, *row.Values[1])
		}
	}
	return strings.Join(out, ",")
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// liveClient is one live connection, through the reference client.
type liveClient struct {
	t       *testing.T
	ws      *websocket.Conn
	frames  chan any
	results map[string]*liveproto.Result
}

func dialLive(t *testing.T, httpURL string) *liveClient {
	t.Helper()
	ws, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(httpURL, "http"), nil)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	c := &liveClient{t: t, ws: ws, frames: make(chan any, 1024), results: map[string]*liveproto.Result{}}
	go func() {
		defer close(c.frames)
		for {
			_, data, err := ws.ReadMessage()
			if err != nil {
				return
			}
			if frame, err := liveproto.ParseServerFrame(data); err == nil {
				c.frames <- frame
			}
		}
	}()
	t.Cleanup(func() { _ = ws.Close() })
	return c
}

func (c *liveClient) subscribe(id, sealed string) {
	c.t.Helper()
	c.results[id] = &liveproto.Result{}
	data, _ := json.Marshal(liveproto.ClientFrame{Type: liveproto.TypeSubscribe, Subscription: id, Sealed: sealed})
	if err := c.ws.WriteMessage(websocket.TextMessage, data); err != nil {
		c.t.Fatal(err)
	}
}

// until applies frames until subscription s1 satisfies cond. The first
// result waits for the slot to stream, which can take until the next
// running-transactions record.
func (c *liveClient) until(what string, cond func(*liveproto.Result) bool) {
	c.t.Helper()
	c.wait(what, func() bool { return cond(c.results["s1"]) })
}

// untilError waits for a live.error on subscription id.
func (c *liveClient) untilError(id string) {
	c.t.Helper()
	c.wait("an error for "+id, func() bool { return c.results[id].Err != nil })
}

func (c *liveClient) wait(what string, cond func() bool) {
	c.t.Helper()
	deadline := time.After(90 * time.Second)
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

func (c *liveClient) apply(frame any) {
	if p, ok := frame.(*liveproto.Progress); ok {
		for _, r := range c.results {
			_ = r.Apply(p)
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
	if r := c.results[id]; r != nil {
		if err := r.Apply(frame); err != nil {
			c.t.Fatalf("client refused %#v: %v", frame, err)
		}
	}
}
