// poller_test.go — table-driven coverage for the schedd-side
// per-instance metrics poller (issue #170 / PR-A).
//
// The poller's contract is narrow:
//
//   1. enumerate ActiveComputeNodes (MemStore auto-seeds
//      'default-local'; tests add siblings via CreateComputeNode);
//   2. enumerate every Instance (state.ListAllInstances), group
//      by NodeID for the per-node join;
//   3. for each active node, dial a fresh VMM via the injected
//      Dialer, call Stats, then Close — same dial-per-node
//      pattern the heartbeat loop uses (issue #120);
//   4. on dial success: build InstanceStat rows (CPUPct, RSSMB,
//      InflightRequests, LastRequestAt), replace the Reader's
//      snapshot, emit the wire rollup;
//   5. on per-node failure: log, increment the partial-error
//      counter, continue — partial > nothing.
//   6. on empty active nodes: Replace([]) clears the Reader and
//      the wire rollup collapses — no zombie samples.
//
// The tests below exercise every branch without spinning up a
// real scheduler loop or a real Postgres; MemStore covers the
// Store surface, a hand-rolled fakeDialer covers the Dialer
// surface, and a hand-rolled fakeVMM covers the VMM surface.

package instancestats

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// --- fakes ------------------------------------------------------------------

// statsFakeDialer implements Dialer for tests. It counts every Dial
// call, threads per-target error injection, and returns a stub VMM
// whose Stats applies the same per-target error / per-instance stat
// map. Mirrors the heartbeat test's fakeDialer shape (issue #120).
type statsFakeDialer struct {
	mu      sync.Mutex
	dials   []string // targetURLs in Dial call order
	closed  int      // number of VMM clients closed by the poller
	dialErr map[string]error
	stats   map[string]*sched.StatsSnapshot // targetURL → snapshot
}

func (d *statsFakeDialer) Dial(_ context.Context, target string, _ *tls.Config) (sched.VMM, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dials = append(d.dials, target)
	if err, ok := d.dialErr[target]; ok {
		return nil, err
	}
	return &statsFakeVMM{target: target, dialer: d}, nil
}

// statsFakeVMM is the stub VMM returned by statsFakeDialer. Stats
// applies the per-target snapshot injection; Close bumps the
// dialer's closed counter so tests can verify the poller actually
// closes each fresh conn (no goroutine leak across ticks).
type statsFakeVMM struct {
	target string
	dialer *statsFakeDialer
}

func (v *statsFakeVMM) Ping(_ context.Context) (*sched.PingOutcome, error) {
	return &sched.PingOutcome{FcVersion: "1.10.0", ServerTime: time.Now()}, nil
}

// Tier A5 (ADR-066) — instancestats tests don't drive
// migration; the four RPCs are no-op stubs to satisfy
// sched.VMM. Production migration goes through
// pkg/sched/migration_handoff_test.go.
func (v *statsFakeVMM) PrepareLiveMigration(context.Context, string, string, string) (sched.LiveMigrationPrepare, error) {
	return sched.LiveMigrationPrepare{}, nil
}
func (v *statsFakeVMM) AdoptMigratedInstance(context.Context, string, string, sched.AppSpec, string, string, string) (sched.LiveMigrationAdopt, error) {
	return sched.LiveMigrationAdopt{}, nil
}
func (v *statsFakeVMM) AcknowledgeMigration(context.Context, string, string, string) error {
	return nil
}
func (v *statsFakeVMM) CancelLiveMigration(context.Context, string, string, string) error {
	return nil
}

// FrameworkReady (issue #470 / PR #470-FU-B) is the vmmd-side
// receipt of the guest-init "framework ready" DGRAM. The
// instancestats poller drives the wake/stats/heartbeat path,
// not the framework-ready path, so the stub returns nil to
// satisfy the closed VMM interface. The actual receipt data
// path is exercised in pkg/vmmdgrpc/bufconn_test.go.
func (v *statsFakeVMM) FrameworkReady(context.Context, string, int64) error {
	return nil
}
func (v *statsFakeVMM) CreateColdBoot(context.Context, string, sched.AppSpec) (*sched.WakeOutcome, error) {
	return &sched.WakeOutcome{}, nil
}
func (v *statsFakeVMM) CreateFromSnapshot(context.Context, string, sched.AppSpec, sched.SnapshotRef) (*sched.WakeOutcome, error) {
	return &sched.WakeOutcome{}, nil
}
func (v *statsFakeVMM) PauseAndSnapshot(context.Context, string, string, string, string) (sched.SnapshotBytes, error) {
	return sched.SnapshotBytes{}, nil
}

// WarmSnapshot (issue #470 / PR #470-FU-A) is the instancestats
// test's no-op seam — the poller doesn't fire warm captures.
func (v *statsFakeVMM) WarmSnapshot(context.Context, string, string, string) (sched.SnapshotBytes, error) {
	return sched.SnapshotBytes{}, nil
}
func (v *statsFakeVMM) Destroy(context.Context, string) error { return nil }

// StopInstance (M-2 / ADR-138 §Decision 1) is the
// graceful signal-then-grace-then-SIGKILL stop
// sequence. Test fakes default to no-op + nil —
// the engine's per-mode dispatch lives in
// pkg/sched/engine_stop_pgtest_test.go (commit 6).
func (v *statsFakeVMM) StopInstance(_ context.Context, _ string, _, _ int32) (*sched.StopInstanceOutcome, error) {
	return nil, nil
}
func (v *statsFakeVMM) StopInstanceOnNode(_ context.Context, _, _ string, _, _ int32) (*sched.StopInstanceOutcome, error) {
	return nil, nil
}

// UpdateEgressAllowlist (tier-2 PR-B) — instancestats tests don't
// drive the egress drift path; egress_drift_test.go covers it.
// Returning nil keeps the sched.VMM contract satisfied for the
// poller tests that wire statsFakeDialer.
func (v *statsFakeVMM) UpdateEgressAllowlist(context.Context, string, []netip.Prefix) error {
	return nil
}

