//go:build metal && function_background_native

package e2e_test

// adr: 1353

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest/functionmigration"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
)

type nativeCoordinatorMode struct {
	Role            string `json:"role"`
	InstanceID      string `json:"instance_id,omitempty"`
	SourceNode      string `json:"source_node,omitempty"`
	DestinationNode string `json:"destination_node"`
	Scenario        string `json:"scenario,omitempty"`
}

func startNativeCoordinator(t *testing.T, ctx context.Context, c functionmigration.Config, mode nativeCoordinatorMode) *functionmigration.Coordinator {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal("locate native fixture executable")
	}
	data, err := json.Marshal(mode)
	if err != nil {
		t.Fatal(err)
	}
	report := os.Getenv("FAAS_FUNCTION_MIGRATION_REPORT")
	if !filepath.IsAbs(report) {
		t.Fatal("coordinator logs require the private native evidence directory")
	}
	logPath := filepath.Join(filepath.Dir(report), "coordinator-"+mode.Role+"-"+uuid.NewString()+".log")
	child, err := functionmigration.StartCoordinator(ctx, binary, []string{"-test.run=^TestFunctionMigrationDestinationPostgresMetal$", "-test.count=1", "-test.timeout=10m"},
		append(os.Environ(), "FAAS_FUNCTION_MIGRATION_COORDINATOR="+string(data)), logPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Close() })
	if child.Identity.BinarySHA != c.TestBinarySHA {
		t.Fatal("coordinator did not execute the freshly qualified binary")
	}
	return child
}

