package vmmdgrpc

// adr: 1662

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

type functionMigrationHandlerVMM struct {
	*migrationHandlerVMM
	mu                                         sync.Mutex
	prepares, recoveries, retirements, forgets int
	retireResult, recovered                    bool
	recoveryErr, cleanupErr                    error
	entered, release                           chan struct{}
}

func (v *functionMigrationHandlerVMM) FunctionMigrationSourceRequired(string) bool { return true }

func (v *functionMigrationHandlerVMM) PrepareFunctionMigrationSource(ctx context.Context, _ fcvm.FunctionMigrationBinding, _ fcvm.SnapshotSpec, register func(context.Context) error) (fcvm.SnapshotInfo, error) {
	if err := register(ctx); err != nil {
		return fcvm.SnapshotInfo{}, err
	}
	v.prepares++
	return fcvm.SnapshotInfo{MemBytes: 4096, VMStateBytes: 1024}, nil
}

func (v *functionMigrationHandlerVMM) RecoverFunctionMigrationSource(ctx context.Context, _ fcvm.FunctionMigrationBinding, authorize func(context.Context) error) (bool, error) {
	if err := authorize(ctx); err != nil {
		return false, err
	}
	v.mu.Lock()
	if v.recovered {
		retired := v.retireResult
		v.mu.Unlock()
		return retired, nil
	}
	v.recoveries++
	v.mu.Unlock()
	if v.entered != nil {
		close(v.entered)
		select {
		case <-v.release:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	if v.recoveryErr != nil {
		return false, v.recoveryErr
	}
	if err := authorize(ctx); err != nil {
		return false, err
	}
	v.mu.Lock()
	v.recovered = true
	v.mu.Unlock()
	return v.retireResult, nil
}

func (v *functionMigrationHandlerVMM) RetireFunctionMigrationSource(context.Context, fcvm.FunctionMigrationBinding) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.retireResult {
		v.retirements++
		v.retireResult = true
	}
	return v.cleanupErr
}

func (v *functionMigrationHandlerVMM) ForgetFunctionMigrationSource(fcvm.FunctionMigrationBinding) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.forgets++
}

func functionMigrationHandlerFixture(t *testing.T, prepared bool) (*Server, *state.MemStore, *functionMigrationHandlerVMM, state.FunctionMigrationSource) {
	t.Helper()
	ctx := t.Context()
	store := state.NewMemStore()
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@source-handler.test", api.PlanHobby)
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
	vmm := &functionMigrationHandlerVMM{migrationHandlerVMM: &migrationHandlerVMM{}}
	s := New(vmm, wire.NewOpsMetrics("vmmd_test"), "1.10.0", nil).WithMigrationStore(store).WithNodeID(node.ID)
	source := state.FunctionMigrationSource{InstanceID: ins.ID, LeaseToken: state.FunctionMigrationTokenPrefix + uuid.NewString(), SourceNodeID: node.ID, SourceWakeID: ins.WakeID}
	if prepared {
		m := &activeMigration{instanceID: ins.ID, leaseToken: source.LeaseToken, memKey: "capture/mem", vmstateKey: "capture/vmstate", leaseExpiresAt: time.Now().Add(time.Minute)}
		if err := s.migrations.put(m); err != nil {
			t.Fatal(err)
		}
		if err := store.ReserveMigrationLease(ctx, state.MigrationLease{InstanceID: ins.ID, LeaseToken: source.LeaseToken, SourceNodeID: node.ID, CreatedAt: time.Now().Add(-time.Hour), LeaseExpiresAt: m.leaseExpiresAt}); err != nil {
			t.Fatal(err)
		}
		if err := store.RegisterFunctionMigrationSource(ctx, source, "capture/mem", "capture/vmstate"); err != nil {
			t.Fatal(err)
		}
		if err := store.PrepareFunctionMigrationSource(ctx, ins.ID, source.LeaseToken); err != nil {
			t.Fatal(err)
		}
	}
	return s, store, vmm, source
}