// UpdateStaticEgressIP (ADR-119) is the no-op test fake.
func (v *statsFakeVMM) UpdateStaticEgressIP(context.Context, string, string, string) error {
	return nil
}

// Logs (issue #254 / Move 4, issue #517 / PR-B) — instancestats
// tests don't drive the log stream path; the scheddgrpc handler
// tests do. Returns nil + an error so the caller's "no log stream"
// branch is exercised. PR-B adds the sinceWrittenAt time lower-bound;
// the fake ignores it.
func (v *statsFakeVMM) Logs(context.Context, string, int64, time.Time) (sched.LogStream, error) {
	return nil, errors.New("instancestats test stubs Logs; use scheddgrpc for Move 4 path")
}

func (v *statsFakeVMM) Stats(_ context.Context) (*sched.StatsSnapshot, error) {
	v.dialer.mu.Lock()
	defer v.dialer.mu.Unlock()
	snap, ok := v.dialer.stats[v.target]
	if !ok {
		return &sched.StatsSnapshot{}, nil
	}
	// Defensive copy so the test can mutate the seed map without
	// touching the poller's view.
	out := &sched.StatsSnapshot{
		LiveCount:   snap.LiveCount,
		LeasedCount: snap.LeasedCount,
		SampledAt:   snap.SampledAt,
	}
	if snap.Instances != nil {
		out.Instances = make([]sched.VMInstanceStat, len(snap.Instances))
		copy(out.Instances, snap.Instances)
	}
	return out, nil
}

func (v *statsFakeVMM) Close() error {
	v.dialer.mu.Lock()
	v.dialer.closed++
	v.dialer.mu.Unlock()
	return nil
}

// Compile-time assertion that the dialer satisfies Dialer.
var _ Dialer = (*statsFakeDialer)(nil)

// --- helpers ----------------------------------------------------------------

// seedTwoNodes returns (default-local, sibling) — mirrors the
// heartbeat test's two-node shape (issue #120). The default-local
// row is auto-seeded by NewMemStore; the sibling is created with
// CreateComputeNode.
func seedTwoNodes(t *testing.T, store *state.MemStore) (state.ComputeNode, state.ComputeNode) {
	t.Helper()
	ctx := context.Background()
	defaultLocal, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("ComputeNodeByName default-local: %v", err)
	}
	sibling, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name:               "node-b",
		TargetURL:          "tcp://10.0.0.2:50051",
		VPCPUs:             8,
		MemMB:              8192,
		MaxConcurrency:     4,
		AdmissionCeilingMB: 4096,
		Active:             true,
	})
	if err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}
	return defaultLocal, sibling
}

// seedInstance builds one Instance row with the given (app, node)
// tuple. The MemStore's CreateInstance signature is positional and
// does NOT accept LastRequestAt directly — that field is read-only
// from durable state, populated by TouchInstancesLastSeen. The
// durable-fallback tests seed it via that public API.
func seedInstance(t *testing.T, store *state.MemStore, appID, nodeID string) state.Instance {
	t.Helper()
	ins, err := store.CreateInstance(context.Background(), appID, "deploy-1", string(state.StateRunning), 256, nodeID, "")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	return ins
}

// nilLogger returns a slog.Logger that discards everything — keeps
// test output clean without requiring the package to import the
// silent helper from cmd/faas.
func nilLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// scrapeMetrics scrapes the daemon's /metrics endpoint and returns
// the raw text. The schedd-side emitter (pkg/wire.NewOpsMetrics)
// owns the Handler — the poller only writes into it. Mirrors the
// pattern in pkg/wire/metrics_instancestats_test.go.
func scrapeMetrics(t *testing.T, m *wire.OpsMetrics) string {
	t.Helper()
	if m == nil {
		return ""
	}
	srv := httptest.NewServer(m.Handler())
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1024)
	for {
		n, err := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return string(buf)
}

// ptrF64 / ptrI64 are tiny helpers for the optional *float64 / *int64
// fields on VMInstanceStat.
func ptrF64(v float64) *float64 { return &v }
func ptrI64(v int64) *int64     { return &v }

// --- tests ------------------------------------------------------------------

// TestPoller_PartialNodeFailure pins the per-node partial-failure
// contract: one node's Dial errors, the sibling's Dial succeeds.
// The failed node bumps the partial-error counter and produces no
// rows; the healthy node's rows still land in the Reader. This is
// the load-bearing behaviour — without it, a single dead vmmd
// would blank the entire rollup.
func TestPoller_PartialNodeFailure(t *testing.T) {
	store := state.NewMemStore()
	dead, live := seedTwoNodes(t, store)
	// Two instances on the live node, one on the dead node — but
	// the dead node's Dial will fail, so only the live rows land.
	insLive := seedInstance(t, store, "app1", live.ID)
	seedInstance(t, store, "app1", live.ID)
	_ = seedInstance(t, store, "app2", dead.ID)

	cpuVal := 42.0
	rssVal := int64(256 * 1024 * 1024)
	dialer := &statsFakeDialer{
		dialErr: map[string]error{dead.TargetURL: errors.New("dial refused")},
		stats: map[string]*sched.StatsSnapshot{
			live.TargetURL: {
				Instances: []sched.VMInstanceStat{
					{InstanceID: insLive.ID, CPUPct: ptrF64(cpuVal), ResidentBytes: ptrI64(rssVal), InflightRequests: 2},
				},
			},
		},
	}
	m := wire.NewOpsMetrics("schedd")
	p := NewPoller(store, dialer, nil, NewReader(), m, nilLogger())

	if err := p.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// Dial was attempted on both nodes (the poller cannot tell in
	// advance that a node is dead — that's the whole point of the
	// dial-fresh policy, same as the heartbeat).
	if got := len(dialer.dials); got != 2 {
		t.Errorf("Dial calls = %d, want 2 (one per active node)", got)
	}
	// The dead node's dial failed so Close was never reached on its
	// stub. The live node closed cleanly.
	if got := dialer.closed; got != 1 {
		t.Errorf("Close calls = %d, want 1 (dial-error path skips Close on the dead node)", got)
	}
	// Reader holds only the live node's row.
	snap := p.Reader.SnapshotAll()
	if len(snap) != 1 {
		t.Fatalf("SnapshotAll len = %d, want 1 (live node only)", len(snap))
	}
	if snap[0].AppID != "app1" || snap[0].NodeID != live.ID {
		t.Errorf("row = %+v, want app1 on %s", snap[0], live.ID)
	}
	// Partial-error counter surfaces in the metrics scrape.
	body := scrapeMetrics(t, m)
	if !strings.Contains(body, `schedd_instance_stats_partial_errors_total{node="`+dead.ID+`"} 1`) {
		t.Errorf("metrics body missing partial-error counter for dead node:\n%s", body)
	}
}

