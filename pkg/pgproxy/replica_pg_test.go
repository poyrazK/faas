package pgproxy

import (
	"context"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

// TestReadOnlyEndpointsRefuseWritesOnThePrimary connects to a database with
// no replica through <id>-ro and <id>-ro-pooler: reads work and writes fail
// as they would on a standby, while <id> still writes.
func TestReadOnlyEndpointsRefuseWritesOnThePrimary(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	databaseID := uuid.NewString()
	role, password := h.addRole(t, databaseID)
	table := "ro_probe_" + uuid.NewString()[:8]
	if _, err := h.backend.Exec(ctx, "CREATE TABLE "+table+" (id int); GRANT SELECT, INSERT, DELETE ON "+table+" TO "+role); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = h.backend.Exec(context.Background(), "DROP TABLE IF EXISTS "+table) })
	writer, err := h.connect(ctx, databaseID+"."+testSuffix, role, password, h.dbName, "require", nil)
	if err != nil {
		t.Fatalf("direct endpoint: %v", err)
	}
	defer func() { _ = writer.Close(ctx) }()
	if _, err := writer.Exec(ctx, "INSERT INTO "+table+" VALUES (1)"); err != nil {
		t.Fatalf("write through the direct endpoint: %v", err)
	}

	for _, label := range []string{databaseID + readOnlySuffix, databaseID + readOnlySuffix + pooledSuffix} {
		t.Run(label[len(databaseID):], func(t *testing.T) {
			reader, err := h.connect(ctx, label+"."+testSuffix, role, password, h.dbName, "require", nil)
			if err != nil {
				t.Fatalf("read-only endpoint: %v", err)
			}
			defer func() { _ = reader.Close(ctx) }()
			var n int
			if err := reader.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 1 {
				t.Fatalf("read = %d, %v; want 1 row", n, err)
			}
			if _, err := reader.Exec(ctx, "INSERT INTO "+table+" VALUES (2)"); sqlState(err) != "25006" {
				t.Fatalf("write through %s = %v, want 25006 (read-only transaction)", label, err)
			}
		})
	}
	// The no-SNI form names the read-only endpoint too.
	reader, err := h.connect(ctx, "127.0.0.1", role, password, h.dbName, "require", map[string]string{"options": "endpoint=" + databaseID + readOnlySuffix})
	if err != nil {
		t.Fatalf("read-only endpoint by option: %v", err)
	}
	defer func() { _ = reader.Close(ctx) }()
	if _, err := reader.Exec(ctx, "DELETE FROM "+table); sqlState(err) != "25006" {
		t.Fatalf("write by option = %v, want 25006", err)
	}
}

