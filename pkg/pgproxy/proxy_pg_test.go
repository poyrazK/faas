//go:build !no_pg

// adr: 1027
package pgproxy

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
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
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/pgscram"
)

const testSuffix = "db.test.local"

type mapCredentials struct {
	mu        sync.Mutex
	verifiers map[string]pgscram.Verifier // databaseID|role
}

func (m *mapCredentials) Verifier(_ context.Context, databaseID, role string) (pgscram.Verifier, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.verifiers[databaseID+"|"+role]
	if !ok {
		return pgscram.Verifier{}, ErrNoVerifier
	}
	return v, nil
}

type countingWaker struct {
	wakes, touches atomic.Int32
	fail           atomic.Bool
}

func (w *countingWaker) Wake(context.Context, string) (string, error) {
	w.wakes.Add(1)
	if w.fail.Load() {
		return "", errors.New("no capacity")
	}
	return "backend", nil
}

func (w *countingWaker) Touch(context.Context, string) error {
	w.touches.Add(1)
	return nil
}

// tcpDialer stands in for vmmd's session: it connects to the test
// PostgreSQL directly.
type tcpDialer struct{ addr string }

func (d tcpDialer) Dial(ctx context.Context, _, _ string) (io.ReadWriteCloser, error) {
	var nd net.Dialer
	return nd.DialContext(ctx, "tcp", d.addr)
}

func selfSignedTLS(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "*." + testSuffix},
		DNSNames: []string{"*." + testSuffix}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
}

type harness struct {
	proxyAddr string
	backend   *pgx.Conn
	creds     *mapCredentials
	waker     *countingWaker
	dbName    string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" || os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("DATABASE_URL required (pg_hba must use scram-sha-256 for non-superusers)")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	cfg := admin.Config()
	h := &harness{backend: admin, creds: &mapCredentials{verifiers: map[string]pgscram.Verifier{}}, waker: &countingWaker{}, dbName: cfg.Database}
	p, err := New(Config{TLS: selfSignedTLS(t), HostSuffix: testSuffix, DecoySecret: []byte(strings.Repeat("k", 32)), TouchInterval: 50 * time.Millisecond},
		h.creds, h.waker, tcpDialer{addr: net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port)))}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctxServe, cancel := context.WithCancel(ctx)
	t.Cleanup(func() { cancel(); _ = ln.Close() })
	go func() { _ = p.Serve(ctxServe, ln) }()
	h.proxyAddr = ln.Addr().String()
	return h
}

// addRole creates a role in PostgreSQL with a fresh verifier and publishes
// the same verifier to the proxy, as the native driver does.
func (h *harness) addRole(t *testing.T, databaseID string) (role, password string) {
	t.Helper()
	role = "proxy_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	password = "pw-" + uuid.NewString()
	v, err := pgscram.NewVerifier(password, pgscram.DefaultIterations)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.backend.Exec(context.Background(), "CREATE ROLE "+role+" LOGIN PASSWORD '"+v.String()+"'"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = h.backend.Exec(context.Background(), "DROP ROLE IF EXISTS "+role) })
	h.creds.mu.Lock()
	h.creds.verifiers[databaseID+"|"+role] = v
	h.creds.mu.Unlock()
	return role, password
}

func (h *harness) connect(ctx context.Context, host, user, password, dbname, sslmode string, params map[string]string) (*pgx.Conn, error) {
	_, port, _ := net.SplitHostPort(h.proxyAddr)
	cfg, err := pgx.ParseConfig("host=" + host + " port=" + port + " user=" + user + " password=" + password + " dbname=" + dbname + " sslmode=" + sslmode + " connect_timeout=10")
	if err != nil {
		return nil, err
	}
	cfg.LookupFunc = func(context.Context, string) ([]string, error) { return []string{"127.0.0.1"}, nil }
	if cfg.TLSConfig != nil {
		cfg.TLSConfig.ServerName = host
		cfg.TLSConfig.InsecureSkipVerify = true //nolint:gosec // test proxy uses a self-signed certificate
	}
	for k, v := range params {
		cfg.RuntimeParams[k] = v
	}
	return pgx.ConnectConfig(ctx, cfg)
}