// TestPoller_PersistentTelemetryAvoidsPerNodeDials pins the production
// observer path: vmmd Stats arrives through the node cache and the 200 ms
// projection never opens a VMM connection.
func TestPoller_PersistentTelemetryAvoidsPerNodeDials(t *testing.T) {
	store := state.NewMemStore()
	_, live := seedTwoNodes(t, store)
	ins := seedInstance(t, store, "app1", live.ID)
	resident := int64(128 * 1024 * 1024)
	cpu := 37.5
	cache := sched.NewNodeTelemetryCache()
	now := time.Unix(500, 0)
	cache.Replace(live.ID, now, now, []sched.NodeTelemetry{{
		InstanceID:       ins.ID,
		ResidentBytes:    &resident,
		CPUPct:           &cpu,
		InflightRequests: 2,
	}})
	dialer := &statsFakeDialer{}
	p := NewPoller(store, dialer, nil, NewReader(), wire.NewOpsMetrics("schedd"), nilLogger()).
		WithTelemetry(cache).
		WithNodeRegistry(sched.NewNodeRegistry([]state.ComputeNode{live}))
	p.Now = func() time.Time { return now }

	if err := p.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := len(dialer.dials); got != 0 {
		t.Fatalf("VMM dials = %d, want 0 on persistent telemetry path", got)
	}
	rows := p.Reader.SnapshotAll()
	if len(rows) != 1 {
		t.Fatalf("SnapshotAll length = %d, want 1", len(rows))
	}
	if rows[0].AppID != "app1" || rows[0].NodeID != live.ID || rows[0].RSSMB != 128 || rows[0].CPUPct != cpu {
		t.Fatalf("row = %+v, want app1/%s rss=128 cpu=%v", rows[0], live.ID, cpu)
	}
	if !rows[0].SampledAt.Equal(now) {
		t.Fatalf("row SampledAt = %v, want telemetry sample time %v", rows[0].SampledAt, now)
	}
}

// TestPoller_FirstSampleCPUUnknown pins the cgroup "first sample"
// invariant: the cumulative CPU counter needs a prior reading to
// produce a rate. The poller stamps CPU=Unknown on the very first
// sample per instance — that is the canonical "I haven't seen
// enough data to emit a rate" sentinel. The Prometheus rollup
// drops the Unknown row from the (app, node) gauge.
//
// PR-A wire: today vmmd emits CPUPct=nil (cgroupstats reader does
// not populate cumulative usage_usec on the wire yet — that's
// PR-B's stats.go extraction). The poller respects that contract:
// nil → Unknown, row lands with CPUPct=NaN and CPU=Unknown.
func TestPoller_FirstSampleCPUUnknown(t *testing.T) {
	store := state.NewMemStore()
	_, live := seedTwoNodes(t, store)
	ins := seedInstance(t, store, "app1", live.ID)

	dialer := &statsFakeDialer{
		stats: map[string]*sched.StatsSnapshot{
			live.TargetURL: {
				Instances: []sched.VMInstanceStat{
					// CPUPct nil, ResidentBytes populated.
					{InstanceID: ins.ID, ResidentBytes: ptrI64(128 * 1024 * 1024)},
				},
			},
		},
	}
	m := wire.NewOpsMetrics("schedd")
	p := NewPoller(store, dialer, nil, NewReader(), m, nilLogger())

	if err := p.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	snap := p.Reader.SnapshotAll()
	if len(snap) != 1 {
		t.Fatalf("SnapshotAll len = %d, want 1", len(snap))
	}
	row := snap[0]
	if row.CPU != Unknown {
		t.Errorf("row.CPU = %v, want Unknown (first sample)", row.CPU)
	}
	if row.CPUPct != 0 { // zero-value float, never set
		t.Errorf("row.CPUPct = %v, want 0 (zero-value when CPU=Unknown)", row.CPUPct)
	}
	if row.RSS != Valid {
		t.Errorf("row.RSS = %v, want Valid (ResidentBytes populated)", row.RSS)
	}
	if row.RSSMB != 128 {
		t.Errorf("row.RSSMB = %v, want 128", row.RSSMB)
	}
	// Wire rollup: CPU sample is NaN (the poller passes through),
	// RSS sample is 128. The CPU gauge must show NO sample line
	// for (app1, live); the RSS gauge must show 128.
	body := scrapeMetrics(t, m)
	if strings.Contains(body, `schedd_instance_cpu_pct{app="app1"`) {
		t.Errorf("CPU gauge emitted a sample despite Unknown row:\n%s", body)
	}
	if !strings.Contains(body, `schedd_instance_rss_mb{app="app1",node="`+live.ID+`"} 128`) {
		t.Errorf("RSS gauge missing 128 sample:\n%s", body)
	}
	// Guardrail: NaN must never reach the scrape.
	if strings.Contains(body, " NaN") {
		t.Errorf("metrics body contains a NaN sample:\n%s", body)
	}
}

