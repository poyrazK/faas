//go:build linux && metal

// spec: §14.A
// adr: 1529
// adr: 1530
package m9

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

type nativeRuntime struct {
	t          *testing.T
	config     Config
	pool       *pgxpool.Pool
	store      *state.PgStore
	source     nativePeer
	dest       nativePeer
	engines    map[string]*sched.Engine
	instance   string
	cleaned    bool
	fault      atomic.Pointer[AckFault]
	adoptMu    sync.Mutex
	adoptions  map[string]*vmmdpb.AdoptMigratedInstanceResponse
	ownedWakes map[string]bool
}

func newRuntime(t *testing.T, ctx context.Context, c Config) *nativeRuntime {
	t.Helper()
	r := &nativeRuntime{t: t, config: c, engines: map[string]*sched.Engine{},
		adoptions: map[string]*vmmdpb.AdoptMigratedInstanceResponse{}, ownedWakes: map[string]bool{}}
	runID := os.Getenv("FAAS_M9_RUN_ID")
	r.source = nativePeer{config: c.Source, runID: runID}
	r.dest = nativePeer{config: c.Destination, runID: runID}
	t.Cleanup(r.cleanup)
	bundle := map[string]string{}
	for _, name := range []string{"leakcheck.sh", "leakcheck_native_loops.py"} {
		body, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "scripts", name))
		require(t, err)
		bundle[name] = string(body)
	}
	r.source.leaks, r.dest.leaks = bundle, bundle
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Fatal("native M9 requires DATABASE_URL; the qualification gate never creates or migrates a database")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal("native M9 database configuration is invalid")
	}
	r.pool, r.store = pool, state.NewPgStore(pool)
	if err := pool.Ping(ctx); err != nil {
		t.Fatal("native M9 database is unavailable")
	}
	return r
}

func (r *nativeRuntime) interceptor(p *nativePeer, source bool) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		observed := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
			if err := invoke(ctx, method, req, reply, cc, opts...); err != nil {
				return err
			}
			request, ours := req.(*vmmdpb.AdoptMigratedInstanceRequest)
			if method == vmmdpb.Vmmd_AdoptMigratedInstance_FullMethodName && ours && request.GetInstanceId() == r.instance {
				if response, ok := reply.(*vmmdpb.AdoptMigratedInstanceResponse); ok {
					r.adoptMu.Lock()
					r.adoptions[p.nodeID] = proto.Clone(response).(*vmmdpb.AdoptMigratedInstanceResponse)
					r.adoptMu.Unlock()
				}
			}
			return nil
		}
		if fault := r.fault.Load(); fault != nil {
			if source {
				return fault.Source(ctx, method, req, reply, cc, observed, opts...)
			}
			return fault.Destination(ctx, method, req, reply, cc, observed, opts...)
		}
		return observed(ctx, method, req, reply, cc, opts...)
	}
}