// TestReplicaOnlyPoolStopsAtReplayBacklog uses a streaming standby and a
// real pool. Run with DATABASE_URL (primary) and GREG_TEST_PG_STANDBY_URL.
func TestReplicaOnlyPoolStopsAtReplayBacklog(t *testing.T) {
	standbyURL := os.Getenv("GREG_TEST_PG_STANDBY_URL")
	if standbyURL == "" {
		t.Skip("GREG_TEST_PG_STANDBY_URL required")
	}
	primary := newHarness(t)
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	standby, err := pgx.Connect(ctx, standbyURL)
	if err != nil {
		t.Fatal(err)
	}
	defer standby.Close(context.Background())
	id := uuid.NewString()
	role, password := primary.addRoleWithPrefix(t, id, dbguest.ReplicaOnlyRolePrefix)
	var primaryDials atomic.Int32
	const limit int64 = 8 << 10
	maxLag := limit
	mu := sync.Mutex{} // the test and the proxy sample the same pgx connection
	sample := func(ctx context.Context, _ string) (Backend, *int64, time.Time, error) {
		mu.Lock()
		defer mu.Unlock()
		var lag *int64
		err := primary.backend.QueryRow(ctx, `
			SELECT greatest(0, pg_wal_lsn_diff(pg_current_wal_flush_lsn(), replay_lsn))::bigint
			FROM pg_stat_replication WHERE application_name='gregale_standby' AND state='streaming' AND replay_lsn IS NOT NULL`).Scan(&lag)
		return Backend{Target: "standby", Node: "standby"}, lag, time.Now(), err
	}
	p, err := New(Config{TLS: selfSignedTLS(t), HostSuffix: testSuffix, DecoySecret: []byte(strings.Repeat("k", 32)), ReplicaMaxReplayLagBytes: &maxLag},
		primary.creds, replicaRouteFunc(sample), setupDialer(func(ctx context.Context, b Backend, id string) (io.ReadWriteCloser, error) {
			cfg := standby.Config()
			if !b.replicaOnly || b.Node != "standby" {
				primaryDials.Add(1)
				cfg = primary.backend.Config()
			}
			return tcpDialer{net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port)))}.Dial(ctx, b, id)
		}), nil)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() { _ = p.Serve(ctx, ln) }()
	proxy := &harness{proxyAddr: ln.Addr().String(), dbName: primary.dbName}
	host := id + "-replica-pooler." + testSuffix
	wait := func(reached func() bool) {
		t.Helper()
		for !reached() {
			select {
			case <-ctx.Done():
				t.Fatal("timed out waiting for WAL replay state")
			case <-time.After(25 * time.Millisecond):
			}
		}
	}
	caughtUp := func() bool {
		_, lag, _, err := sample(ctx, id)
		return err == nil && lag != nil && *lag == 0
	}
	wait(caughtUp)
	if conn, err := proxy.connect(ctx, id+"-ro-pooler."+testSuffix, role, password, proxy.dbName, "require", nil); sqlState(err) != "08004" {
		if conn != nil {
			_ = conn.Close(ctx)
		}
		t.Fatalf("replica-only credential on fallback endpoint = %v, want 08004", err)
	}
	reader, err := proxy.connect(ctx, host, role, password, proxy.dbName, "require", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close(context.Background())
	checkStandby := func(conn *pgx.Conn) {
		t.Helper()
		var recovery bool
		if err := conn.QueryRow(ctx, "SELECT pg_is_in_recovery()").Scan(&recovery); err != nil || !recovery {
			t.Fatalf("read reached standby: %v, %v", recovery, err)
		}
	}
	checkStandby(reader)

	if _, err := standby.Exec(ctx, "SELECT pg_wal_replay_pause()"); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = standby.Exec(context.Background(), "SELECT pg_wal_replay_resume()") }()
	wait(func() bool {
		var state string
		return standby.QueryRow(ctx, "SELECT pg_get_wal_replay_pause_state()").Scan(&state) == nil && state == "paused"
	})
	table := "replay_probe_" + id[:8]
	mu.Lock()
	_, err = primary.backend.Exec(ctx, "CREATE TABLE "+table+" AS SELECT n, md5(n::text) FROM generate_series(1, 4000) n")
	mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = primary.backend.Exec(context.Background(), "DROP TABLE "+table) }()
	wait(func() bool {
		_, lag, _, err := sample(ctx, id)
		return err == nil && lag != nil && *lag > limit
	})

	if err := reader.QueryRow(ctx, "SELECT 1").Scan(new(int)); sqlState(err) != "57P03" {
		t.Fatalf("checkout on an already connected client = %v, want 57P03", err)
	}
	for _, h := range []string{host, id + "-replica." + testSuffix} {
		if conn, err := proxy.connect(ctx, h, role, password, proxy.dbName, "require", nil); sqlState(err) != "57P03" {
			if conn != nil {
				_ = conn.Close(ctx)
			}
			t.Fatalf("new connection with backlog = %v, want 57P03", err)
		}
	}
	if _, err := standby.Exec(ctx, "SELECT pg_wal_replay_resume()"); err != nil {
		t.Fatal(err)
	}
	wait(caughtUp)
	reader, err = proxy.connect(ctx, host, role, password, proxy.dbName, "require", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close(context.Background())
	checkStandby(reader)
	if got := primaryDials.Load(); got != 0 {
		t.Fatalf("replica-only traffic opened %d primary sessions", got)
	}
}