func runNativeCoordinatorChild(t *testing.T, c functionmigration.Config, pool *pgxpool.Pool) {
	t.Helper()
	var mode nativeCoordinatorMode
	if json.Unmarshal([]byte(os.Getenv("FAAS_FUNCTION_MIGRATION_COORDINATOR")), &mode) != nil ||
		(mode.DestinationNode != c.Hosts[0].NodeID && mode.DestinationNode != c.Hosts[1].NodeID) {
		t.Fatal("invalid private coordinator mode")
	}
	events := os.NewFile(3, "native-coordinator-events")
	if events == nil {
		t.Fatal("native coordinator requires its inherited event pipe")
	}
	defer events.Close()
	encoder := json.NewEncoder(events)
	send := func(e functionmigration.CoordinatorEvent) {
		if err := encoder.Encode(e); err != nil {
			t.Fatal("write private coordinator boundary")
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	store := state.NewPgStore(pool)
	ledger := sched.NewLedger()
	if mode.Role == "recover" {
		if mode.InstanceID != "" || mode.SourceNode != "" || mode.Scenario != "" {
			t.Fatal("replacement must discover attempts from PostgreSQL")
		}
		router, _, _ := nativeMigrationRouter(t, ctx, c, nil, false)
		engine, err := sched.NewEngine(ctx, store, ledger, router, nil, c.FCVersion, slog.Default())
		if err != nil {
			t.Fatal(err)
		}
		engine.WithOwnerNodeID(mode.DestinationNode)
		entries, err := store.ListRestartFunctionMigrationDestinations(ctx, mode.DestinationNode, "", api.MigratingWatchdogTickLimit)
		if err != nil || len(entries) != 1 {
			t.Fatal("replacement did not discover exactly one durable fixture attempt")
		}
		n, err := engine.RecoverFunctionMigrations(ctx)
		if err != nil || n != 1 {
			t.Fatal("production startup recovery did not finish the durable handoff", err)
		}
		if err := engine.SeedLedger(ctx); err != nil {
			t.Fatal("replacement failed production ledger seeding", err)
		}
		send(functionmigration.CoordinatorEvent{Phase: "recovered", Completed: n, Seeded: true, DiscoveredPhase: entries[0].Phase})
		return
	}
	if mode.Role != "migrate" || !functionmigration.RestartScenario(mode.Scenario) || mode.InstanceID == "" || mode.SourceNode == mode.DestinationNode ||
		(mode.SourceNode != c.Hosts[0].NodeID && mode.SourceNode != c.Hosts[1].NodeID) {
		t.Fatal("invalid migration crash fixture")
	}
	gate := &nativeDestinationGate{store: store, ready: make(chan state.FunctionMigrationDestination, 1), release: make(chan struct{})}
	router, _, _ := nativeMigrationRouter(t, ctx, c, gate, false)
	engine, err := sched.NewEngine(ctx, store, ledger, router, nil, c.FCVersion, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	backing := state.Store(store)
	if mode.Scenario == functionmigration.RestartCommitted {
		backing = statetest.NewPg(&nativeCrashCommitBarrier{PgStore: store, notify: func() error { return encoder.Encode(functionmigration.CoordinatorEvent{Phase: "committed"}) }})
	}
	metrics := nativeMigrationMetrics{prometheus.NewCounter(prometheus.CounterOpts{Name: "native_child_migration_decisions_total", Help: "Child migration decisions."})}
	handoff := sched.NewMigrationHarness(ctx, backing, router, metrics, slog.Default(), mode.DestinationNode, engine.BuildAppSpecForMigration, ledger,
		func(ctx context.Context, id string) (int, int, error) {
			n, err := store.ComputeNodeByID(ctx, id)
			return n.AdmissionCeilingMB, n.VCPUBudget, err
		})
	done := make(chan error, 1)
	go func() { done <- handoff.MigrateOne(ctx, mode.InstanceID, mode.SourceNode) }()
	select {
	case <-gate.ready:
	case err := <-done:
		t.Fatal("child migration stopped before readiness", err)
	case <-ctx.Done():
		t.Fatal("child readiness timed out")
	}
	send(functionmigration.CoordinatorEvent{Phase: "ready"})
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil || line != "continue\n" {
		t.Fatal("child lost its explicit readiness barrier")
	}
	gate.open()
	select {
	case err := <-done:
		t.Fatal("crash fixture unexpectedly completed migration", err)
	case <-ctx.Done():
		t.Fatal("crash fixture timed out")
	}
}

type nativeCrashCommitBarrier struct {
	PgStore *state.PgStore
	notify  func() error
}

func (s *nativeCrashCommitBarrier) MigrateInstanceOwnerWithRuntimeConfig(ctx context.Context, id, from, to, token string, input state.RuntimeConfigMigration) error {
	if err := s.PgStore.MigrateInstanceOwnerWithRuntimeConfig(ctx, id, from, to, token, input); err != nil {
		return err
	}
	if err := s.notify(); err != nil {
		return err
	} // Real commit completed; no activation or source acknowledgement yet.
	<-ctx.Done() // The parent must SIGKILL this process, without deferred rollback.
	return ctx.Err()
}

func finishNativeCoordinatorRestart(t *testing.T, ctx context.Context, f *nativeDestinationFixture, child *functionmigration.Coordinator, source state.Instance, d state.FunctionMigrationDestination, sourceClient *sched.VMMClient, destinationConn *grpc.ClientConn) {
	t.Helper()
	r := f.run
	e := &functionmigration.RestartEvidence{Before: child.Identity}
	r.Restart = e
	if r.Scenario == functionmigration.RestartCommitted {
		if err := child.Release(); err != nil {
			t.Fatal("release child readiness boundary")
		}
		if _, err := child.Event(ctx, "committed"); err != nil {
			t.Fatal(err)
		}
	}
	crashCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := child.Kill(crashCtx); err != nil {
		t.Fatal(err)
	}
	e.SIGKILLVerified = true
	current, err := f.store.GetFunctionMigrationDestination(ctx, source.ID, d.Grant.Source.LeaseToken)
	if err != nil || !current.Grant.Equal(d.Grant) || current.Receipt == nil || !current.Receipt.Equal(*d.Receipt) {
		t.Fatal("coordinator death changed the durable destination")
	}
	owner, err := f.store.InstanceByID(ctx, source.ID)
	if err != nil {
		t.Fatal("read owner after coordinator death")
	}
	if r.Scenario == functionmigration.RestartReady {
		if current.Phase != state.FunctionDestinationReady || owner.NodeID != source.NodeID || owner.WakeID != source.WakeID || owner.State != string(state.StateMigrating) {
			t.Fatal("uncommitted crash moved source ownership")
		}
		e.PendingRetained = true
	} else {
		if current.Phase != state.FunctionDestinationCommitted || owner.NodeID != r.DestinationNode || owner.WakeID != r.DestinationWake || owner.State != string(state.StateRunning) {
			t.Fatal("post-commit crash lost the committed epoch")
		}
		e.CommittedAtCrash = true
	}
	if _, err := f.store.GetMigrationLease(ctx, source.ID, d.Grant.Source.LeaseToken); err != nil {
		t.Fatal("coordinator death discarded source cleanup authority")
	}
	e.LeaseRetained = true
	e.SourceAtCrash = r.Source
	nativeMigrationProbe(t, ctx, source.NodeID, "owner", &e.SourceAtCrash)
	if !nativeDestinationExecutionRefused(ctx, destinationConn, source.ID, f.slug+"."+f.config.AppDomain) {
		t.Fatal("crashed coordinator's destination served before activation")
	}
	e.ExecutionClosed = true
	replacement := startNativeCoordinator(t, ctx, f.config, nativeCoordinatorMode{Role: "recover", DestinationNode: r.DestinationNode})
	e.After = replacement.Identity
	e.Startup, err = replacement.Event(ctx, "recovered")
	if err != nil {
		t.Fatal(err)
	}
	if err := replacement.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	e.DestinationAfter = r.Destination
	nativeMigrationProbe(t, ctx, r.DestinationNode, "owner", &e.DestinationAfter)
	e.SQLAfter = f.sql.Sessions(ctx, t)
	verifyNativeCommittedDestination(t, ctx, f, source, d, sourceClient)
	r.CallsAfter = f.control.Calls()
}