func (r *nativeRuntime) preflight(t *testing.T, ctx context.Context) map[string]any {
	peers := map[string]any{}
	boots := map[string]bool{}
	fcVersion := ""
	for _, p := range []*nativePeer{&r.source, &r.dest} {
		evidence, err := p.action(ctx, "claim")
		require(t, err)
		p.claimed = true
		boot, ok := evidence["boot_id"].(string)
		if !ok || !CanonicalUUID(boot) || boots[boot] {
			t.Fatal("native M9 requires two distinct, attested host boot identities")
		}
		boots[boot] = true
		node, err := r.store.ComputeNodeByName(ctx, p.config.Name)
		require(t, err)
		if !node.Lifecycle.IsAdmitting() || !node.Active || node.LifecycleGeneration <= 0 {
			t.Fatal("native M9 peers must already be admitting; the gate never activates nodes")
		}
		p.nodeID = node.ID
		require(t, p.connect(ctx, r.interceptor(p, p == &r.source)))
		identity, err := p.client.RuntimeAdmissionIdentity(ctx, &vmmdpb.RuntimeAdmissionIdentityRequest{})
		require(t, err)
		if identity.GetNodeId() != node.ID || !CanonicalUUID(identity.GetIncarnation()) || identity.GetSnapshotRestoreVersion() == 0 {
			t.Fatal("native M9 requires registered runtime ownership and measured snapshot restore on each peer")
		}
		p.incarnation = identity.GetIncarnation()
		ping, err := p.client.Ping(ctx, &vmmdpb.PingRequest{})
		require(t, err)
		if ping.GetFcVersion() == "" || fcVersion != "" && fcVersion != ping.GetFcVersion() {
			t.Fatal("native M9 requires the same known Firecracker version on both peers")
		}
		fcVersion = ping.GetFcVersion()
		require(t, r.empty(ctx, p))
		leaks, err := p.action(ctx, "leaks")
		require(t, err)
		evidence["node_id"], evidence["runtime_incarnation"], evidence["firecracker_version"] = node.ID, identity.GetIncarnation(), fcVersion
		evidence["leaks_before"] = leaks
		peers[p.config.Name] = evidence
	}
	if r.source.nodeID == r.dest.nodeID {
		t.Fatal("native M9 peers resolve to the same compute node")
	}
	app, err := r.store.AppByID(ctx, r.config.AppID)
	require(t, err)
	dep, err := r.store.DeploymentByID(ctx, r.config.DeploymentID)
	require(t, err)
	// Request mode and a zero floor make explicit cleanup stable: no service
	// reconciler should recreate the fixture while the leak checks run.
	if app.NodeID != r.source.nodeID || dep.AppID != app.ID || dep.Status != state.DeployLive || len(dep.Sidecars) != 0 ||
		app.EffectiveMinInstances() != 0 || dep.EffectiveMinInstances() != 0 ||
		app.IdleTimeoutS < r.config.TimeoutSeconds ||
		app.Manifest.ExecutionMode != "" && app.Manifest.ExecutionMode != "request" {
		t.Fatal("native M9 requires a live, sidecar-free request deployment, zero floor and idle timeout covering the drill")
	}
	for _, ins := range mustInstances(t, ctx, r.store, app.ID) {
		if state.State(ins.State).CountsForRAM() {
			t.Fatal("native M9 fixture must be parked before qualification")
		}
	}
	router := sched.NewVMMRouter(nil, nil, nil).
		WithCloneWakeDispatchRecorder(r.store.RecordCloneCheckpointWakeDispatch).
		WithCloneMigrationAdoptionRecorder(r.store.RecordCloneCheckpointMigrationAdoption).
		WithCloneMigrationResumeRecorder(r.store.RecordCloneCheckpointMigrationResume)
	for _, p := range []*nativePeer{&r.source, &r.dest} {
		router.SetClient(p.nodeID, sched.NewVMMClient(p.conn))
	}
	for _, p := range []*nativePeer{&r.source, &r.dest} {
		engine, err := sched.NewEngine(ctx, r.store, sched.NewNodeLedger(), router,
			sched.PoolNotifier{Pool: r.pool}, fcVersion, slog.New(slog.NewTextHandler(io.Discard, nil)))
		require(t, err)
		r.engines[p.nodeID] = engine.WithOwnerNodeID(p.nodeID)
	}
	return map[string]any{"peers": peers, "app_id": app.ID, "deployment_id": dep.ID, "rootfs_key": dep.RootfsKey}
}

func (r *nativeRuntime) empty(ctx context.Context, p *nativePeer) error {
	stats, err := p.client.Stats(ctx, &vmmdpb.StatsRequest{})
	if err != nil {
		return err
	}
	ram, err := r.store.ComputeNodeUsedMB(ctx, p.nodeID)
	if err != nil {
		return err
	}
	cpu, err := r.store.ComputeNodeUsedCPUMillicoresByNode(ctx, []string{p.nodeID})
	if err != nil {
		return err
	}
	leases, err := r.store.ListMigrationSourceLeasesForRecovery(ctx, p.nodeID, time.Now(), "", 1)
	if err != nil {
		return err
	}
	if stats.GetLiveCount() != 0 || stats.GetLeasedCount() != 0 || len(stats.GetInstances()) != 0 ||
		ram != 0 || cpu[p.nodeID] != 0 || len(leases) != 0 {
		return errors.New("native M9 peer has resident work, retained capacity or source leases; drain and reconcile it before the drill")
	}
	return nil
}