// TestPoller_CPUBaselineResetOnRegression pins the regression /
// cgroup-recreation behaviour: a cumulative counter that goes
// backwards is a brand-new cgroup (Firecracker rebuild, jailer
// restart, cgroup recreation). The poller stamps the next sample
// as Unknown to flush the stale baseline. PR-A wire currently
// emits CPUPct directly from the cumulative counter via the
// Stats handler, so this test exercises the *rollup* semantics
// rather than the cgroup-level delta math (the cgroup delta math
// lives in PR-B's stats.go; PR-A's poller respects whatever the
// wire sends and lets the rollup collapse).
//
// What PR-A does own: when the wire sends CPUPct=nil (the
// "vmmd has no value yet" sentinel), the poller stamps Unknown.
// The next non-nil sample goes straight through — the rollup
// never produces a synthetic NaN.
func TestPoller_CPUBaselineResetOnRegression(t *testing.T) {
	store := state.NewMemStore()
	_, live := seedTwoNodes(t, store)
	ins := seedInstance(t, store, "app1", live.ID)

	dialer := &statsFakeDialer{
		stats: map[string]*sched.StatsSnapshot{
			live.TargetURL: {
				Instances: []sched.VMInstanceStat{
					{InstanceID: ins.ID, ResidentBytes: ptrI64(64 * 1024 * 1024)},
				},
			},
		},
	}
	m := wire.NewOpsMetrics("schedd")
	p := NewPoller(store, dialer, nil, NewReader(), m, nilLogger())

	// Tick 1: wire is nil → CPU=Unknown. The gauge is unobserved.
	if err := p.Tick(context.Background()); err != nil {
		t.Fatalf("Tick 1: %v", err)
	}
	snap := p.Reader.SnapshotAll()
	if snap[0].CPU != Unknown {
		t.Errorf("after tick 1: row.CPU = %v, want Unknown", snap[0].CPU)
	}
	body1 := scrapeMetrics(t, m)
	if strings.Contains(body1, `schedd_instance_cpu_pct{`) {
		t.Errorf("after tick 1: CPU gauge emitted a sample:\n%s", body1)
	}

	// Tick 2: wire sends a real CPUPct (post-baseline). The gauge
	// now shows the value.
	dialer.stats[live.TargetURL].Instances[0].CPUPct = ptrF64(33.0)
	if err := p.Tick(context.Background()); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}
	snap = p.Reader.SnapshotAll()
	if snap[0].CPU != Valid {
		t.Errorf("after tick 2: row.CPU = %v, want Valid", snap[0].CPU)
	}
	if snap[0].CPUPct != 33.0 {
		t.Errorf("after tick 2: row.CPUPct = %v, want 33", snap[0].CPUPct)
	}
	body2 := scrapeMetrics(t, m)
	if !strings.Contains(body2, `schedd_instance_cpu_pct{app="app1",node="`+live.ID+`"} 33`) {
		t.Errorf("after tick 2: CPU gauge missing 33 sample:\n%s", body2)
	}
}

// TestPoller_EmptyNodesClearsReader pins the "no live nodes"
// reset: a Tick with no active compute_nodes (or no live
// instances on the active nodes) clears the Reader and the wire
// rollup. Without this, a destroyed app's last rollup would
// linger until the next Tick brought it back — drift between
// the live view and the durable state.
func TestPoller_EmptyNodesClearsReader(t *testing.T) {
	store := state.NewMemStore()
	_, live := seedTwoNodes(t, store)
	ins := seedInstance(t, store, "app1", live.ID)

	dialer := &statsFakeDialer{
		stats: map[string]*sched.StatsSnapshot{
			live.TargetURL: {
				Instances: []sched.VMInstanceStat{
					{InstanceID: ins.ID, ResidentBytes: ptrI64(64 * 1024 * 1024)},
				},
			},
		},
	}
	m := wire.NewOpsMetrics("schedd")
	p := NewPoller(store, dialer, nil, NewReader(), m, nilLogger())

	// Tick 1: populate.
	if err := p.Tick(context.Background()); err != nil {
		t.Fatalf("Tick 1: %v", err)
	}
	if got := len(p.Reader.SnapshotAll()); got != 1 {
		t.Fatalf("after tick 1: SnapshotAll len = %d, want 1", got)
	}
	body1 := scrapeMetrics(t, m)
	if !strings.Contains(body1, `schedd_instance_rss_mb{app="app1"`) {
		t.Fatalf("after tick 1: RSS gauge missing populated row:\n%s", body1)
	}

	// Now drain the live instance from durable state. The
	// poller enumerates via ListAllInstances, which filters by
	// state ∈ {RUNNING, WAKING, COLD_BOOTING, SNAPSHOTTING} —
	// removing the row entirely is the cleanest way to pin the
	// "no live instances" path without going through the full
	// Park → terminal transition.
	if err := store.DeleteInstance(context.Background(), ins.ID); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}
	// vmmd now returns an empty snapshot for the node (no
	// instances to report).
	dialer.stats[live.TargetURL] = &sched.StatsSnapshot{}
	if err := p.Tick(context.Background()); err != nil {
		t.Fatalf("Tick 2: %v", err)
	}

	if got := len(p.Reader.SnapshotAll()); got != 0 {
		t.Errorf("after tick 2: SnapshotAll len = %d, want 0 (no live rows)", got)
	}
	body2 := scrapeMetrics(t, m)
	for _, prefix := range []string{
		`schedd_instance_cpu_pct{`,
		`schedd_instance_rss_mb{`,
		`schedd_instance_inflight_requests{`,
	} {
		if strings.Contains(body2, prefix) {
			t.Errorf("after tick 2: metrics body still contains sample line %q", prefix)
		}
	}
}

