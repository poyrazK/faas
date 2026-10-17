//go:build metal && function_background_native

package e2e_test

// adr: 1352

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/e2etest/functionmigration"
	"github.com/onebox-faas/faas/pkg/e2etest/functionsql"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"github.com/onebox-faas/faas/pkg/wire"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
)

type nativeDestinationGate struct {
	store   *state.PgStore
	ready   chan state.FunctionMigrationDestination
	release chan struct{}
	once    sync.Once
	call    *nativeAdoptionCall
}

func (g *nativeDestinationGate) intercept(ctx context.Context, method string, req, reply any, conn *grpc.ClientConn, invoke grpc.UnaryInvoker, options ...grpc.CallOption) error {
	if err := invoke(ctx, method, req, reply, conn, options...); err != nil {
		return err
	}
	r, ok := req.(*vmmdpb.AdoptMigratedInstanceRequest)
	if !ok {
		return nil
	}
	d, err := g.store.GetFunctionMigrationDestination(ctx, r.GetInstanceId(), r.GetLeaseToken())
	if err != nil {
		return err
	}
	if d.Phase != state.FunctionDestinationReady {
		return nil // Committed activation is a separate RPC without a callback.
	}
	call, err := captureNativeAdoption(ctx, method, req, reply, conn, invoke, options)
	if err != nil {
		return err
	}
	g.call = call // Published to the observer by the ready channel below.
	select {
	case g.ready <- d:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-g.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *nativeDestinationGate) open() { g.once.Do(func() { close(g.release) }) }

type nativeMigrationMetrics struct{ counter prometheus.Counter }

func (m nativeMigrationMetrics) LiveMigrationDecisions(string) prometheus.Counter { return m.counter }

func nativeMigrationRouter(t *testing.T, ctx context.Context, c functionmigration.Config, gate *nativeDestinationGate) (*sched.VMMRouter, map[string]*sched.VMMClient, map[string]*grpc.ClientConn) {
	t.Helper()
	var nodes []sched.ComputeNodeInfo
	for _, host := range c.Hosts {
		nodes = append(nodes, sched.ComputeNodeInfo{ID: host.NodeID, TargetURL: "unix://" + host.LocalSocket})
	}
	router := sched.NewVMMRouter(nodes, func(ctx context.Context, target string, tlsCfg *tls.Config) (sched.VMM, error) {
		return sched.DialVMMContext(ctx, target, tlsCfg)
	}, nil)
	clients, connections := map[string]*sched.VMMClient{}, map[string]*grpc.ClientConn{}
	for _, node := range nodes {
		conn, err := wire.DialContext(ctx, node.TargetURL, nil, grpc.WithChainUnaryInterceptor(gate.intercept))
		if err != nil {
			t.Fatal("dial native fixture VM owner")
		}
		client := sched.NewVMMClient(conn)
		t.Cleanup(func() { _ = client.Close() })
		router.SetClient(node.ID, client) // Real RPC clients; no native VMM is simulated.
		clients[node.ID], connections[node.ID] = client, conn
		ping, err := client.Ping(ctx)
		if err != nil || ping.FcVersion != c.FCVersion {
			t.Fatal("native fixture Firecracker version differs")
		}
		stats, err := client.Stats(ctx)
		if err != nil || stats.LiveCount != 0 || stats.LeasedCount != 0 || len(stats.Instances) != 0 {
			t.Fatal("native migration requires otherwise empty compute hosts")
		}
	}
	return router, clients, connections
}

func runNativeDestinationSQL(t *testing.T, c functionmigration.Config, catalog *pgxpool.Pool, runtime, scenario string) *functionmigration.Run {
	f := newNativeDestinationFixture(t, c, catalog, runtime)
	f.run.Scenario = scenario
	ctx, cancel := context.WithTimeout(t.Context(), sourceDeployCtxTimeout())
	defer cancel()
	gate := &nativeDestinationGate{store: f.store, ready: make(chan state.FunctionMigrationDestination, 1), release: make(chan struct{})}
	router, clients, connections := nativeMigrationRouter(t, ctx, c, gate)
	f.deploy(t, runtime)
	f.get(t, ctx, "state", 200)
	instances, err := f.store.ListInstancesForApp(ctx, f.appID)
	if err != nil {
		t.Fatal("read native migration source")
	}
	ins := pickState(instances, state.StateRunning)
	if ins.ID == "" || clients[ins.NodeID] == nil {
		t.Fatal("source did not start on a declared native host")
	}
	to := c.Hosts[0].NodeID
	if to == ins.NodeID {
		to = c.Hosts[1].NodeID
	}
	r := f.run
	r.InstanceID, r.SourceNode, r.SourceWake, r.DestinationNode = ins.ID, ins.NodeID, ins.WakeID, to
	parent, err := f.store.GetInstanceApplicationStandardRuntimeReceipt(ctx, ins.ID)
	if err != nil || runtimeadmission.CheckSnapshotParent(parent) != nil || parent.Binding.NodeID != ins.NodeID {
		t.Fatal("source lacks measured native consumption")
	}
	verifyNativeMigrationBuildInputs(t, ctx, f, ins, parent)
	r.Source = nativeReceiptOwner(t, parent)
	nativeMigrationProbe(t, ctx, ins.NodeID, "owner", &r.Source)
	r.Source.Verified = true
	seedNativeMigrationPools(t, ctx, f, clients[ins.NodeID], ins.ID)
	r.CallsBefore = f.control.Calls()
	ledger := sched.NewLedger()
	f.ledger = ledger
	engine, err := sched.NewEngine(ctx, f.store, ledger, router, nil, c.FCVersion, slog.Default())
	if err != nil {
		t.Fatal("construct native schedd migration coordinator", err)
	}
	metrics := nativeMigrationMetrics{prometheus.NewCounter(prometheus.CounterOpts{Name: "native_migration_decisions_total", Help: "Native fixture migration decisions."})}
	store := state.Store(f.store)
	var commitFault *nativeCommitReplyLoss
	if scenario == functionmigration.LostCommit {
		commitFault = &nativeCommitReplyLoss{PgStore: f.store, instanceID: ins.ID}
		store = statetest.NewPg(commitFault)
	}
	handoff := sched.NewMigrationHarness(ctx, store, router, metrics, slog.Default(), to, engine.BuildAppSpecForMigration, ledger,
		func(ctx context.Context, id string) (int, int, error) {
			n, err := f.store.ComputeNodeByID(ctx, id)
			return n.AdmissionCeilingMB, n.VCPUBudget, err
		})
	f.control.Arm()
	f.control.ArmRecovery()
	done := make(chan struct{})
	var migrationErr error
	go func() { migrationErr = handoff.MigrateOne(ctx, ins.ID, ins.NodeID); close(done) }()
	t.Cleanup(func() {
		f.control.Release()
		gate.open()
		cancel()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			t.Error("native migration coordinator did not finish cleanup")
		}
	})
	backgroundNativeWait(t, ctx, func() bool {
		all, _ := f.sql.Connections(ctx, t)
		return f.control.Arrivals() == 4 && all == 0 && f.proxy.ConnectionCount() == 0
	})
	assertNativeMigrationHeld(t, done, "capture")
	sourcePermission, err := f.store.GetFunctionMigrationSourceByInstance(ctx, ins.ID)
	if err != nil || sourcePermission.CaptureToken == "" || sourcePermission.SourceNodeID != ins.NodeID || sourcePermission.SourceWakeID != ins.WakeID {
		t.Fatal("checkpoint effects lack durable source permission")
	}
	lease, err := f.store.GetMigrationLease(ctx, ins.ID, sourcePermission.LeaseToken)
	if err != nil || !lease.Pending {
		t.Fatal("checkpoint effects lack a pending migration lease")
	}
	r.CaptureHeld, r.DrainedBeforeCapture = true, true
	r.CheckpointWorkers = nativeMigrationWorkers(f.control.CheckpointWorkers())
	f.control.ReleaseCheckpoint()
	backgroundNativeWait(t, ctx, func() bool {
		all, _ := f.sql.Connections(ctx, t)
		return f.control.RecoveryArrivals() == 4 && all == 8 && f.proxy.ConnectionCount() == 8
	})
	assertNativeMigrationHeld(t, done, "recovery")
	issued, err := f.store.GetFunctionMigrationDestination(ctx, ins.ID, sourcePermission.LeaseToken)
	if err != nil || issued.Phase != state.FunctionDestinationIssued || issued.Receipt != nil {
		t.Fatal("destination became ready before all recovery ACKs")
	}
	owner, err := f.store.InstanceByID(ctx, ins.ID)
	if err != nil || owner.State != string(state.StateMigrating) || owner.NodeID != ins.NodeID || owner.WakeID != ins.WakeID {
		t.Fatal("issued destination changed source ownership before recovery")
	}
	if _, err := f.store.GetFunctionMigrationCapture(ctx, ins.ID, sourcePermission.LeaseToken); err != nil {
		t.Fatal("destination restore lacks the source's durable measured capture")
	}
	select {
	case <-gate.ready:
		t.Fatal("readiness preceded the final resource recovery ACK")
	default:
	}
	r.RecoveryHeld = true
	r.RecoveryWorkers, r.DestinationSQL = nativeMigrationWorkers(f.control.RecoveryWorkers()), f.sql.Sessions(ctx, t)
	f.control.Release()
	var destination state.FunctionMigrationDestination
	select {
	case destination = <-gate.ready:
	case <-done:
		t.Fatal("migration failed before readiness", migrationErr)
	case <-ctx.Done():
		t.Fatal("destination never returned measured readiness")
	}
	verifyNativeReadyDestination(t, ctx, f, ins, destination, connections[to])
	r.CallsAtReady = f.control.Calls()
	if scenario == functionmigration.LostReadiness || scenario == functionmigration.Duplicate {
		exerciseNativeAdoptionReplies(t, ctx, f, gate.call, ins, destination, connections[to])
	}
	gate.open()
	select {
	case <-done:
		if migrationErr != nil {
			t.Fatal("native ownership handoff failed", migrationErr)
		}
	case <-ctx.Done():
		t.Fatal("native ownership handoff did not finish")
	}
	verifyNativeCommittedDestination(t, ctx, f, ins, destination, clients[ins.NodeID])
	r.CallsAfter = f.control.Calls()
	if commitFault != nil {
		r.Fault = commitFault.evidence()
	}
	return r
}

