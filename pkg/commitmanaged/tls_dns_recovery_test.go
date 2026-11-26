package commitmanaged

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	commitwork "github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/net/dns/dnsmessage"
)

const managerTLSTestHost = "commit-fixture.invalid"

// The fixture resolver lives only in this test process. Each exchange uses an
// in-memory DNS connection, so neither system DNS nor hosts files are changed.
type managerTestDNS struct {
	denied  atomic.Bool
	queries atomic.Int64
	wg      sync.WaitGroup
	mu      sync.Mutex
	closing bool
}

func (d *managerTestDNS) resolver() *net.Resolver {
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		d.mu.Lock()
		if d.closing {
			d.mu.Unlock()
			return nil, errors.New("test resolver closed")
		}
		d.wg.Add(1)
		d.mu.Unlock()
		client, server := net.Pipe()
		go func() {
			defer d.wg.Done()
			defer server.Close()
			var size [2]byte
			if _, err := io.ReadFull(server, size[:]); err != nil {
				return
			}
			request := make([]byte, int(binary.BigEndian.Uint16(size[:])))
			if _, err := io.ReadFull(server, request); err != nil {
				return
			}
			var message dnsmessage.Message
			if err := message.Unpack(request); err != nil || len(message.Questions) != 1 {
				return
			}
			question := message.Questions[0]
			response := dnsmessage.Message{Header: dnsmessage.Header{ID: message.Header.ID, Response: true, Authoritative: true, RecursionAvailable: true}, Questions: message.Questions}
			if question.Name.String() != managerTLSTestHost+"." {
				response.Header.RCode = dnsmessage.RCodeNameError
			} else {
				d.queries.Add(1)
				if question.Type == dnsmessage.TypeA {
					ip := [4]byte{127, 0, 0, 1}
					if d.denied.Load() {
						ip[3] = 2
					}
					response.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: question.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}, Body: &dnsmessage.AResource{A: ip}}}
				}
			}
			payload, err := response.Pack()
			if err != nil || len(payload) > 65535 {
				return
			}
			packet := make([]byte, 2+len(payload))
			binary.BigEndian.PutUint16(packet[:2], uint16(len(payload)))
			copy(packet[2:], payload)
			_, _ = server.Write(packet)
		}()
		return client, nil
	}}
}

func (d *managerTestDNS) close() {
	d.mu.Lock()
	d.closing = true
	d.mu.Unlock()
	d.wg.Wait()
}