// TestPoller_AppNodeRollups pins the per-(app, node) rollup
// semantics: two siblings of the same (app, node) collapse to
// max CPU, sum RSS, sum inflight. This is the per-instance→per-
// node collapse the (app, node) label cardinality is designed to
// enforce.
func TestPoller_AppNodeRollups(t *testing.T) {
	store := state.NewMemStore()
	_, live := seedTwoNodes(t, store)
	a := seedInstance(t, store, "app1", live.ID)
	b := seedInstance(t, store, "app1", live.ID)

	dialer := &statsFakeDialer{
		stats: map[string]*sched.StatsSnapshot{
			live.TargetURL: {
				Instances: []sched.VMInstanceStat{
					{InstanceID: a.ID, CPUPct: ptrF64(30), ResidentBytes: ptrI64(100 * 1024 * 1024), InflightRequests: 1},
					{InstanceID: b.ID, CPUPct: ptrF64(75), ResidentBytes: ptrI64(50 * 1024 * 1024), InflightRequests: 4},
				},
			},
		},
	}
	m := wire.NewOpsMetrics("schedd")
	p := NewPoller(store, dialer, nil, NewReader(), m, nilLogger())

	if err := p.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// Reader: both rows present, deterministic (appID, instanceID)
	// order. Don't pin the per-row order — the Reader sorts by
	// InstanceID alphabetically, and newID() doesn't guarantee
	// `a` < `b` (each id is a fresh UUID). What we DO pin is the
	// set: both ids must be present and the gauge rollup must
	// collapse them.
	snap := p.Reader.SnapshotAll()
	if len(snap) != 2 {
		t.Fatalf("SnapshotAll len = %d, want 2", len(snap))
	}
	seen := map[string]bool{}
	for _, row := range snap {
		if row.AppID != "app1" || row.NodeID != live.ID {
			t.Errorf("row = %+v, want app1 on %s", row, live.ID)
		}
		seen[row.InstanceID] = true
	}
	if !seen[a.ID] || !seen[b.ID] {
		t.Errorf("SnapshotAll = %+v; missing one of [%s, %s]", seen, a.ID, b.ID)
	}
	// Wire rollup: max(30, 75)=75, sum(100, 50)=150, sum(1, 4)=5.
	body := scrapeMetrics(t, m)
	for _, want := range []string{
		`schedd_instance_cpu_pct{app="app1",node="` + live.ID + `"} 75`,
		`schedd_instance_rss_mb{app="app1",node="` + live.ID + `"} 150`,
		`schedd_instance_inflight_requests{app="app1",node="` + live.ID + `"} 5`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics body missing %q:\n%s", want, body)
		}
	}
}

// TestPoller_DurableLastRequestFallback pins the wire-vs-durable
// fallback for LastRequestAt: PR-A doesn't yet populate the wire
// (PR-B's ActivityTracker will), so the poller falls back to
// state.Instance.LastRequestAt — that field is updated by the
// existing per-request codepath (pkg/gateway stamps LastRequest
// on every forward). Without the fallback, LastRequestAt would
// be zero today, breaking future "instance is idle" derivations
// that #171 (reaper) and #169 (scale-up) will run.
func TestPoller_DurableLastRequestFallback(t *testing.T) {
	store := state.NewMemStore()
	_, live := seedTwoNodes(t, store)
	ins := seedInstance(t, store, "app1", live.ID)
	// Seed the durable last_request_at via the public Touch
	// surface (the same one the gateway uses every 15 s — spec
	// §4.1 last-seen flush).
	when := time.Now().Add(-3 * time.Second)
	if _, err := store.TouchInstancesLastSeen(context.Background(), []state.InstanceTouch{
		{InstanceID: ins.ID, LastRequest: when},
	}); err != nil {
		t.Fatalf("TouchInstancesLastSeen: %v", err)
	}

	// Wire snapshot has empty LastRequestAt — PR-A: vmmd does not
	// populate it yet. The poller must fall back to the durable
	// state we just stamped.
	dialer := &statsFakeDialer{
		stats: map[string]*sched.StatsSnapshot{
			live.TargetURL: {
				Instances: []sched.VMInstanceStat{
					{InstanceID: ins.ID, ResidentBytes: ptrI64(64 * 1024 * 1024)},
				},
			},
		},
	}
	p := NewPoller(store, dialer, nil, NewReader(), nil, nilLogger())

	if err := p.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	snap := p.Reader.SnapshotAll()
	if len(snap) != 1 {
		t.Fatalf("SnapshotAll len = %d, want 1", len(snap))
	}
	// Allow a tiny epsilon — SetInstanceRuntime stamps with the
	// current wall clock at the time of the call, which can
	// advance a few µs before the durable row lands.
	if d := snap[0].LastRequestAt.Sub(when); d < -time.Millisecond || d > time.Millisecond {
		t.Errorf("row.LastRequestAt = %v, want ~%v (durable fallback)", snap[0].LastRequestAt, when)
	}
}

// TestPoller_WireLastRequestWins pins the wire-vs-durable
// tiebreaker: when the wire carries a non-zero LastRequestAt,
// the poller uses that. PR-B's ActivityTracker will populate the
// wire; PR-A still respects it so a wire-warmed-up fleet
// seamlessly upgrades without re-architecting the poller.
func TestPoller_WireLastRequestWins(t *testing.T) {
	store := state.NewMemStore()
	_, live := seedTwoNodes(t, store)
	ins := seedInstance(t, store, "app1", live.ID)
	durableWhen := time.Now().Add(-30 * time.Second)
	if _, err := store.TouchInstancesLastSeen(context.Background(), []state.InstanceTouch{
		{InstanceID: ins.ID, LastRequest: durableWhen},
	}); err != nil {
		t.Fatalf("TouchInstancesLastSeen: %v", err)
	}
	wireWhen := time.Now().Add(-1 * time.Second)

	dialer := &statsFakeDialer{
		stats: map[string]*sched.StatsSnapshot{
			live.TargetURL: {
				Instances: []sched.VMInstanceStat{
					{InstanceID: ins.ID, ResidentBytes: ptrI64(64 * 1024 * 1024), LastRequestAt: wireWhen},
				},
			},
		},
	}
	p := NewPoller(store, dialer, nil, NewReader(), nil, nilLogger())

	if err := p.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	snap := p.Reader.SnapshotAll()
	if len(snap) != 1 {
		t.Fatalf("SnapshotAll len = %d, want 1", len(snap))
	}
	// Wire wins: must equal wireWhen, not durableWhen.
	if !snap[0].LastRequestAt.Equal(wireWhen) {
		t.Errorf("row.LastRequestAt = %v, want %v (wire wins)", snap[0].LastRequestAt, wireWhen)
	}
}

