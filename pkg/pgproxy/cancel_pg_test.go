//go:build !no_pg

// adr: 1027 — query cancellation through the proxy: the client's own key
// cancels its query on the direct and the pooled endpoint, in plaintext and
// over TLS; another instance forwards it to the one that issued the key;
// an unknown key or an idle pooled client's key cancels nothing.
package pgproxy

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// sleeping runs pg_sleep on conn in the background and waits until
// PostgreSQL shows it running; the channel yields the query's error.
func (h *harness) sleeping(t *testing.T, ctx context.Context, conn *pgx.Conn, role string, seconds int) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, err := conn.Exec(ctx, "SELECT pg_sleep("+strconv.Itoa(seconds)+")")
		done <- err
	}()
	watcher, err := pgx.ConnectConfig(ctx, h.backend.Config())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = watcher.Close(ctx) }()
	for deadline := time.Now().Add(10 * time.Second); ; {
		var n int
		if err := watcher.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE usename = $1 AND state = 'active' AND query LIKE 'SELECT pg_sleep%'", role).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return done
		}
		if time.Now().After(deadline) {
			t.Fatal("the query never started")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// plaintextCancel sends a cancel request without TLS, as most drivers do,
// and waits for the proxy to close the connection.
func plaintextCancel(t *testing.T, addr string, code uint32, pid uint32, secret []byte) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	buf := make([]byte, 12, 16)
	binary.BigEndian.PutUint32(buf, uint32(12+len(secret)))
	binary.BigEndian.PutUint32(buf[4:], code)
	binary.BigEndian.PutUint32(buf[8:], pid)
	buf = append(buf, secret...)
	if _, err := conn.Write(buf); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, conn); err != nil {
		t.Fatalf("the proxy did not close the cancel connection: %v", err)
	}
}

func canceledWithin(t *testing.T, done <-chan error, what string) {
	t.Helper()
	select {
	case err := <-done:
		if sqlState(err) != "57014" {
			t.Fatalf("%s: query ended with %v, want 57014 (canceled)", what, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("%s: the query was not canceled", what)
	}
}

func TestCancelReachesTheClientsBackend(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	databaseID := uuid.NewString()
	role, password := h.addRole(t, databaseID)
	for _, endpoint := range []string{databaseID, databaseID + pooledSuffix} {
		conn, err := h.connect(ctx, endpoint+"."+testSuffix, role, password, h.dbName, "require", nil)
		if err != nil {
			t.Fatal(err)
		}
		// The key is the proxy's, not PostgreSQL's.
		var backendPID uint32
		if err := conn.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&backendPID); err != nil {
			t.Fatal(err)
		}
		if conn.PgConn().PID() == backendPID {
			t.Fatalf("%s: the client got the backend's own key", endpoint)
		}
		// Over TLS (pgx, like libpq 17+, encrypts the cancel of an
		// encrypted session) and in plaintext (older drivers).
		done := h.sleeping(t, ctx, conn, role, 30)
		if err := conn.PgConn().CancelRequest(ctx); err != nil {
			t.Fatalf("%s: cancel over TLS: %v", endpoint, err)
		}
		canceledWithin(t, done, endpoint+" over TLS")
		done = h.sleeping(t, ctx, conn, role, 30)
		plaintextCancel(t, h.proxyAddr, cancelRequestCode, conn.PgConn().PID(), conn.PgConn().SecretKey())
		canceledWithin(t, done, endpoint+" in plaintext")
		var one int
		if err := conn.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
			t.Fatalf("%s: the session after a cancel: %v", endpoint, err)
		}
		_ = conn.Close(ctx)
	}
	if got := testutil.ToFloat64(h.metrics.cancels.WithLabelValues("ok")); got != 4 {
		t.Fatalf("cancels ok = %v, want 4", got)
	}
}