func sqlState(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func TestProxyAuthenticatesBeforeWaking(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	databaseID := uuid.NewString()
	role, password := h.addRole(t, databaseID)
	host := databaseID + "." + testSuffix

	conn, err := h.connect(ctx, host, role, password, h.dbName, "require", nil)
	if err != nil {
		t.Fatalf("right password: %v", err)
	}
	var who string
	if err := conn.QueryRow(ctx, "select current_user").Scan(&who); err != nil || who != role {
		t.Fatalf("current_user = %q, %v", who, err)
	}
	time.Sleep(200 * time.Millisecond) // a live session keeps touching the database
	_ = conn.Close(ctx)
	if h.waker.wakes.Load() != 1 || h.waker.touches.Load() == 0 {
		t.Fatalf("wakes=%d touches=%d after one session", h.waker.wakes.Load(), h.waker.touches.Load())
	}

	refused := []struct {
		name, host, user, password, sslmode, want string
	}{
		{"wrong password", host, role, "wrong-password", "require", "28P01"},
		{"unknown database", uuid.NewString() + "." + testSuffix, role, password, "require", "28P01"},
		{"unknown role", host, "nobody", password, "require", "28P01"},
		{"plaintext", host, role, password, "disable", "08P01"},
		{"no route", "127.0.0.1", role, password, "require", "08004"},
	}
	for _, tc := range refused {
		before := h.waker.wakes.Load()
		_, err := h.connect(ctx, tc.host, tc.user, tc.password, h.dbName, tc.sslmode, nil)
		if sqlState(err) != tc.want {
			t.Errorf("%s: err = %v, want SQLSTATE %s", tc.name, err, tc.want)
		}
		if h.waker.wakes.Load() != before {
			t.Errorf("%s: woke the database before authenticating", tc.name)
		}
	}
}

func TestProxyRoutesWithoutSNIAndForwardsBackendErrors(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	databaseID := uuid.NewString()
	role, password := h.addRole(t, databaseID)

	conn, err := h.connect(ctx, "127.0.0.1", role, password, h.dbName, "require", map[string]string{"options": "endpoint=" + databaseID})
	if err != nil {
		t.Fatalf("options routing: %v", err)
	}
	_ = conn.Close(ctx)

	if _, err := h.connect(ctx, databaseID+"."+testSuffix, role, password, "no_such_database", "require", nil); sqlState(err) != "3D000" {
		t.Fatalf("missing backend database = %v, want 3D000 forwarded", err)
	}

	h.waker.fail.Store(true)
	if _, err := h.connect(ctx, databaseID+"."+testSuffix, role, password, h.dbName, "require", nil); sqlState(err) != "57P03" {
		t.Fatalf("wake failure = %v, want 57P03", err)
	}
}

func TestVerifierStoreRoundTrip(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" || os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("DATABASE_URL required")
	}
	pool := pgtest.Open(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	store, err := NewVerifierStore(pool, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	ctx, id := context.Background(), uuid.NewString()
	if _, err := store.Verifier(ctx, id, "app"); !errors.Is(err, ErrNoVerifier) {
		t.Fatalf("missing verifier = %v", err)
	}
	v, _ := pgscram.NewVerifier("store-test-password", pgscram.DefaultIterations)
	if err := store.Put(ctx, id, "app", v); err != nil {
		t.Fatal(err)
	}
	got, err := store.Verifier(ctx, id, "app")
	if err != nil || got.String() != v.String() {
		t.Fatalf("verifier = %v, %v", got, err)
	}
	if err := store.Delete(ctx, id, "app"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Verifier(ctx, id, "app"); err != nil {
		t.Fatal("a cached verifier must survive until its TTL")
	}
	fresh, _ := NewVerifierStore(pool, time.Minute)
	if _, err := fresh.Verifier(ctx, id, "app"); !errors.Is(err, ErrNoVerifier) {
		t.Fatalf("deleted verifier = %v", err)
	}
	if err := store.Put(ctx, "not-a-uuid", "app", v); err == nil {
		t.Fatal("invalid database id accepted")
	}
}