// TestPoller_FreshDialPerTick is the issue #120 invariant for the
// stats poller: every Tick pays the dial cost once per active
// node. A regression that routes through the router cache would
// drop the dial count even though the underlying Stats still
// succeeds. Two ticks ⇒ two dial waves, even with no state
// change in between.
func TestPoller_FreshDialPerTick(t *testing.T) {
	store := state.NewMemStore()
	dialer := &statsFakeDialer{}
	p := NewPoller(store, dialer, nil, NewReader(), wire.NewOpsMetrics("schedd"), nilLogger())

	for i := 0; i < 3; i++ {
		if err := p.Tick(context.Background()); err != nil {
			t.Fatalf("Tick %d: %v", i, err)
		}
	}
	if got := len(dialer.dials); got != 3 {
		t.Errorf("Dial calls after 3 ticks = %d, want 3 (one per tick per active node)", got)
	}
	if got := dialer.closed; got != 3 {
		t.Errorf("Close calls after 3 ticks = %d, want 3 (no leaked clients)", got)
	}
}

// TestPoller_UnknownInstanceFiltered pins the join-with-durable-
// state invariant: a wire entry for an instance id we have no
// state.Instance for (e.g. concurrent destroy) MUST be skipped —
// publishing it would land a row with empty AppID in the rollup
// and silently corrupt dashboards.
func TestPoller_UnknownInstanceFiltered(t *testing.T) {
	store := state.NewMemStore()
	_, live := seedTwoNodes(t, store)
	known := seedInstance(t, store, "app1", live.ID)

	dialer := &statsFakeDialer{
		stats: map[string]*sched.StatsSnapshot{
			live.TargetURL: {
				Instances: []sched.VMInstanceStat{
					{InstanceID: known.ID, ResidentBytes: ptrI64(64 * 1024 * 1024)},
					{InstanceID: "ghost-id", ResidentBytes: ptrI64(64 * 1024 * 1024)}, // not in store
					{InstanceID: "", ResidentBytes: ptrI64(64 * 1024 * 1024)},         // empty id
				},
			},
		},
	}
	p := NewPoller(store, dialer, nil, NewReader(), nil, nilLogger())

	if err := p.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	snap := p.Reader.SnapshotAll()
	if len(snap) != 1 {
		t.Fatalf("SnapshotAll len = %d, want 1 (only known instance)", len(snap))
	}
	if snap[0].InstanceID != known.ID {
		t.Errorf("row.InstanceID = %s, want %s", snap[0].InstanceID, known.ID)
	}
}

// TestPoller_TableDriven wraps the canonical scenarios into the
// table the package convention prefers (CLAUDE.md: table-driven
// tests). The dedicated tests above are the diagnostic surface
// when one breaks.
func TestPoller_TableDriven(t *testing.T) {
	t.Run("first sample CPU=Unknown", func(t *testing.T) {
		store := state.NewMemStore()
		_, live := seedTwoNodes(t, store)
		ins := seedInstance(t, store, "app1", live.ID)
		dialer := &statsFakeDialer{
			stats: map[string]*sched.StatsSnapshot{
				live.TargetURL: {Instances: []sched.VMInstanceStat{
					{InstanceID: ins.ID, ResidentBytes: ptrI64(128 * 1024 * 1024)},
				}},
			},
		}
		p := NewPoller(store, dialer, nil, NewReader(), wire.NewOpsMetrics("schedd"), nilLogger())
		if err := p.Tick(context.Background()); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		row := p.Reader.SnapshotAll()
		if row[0].CPU != Unknown || row[0].RSS != Valid || row[0].RSSMB != 128 {
			t.Errorf("row = %+v, want CPU=Unknown RSS=Valid RSSMB=128", row[0])
		}
	})
	t.Run("partial node failure continues", func(t *testing.T) {
		store := state.NewMemStore()
		dead, live := seedTwoNodes(t, store)
		ins := seedInstance(t, store, "app1", live.ID)
		dialer := &statsFakeDialer{
			dialErr: map[string]error{dead.TargetURL: errors.New("dead")},
			stats: map[string]*sched.StatsSnapshot{
				live.TargetURL: {Instances: []sched.VMInstanceStat{
					{InstanceID: ins.ID, ResidentBytes: ptrI64(64 * 1024 * 1024)},
				}},
			},
		}
		m := wire.NewOpsMetrics("schedd")
		p := NewPoller(store, dialer, nil, NewReader(), m, nilLogger())
		if err := p.Tick(context.Background()); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		// Live node's row lands, dead node's row does not.
		row := p.Reader.SnapshotAll()
		if len(row) != 1 || row[0].AppID != "app1" {
			t.Errorf("rows = %+v, want 1 row for app1", row)
		}
		// Partial-error counter bumped for the dead node.
		body := scrapeMetrics(t, m)
		if !strings.Contains(body, `schedd_instance_stats_partial_errors_total{node="`+dead.ID+`"} 1`) {
			t.Errorf("partial-error counter missing")
		}
	})
	t.Run("durable last-request fallback", func(t *testing.T) {
		store := state.NewMemStore()
		_, live := seedTwoNodes(t, store)
		ins := seedInstance(t, store, "app1", live.ID)
		when := time.Now().Add(-3 * time.Second)
		if _, err := store.TouchInstancesLastSeen(context.Background(), []state.InstanceTouch{
			{InstanceID: ins.ID, LastRequest: when},
		}); err != nil {
			t.Fatalf("TouchInstancesLastSeen: %v", err)
		}
		dialer := &statsFakeDialer{
			stats: map[string]*sched.StatsSnapshot{
				live.TargetURL: {Instances: []sched.VMInstanceStat{
					{InstanceID: ins.ID, ResidentBytes: ptrI64(64 * 1024 * 1024)},
				}},
			},
		}
		p := NewPoller(store, dialer, nil, NewReader(), nil, nilLogger())
		if err := p.Tick(context.Background()); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		row := p.Reader.SnapshotAll()
		if d := row[0].LastRequestAt.Sub(when); d < -time.Millisecond || d > time.Millisecond {
			t.Errorf("LastRequestAt = %v, want ~%v (durable fallback)", row[0].LastRequestAt, when)
		}
	})
	t.Run("unknown instance filtered", func(t *testing.T) {
		store := state.NewMemStore()
		_, live := seedTwoNodes(t, store)
		known := seedInstance(t, store, "app1", live.ID)
		dialer := &statsFakeDialer{
			stats: map[string]*sched.StatsSnapshot{
				live.TargetURL: {Instances: []sched.VMInstanceStat{
					{InstanceID: known.ID, ResidentBytes: ptrI64(64 * 1024 * 1024)},
					{InstanceID: "ghost", ResidentBytes: ptrI64(64 * 1024 * 1024)},
				}},
			},
		}
		p := NewPoller(store, dialer, nil, NewReader(), nil, nilLogger())
		if err := p.Tick(context.Background()); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		row := p.Reader.SnapshotAll()
		if len(row) != 1 || row[0].InstanceID != known.ID {
			t.Errorf("rows = %+v, want 1 row for known instance", row)
		}
	})
}