func TestFunctionMigrationHandlersCancelRequiresFenceAndKeepsLease(t *testing.T) {
	s, store, vmm, source := functionMigrationHandlerFixture(t, true)
	ctx := t.Context()
	req := &vmmdpb.CancelLiveMigrationRequest{InstanceId: source.InstanceID, LeaseToken: source.LeaseToken}
	if _, err := s.CancelLiveMigration(ctx, req); err == nil || vmm.recoveries != 0 || vmm.resumes != 0 {
		t.Fatal("cancel resumed source without schedd fence", err)
	}
	if _, err := store.FenceFunctionMigrationSource(ctx, source.InstanceID, source.LeaseToken); err != nil {
		t.Fatal(err)
	}
	vmm.entered, vmm.release = make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(vmm.release) }) }
	defer unblock()
	done := make(chan error, 1)
	go func() {
		response, err := s.CancelLiveMigration(ctx, req)
		if response.GetSourceRetired() {
			t.Error("recovered source reported retired")
		}
		done <- err
	}()
	select {
	case <-vmm.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("no recovery")
	}
	ins, _ := store.MigrationInstanceByID(ctx, source.InstanceID)
	if ins.State != string(state.StateMigrating) || ins.LeaseToken != state.FunctionMigrationRollbackToken(source.LeaseToken) || vmm.deletes != 0 {
		t.Fatal("published source or dropped capture before recovery ACK")
	}
	lateCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	_, err := s.AcknowledgeMigration(lateCtx, &vmmdpb.AcknowledgeMigrationRequest{InstanceId: source.InstanceID, LeaseToken: source.LeaseToken})
	cancel()
	if err == nil {
		t.Fatal("ack bypassed in-flight recovery")
	}
	unblock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetMigrationLease(ctx, source.InstanceID, source.LeaseToken); err != nil {
		t.Fatal("removed lease before scheduler completion", err)
	}
	if _, err := s.CancelLiveMigration(ctx, req); err != nil {
		t.Fatal(err)
	}
	if vmm.recoveries != 1 {
		t.Fatal("duplicate cancel replayed resource callback", vmm.recoveries)
	}
	if err := store.CompleteFunctionMigrationSource(ctx, source.InstanceID, source.LeaseToken, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CancelLiveMigration(ctx, req); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetMigrationLease(ctx, source.InstanceID, source.LeaseToken); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("completed lease retained", err)
	}
	if vmm.deletes != 1 || vmm.resumes != 0 || vmm.destroys != 0 {
		t.Fatal("qualified rollback fell back to generic VM operations")
	}
	if _, err := s.AcknowledgeMigration(ctx, &vmmdpb.AcknowledgeMigrationRequest{InstanceId: source.InstanceID, LeaseToken: source.LeaseToken}); err != nil {
		t.Fatal(err)
	}
	if vmm.retirements != 0 {
		t.Fatal("late ack retired recovered source")
	}
}

