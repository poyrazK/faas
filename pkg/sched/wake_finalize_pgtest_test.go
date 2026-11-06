// adr: 20261105090246
package sched_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
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
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// finalizeTracer counts the statements a wake starts after vmmd returned and
// can delay each one by a simulated control-plane round trip.
type finalizeTracer struct {
	armed  atomic.Bool
	delay  atomic.Int64 // nanoseconds before each armed statement
	mu     sync.Mutex
	starts []time.Time
}

func (f *finalizeTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	if !f.armed.Load() {
		return ctx
	}
	f.mu.Lock()
	f.starts = append(f.starts, time.Now())
	f.mu.Unlock()
	if d := time.Duration(f.delay.Load()); d > 0 {
		time.Sleep(d)
	}
	return ctx
}

func (f *finalizeTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (f *finalizeTracer) startedWithin(from, to time.Time) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, at := range f.starts {
		if !at.Before(from) && at.Before(to) {
			n++
		}
	}
	return n
}

// TestPostgresBootedWakeFinalize measures, on real Postgres, how long a boot
// whose deployment delivers secrets waits between vmmd returning and the wake
// answering, with its bookkeeping inline and after the answer. The simulated
// round trip stands in for a compute node's schedd talking to the control
// plane; these are local numbers, not production ones.
func TestPostgresBootedWakeFinalize(t *testing.T) {
	const (
		secrets      = 4
		iterations   = 5
		bootDuration = 50 * time.Millisecond
	)
	for _, tc := range []struct {
		name  string
		async bool
		rtt   time.Duration
	}{
		{name: "inline, local"},
		{name: "async, local", async: true},
		{name: "inline, 3ms round trip", rtt: 3 * time.Millisecond},
		{name: "async, 3ms round trip", async: true, rtt: 3 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			store, tracer := finalizePgStore(t)
			acct, app := seedFinalizeApp(t, store, secrets)
			vmm := &sched.FakeVMM{}
			e := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0").
				WithOpsMetrics(wire.NewOpsMetrics("schedd")).WithNotifyInPublication()
			placeOnLocalNode(t, store, e, app.ID)
			if tc.async {
				e.WithAsyncWakeFinalize()
			}
			var vmmStarted time.Time
			*vmm.SleepFor() = bootDuration
			*vmm.ColdBootHook() = func() { vmmStarted = time.Now() }
			tracer.delay.Store(int64(tc.rtt))

			afterVMM := make([]time.Duration, 0, iterations)
			statements := 0
			for i := range iterations {
				tracer.armed.Store(true)
				result, err := e.Wake(ctx, app.ID, "", "", "")
				answered := time.Now()
				tracer.armed.Store(false)
				if err != nil || result.InstanceID == "" {
					t.Fatalf("Wake %d: %+v, %v", i, result, err)
				}
				vmmEnded := vmmStarted.Add(bootDuration)
				afterVMM = append(afterVMM, answered.Sub(vmmEnded))
				statements = tracer.startedWithin(vmmEnded, answered)
				if err := e.WaitWakeFinalize(ctx); err != nil {
					t.Fatal(err)
				}
				for k := range secrets {
					row, err := store.GetAppSecret(ctx, acct.ID, app.ID, fmt.Sprintf("TOKEN_%d", k))
					if err != nil || row.DeliveryStatus != state.SecretDeliveryDelivered {
						t.Fatalf("wake %d: TOKEN_%d delivery %+v, %v", i, k, row, err)
					}
				}
				if err := e.Park(ctx, result.InstanceID); err != nil {
					t.Fatalf("Park %d: %v", i, err)
				}
				tracer.mu.Lock()
				tracer.starts = tracer.starts[:0]
				tracer.mu.Unlock()
			}
			slices.Sort(afterVMM)
			t.Logf("%s: vmmd return -> answer median %.1f ms (min %.1f, max %.1f) over %d wakes; %d statements after vmmd on the last",
				tc.name, finalizeMS(afterVMM[len(afterVMM)/2]), finalizeMS(afterVMM[0]), finalizeMS(afterVMM[len(afterVMM)-1]), iterations, statements)
		})
	}
}

func finalizeMS(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func finalizePgStore(t *testing.T) (*state.PgStore, *finalizeTracer) {
	t.Helper()
	base := pgtest.OpenMigrated(t)
	cfg := base.Config().Copy()
	tracer := &finalizeTracer{}
	cfg.ConnConfig.Tracer = tracer
	cfg.MinConns = 8
	cfg.MaxConns = db.DaemonMaxConnections["schedd"]
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("traced pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return state.NewPgStore(pool), tracer
}

func seedFinalizeApp(t *testing.T, store *state.PgStore, secrets int) (state.Account, state.App) {
	t.Helper()
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "finalize-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "finalize-" + uuid.NewString()[:8], Type: state.AppTypeFunction,
		RAMMB: 512, MaxConcurrency: 4, IdleTimeoutS: 30})
	if err != nil {
		t.Fatal(err)
	}
	refs := map[string]string{}
	for k := range secrets {
		key := fmt.Sprintf("TOKEN_%d", k)
		refs[key] = "secret:" + key
		if err := store.UpsertAppSecret(ctx, acct.ID, app.ID, key, []byte(key+"-sealed")); err != nil {
			t.Fatal(err)
		}
	}
	grants, err := json.Marshal(refs)
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:finalize", Status: state.DeployLive, OverrideEnvSecrets: grants})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	return acct, app
}

// placeOnLocalNode makes e the owning schedd of the default node and places
// from its registry and live capacity reports, as cmd/schedd does.
func placeOnLocalNode(t *testing.T, store *state.PgStore, e *sched.Engine, appID string) {
	t.Helper()
	ctx := t.Context()
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.HeartbeatComputeNode(ctx, node.ID); err != nil {
		t.Fatal(err)
	}
	nodes, err := store.ActiveComputeNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	e.WithNodeRegistry(sched.NewNodeRegistry(nodes))
	if err := store.SetAppNodeID(ctx, appID, node.ID); err != nil {
		t.Fatal(err)
	}
	e.WithOwnerNodeID(node.ID)
	for _, n := range nodes {
		e.CapacityTable().Replace(sched.CapacityReport{NodeID: n.ID, SampledAt: time.Now(), RAMHeadroomMB: int32(n.MemMB)})
	}
}