// TestPoller_KeepsRowOnAllUnknown pins that the poller does NOT
// drop a row when the wire returns nil for both CPUPct and
// ResidentBytes. The Reader's contract is "a snapshot per live
// instance, even if every metric is Unknown" — the scale-up /
// reaper code (#169 / #171) needs the row to know the instance
// exists and to skip it cleanly when its metrics are absent.
// Dropping it would create a phantom scale-up signal ("instance
// disappeared") on the very first tick.
func TestPoller_KeepsRowOnAllUnknown(t *testing.T) {
	store := state.NewMemStore()
	_, live := seedTwoNodes(t, store)
	ins := seedInstance(t, store, "app1", live.ID)

	dialer := &statsFakeDialer{
		stats: map[string]*sched.StatsSnapshot{
			live.TargetURL: {
				Instances: []sched.VMInstanceStat{
					// Both nil → both Unknown.
					{InstanceID: ins.ID},
				},
			},
		},
	}
	p := NewPoller(store, dialer, nil, NewReader(), nil, nilLogger())
	if err := p.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	row := p.Reader.SnapshotAll()
	if len(row) != 1 {
		t.Fatalf("SnapshotAll len = %d, want 1 (poller must keep all-unknown rows)", len(row))
	}
	if row[0].CPU != Unknown || row[0].RSS != Unknown {
		t.Errorf("row = %+v, want CPU=RSS=Unknown", row[0])
	}
}

// TestPoller_WireRollupExcludesNaN pins that when the poller
// hands a row with all-Unknown metrics to the wire side, the
// Prometheus scrape body never contains the literal "NaN".
// The wire rollup drops NaN before max/sum so the gauge
// encoder never sees it; a regression here would surface as a
// scrape that breaks Prometheus parsers (NaN in OpenMetrics is
// permitted but most production scrapers refuse it).
func TestPoller_WireRollupExcludesNaN(t *testing.T) {
	store := state.NewMemStore()
	_, live := seedTwoNodes(t, store)
	ins := seedInstance(t, store, "app1", live.ID)

	dialer := &statsFakeDialer{
		stats: map[string]*sched.StatsSnapshot{
			live.TargetURL: {
				Instances: []sched.VMInstanceStat{
					{InstanceID: ins.ID},
				},
			},
		},
	}
	m := wire.NewOpsMetrics("schedd")
	p := NewPoller(store, dialer, nil, NewReader(), m, nilLogger())
	if err := p.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	body := scrapeMetrics(t, m)
	if strings.Contains(body, " NaN") {
		t.Errorf("metrics body contains a NaN sample:\n%s", body)
	}
}

// Compile-time guard: NaN is the sentinel; math.NaN must be the
// canonical source. Pinning this in the test file prevents a
// future refactor from importing a stray third-party NaN helper
// that compares unequal.
func TestPoller_NaNSentinel(t *testing.T) {
	v := math.NaN()
	if !math.IsNaN(v) {
		t.Error("math.NaN is not NaN — sentinel broken")
	}
}

