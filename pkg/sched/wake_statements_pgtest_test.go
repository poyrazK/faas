// adr: 1151 — counts the Postgres round trips a thaw makes, statement by
// statement, against a real database.
package sched

import (
	"context"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// statementTracer records the name of every statement while armed. sqlc
// statements carry their "-- name: X" comment; others are recorded by their
// first words.
type statementTracer struct {
	armed     atomic.Bool
	mu        sync.Mutex
	names     []string
	intervals [][2]time.Time
}

type statementIndexKey struct{}

func (s *statementTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !s.armed.Load() {
		return ctx
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.names = append(s.names, statementName(data.SQL))
	s.intervals = append(s.intervals, [2]time.Time{time.Now()})
	return context.WithValue(ctx, statementIndexKey{}, len(s.intervals)-1)
}

func (s *statementTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	i, ok := ctx.Value(statementIndexKey{}).(int)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.intervals[i][1] = time.Now()
}

// sequentialRoundTrips is the longest chain of statements that ran one after
// another: statements that overlapped in time count once. Statements that had
// not finished by answered (asynchronous work after the answer) are left out.
func (s *statementTracer) sequentialRoundTrips(answered time.Time) int {
	s.mu.Lock()
	var intervals [][2]time.Time
	for _, iv := range s.intervals {
		if !iv[1].IsZero() && !iv[1].After(answered) {
			intervals = append(intervals, iv)
		}
	}
	s.mu.Unlock()
	sort.Slice(intervals, func(i, j int) bool { return intervals[i][1].Before(intervals[j][1]) })
	chain, lastEnd := 0, time.Time{}
	for _, iv := range intervals {
		if !iv[0].Before(lastEnd) {
			chain++
			lastEnd = iv[1]
		}
	}
	return chain
}

func (s *statementTracer) recorded() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.names...)
}

func statementName(sql string) string {
	sql = strings.TrimSpace(sql)
	if rest, ok := strings.CutPrefix(sql, "-- name: "); ok {
		if name, _, found := strings.Cut(rest, " "); found {
			return name
		}
	}
	fields := strings.Fields(sql)
	if len(fields) > 4 {
		fields = fields[:4]
	}
	return strings.Join(fields, " ")
}

// tracedPgStore opens a migrated test database and returns a store whose
// statements tracer records while armed.
func tracedPgStore(t *testing.T) (*state.PgStore, *statementTracer) {
	t.Helper()
	base := pgtest.OpenMigrated(t)
	cfg := base.Config().Copy()
	tracer := &statementTracer{}
	cfg.ConnConfig.Tracer = tracer
	// Production pools keep idle connections; without them concurrent reads
	// would wait for new connections and look sequential.
	cfg.MinConns = 8
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("traced pool: %v", err)
	}
	for deadline := time.Now().Add(5 * time.Second); pool.Stat().IdleConns() < cfg.MinConns && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	t.Cleanup(pool.Close)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return state.NewPgStore(pool), tracer
}

func TestPostgresThawStatements(t *testing.T) {
	store, tracer := tracedPgStore(t)
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "thaw-statements-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "thaw-" + uuid.NewString()[:8], Type: state.AppTypeFunction,
		RAMMB: 512, MaxConcurrency: 4, IdleTimeoutS: 30})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:thaw", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSnapshot(ctx, state.Snapshot{DeploymentID: dep.ID, FCVersion: "1.10.0",
		StorageKey: "snap/" + dep.ID + "/captures/c1/v2/mem", MemBytes: 1 << 20}); err != nil {
		t.Fatal(err)
	}
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	ins, err := store.CreateInstanceWithMode(ctx, app.ID, dep.ID, string(state.StateWaking), app.RAMMB, node.ID, uuid.NewString(), string(state.InstanceModeNormal))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.RuntimeAppValuesForDeployment(ctx, app.AccountID, app.ID, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := state.NewRuntimeAppConfigFence(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if ins, err = store.PublishOwnedInstanceRuntime(ctx, state.RuntimeInstancePublication{
		AccountID: app.AccountID, AppID: app.ID, InstanceID: ins.ID, NodeID: ins.NodeID, WakeID: ins.WakeID,
		ExpectedState: string(state.StateWaking), Fence: fence.SecretFence, ConfigFence: fence,
		Netns: "fc-" + ins.ID, HostIP: "10.100.0.2", GuestUID: 20001,
	}); err != nil {
		t.Fatal(err)
	}
	vmm := &frozenFakeVMM{fakeVMM: &fakeVMM{}}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(wire.NewOpsMetrics("schedd"))
	limits := api.MustLimitsFor(api.PlanPro)
	if err := e.Ledger().Admit(Request{Instance: ins.ID, AppID: app.ID, DeploymentID: dep.ID, Plan: api.PlanPro,
		RAMMB: app.RAMMB, VCPU: limits.VCPU, CPUMillicores: 1000, MaxConcurrency: app.MaxConcurrency, NodeID: node.ID}); err != nil {
		t.Fatal(err)
	}
	e.EnableFrozenTier()
	// The thaw path needs only the frozen end state: the row in FROZEN and
	// the ledger's frozen charge. FreezeIdle's eligibility rules (snapshot
	// readiness and the rest) are covered by the MemStore fixtures.
	if err := updateInstanceStateCAS(ctx, store, ins.ID, string(state.StateRunning), string(state.StateFrozen)); err != nil {
		t.Fatalf("mark frozen: %v", err)
	}
	e.Ledger().Freeze(ins.ID, frozenChargeMB(ins.RAMMB, 150))
	// The template database seeded the node row when the package's first
	// Postgres test ran; a live vmmd keeps it fresh, or the thaw parks the VM
	// as unreachable.
	if err := store.HeartbeatComputeNode(ctx, node.ID); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}

	tracer.armed.Store(true)
	result, err := e.Wake(ctx, app.ID, "", "", "")
	answered := time.Now()
	tracer.armed.Store(false)
	names := tracer.recorded()
	tracer.mu.Lock()
	intervals := append([][2]time.Time(nil), tracer.intervals...)
	tracer.mu.Unlock()
	for i, name := range names {
		start := float64(intervals[i][0].Sub(intervals[0][0]).Microseconds()) / 1000
		if intervals[i][1].IsZero() || intervals[i][1].After(answered) {
			t.Logf("%2d %-40s start %5.1f ms  after the answer", i+1, name, start)
			continue
		}
		t.Logf("%2d %-40s start %5.1f ms  took %4.1f ms", i+1, name, start, float64(intervals[i][1].Sub(intervals[i][0]).Microseconds())/1000)
	}
	t.Logf("thaw: %d statements, %d sequential round trips", len(names), tracer.sequentialRoundTrips(answered))
	if err != nil || result.InstanceID != ins.ID || vmm.thaws != 1 {
		t.Fatalf("Wake: %+v, %v, thaws %d", result, err, vmm.thaws)
	}
	// Timing-based counts vary with load; the repeated reads this path used
	// to make are exact. Admission reuses the wake's app read.
	repeats := map[string]int{}
	for _, name := range names {
		repeats[name]++
	}
	for _, name := range []string{"ManagedPostgresAdmissionFenced", "GetApplicationStandardEnrollment", "select id, account_id, slug,"} {
		if repeats[name] != 1 {
			t.Errorf("%q ran %d times in one thaw, want once", name, repeats[name])
		}
	}
}