func nativeReceiptOwner(t *testing.T, receipt runtimeadmission.Receipt) functionmigration.Owner {
	t.Helper()
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal("hash measured native receipt")
	}
	return functionmigration.Owner{PID: receipt.ArtifactConsumption.ProcessPID, Start: receipt.ArtifactConsumption.ProcessStart,
		UID: int(receipt.LeaseUID), Netns: receipt.Netns, ReceiptSHA: nativeSHA(raw)}
}

func assertNativeMigrationHeld(t *testing.T, done <-chan struct{}, phase string) {
	t.Helper()
	select {
	case <-done:
		t.Fatal("migration completed before independent " + phase + " witness")
	default:
	}
}

func nativeMigrationWorkers(workers []functionsql.WorkerWitness) []functionmigration.Worker {
	result := make([]functionmigration.Worker, 0, len(workers))
	for _, w := range workers {
		result = append(result, functionmigration.Worker{PID: w.PID, Generations: w.Generations})
	}
	return result
}

func nativeSourceRetired(ctx context.Context, s *state.PgStore, cli *sched.VMMClient, ins state.Instance, lease string) bool {
	if _, err := s.GetMigrationLease(ctx, ins.ID, lease); !errors.Is(err, state.ErrNotFound) {
		return false
	}
	stats, err := cli.Stats(ctx)
	if err != nil {
		return false
	}
	for _, stat := range stats.Instances {
		if stat.InstanceID == ins.ID {
			return false
		}
	}
	return true
}
