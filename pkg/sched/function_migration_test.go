package sched

// adr: 20261018030613

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

type functionMigrationTestVMM struct {
	*fakeVMM
	store              *state.MemStore
	mu                 sync.Mutex
	attempts           int
	retired, recovered bool
	fault              error
	entered, release   chan struct{}
}

func (v *functionMigrationTestVMM) CancelFunctionMigrationSource(ctx context.Context, _ string, id, token string) (bool, error) {
	source, err := v.store.GetFunctionMigrationSource(ctx, id, token)
	if err != nil {
		return false, err
	}
	if source.Phase == state.FunctionMigrationRecovered || source.Phase == state.FunctionMigrationRetired {
		return source.Phase == state.FunctionMigrationRetired, v.store.DeleteMigrationLease(ctx, token)
	}
	ins, err := v.store.MigrationInstanceByID(ctx, id)
	if err != nil {
		return false, err
	}
	if source.Phase != state.FunctionMigrationRecovering || ins.State != string(state.StateMigrating) || ins.LeaseToken != state.FunctionMigrationRollbackToken(token) {
		return false, errors.New("missing durable recovery fence")
	}
	v.mu.Lock()
	if v.recovered {
		retired := v.retired
		v.mu.Unlock()
		return retired, nil
	}
	v.attempts++
	v.mu.Unlock()
	if v.entered != nil {
		close(v.entered)
		select {
		case <-v.release:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	if v.fault != nil {
		return false, v.fault
	}
	v.mu.Lock()
	v.recovered = true
	v.mu.Unlock()
	return v.retired, nil
}

func (v *functionMigrationTestVMM) AcknowledgeMigration(ctx context.Context, _ string, id, token string) error {
	if v.fault != nil {
		return v.fault
	}
	v.mu.Lock()
	v.retired = true
	v.mu.Unlock()
	return v.store.DeleteMigrationLease(ctx, token)
}

func functionMigrationSchedulerFixture(t *testing.T) (*state.MemStore, state.FunctionMigrationSource, *functionMigrationTestVMM) {
	t.Helper()
	ctx := t.Context()
	store := state.NewMemStore()
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@source-scheduler.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "source-" + uuid.NewString(), NodeID: node.ID, RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	ins, err := store.CreateInstance(ctx, app.ID, "", string(state.StateRunning), 128, node.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	source := state.FunctionMigrationSource{InstanceID: ins.ID, LeaseToken: state.FunctionMigrationTokenPrefix + uuid.NewString(), SourceNodeID: node.ID, SourceWakeID: ins.WakeID}
	if err := store.ReserveMigrationLease(ctx, state.MigrationLease{InstanceID: ins.ID, LeaseToken: source.LeaseToken, SourceNodeID: node.ID, CreatedAt: time.Now().Add(-time.Hour), LeaseExpiresAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterFunctionMigrationSource(ctx, source, "capture/mem", "capture/vmstate"); err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareFunctionMigrationSource(ctx, source.InstanceID, source.LeaseToken); err != nil {
		t.Fatal(err)
	}
	return store, source, &functionMigrationTestVMM{fakeVMM: &fakeVMM{}, store: store}
}

func TestFunctionMigrationSchedulerHeldRecoveryAndLateCommit(t *testing.T) {
	for _, retired := range []bool{false, true} {
		t.Run(map[bool]string{false: "recovered", true: "retired"}[retired], func(t *testing.T) {
			store, source, vmm := functionMigrationSchedulerFixture(t)
			ctx := t.Context()
			vmm.retired = retired
			vmm.entered, vmm.release = make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(vmm.release) }) }
			defer unblock()
			done := make(chan error, 1)
			go func() {
				handled, actual, err := rollbackFunctionMigrationSource(ctx, store, vmm, source.InstanceID, source.LeaseToken)
				if !handled || actual != retired {
					t.Error("lost qualified source outcome")
				}
				done <- err
			}()
			select {
			case <-vmm.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("no recovery")
			}
			ins, _ := store.MigrationInstanceByID(ctx, source.InstanceID)
			if ins.State != string(state.StateMigrating) || ins.LeaseToken != state.FunctionMigrationRollbackToken(source.LeaseToken) {
				t.Fatal("published source before ACK")
			}
			if err := store.MigrateInstanceOwner(ctx, source.InstanceID, source.SourceNodeID, uuid.NewString(), source.LeaseToken); !errors.Is(err, state.ErrConflict) {
				t.Fatal("late commit passed fence", err)
			}
			unblock()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			ins, _ = store.MigrationInstanceByID(ctx, source.InstanceID)
			want := string(state.StateRunning)
			if retired {
				want = string(state.StateStopped)
			}
			if ins.State != want || ins.WakeID != source.SourceWakeID || ins.NodeID != source.SourceNodeID {
				t.Fatal("wrong source ownership completion", ins)
			}
			if _, err := store.GetMigrationLease(ctx, source.InstanceID, source.LeaseToken); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("confirmed source retained cleanup record", err)
			}
			if vmm.attempts != 1 || vmm.cancels != 0 {
				t.Fatal("used generic cancel or replayed recovery")
			}
		})
	}
}

func TestFunctionMigrationSchedulerExpiryRetainsCapacityUntilPhysicalResult(t *testing.T) {
	store, source, vmm := functionMigrationSchedulerFixture(t)
	ctx := t.Context()
	if err := store.RenewMigrationLease(ctx, source.LeaseToken, source.SourceNodeID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	ledger := NewNodeLedger()
	ins, _ := store.MigrationInstanceByID(ctx, source.InstanceID)
	if err := ledger.Admit(Request{Instance: ins.ID, AppID: ins.AppID, Plan: api.PlanHobby, RAMMB: 128, VCPU: 1, Kind: KindWake, NodeID: source.SourceNodeID}); err != nil {
		t.Fatal(err)
	}
	e, err := NewEngine(ctx, store, ledger, vmm, &fakeNotifier{}, "1.10.0", testLog())
	if err != nil {
		t.Fatal(err)
	}
	e.WithOwnerNodeID(source.SourceNodeID)
	e.WithOpsMetrics(wire.NewOpsMetrics("schedd"))
	vmm.fault = io.EOF
	if n, err := e.ReconcileExpiredMigrations(ctx); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if _, err := e.reconcileAppAdmission(ctx, ins.AppID); err != nil {
		t.Fatal(err)
	}
	if !ledger.ResidentFor(ins.ID) || ledger.Concurrency(ins.AppID) != 1 || vmm.cancels != 0 {
		t.Fatal("unconfirmed source released residency or used legacy watchdog")
	}
	current, _ := store.MigrationInstanceByID(ctx, ins.ID)
	if current.State != string(state.StateMigrating) || current.LeaseToken != state.FunctionMigrationRollbackToken(source.LeaseToken) {
		t.Fatal("legacy watchdog reopened failed source")
	}
	if _, err := store.GetMigrationLease(ctx, ins.ID, source.LeaseToken); err != nil {
		t.Fatal("lost retry authority", err)
	}
	vmm.fault = nil
	vmm.retired = true
	if n, err := e.ReconcileExpiredMigrations(ctx); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if ledger.ResidentFor(ins.ID) || ledger.Concurrency(ins.AppID) != 0 {
		t.Fatal("confirmed retirement retained scheduler capacity")
	}
	current, _ = store.MigrationInstanceByID(ctx, ins.ID)
	if current.State != string(state.StateStopped) {
		t.Fatal("retired source published running")
	}
}

func TestFunctionMigrationSchedulerCommittedOwnerAndMissingCapability(t *testing.T) {
	store, source, vmm := functionMigrationSchedulerFixture(t)
	ctx := t.Context()
	if handled, _, err := rollbackFunctionMigrationSource(ctx, store, vmm.fakeVMM, source.InstanceID, source.LeaseToken); !handled || err == nil {
		t.Fatal("qualified source fell back without retirement-aware transport")
	}
	if err := store.MarkInstanceMigrating(ctx, source.InstanceID, source.SourceNodeID, source.LeaseToken); err != nil {
		t.Fatal(err)
	}
	peer := uuid.NewString()
	if err := store.MigrateInstanceOwner(ctx, source.InstanceID, source.SourceNodeID, peer, source.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if handled, retired, err := rollbackFunctionMigrationSource(ctx, store, vmm, source.InstanceID, source.LeaseToken); !handled || !retired || err != nil {
		t.Fatal(handled, retired, err)
	}
	ins, _ := store.MigrationInstanceByID(ctx, source.InstanceID)
	if ins.NodeID != peer || ins.State != string(state.StateRunning) || vmm.attempts != 0 {
		t.Fatal("committed peer was overwritten or source resumed")
	}
}

func TestFunctionMigrationSchedulerLegacyAbortKeepsCapacity(t *testing.T) {
	store, source, _ := functionMigrationSchedulerFixture(t)
	ctx := t.Context()
	ins, _ := store.MigrationInstanceByID(ctx, source.InstanceID)
	ledger := NewNodeLedger()
	if err := ledger.Admit(Request{Instance: ins.ID, AppID: ins.AppID, Plan: api.PlanHobby, RAMMB: 128, VCPU: 1, NodeID: source.SourceNodeID}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkInstanceMigrating(ctx, ins.ID, source.SourceNodeID, source.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err := store.CancelInstanceMigration(ctx, ins.ID, source.SourceNodeID, source.LeaseToken); err != nil {
		t.Fatal(err)
	}
	e := &Engine{store: store, ledger: ledger}
	if _, err := e.reconcileAppAdmission(ctx, ins.AppID); err != nil {
		t.Fatal(err)
	}
	if !ledger.ResidentFor(ins.ID) || ledger.Concurrency(ins.AppID) != 1 {
		t.Fatal("token-cleared rollback released unconfirmed source")
	}
}
