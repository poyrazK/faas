// adr: 1151 — counts the Postgres round trips a thaw makes, statement by
// statement, against a real database.
package sched

import (
	"context"
	"slices"
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
	// pids is the backend that ran each statement, which tells one
	// transaction's statements from concurrent ones.
	pids []uint32
}

type statementIndexKey struct{}

func (s *statementTracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !s.armed.Load() {
		return ctx
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.names = append(s.names, statementName(data.SQL))
	s.intervals = append(s.intervals, [2]time.Time{time.Now()})
	s.pids = append(s.pids, conn.PgConn().PID())
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

// transactionOf returns the statements of the transaction that ran the
// statement named name: those on its backend from the begin before it to
// the commit or rollback after it.
func (s *statementTracer) transactionOf(name string) map[int]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	at := slices.Index(s.names, name)
	if at < 0 {
		return nil
	}
	pid, first, last := s.pids[at], at, at
	for i := at; i >= 0; i-- {
		if s.pids[i] == pid {
			first = i
			if s.names[i] == "begin" {
				break
			}
		}
	}
	for i := at; i < len(s.names); i++ {
		if s.pids[i] == pid {
			last = i
			if s.names[i] == "commit" || s.names[i] == "rollback" {
				break
			}
		}
	}
	tx := map[int]bool{}
	for i := first; i <= last; i++ {
		if s.pids[i] == pid {
			tx[i] = true
		}
	}
	return tx
}

// roundTripsWithin is sequentialRoundTrips over the statements that started
// at or after from and ended at or before to, leaving out the excluded ones.
func (s *statementTracer) roundTripsWithin(from, to time.Time, exclude map[int]bool) (statements, roundTrips int) {
	s.mu.Lock()
	var intervals [][2]time.Time
	for i, iv := range s.intervals {
		if exclude[i] {
			continue
		}
		if !iv[1].IsZero() && !iv[0].Before(from) && !iv[1].After(to) {
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
	return len(intervals), chain
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
func tracedPgStore(t *testing.T) (*state.PgStore, *pgxpool.Pool, *statementTracer) {
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
	return state.NewPgStore(pool), pool, tracer
}

// postgresFrozen is a published instance frozen in place on real Postgres,
// with an engine whose ledger holds its frozen charge.
type postgresFrozen struct {
	store  *state.PgStore
	pool   *pgxpool.Pool
	tracer *statementTracer
	e      *Engine
	vmm    *frozenFakeVMM
	app    state.App
	ins    state.Instance
}

func seedPostgresFrozen(t *testing.T) postgresFrozen {
	t.Helper()
	store, pool, tracer := tracedPgStore(t)
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
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(wire.NewOpsMetrics("schedd")).WithNotifyInPublication()
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
	return postgresFrozen{store: store, pool: pool, tracer: tracer, e: e, vmm: vmm, app: app, ins: ins}
}

func TestPostgresThawStatements(t *testing.T) {
	pf := seedPostgresFrozen(t)
	store, pool, tracer, e, vmm, app, ins := pf.store, pf.pool, pf.tracer, pf.e, pf.vmm, pf.app, pf.ins
	ctx := t.Context()
	listener, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Release()
	if _, err := listener.Exec(ctx, "listen "+db.NotifyInstanceChanged); err != nil {
		t.Fatal(err)
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
	// The transition event and the notification ride in the publication's
	// proof statement; no backoff is recorded, so none is cleared.
	for _, name := range []string{"insert into events (actor,", "SELECT pg_notify($1, $2)", "DeploymentClearSnapshotBackoff"} {
		if repeats[name] != 0 {
			t.Errorf("%q ran %d times after the publication, want none", name, repeats[name])
		}
	}
	wait, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	n, err := listener.Conn().WaitForNotification(wait)
	if err != nil || n.Payload != instanceChangedPayload(ins.ID, app.ID, state.StateRunning, ins.WakeID) {
		t.Fatalf("instance_changed = %+v, %v", n, err)
	}
	if events := runningTransitions(t, store, ins.ID, "frozen_thaw"); events != 1 {
		t.Fatalf("frozen_thaw events = %d, want 1", events)
	}
}

// ADR-1740: a ticketed thaw sends the runtime-values reads beside the vmmd
// call and then one guarded statement; none of the ordinary path's reads.
func TestPostgresTicketedThawStatements(t *testing.T) {
	pf := seedPostgresFrozen(t)
	ctx := t.Context()
	pf.e.EnableFrozenFastPath()
	pf.e.issueThawTicket(ctx, pf.ins, frozenChargeMB(pf.ins.RAMMB, 150))
	if _, ok := pf.e.thawTickets.byApp[pf.app.ID][pf.ins.ID]; !ok {
		t.Fatal("no thaw ticket for an eligible frozen VM")
	}
	listener, err := pf.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Release()
	if _, err := listener.Exec(ctx, "listen "+db.NotifyInstanceChanged); err != nil {
		t.Fatal(err)
	}

	pf.tracer.armed.Store(true)
	result, err := pf.e.Wake(ctx, pf.app.ID, "", "", "")
	answered := time.Now()
	pf.tracer.armed.Store(false)
	if err != nil || result.InstanceID != pf.ins.ID || pf.vmm.thaws != 1 {
		t.Fatalf("Wake: %+v, %v, thaws %d", result, err, pf.vmm.thaws)
	}
	var names []string
	for i, name := range pf.tracer.recorded() {
		pf.tracer.mu.Lock()
		ended := pf.tracer.intervals[i][1]
		pf.tracer.mu.Unlock()
		if !ended.IsZero() && !ended.After(answered) {
			names = append(names, name)
		}
	}
	t.Logf("ticketed thaw: %v, %d sequential round trips", names, pf.tracer.sequentialRoundTrips(answered))
	// The ticket carries the workload-retirement gate's verdict (ADR-1741):
	// the thaw is one statement guarded by the app's runtime generation.
	want := map[string]int{"ThawFrozenInstance": 1}
	got := map[string]int{}
	for _, name := range names {
		got[name]++
	}
	if len(got) != len(want) {
		t.Fatalf("statements before the answer = %v, want exactly %v", got, want)
	}
	for name, n := range want {
		if got[name] != n {
			t.Fatalf("statements before the answer = %v, want exactly %v", got, want)
		}
	}
	wait, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	n, err := listener.Conn().WaitForNotification(wait)
	if err != nil || n.Payload != instanceChangedPayload(pf.ins.ID, pf.app.ID, state.StateRunning, pf.ins.WakeID) {
		t.Fatalf("instance_changed = %+v, %v", n, err)
	}
	if events := runningTransitions(t, pf.store, pf.ins.ID, "frozen_thaw"); events != 1 {
		t.Fatalf("frozen_thaw events = %d, want 1", events)
	}
}

// The ordinary path's statements for a wake with no VM to reuse: a cold boot,
// and a restore from the deployment's snapshot. The baseline for ADR-1741.
func TestPostgresBootStatements(t *testing.T) {
	for _, tc := range []struct {
		name     string
		snapshot bool
		// ownerCache wakes once with schedd's owner cache on (ADR-1741),
		// parks, and traces the next wake, which finds the entry cached.
		ownerCache bool
	}{
		{name: "cold boot"},
		{name: "snapshot restore", snapshot: true},
		{name: "cold boot, owner cache warm", ownerCache: true},
		{name: "snapshot restore, owner cache warm", snapshot: true, ownerCache: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, _, tracer := tracedPgStore(t)
			ctx := t.Context()
			acct, err := store.CreateAccount(ctx, "boot-statements-"+uuid.NewString()+"@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "boot-" + uuid.NewString()[:8], Type: state.AppTypeFunction,
				RAMMB: 512, MaxConcurrency: 4, IdleTimeoutS: 30})
			if err != nil {
				t.Fatal(err)
			}
			dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
				ImageDigest: "sha256:boot", Status: state.DeployLive})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
				t.Fatal(err)
			}
			if tc.snapshot {
				if _, err := store.CreateSnapshot(ctx, state.Snapshot{DeploymentID: dep.ID, FCVersion: "1.10.0",
					// A snapshot whose memory differs from the app's RAM is
					// retired before admission and the wake cold boots.
					StorageKey: "snap/" + dep.ID + "/captures/c1/v2/mem", MemBytes: int64(app.RAMMB) << 20}); err != nil {
					t.Fatal(err)
				}
			}
			node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.HeartbeatComputeNode(ctx, node.ID); err != nil {
				t.Fatal(err)
			}
			// vmmd takes bootDuration, as a real restore or boot takes
			// hundreds of milliseconds; statements that run meanwhile (the
			// scale-out stamp) are not on the wake's critical path.
			const bootDuration = 150 * time.Millisecond
			vmm := &fakeVMM{}
			e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(wire.NewOpsMetrics("schedd")).WithNotifyInPublication()
			// cmd/schedd places from its notification-backed node registry.
			nodes, err := store.ActiveComputeNodes(ctx)
			if err != nil {
				t.Fatal(err)
			}
			e.WithNodeRegistry(NewNodeRegistry(nodes))
			// A compute node's schedd owns the apps placed on its node.
			if err := store.SetAppNodeID(ctx, app.ID, node.ID); err != nil {
				t.Fatal(err)
			}
			e.WithOwnerNodeID(node.ID)
			// vmmd streams live capacity to schedd; placement reads usage from
			// it rather than from Postgres.
			for _, n := range nodes {
				e.CapacityTable().Replace(CapacityReport{NodeID: n.ID, SampledAt: time.Now(), RAMHeadroomMB: int32(n.MemMB)})
			}
			if tc.ownerCache {
				e.WithOwnerCache()
				first, err := e.Wake(ctx, app.ID, "", "", "")
				if err != nil {
					t.Fatalf("first Wake: %v", err)
				}
				if err := e.Park(ctx, first.InstanceID); err != nil {
					t.Fatalf("Park: %v", err)
				}
				vmm.coldBoots, vmm.restores = 0, 0
			}

			var vmmStarted time.Time
			vmm.sleepFor = bootDuration
			vmm.restoreHook = func() { vmmStarted = time.Now() }
			vmm.coldBootHook = vmm.restoreHook
			tracer.armed.Store(true)
			wakeStarted := time.Now()
			result, err := e.Wake(ctx, app.ID, "", "", "")
			answered := time.Now()
			tracer.armed.Store(false)
			if err != nil || result.InstanceID == "" {
				t.Fatalf("Wake: %+v, %v", result, err)
			}
			names := tracer.recorded()
			before := 0
			for i, name := range names {
				tracer.mu.Lock()
				ended := tracer.intervals[i][1]
				tracer.mu.Unlock()
				if !ended.IsZero() && !ended.After(answered) {
					before++
					tracer.mu.Lock()
					started, pid := tracer.intervals[i][0], tracer.pids[i]
					tracer.mu.Unlock()
					t.Logf("%2d %-44s start %6.1f ms took %4.1f ms pid %d", before, name,
						float64(started.Sub(wakeStarted).Microseconds())/1000, float64(ended.Sub(started).Microseconds())/1000, pid)
				}
			}
			t.Logf("%s: %d statements before the answer, %d sequential round trips (cold boots %d, restores %d)",
				tc.name, before, tracer.sequentialRoundTrips(answered), vmm.coldBoots, vmm.restores)
			vmmEnded := vmmStarted.Add(bootDuration)
			// The scale-out stamp is its own transaction, started before the
			// boot and joined only before the publication; it runs beside the
			// wake, not on its path.
			stamp := tracer.transactionOf("WriteRuntimeScalingState")
			preStatements, preRTs := tracer.roundTripsWithin(wakeStarted, vmmStarted, stamp)
			postStatements, postRTs := tracer.roundTripsWithin(vmmEnded, answered, stamp)
			t.Logf("%s: critical path %d round trips: %d before vmmd (%d statements), %d after it (%d statements); %d statements beside it (the scale-out stamp's %d and any during the boot)",
				tc.name, preRTs+postRTs, preRTs, preStatements, postRTs, postStatements, before-preStatements-postStatements, len(stamp))
			if wantRestores := map[bool]int{true: 1}[tc.snapshot]; vmm.restores != wantRestores || vmm.coldBoots != 1-wantRestores {
				t.Fatalf("%s: cold boots %d, restores %d; want the wake to %s", tc.name, vmm.coldBoots, vmm.restores,
					map[bool]string{true: "restore the snapshot", false: "cold boot"}[tc.snapshot])
			}
		})
	}
}
