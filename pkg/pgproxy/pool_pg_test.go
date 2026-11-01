//go:build !no_pg

// adr: 1027 — the pooled endpoint: many clients share a few server
// connections per database, transaction by transaction, with protocol-level
// prepared statements prepared again on whichever server runs them.
package pgproxy

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// poolTable creates a table the role may use and returns its name.
func (h *harness) poolTable(t *testing.T, role string) string {
	t.Helper()
	table := "pooled_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	ctx := context.Background()
	if _, err := h.backend.Exec(ctx, "CREATE TABLE "+table+" (id serial PRIMARY KEY, v int NOT NULL); GRANT ALL ON "+table+" TO "+role+"; GRANT USAGE ON SEQUENCE "+table+"_id_seq TO "+role); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = h.backend.Exec(context.Background(), "DROP TABLE IF EXISTS "+table) })
	return table
}

// serversOf counts the role's PostgreSQL sessions.
func (h *harness) serversOf(t *testing.T, role string) int {
	t.Helper()
	var n int
	if err := h.backend.QueryRow(context.Background(), "SELECT count(*) FROM pg_stat_activity WHERE usename = $1", role).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPooledClientsShareFewServers(t *testing.T) {
	h := newHarnessWith(t, func(c *Config) { c.PoolMaxServers = 3 })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	databaseID := uuid.NewString()
	role, password := h.addRole(t, databaseID)
	table := h.poolTable(t, role)
	host := databaseID + pooledSuffix + "." + testSuffix

	const clients, rounds = 30, 20
	var peak int
	var peakMu sync.Mutex
	stopWatch, watched := make(chan struct{}), make(chan struct{})
	watcher, err := pgx.ConnectConfig(ctx, h.backend.Config())
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		defer close(watched)
		defer func() { _ = watcher.Close(context.WithoutCancel(ctx)) }()
		for {
			select {
			case <-stopWatch:
				return
			case <-time.After(20 * time.Millisecond):
			}
			var n int
			if err := watcher.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE usename = $1", role).Scan(&n); err != nil {
				continue
			}
			peakMu.Lock()
			peak = max(peak, n)
			peakMu.Unlock()
		}
	}()
	var wg sync.WaitGroup
	errs := make(chan error, clients)
	for c := 0; c < clients; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := h.connect(ctx, host, role, password, h.dbName, "require", nil)
			if err != nil {
				errs <- fmt.Errorf("connect: %w", err)
				return
			}
			defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
			for r := 0; r < rounds; r++ {
				// pgx caches these as named prepared statements, which may
				// land on any server of the pool.
				var sum int
				if err := conn.QueryRow(ctx, "SELECT $1::int + $2::int", c, r).Scan(&sum); err != nil || sum != c+r {
					errs <- fmt.Errorf("prepared select = %d, %w", sum, err)
					return
				}
				tx, err := conn.Begin(ctx)
				if err != nil {
					errs <- err
					return
				}
				var pid1, pid2, seen int
				_ = tx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid1)
				if _, err := tx.Exec(ctx, "INSERT INTO "+table+" (v) VALUES ($1)", c); err != nil {
					errs <- fmt.Errorf("insert: %w", err)
					return
				}
				_ = tx.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid2)
				if err := tx.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE v = $1", c).Scan(&seen); err != nil || seen != r+1 {
					errs <- fmt.Errorf("own rows in transaction = %d, %w", seen, err)
					return
				}
				if pid1 != pid2 {
					errs <- fmt.Errorf("a transaction moved between servers: %d then %d", pid1, pid2)
					return
				}
				if err := tx.Commit(ctx); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(stopWatch)
	<-watched
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var rows int
	if err := h.backend.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&rows); err != nil || rows != clients*rounds {
		t.Fatalf("rows = %d, %v; want %d", rows, err, clients*rounds)
	}
	peakMu.Lock()
	defer peakMu.Unlock()
	if peak > 3 || peak == 0 {
		t.Fatalf("%d clients used up to %d server connections; want 1-3", clients, peak)
	}
	t.Logf("%d clients x %d transactions over at most %d server connections", clients, rounds, peak)
}

func TestPooledClientLeavingMidTransactionRollsBack(t *testing.T) {
	h := newHarnessWith(t, func(c *Config) { c.PoolMaxServers = 1 })
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	databaseID := uuid.NewString()
	role, password := h.addRole(t, databaseID)
	table := h.poolTable(t, role)
	host := databaseID + pooledSuffix + "." + testSuffix

	conn, err := h.connect(ctx, host, role, password, h.dbName, "require", nil)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO "+table+" (v) VALUES (1)"); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(ctx) // leaves inside the transaction
	other, err := h.connect(ctx, host, role, password, h.dbName, "require", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close(ctx) }()
	var rows int
	if err := other.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("rows after an abandoned transaction = %d, %v; want 0 (rolled back, server discarded)", rows, err)
	}
}