// A key cancels nothing once its client holds no server, even when the
// pool lends that server to another client, and an unknown key cancels
// nothing at all.
func TestCancelSparesOtherClients(t *testing.T) {
	h := newHarnessWith(t, func(c *Config) { c.PoolMaxServers = 1 })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	databaseID := uuid.NewString()
	role, password := h.addRole(t, databaseID)
	host := databaseID + pooledSuffix + "." + testSuffix
	idle, err := h.connect(ctx, host, role, password, h.dbName, "require", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = idle.Close(ctx) }()
	if _, err := idle.Exec(ctx, "SELECT 1"); err != nil {
		t.Fatal(err)
	}
	busy, err := h.connect(ctx, host, role, password, h.dbName, "require", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = busy.Close(ctx) }()
	// One server: the busy client runs on the server the idle one used.
	done := h.sleeping(t, ctx, busy, role, 2)
	plaintextCancel(t, h.proxyAddr, cancelRequestCode, idle.PgConn().PID(), idle.PgConn().SecretKey())
	plaintextCancel(t, h.proxyAddr, cancelRequestCode, busy.PgConn().PID()^1, busy.PgConn().SecretKey())
	plaintextCancel(t, h.proxyAddr, cancelRequestCode, busy.PgConn().PID(), []byte("too long a secret"))
	if err := <-done; err != nil {
		t.Fatalf("another client's key, or an unknown one, canceled the query: %v", err)
	}
	if got := testutil.ToFloat64(h.metrics.cancels.WithLabelValues("unknown")); got != 2 {
		t.Fatalf("unknown cancels = %v, want 2", got)
	}
}

// A cancel that reaches another instance is forwarded to the one that
// issued the key, once: a forwarded cancel is never forwarded again.
func TestCancelIsForwardedToTheIssuingInstance(t *testing.T) {
	h := newHarnessWith(t, func(c *Config) { c.InstanceID = 1 })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	other := NewMetrics(prometheus.NewRegistry())
	cfg := Config{TLS: selfSignedTLS(t), HostSuffix: testSuffix, DecoySecret: make([]byte, 32), InstanceID: 2,
		CancelPeers: map[uint8]string{1: h.proxyAddr}, Metrics: other}
	backend := h.backend.Config()
	b, err := New(cfg, h.creds, h.waker, tcpDialer{addr: net.JoinHostPort(backend.Host, strconv.Itoa(int(backend.Port)))}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveCtx, stop := context.WithCancel(ctx)
	defer stop()
	go func() { _ = b.Serve(serveCtx, ln) }()
	defer func() { _ = ln.Close() }()

	databaseID := uuid.NewString()
	role, password := h.addRole(t, databaseID)
	conn, err := h.connect(ctx, databaseID+"."+testSuffix, role, password, h.dbName, "require", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if conn.PgConn().PID()>>24 != 1 {
		t.Fatalf("key %08x does not name instance 1", conn.PgConn().PID())
	}
	// A forwarded cancel for a key of another instance is dropped.
	done := h.sleeping(t, ctx, conn, role, 30)
	plaintextCancel(t, ln.Addr().String(), forwardedCancelCode, conn.PgConn().PID(), conn.PgConn().SecretKey())
	select {
	case err := <-done:
		t.Fatalf("a re-forwarded cancel reached the backend: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	plaintextCancel(t, ln.Addr().String(), cancelRequestCode, conn.PgConn().PID(), conn.PgConn().SecretKey())
	canceledWithin(t, done, "through instance 2")
	if fw, ok := testutil.ToFloat64(other.cancels.WithLabelValues("forwarded")), testutil.ToFloat64(h.metrics.cancels.WithLabelValues("ok")); fw != 1 || ok != 1 {
		t.Fatalf("forwarded %v by instance 2, ok %v on instance 1; want 1 and 1", fw, ok)
	}
	if _, err := New(Config{TLS: cfg.TLS, HostSuffix: testSuffix, DecoySecret: make([]byte, 32), InstanceID: 3, CancelPeers: map[uint8]string{3: "x:1"}},
		h.creds, h.waker, tcpDialer{}, nil); err == nil {
		t.Fatal("a peer list naming the instance itself was accepted")
	}
}