func (r *nativeRuntime) waitEmpty(ctx context.Context, p *nativePeer) error {
	waitCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for {
		err := r.empty(waitCtx, p)
		if err == nil {
			return nil
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("M9 peer did not release its original resources and source leases: %w", err)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (r *nativeRuntime) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for _, p := range []*nativePeer{&r.source, &r.dest} {
		if p.claimed {
			if _, err := p.action(ctx, "restore"); err != nil {
				r.t.Errorf("restore M9 peer: %v", err)
			}
		}
	}
	if !r.cleaned && r.instance != "" && r.store != nil {
		if err := r.parkOwned(ctx); err != nil {
			r.t.Errorf("retain uncertain M9 fixture for inspection: %v", err)
		}
	}
	for _, p := range []*nativePeer{&r.source, &r.dest} {
		if err := p.close(); err != nil {
			r.t.Errorf("close M9 tunnel: %v", err)
		}
	}
	if r.pool != nil {
		r.pool.Close()
	}
	// The outer runner releases peer leases only after it has verified the
	// complete test event stream AND independent post-run peer leak checks.
}

func (r *nativeRuntime) parkOwned(ctx context.Context) error {
	ins, err := r.store.InstanceByID(ctx, r.instance)
	if err != nil {
		return err
	}
	if fault := r.fault.Load(); fault != nil {
		_, wake := fault.DestinationIdentity()
		if wake != "" {
			r.ownedWakes[wake] = true
		}
	}
	r.adoptMu.Lock()
	for _, adopted := range r.adoptions {
		if CanonicalUUID(adopted.GetWakeId()) {
			r.ownedWakes[adopted.GetWakeId()] = true
		}
	}
	r.adoptMu.Unlock()
	if ins.AppID != r.config.AppID || ins.DeploymentID != r.config.DeploymentID || !r.ownedWakes[ins.WakeID] {
		return errors.New("fixture ownership changed outside this drill; refusing generic destruction")
	}
	if !state.State(ins.State).CountsForRAM() {
		return nil
	}
	engine := r.engines[ins.NodeID]
	if engine == nil || ins.State != string(state.StateRunning) {
		return errors.New("fixture handoff remains unresolved; preserve native and database evidence")
	}
	return engine.Park(ctx, ins.ID)
}

func mustInstances(t *testing.T, ctx context.Context, store *state.PgStore, appID string) []state.Instance {
	t.Helper()
	rows, err := store.ListInstancesForApp(ctx, appID)
	require(t, err)
	return rows
}

func require(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func oneOwner(ctx context.Context, r *nativeRuntime, nodeID, wake string) (state.Instance, error) {
	rows, err := r.store.ListInstancesForApp(ctx, r.config.AppID)
	if err != nil {
		return state.Instance{}, err
	}
	var owner state.Instance
	resident := 0
	for _, ins := range rows {
		if state.State(ins.State).CountsForRAM() {
			resident++
			owner = ins
		}
	}
	if resident != 1 || owner.ID != r.instance || owner.NodeID != nodeID || owner.WakeID != wake ||
		owner.DeploymentID != r.config.DeploymentID || owner.State != string(state.StateRunning) {
		return state.Instance{}, fmt.Errorf("M9 requires one expected serving owner; found %d resident rows", resident)
	}
	return owner, nil
}

func emitProof(t *testing.T, phase string, details any) {
	t.Helper()
	record := map[string]any{"version": 1, "run_id": os.Getenv("FAAS_M9_RUN_ID"),
		"revision": os.Getenv("FAAS_M9_REVISION"), "phase": phase, "details": details}
	data, err := json.Marshal(record)
	require(t, err)
	t.Log("M9_EVIDENCE " + string(data))
}