func TestPooledErrorsAndCopy(t *testing.T) {
	h := newHarnessWith(t, func(c *Config) { c.PoolMaxServers = 2 })
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	databaseID := uuid.NewString()
	role, password := h.addRole(t, databaseID)
	table := h.poolTable(t, role)
	host := databaseID + pooledSuffix + "." + testSuffix
	conn, err := h.connect(ctx, host, role, password, h.dbName, "require", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()

	var one int
	if err := conn.QueryRow(ctx, "SELECT 1 / $1::int", 0).Scan(&one); sqlState(err) != "22012" {
		t.Fatalf("division by zero = %v, want 22012", err)
	}
	if err := conn.QueryRow(ctx, "SELECT 1 / $1::int", 1).Scan(&one); err != nil || one != 1 {
		t.Fatalf("after an error = %d, %v", one, err)
	}
	if _, err := conn.Exec(ctx, "SELECT no_such_column FROM "+table); sqlState(err) != "42703" {
		t.Fatalf("unknown column = %v, want 42703", err)
	}

	n, err := conn.CopyFrom(ctx, pgx.Identifier{table}, []string{"v"}, pgx.CopyFromRows([][]any{{7}, {8}, {9}}))
	if err != nil || n != 3 {
		t.Fatalf("copy in = %d, %v", n, err)
	}
	var out bytes.Buffer
	if _, err := conn.PgConn().CopyTo(ctx, &out, "COPY (SELECT v FROM "+table+" ORDER BY v) TO STDOUT"); err != nil || out.String() != "7\n8\n9\n" {
		t.Fatalf("copy out = %q, %v", out.String(), err)
	}
	// Simple protocol, several statements in one query.
	results, err := conn.PgConn().Exec(ctx, "SELECT 1; SELECT 2").ReadAll()
	if err != nil || len(results) != 2 {
		t.Fatalf("multi-statement query = %d results, %v", len(results), err)
	}
}

func TestPooledRefusesWhatItCannotShare(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	databaseID := uuid.NewString()
	role, password := h.addRole(t, databaseID)
	host := databaseID + pooledSuffix + "." + testSuffix
	before := h.waker.wakes.Load()
	if _, err := h.connect(ctx, host, role, "wrong-password", h.dbName, "require", nil); sqlState(err) != "28P01" {
		t.Fatalf("wrong password = %v, want 28P01", err)
	}
	if _, err := h.connect(ctx, host, role, password, h.dbName, "require", map[string]string{"search_path": "elsewhere"}); sqlState(err) != "08P01" {
		t.Fatalf("session parameter = %v, want 08P01", err)
	}
	if h.waker.wakes.Load() != before {
		t.Fatal("a refused pooled client woke the database")
	}
	// The no-SNI form of the pooled endpoint.
	conn, err := h.connect(ctx, "127.0.0.1", role, password, h.dbName, "require", map[string]string{"options": "endpoint=" + databaseID + pooledSuffix})
	if err != nil {
		t.Fatalf("pooled endpoint by option: %v", err)
	}
	_ = conn.Close(ctx)
}

func TestPooledServersCloseWhenIdle(t *testing.T) {
	h := newHarnessWith(t, func(c *Config) { c.PoolServerIdle = 300 * time.Millisecond })
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	databaseID := uuid.NewString()
	role, password := h.addRole(t, databaseID)
	conn, err := h.connect(ctx, databaseID+pooledSuffix+"."+testSuffix, role, password, h.dbName, "require", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, "SELECT 1"); err != nil {
		t.Fatal(err)
	}
	touches := h.waker.touches.Load()
	for deadline := time.Now().Add(5 * time.Second); h.serversOf(t, role) != 0; {
		if time.Now().After(deadline) {
			t.Fatalf("%d server connections stayed open past the idle timeout", h.serversOf(t, role))
		}
		time.Sleep(50 * time.Millisecond)
	}
	if h.waker.touches.Load() == touches {
		t.Fatal("open pooled servers did not keep the database touched")
	}
	// The client is still connected; its next query opens a server again.
	var one int
	if err := conn.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Fatalf("query after the pool emptied = %d, %v", one, err)
	}
}

func TestPooledCapIsSharedAndBounded(t *testing.T) {
	h := newHarnessWith(t, func(c *Config) { c.PoolMaxServers, c.PoolWait = 1, 300*time.Millisecond })
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	databaseID := uuid.NewString()
	host := databaseID + pooledSuffix + "." + testSuffix
	roleA, passwordA := h.addRole(t, databaseID)
	roleB, passwordB := h.addRole(t, databaseID)
	a, err := h.connect(ctx, host, roleA, passwordA, h.dbName, "require", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close(ctx) }()
	// A's server is idle now; B takes the database's one slot from it.
	b, err := h.connect(ctx, host, roleB, passwordB, h.dbName, "require", nil)
	if err != nil {
		t.Fatalf("second role under a cap of one: %v", err)
	}
	defer func() { _ = b.Close(ctx) }()
	var who string
	if err := b.QueryRow(ctx, "SELECT current_user").Scan(&who); err != nil || who != roleB {
		t.Fatalf("current_user = %q, %v", who, err)
	}
	// While B holds the slot in a transaction, A waits, then gives up.
	tx, err := b.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.QueryRow(ctx, "SELECT current_user").Scan(&who); sqlState(err) != "53300" {
		t.Fatalf("query while the only server is busy = %v, want 53300", err)
	}
	_ = tx.Rollback(ctx)
	if h.serversOf(t, roleA)+h.serversOf(t, roleB) > 1 {
		t.Fatal("the database's cap of one server was exceeded")
	}
}