func managerUntrustedCA(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	cert := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "unrelated test CA"},
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "untrusted-ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCommitManagerTLSAndDNSRecovery(t *testing.T) {
	// Platform triggers refer to public, so each run needs its own migrated DB.
	t.Setenv(pgtest.UseTemplateDatabase, "1")
	e := newChangesEnv(t, true)
	cluster := pgtest.OpenTLSClusterWithDNSNames(t, managerTLSTestHost)
	t.Setenv("PGSSLROOTCERT", cluster.CAPath)
	untrustedCA := managerUntrustedCA(t)
	ctx, cancel := context.WithTimeout(t.Context(), 75*time.Second)
	defer cancel()

	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.account.ID, Slug: "tls-worker-" + uuid.NewString()[:8], RAMMB: 512, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	const queue = "tls-changes"
	if _, err := e.store.CreateQueueBinding(ctx, state.QueueBinding{AccountID: e.account.ID, AppID: app.ID, Name: queue,
		QueueName: queue, Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1}); err != nil {
		t.Fatal(err)
	}
	feeds := []commitwork.ChangeFeed{{Name: "orders", Table: "public.orders", Columns: []string{"id", "status"}}}
	feedJSON, err := json.Marshal(feeds)
	if err != nil {
		t.Fatal(err)
	}
	source, err := e.store.UpsertCommitChangesSource(ctx, e.account.ID, app.ID, "tls-"+app.Slug,
		state.CommitChangesConfig{QueueName: queue, Feeds: feedJSON, PollIntervalMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cluster.Admin.Exec(ctx, `CREATE TABLE public.orders (id int PRIMARY KEY, status text NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	password, err := commitwork.NewRelayPassword()
	if err != nil {
		t.Fatal(err)
	}
	tx, err := cluster.Admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := commitwork.InstallChanges(ctx, tx, commitwork.ChangesInstall{SourceID: source.ID, Feeds: feeds, RelayPassword: password})
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	connection, err := url.Parse(cluster.URL(installed.RelayRole, password))
	if err != nil {
		t.Fatal(err)
	}
	connection.Host = net.JoinHostPort(managerTLSTestHost, connection.Port())
	raw := connection.String()
	sealed, err := commitwork.SealConnection(e.manager.Identities[0].Recipient(), source.ID, raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetCommitSourceConnection(ctx, e.account.ID, source.ID, sealed); err != nil {
		t.Fatal(err)
	}
	policy := commitwork.NetworkPolicy{Hosts: map[string]bool{managerTLSTestHost: true}, Prefixes: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}}
	e.manager.Policy = policy
	openHealthy := e.manager.Open
	e.manager.Open = func(openCtx context.Context, credential string, networkPolicy commitwork.NetworkPolicy) (*pgxpool.Pool, error) {
		if credential == raw {
			return commitwork.OpenPool(openCtx, credential, networkPolicy)
		}
		return openHealthy(openCtx, credential, networkPolicy)
	}
	dns := &managerTestDNS{}
	previousResolver := net.DefaultResolver
	net.DefaultResolver = dns.resolver()
	t.Cleanup(func() { net.DefaultResolver = previousResolver; dns.close() })

	// Verify the qualified hostname negotiates TLS through the real dialer.
	initialPool, err := commitwork.OpenPool(ctx, raw, policy)
	if err != nil {
		t.Fatalf("initial verified TLS connection: %v", err)
	}
	var encrypted bool
	if err := initialPool.QueryRow(ctx, `SELECT ssl FROM pg_stat_ssl WHERE pid=pg_backend_pid()`).Scan(&encrypted); err != nil || !encrypted {
		initialPool.Close()
		t.Fatalf("connection was not verified TLS: encrypted=%t err=%v", encrypted, err)
	}
	initialPool.Close()
	type report struct {
		source, status string
		accepted       int
	}
	reports := make(chan report, 128)
	e.manager.Report = func(source, status string, accepted int) {
		select {
		case reports <- report{source, status, accepted}:
		case <-ctx.Done():
		}
	}
	var buffered []report
	waitReport := func(id, status string, accepted int) {
		t.Helper()
		for {
			for i, item := range buffered {
				if item.source == id && item.status == status && item.accepted == accepted {
					buffered = append(buffered[:i], buffered[i+1:]...)
					return
				}
			}
			select {
			case item := <-reports:
				if strings.Contains(item.status, password) || strings.Contains(item.status, raw) || strings.Contains(item.status, installed.RelayRole) {
					t.Fatal("manager status exposed a credential")
				}
				if item.source == "" && item.status == "source_scan_failed" {
					t.Fatal("platform source scan failed")
				}
				if item.source == id && item.status == status && item.accepted == accepted {
					return
				}
				buffered = append(buffered, item)
			case <-ctx.Done():
				t.Fatalf("waiting for %s (%d accepted): %v", status, accepted, ctx.Err())
			}
		}
	}
	freeze := func() {
		t.Helper()
		for {
			tag, err := e.control.Exec(ctx, `UPDATE commit_sources SET relay_next_poll_at=now()+interval '1 hour'
 WHERE id=$1::uuid AND relay_poll_token IS NULL`, source.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tag.RowsAffected() == 1 {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	due := func() {
		t.Helper()
		tag, err := e.control.Exec(ctx, `UPDATE commit_sources SET relay_next_poll_at='-infinity' WHERE id=$1::uuid AND relay_poll_token IS NULL`, source.ID)
		if err != nil || tag.RowsAffected() != 1 {
			t.Fatalf("source scheduling: rows=%d err=%v", tag.RowsAffected(), err)
		}
	}
	assertSafeFailure := func() {
		t.Helper()
		pool, err := commitwork.OpenPool(ctx, raw, policy)
		if err == nil {
			pool.Close()
			t.Fatal("untrusted or disallowed destination connected")
		}
		if strings.Contains(err.Error(), password) || strings.Contains(err.Error(), raw) || strings.Contains(err.Error(), installed.RelayRole) {
			t.Fatal("connection error exposed credentials")
		}
	}
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(NewObservationCollector(e.store))
	assertMetrics := func(unknown, failing float64) {
		t.Helper()
		families, err := registry.Gather()
		if err != nil {
			t.Fatal(err)
		}
		values := map[string]float64{}
		for _, family := range families {
			if len(family.Metric) != 1 || len(family.Metric[0].Label) != 0 {
				t.Fatal("unexpected observation metric labels")
			}
			values[family.GetName()] = family.Metric[0].GetGauge().GetValue()
		}
		if values["schedd_commit_relay_observation_snapshot_success"] != 1 || values["schedd_commit_relay_enabled_sources"] != 2 ||
			values["schedd_commit_relay_unknown_sources"] != unknown || values["schedd_commit_relay_failing_sources"] != failing {
			t.Fatalf("source health metrics: %v", values)
		}
	}
	for _, db := range []*pgxpool.Pool{e.customer, cluster.Admin} {
		if _, err := db.Exec(ctx, `INSERT INTO public.orders(id,status) VALUES(1,'initial')`); err != nil {
			t.Fatal(err)
		}
	}
	var initialEvent string
	if err := cluster.Admin.QueryRow(ctx, `SELECT event_id::text FROM public.gregale_outbox`).Scan(&initialEvent); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- e.manager.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("manager shutdown: %v", err)
		}
	}()
	waitReport(e.source.ID, "healthy", 1)
	waitReport(source.ID, "healthy", 1)
	t.Log("both sources delivered with a trusted TLS destination")
	original, err := e.store.CommitReceiptByEvent(ctx, e.account.ID, source.ID, initialEvent)
	if err != nil || original.ID == "" || original.InvocationID == "" {
		t.Fatalf("initial receipt: %+v err=%v", original, err)
	}
	freeze()
	assertMetrics(0, 0)

	if err := os.Setenv("PGSSLROOTCERT", untrustedCA); err != nil {
		t.Fatal(err)
	}
	if _, err := cluster.Admin.Exec(ctx, `INSERT INTO public.orders(id,status) VALUES(2,'untrusted')`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.customer.Exec(ctx, `INSERT INTO public.orders(id,status) VALUES(2,'healthy')`); err != nil {
		t.Fatal(err)
	}
	e.makeDue(t)
	due()
	waitReport(source.ID, "database_unavailable", 0)
	waitReport(e.source.ID, "healthy", 1)
	assertSafeFailure()
	assertMetrics(1, 1)
	t.Log("untrusted certificate rejected while the healthy source delivered")
	unknown, err := e.store.CommitSourceByID(ctx, e.account.ID, source.ID)
	if err != nil || unknown.PendingEvents != nil || unknown.RelayStatus != "database_unavailable" {
		t.Fatalf("untrusted source observation: %+v err=%v", unknown, err)
	}

	freeze()
	if err := os.Setenv("PGSSLROOTCERT", cluster.CAPath); err != nil {
		t.Fatal(err)
	}
	dns.denied.Store(true)
	queriesBefore := dns.queries.Load()
	var previousCheck time.Time
	if err := e.control.QueryRow(ctx, `SELECT last_checked_at FROM commit_sources WHERE id=$1::uuid`, source.ID).Scan(&previousCheck); err != nil {
		t.Fatal(err)
	}
	if _, err := cluster.Admin.Exec(ctx, `INSERT INTO public.orders(id,status) VALUES(3,'dns-denied')`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.customer.Exec(ctx, `INSERT INTO public.orders(id,status) VALUES(3,'healthy')`); err != nil {
		t.Fatal(err)
	}
	e.makeDue(t)
	due()
	waitReport(source.ID, "database_unavailable", 0)
	waitReport(e.source.ID, "healthy", 1)
	// A delayed report from the preceding failure must not stand in for a
	// poll that actually used the changed DNS answer.
	for {
		current, err := e.store.CommitSourceByID(ctx, e.account.ID, source.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.LastCheckedAt != nil && current.LastCheckedAt.After(previousCheck) && current.RelayStatus == "database_unavailable" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("manager did not record the changed DNS failure")
		case <-time.After(20 * time.Millisecond):
		}
	}
	if dns.queries.Load() <= queriesBefore {
		t.Fatal("manager did not resolve the changed destination")
	}
	assertSafeFailure()
	assertMetrics(1, 1)
	t.Log("changed DNS destination rejected while the healthy source delivered")
	var pending int
	if err := cluster.Admin.QueryRow(ctx, `SELECT count(*) FROM public.gregale_outbox WHERE accepted_at IS NULL`).Scan(&pending); err != nil || pending != 2 {
		t.Fatalf("pending during trust and DNS failures=%d err=%v", pending, err)
	}

	freeze()
	dns.denied.Store(false)
	queriesBefore = dns.queries.Load()
	due()
	waitReport(source.ID, "healthy", 2)
	if dns.queries.Load() <= queriesBefore {
		t.Fatal("manager did not resolve the restored destination")
	}
	assertMetrics(0, 0)
	t.Log("restored trust and DNS recovered pending delivery")
	var backoff int
	if err := e.control.QueryRow(ctx, `SELECT relay_poll_backoff_ms FROM commit_sources WHERE id=$1::uuid`, source.ID).Scan(&backoff); err != nil || backoff != 0 {
		t.Fatalf("recovery backoff=%d err=%v", backoff, err)
	}
	rows, err := cluster.Admin.Query(ctx, `SELECT event_id::text,event_type,payload,receipt_id::text,invocation_id::text FROM public.gregale_outbox ORDER BY created_at,event_id`)
	if err != nil {
		t.Fatal(err)
	}
	type checkpoint struct {
		event, kind, receipt, invocation string
		payload                          []byte
	}
	var checkpoints []checkpoint
	for rows.Next() {
		var item checkpoint
		if err := rows.Scan(&item.event, &item.kind, &item.payload, &item.receipt, &item.invocation); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		checkpoints = append(checkpoints, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(checkpoints) != 3 {
		t.Fatalf("recovered checkpoints=%d err=%v", len(checkpoints), err)
	}
	for _, item := range checkpoints {
		receipt, err := e.store.CommitReceiptByEvent(ctx, e.account.ID, source.ID, item.event)
		if err != nil || receipt.ID != item.receipt || receipt.InvocationID != item.invocation {
			t.Fatalf("checkpoint receipt mismatch: %+v err=%v", receipt, err)
		}
		replayed, err := e.store.AcceptCommitChange(ctx, e.account.ID, source.ID, item.event, item.kind, item.payload, state.Invocation{}, nil)
		if err != nil || replayed.ID != receipt.ID || replayed.InvocationID != receipt.InvocationID {
			t.Fatalf("replay changed receipt: %+v err=%v", replayed, err)
		}
		if item.event == initialEvent && (receipt.ID != original.ID || receipt.InvocationID != original.InvocationID) {
			t.Fatal("pre-failure receipt changed")
		}
	}
	var receipts, invocations int
	if err := e.control.QueryRow(ctx, `SELECT count(*),count(DISTINCT invocation_id) FROM commit_receipts WHERE source_id=$1::uuid`, source.ID).Scan(&receipts, &invocations); err != nil || receipts != 3 || invocations != 3 {
		t.Fatalf("source receipts=%d invocations=%d err=%v", receipts, invocations, err)
	}
	if n := e.pending(t); n != 0 {
		t.Fatalf("healthy source retained %d pending changes", n)
	}
}