// TestPoller_DecodesCpuThrottledSecondsIntoWireRow pins the
// end-to-end contract for issue #301 acceptance #4 — schedd must
// decode InstanceStats.CpuThrottledSeconds into
// wire.InstanceStatRow.ThrottledUsec. Without this decode
// (PR #390 review finding #2, ship-blocker) the wire row reaches
// ReplaceInstanceStats with ThrottledUsec=NaN, the per-(account_id,
// app_id) baseline is never updated, the top-N admission primitive
// never sees the throttle dimension, and the top-throttled-apps
// dashboard panel is permanently empty in production.
//
// The schedd-side poller is the bridge that takes the wire field
// (vmmd populates it from cgroupstats.Sample) and hands it to
// wire.OpsMetrics.ReplaceInstanceStats. This test exercises that
// bridge by hooking the wire rollup via Metrics and asserting the
// ReplaceInstanceStats sees a non-NaN ThrottledUsec for the row.
//
// Note: the actual counter emission (vmmd_cpu_throttle_seconds_total)
// lives in vmmd's per-tick Sampler via EmitTopAppThrottle — that
// is a vmmd-side concern, not a schedd-side one. This test
// verifies the schedd-side contribution: the wire row's
// ThrottledUsec is populated, the per-(account_id, app_id)
// baseline in throttleSecondsLastSeen is updated, and the
// topAppSet sample is bumped (so the vmmd-side top-N ranking
// will admit the (account_id, app_id) tuple).
func TestPoller_DecodesCpuThrottledSecondsIntoWireRow(t *testing.T) {
	store := state.NewMemStore()
	_, live := seedTwoNodes(t, store)
	ins := seedInstance(t, store, "app1", live.ID)

	dialer := &statsFakeDialer{
		stats: map[string]*sched.StatsSnapshot{
			live.TargetURL: {
				Instances: []sched.VMInstanceStat{
					{
						InstanceID:          ins.ID,
						CpuThrottledSeconds: ptrF64(4.5), // 4.5s cumulative
					},
				},
			},
		},
	}
	m := wire.NewOpsMetrics("schedd")
	p := NewPoller(store, dialer, nil, NewReader(), m, nilLogger())
	if err := p.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	// The wire rollup must have updated throttleSecondsLastSeen
	// for the (anonymous, app1) tuple — query the baseline via
	// the (currently package-private) accessor or, more
	// defensively, scrape the metrics and verify the
	// pre-instantiated ("other", "other") overflow row has not
	// been touched. The load-bearing signal is that the
	// topAppSet sample was bumped, which we observe via the
	// TopAppSet() test seam: the rolling count for (anonymous,
	// app1) is > 0.
	ts := m.TopAppSet()
	if ts == nil {
		t.Fatal("TopAppSet() returned nil; the primitive should be non-nil on a freshly-constructed OpsMetrics")
	}
	snap := ts.SnapshotAppCounts()
	// topAppSet is keyed (accountID, appID); the poller
	// populates accountID from the wire row's AccountID which
	// is empty (the MemStore-seeded instance has no owner),
	// collapsed to "anonymous" by the rollup's accountLabel
	// path. The composite key is therefore ("anonymous",
	// "app1"). Single-row admission under cap=100.
	key := m.AppKeyForTest("anonymous", "app1")
	count, ok := snap[key]
	if !ok || count == 0 {
		t.Errorf("topAppSet sample for (anonymous, app1) = %d (present=%v); want count > 0; full snapshot: %+v", count, ok, snap)
	}
	// Also verify the per-pair baseline (throttleSecondsLastSeen)
	// was updated — query the test seam. The baseline must
	// reflect the wire value (4.5s = 4,500,000 usec).
	if seen := m.ThrottleSecondsLastSeenForTest("anonymous", "app1"); seen != 4_500_000 {
		t.Errorf("throttleSecondsLastSeen for (anonymous, app1) = %g; want 4,500,000 (4.5s × 1e6)", seen)
	}
}

// TestPoller_CpuThrottledSecondsNilLeavesThrottledUsecAtNaN pins
// the nil-on-wire contract: vmmd has no baseline for the
// instance's cgroup yet (first sample, regression, non-Linux
// host) and the wire sends a nil *float64. The poller must leave
// ThrottledUsec at NaN so the rollup's regression-handling
// branch excludes the row (matches the CPUSeconds contract).
func TestPoller_CpuThrottledSecondsNilLeavesThrottledUsecAtNaN(t *testing.T) {
	store := state.NewMemStore()
	_, live := seedTwoNodes(t, store)
	ins := seedInstance(t, store, "app1", live.ID)

	dialer := &statsFakeDialer{
		stats: map[string]*sched.StatsSnapshot{
			live.TargetURL: {
				Instances: []sched.VMInstanceStat{
					{InstanceID: ins.ID}, // CpuThrottledSeconds nil
				},
			},
		},
	}
	m := wire.NewOpsMetrics("schedd")
	p := NewPoller(store, dialer, nil, NewReader(), m, nilLogger())
	if err := p.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	// The (anonymous, app1) tuple must NOT have been admitted
	// to topAppSet because the wire value is NaN (the
	// rollup's "if math.IsNaN(r.ThrottledUsec) { continue }"
	// guard skips the row). The other bucket pre-instantiation
	// is unaffected.
	ts := m.TopAppSet()
	if ts == nil {
		t.Fatal("TopAppSet() returned nil")
	}
	snap := ts.SnapshotAppCounts()
	if _, ok := snap[m.AppKeyForTest("anonymous", "app1")]; ok {
		t.Errorf("topAppSet admitted (anonymous, app1) for nil CpuThrottledSeconds; full snapshot: %+v", snap)
	}
	// The baseline must NOT have been updated — the rollup's
	// NaN guard skipped the row.
	if seen := m.ThrottleSecondsLastSeenForTest("anonymous", "app1"); seen != 0 {
		t.Errorf("throttleSecondsLastSeen for (anonymous, app1) = %g; want 0 (nil contract — NaN guard must skip)", seen)
	}
}

func TestPoller_PersistentRequestRateSurvivesCachedTicksAndExpires(t *testing.T) {
	store := state.NewMemStore()
	_, node := seedTwoNodes(t, store)
	ins := seedInstance(t, store, "app1", node.ID)
	cache := sched.NewNodeTelemetryCache()
	now := time.Unix(500, 0)
	p := NewPoller(store, &statsFakeDialer{}, nil, NewReader(), nil, nilLogger()).WithTelemetry(cache)
	p.Now = func() time.Time { return now }
	publish := func(count *int64) {
		cache.Replace(node.ID, now, now, []sched.NodeTelemetry{{InstanceID: ins.ID, RequestCountTotal: count}})
	}
	tick := func(want float64, valid bool) {
		t.Helper()
		if err := p.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got, ok := p.Reader.RequestsPerSecond("app1"); got != want || ok != valid {
			t.Fatalf("rate=(%v,%v), want (%v,%v)", got, ok, want, valid)
		}
	}
	count := int64(0)
	publish(&count)
	tick(0, false)
	now = now.Add(time.Second)
	count = 140
	publish(&count)
	tick(140, true)
	for range 4 {
		now = now.Add(200 * time.Millisecond)
		tick(140, true)
	}
	now = now.Add(200 * time.Millisecond)
	publish(&count)
	tick(0, true)
	now = now.Add(sched.TelemetryFreshness + time.Nanosecond)
	tick(0, false)
	now = now.Add(time.Second)
	count = 500
	publish(&count)
	tick(0, false)
	now = now.Add(time.Second)
	publish(nil)
	tick(0, false)
	now = now.Add(time.Second)
	count = -1
	publish(&count)
	tick(0, false)
}
