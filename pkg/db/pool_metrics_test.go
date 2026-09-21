package db

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// openTestPoolWithMaxConns opens a pool with an exact connection cap.
//
// pgtest.Open cannot be used here: it fixes the pool's configuration, and
// these tests need a cap of exactly 1 to make pool exhaustion deterministic
// rather than load-dependent. The skip behaviour deliberately mirrors
// pgtest.Open's, so a developer without a local cluster sees the same skip
// they get from every other Postgres-backed test rather than a failure.
func openTestPoolWithMaxConns(t *testing.T, maxConns int32) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("FAAS_SKIP_PG_TESTS set; skipping Postgres integration test")
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres:///faas?host=/run/postgresql&user=faas"
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Skipf("pool metrics: cannot parse DATABASE_URL (%v); skipping", err)
	}
	cfg.MaxConns = maxConns
	cfg.MinConns = 0
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Skipf("pool metrics: cannot connect to Postgres (%v); skipping", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("pool metrics: Postgres not reachable (%v); skipping", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// gather collects one scrape from a registry holding only c and returns the
// metric families keyed by name.
func gather(t *testing.T, c *PoolCollector) map[string]*dto.MetricFamily {
	t.Helper()
	reg := prometheus.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("Register: %v", err)
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	out := make(map[string]*dto.MetricFamily, len(families))
	for _, f := range families {
		out[f.GetName()] = f
	}
	return out
}

// value returns the single sample's value for an unlabelled family.
func value(t *testing.T, families map[string]*dto.MetricFamily, name string) float64 {
	t.Helper()
	f, ok := families[name]
	if !ok {
		t.Fatalf("metric %q absent; got %d families", name, len(families))
	}
	if len(f.GetMetric()) != 1 {
		t.Fatalf("metric %q has %d samples, want 1", name, len(f.GetMetric()))
	}
	m := f.GetMetric()[0]
	if m.GetGauge() != nil {
		return m.GetGauge().GetValue()
	}
	if m.GetCounter() != nil {
		return m.GetCounter().GetValue()
	}
	t.Fatalf("metric %q is neither gauge nor counter", name)
	return 0
}

// labelled returns the sample value for a single-label family.
func labelled(t *testing.T, families map[string]*dto.MetricFamily, name, label string) float64 {
	t.Helper()
	f, ok := families[name]
	if !ok {
		t.Fatalf("metric %q absent", name)
	}
	for _, m := range f.GetMetric() {
		for _, lp := range m.GetLabel() {
			if lp.GetValue() == label {
				return m.GetGauge().GetValue()
			}
		}
	}
	t.Fatalf("metric %q has no sample labelled %q", name, label)
	return 0
}

// TestPoolCollectorNilPoolIsSafe pins the contract that a daemon which has
// not opened Postgres still serves /metrics. Several daemons (vmmd without a
// [compute_node] name, and every daemon during early boot) hold a nil pool,
// and a collector that panicked or errored there would take the scrape
// endpoint down with it — turning an observability addition into an outage.
func TestPoolCollectorNilPoolIsSafe(t *testing.T) {
	c := NewPoolCollector("testd", nil)
	families := gather(t, c)
	if len(families) != 0 {
		t.Errorf("nil pool produced %d metric families, want 0", len(families))
	}

	// A nil *PoolCollector must also be inert: RegisterPoolMetrics guards
	// this, but Describe/Collect are exported and reachable directly.
	var nilc *PoolCollector
	descs := make(chan *prometheus.Desc, 16)
	nilc.Describe(descs)
	close(descs)
	if n := len(descs); n != 0 {
		t.Errorf("nil collector described %d metrics, want 0", n)
	}
	metrics := make(chan prometheus.Metric, 16)
	nilc.Collect(metrics)
	close(metrics)
	if n := len(metrics); n != 0 {
		t.Errorf("nil collector collected %d metrics, want 0", n)
	}
}

// TestPoolCollectorDescribesEveryMetric pins the Describe/Collect symmetry.
// A Desc that Describe omits still scrapes, but Prometheus cannot detect a
// name collision against it at registration time, so the drift shows up as a
// duplicate series in production rather than an error at boot.
func TestPoolCollectorDescribesEveryMetric(t *testing.T) {
	c := NewPoolCollector("testd", nil)
	ch := make(chan *prometheus.Desc, 64)
	c.Describe(ch)
	close(ch)
	const want = 9
	if got := len(ch); got != want {
		t.Errorf("Describe emitted %d descriptors, want %d", got, want)
	}
}

// TestPoolCollectorReportsSaturation is the reason this collector exists.
//
// Exporting a connection count is easy and nearly useless; the question an
// operator has before lowering a DaemonMaxConnections entry is "did anything
// ever have to wait for a connection?". pgxpool distinguishes two answers and
// this test pins both, because they call for opposite responses:
//
//   - empty_acquires_total — a caller waited and then got a connection. The
//     cap is tight but the work completed.
//   - canceled_acquires_total — a caller gave up while waiting. The cap is
//     already costing customer-visible timeouts.
//
// A pool of exactly one connection makes both reachable deterministically.
//
// Note the baseline below: empty_acquires is already non-zero before any
// contention, because MinConns=0 means the pool's very first acquisition has
// to construct a connection and pgx counts that as "empty". That is why the
// metric's HELP warns against reading it as pure saturation, and why the
// assertions here measure a DELTA rather than an absolute. canceled_acquires
// has no such warm-up floor, which is what makes it the signal a capacity
// decision should rest on.
func TestPoolCollectorReportsSaturation(t *testing.T) {
	pool := openTestPoolWithMaxConns(t, 1)
	c := NewPoolCollector("testd", pool)
	ctx := context.Background()

	families := gather(t, c)
	if got := value(t, families, "testd_db_pool_max_conns"); got != 1 {
		t.Fatalf("max_conns = %v, want 1", got)
	}
	baselineEmpty := value(t, families, "testd_db_pool_empty_acquires_total")
	if got := value(t, families, "testd_db_pool_canceled_acquires_total"); got != 0 {
		t.Fatalf("canceled_acquires before contention = %v, want 0 — it must have no warm-up floor", got)
	}

	// Hold the only connection.
	held, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	families = gather(t, c)
	if got := labelled(t, families, "testd_db_pool_conns", "acquired"); got != 1 {
		t.Errorf("conns{acquired} = %v, want 1 while the only connection is held", got)
	}

	// A caller that gives up while waiting is a cancelled acquire.
	waitCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	if _, err := pool.Acquire(waitCtx); err == nil {
		t.Fatal("Acquire succeeded against a fully-held pool, want a timeout")
	}
	cancel()

	families = gather(t, c)
	if got := value(t, families, "testd_db_pool_canceled_acquires_total"); got < 1 {
		t.Errorf("canceled_acquires = %v, want >= 1 after an abandoned acquire", got)
	}

	// A caller that waits and then succeeds is an empty acquire. Release the
	// held connection while a second acquire is already blocked on it.
	done := make(chan error, 1)
	go func() {
		conn, err := pool.Acquire(ctx)
		if err == nil {
			conn.Release()
		}
		done <- err
	}()
	// Give the goroutine time to block on the empty pool before releasing.
	time.Sleep(100 * time.Millisecond)
	held.Release()
	if err := <-done; err != nil {
		t.Fatalf("queued Acquire: %v", err)
	}

	families = gather(t, c)
	if got := value(t, families, "testd_db_pool_empty_acquires_total"); got <= baselineEmpty {
		t.Errorf("empty_acquires = %v, want > the %v baseline after an acquire waited for a release", got, baselineEmpty)
	}
	if got := value(t, families, "testd_db_pool_acquires_total"); got < 2 {
		t.Errorf("acquires_total = %v, want >= 2", got)
	}
	if got := value(t, families, "testd_db_pool_acquire_wait_seconds_total"); got <= 0 {
		t.Errorf("acquire_wait_seconds = %v, want > 0 after a blocked acquire", got)
	}
}

// TestPoolCollectorReportsHubShape pins the gauges that make an
// evidence-backed DaemonMaxConnections cut possible.
//
// Before ADR-190 every SubscribeWithReconnect call parked its own connection,
// so "subscribers" and "parked connections" were the same number and the
// per-daemon caps were sized around it. The hub broke that identity. The cut
// is only safe if the identity is observably broken in production, which is
// what conns=1 alongside subscribers=N demonstrates.
func TestPoolCollectorReportsHubShape(t *testing.T) {
	pool := openTestPoolWithMaxConns(t, 4)
	c := NewPoolCollector("testd", pool)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// No hub yet: a pool nobody subscribed on parks nothing.
	families := gather(t, c)
	if got := value(t, families, "testd_db_notify_hub_conns"); got != 0 {
		t.Errorf("hub_conns with no subscribers = %v, want 0", got)
	}

	// Two subscribers on two distinct channels.
	for _, ch := range [][]string{{"pool_metrics_test_a"}, {"pool_metrics_test_b"}} {
		if _, err := SubscribeWithReconnect(ctx, pool, ch, nil); err != nil {
			t.Fatalf("SubscribeWithReconnect(%v): %v", ch, err)
		}
	}

	families = gather(t, c)
	if got := value(t, families, "testd_db_notify_hub_conns"); got != 1 {
		t.Errorf("hub_conns = %v, want exactly 1 — two subscribers must share one connection", got)
	}
	if got := value(t, families, "testd_db_notify_hub_subscribers"); got != 2 {
		t.Errorf("hub_subscribers = %v, want 2", got)
	}
	if got := value(t, families, "testd_db_notify_hub_channels"); got != 2 {
		t.Errorf("hub_channels = %v, want 2", got)
	}

	// The whole point: the pool is holding far fewer connections than it has
	// subscribers. Under the pre-hub model this assertion would fail.
	acquired := labelled(t, families, "testd_db_pool_conns", "acquired")
	subscribers := value(t, families, "testd_db_notify_hub_subscribers")
	if acquired >= subscribers {
		t.Errorf("pool holds %v connections for %v subscribers — the hub is not multiplexing", acquired, subscribers)
	}
}