func TestFunctionMigrationHandlersCommittedSourceNeverResumes(t *testing.T) {
	s, store, vmm, source := functionMigrationHandlerFixture(t, true)
	ctx := t.Context()
	if err := store.MarkInstanceMigrating(ctx, source.InstanceID, source.SourceNodeID, source.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateInstanceOwner(ctx, source.InstanceID, source.SourceNodeID, uuid.NewString(), source.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CancelLiveMigration(ctx, &vmmdpb.CancelLiveMigrationRequest{InstanceId: source.InstanceID, LeaseToken: source.LeaseToken}); err == nil {
		t.Fatal("cancel accepted committed ownership")
	}
	if _, err := s.AcknowledgeMigration(ctx, &vmmdpb.AcknowledgeMigrationRequest{InstanceId: source.InstanceID, LeaseToken: source.LeaseToken}); err != nil {
		t.Fatal(err)
	}
	if vmm.retirements != 1 || vmm.recoveries != 0 || vmm.resumes != 0 || vmm.destroys != 0 {
		t.Fatal("committed source resumed or used unbound teardown")
	}
}

func TestFunctionMigrationHandlersExpiryWaitsForScheduler(t *testing.T) {
	s, store, vmm, source := functionMigrationHandlerFixture(t, true)
	ctx := t.Context()
	m, err := s.migrations.get(source.InstanceID, source.LeaseToken)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.cleanupExpiredMigration(ctx, m); err == nil || vmm.recoveries != 0 {
		t.Fatal("expiry inferred permission to resume", err)
	}
	if _, err := store.FenceFunctionMigrationSource(ctx, source.InstanceID, source.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err := s.cleanupExpiredMigration(ctx, m); err == nil || vmm.recoveries != 1 {
		t.Fatal("expiry lost recovery or removed uncommitted receipt", err)
	}
	if _, err := store.GetFunctionMigrationSource(ctx, source.InstanceID, source.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteFunctionMigrationSource(ctx, source.InstanceID, source.LeaseToken, false); err != nil {
		t.Fatal(err)
	}
	if err := s.cleanupExpiredMigration(ctx, m); err != nil {
		t.Fatal(err)
	}
	if vmm.recoveries != 1 {
		t.Fatal("expiry replayed recovery")
	}
}

func TestFunctionMigrationHandlersRetirementAndRestartUncertainty(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "retired", true: "unknown-owner"}[unknown], func(t *testing.T) {
			s, store, vmm, source := functionMigrationHandlerFixture(t, true)
			ctx := t.Context()
			if _, err := store.FenceFunctionMigrationSource(ctx, source.InstanceID, source.LeaseToken); err != nil {
				t.Fatal(err)
			}
			vmm.retireResult = true
			if unknown {
				vmm.recoveryErr = runtimeadmission.ErrStale
			}
			response, err := s.CancelLiveMigration(ctx, &vmmdpb.CancelLiveMigrationRequest{InstanceId: source.InstanceID, LeaseToken: source.LeaseToken})
			if unknown {
				if err == nil || response != nil {
					t.Fatal("unknown physical owner acknowledged recovery")
				}
			} else if err != nil || !response.GetSourceRetired() {
				t.Fatal("retirement lost on wire", response, err)
			}
			ins, _ := store.MigrationInstanceByID(ctx, source.InstanceID)
			if ins.State != string(state.StateMigrating) {
				t.Fatal("vmmd wrote ownership state")
			}
			if _, err := store.GetMigrationLease(ctx, source.InstanceID, source.LeaseToken); err != nil {
				t.Fatal("uncertain/uncommitted result lost lease", err)
			}
		})
	}
}

func TestFunctionMigrationHandlersPreparationAndDestinationGate(t *testing.T) {
	s, store, vmm, source := functionMigrationHandlerFixture(t, false)
	ctx := t.Context()
	prepared, err := s.PrepareLiveMigration(ctx, &vmmdpb.PrepareLiveMigrationRequest{InstanceId: source.InstanceID, SnapshotStorageKey: "capture/mem"})
	if !functionlifetime.NativeQualificationBuild {
		if err == nil || vmm.prepares != 0 {
			t.Fatal("production activated qualified source capture", err)
		}
		return
	}
	if err != nil || vmm.prepares != 1 || vmm.snapshots != 0 {
		t.Fatal("qualified capture used generic snapshot", err)
	}
	if err := store.MarkInstanceMigrating(ctx, source.InstanceID, source.SourceNodeID, prepared.GetLeaseToken()); err != nil {
		t.Fatal(err)
	}
	for _, runtime := range []string{"node22", "node24", "python312", "binary"} {
		_, err := s.AdoptMigratedInstance(ctx, &vmmdpb.AdoptMigratedInstanceRequest{InstanceId: source.InstanceID, MemStorageKey: "capture/mem", VmstateStorageKey: "capture/vmstate", LeaseToken: prepared.GetLeaseToken(), AppSpec: &vmmdpb.AppSpec{Runtime: runtime}})
		if err == nil || vmm.wakeReq.Instance != "" {
			t.Fatal("source-only capture activated destination", runtime, err)
		}
	}
}

func TestFunctionMigrationHandlersGenericReleaseRetainsSource(t *testing.T) {
	_, store, _, source := functionMigrationHandlerFixture(t, true)
	leaser := NewPGMigrationLeaser(store, nil)
	if err := leaser.Release(t.Context(), sched.LeaseToken(source.LeaseToken), source.SourceNodeID); !errors.Is(err, state.ErrConflict) {
		t.Fatal("generic release discarded source authority", err)
	}
	if _, err := store.GetFunctionMigrationSource(t.Context(), source.InstanceID, source.LeaseToken); err != nil {
		t.Fatal(err)
	}
}
