package state_test

// adr: 1575

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedPgFunctionMigrationSource(t *testing.T, store *state.PgStore, node string) state.FunctionMigrationSource {
	t.Helper()
	ctx := t.Context()
	_, id := seedRunningInstance(t, store, ctx, node)
	ins, err := store.MigrationInstanceByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	source := state.FunctionMigrationSource{InstanceID: id, LeaseToken: state.FunctionMigrationTokenPrefix + uuid.NewString(), SourceNodeID: node, SourceWakeID: ins.WakeID}
	if err := store.ReserveMigrationLease(ctx, state.MigrationLease{InstanceID: id, LeaseToken: source.LeaseToken, SourceNodeID: node, CreatedAt: time.Now().Add(-time.Hour), LeaseExpiresAt: time.Now().Add(time.Minute), Pending: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterFunctionMigrationSource(ctx, source, "capture/mem", "capture/vmstate"); err != nil {
		t.Fatal(err)
	}
	lease, err := store.GetMigrationLease(ctx, id, source.LeaseToken)
	if err != nil || !lease.Pending || lease.MemStorageKey != "capture/mem" || lease.VMStateStorageKey != "capture/vmstate" {
		t.Fatal("capture handles were not durable before effects", err)
	}
	if err := store.PrepareFunctionMigrationSource(ctx, id, source.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteMigrationLease(ctx, source.LeaseToken, "capture/mem", "capture/vmstate"); err != nil {
		t.Fatal(err)
	}
	return source
}

func TestPgStoreFunctionMigrationSourceRollback(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	node := resolveDefaultLocal(t, ctx, store)
	for _, phaseTwo := range []bool{false, true} {
		for _, retired := range []bool{false, true} {
			source := seedPgFunctionMigrationSource(t, store, node)
			if phaseTwo {
				if err := store.MarkInstanceMigrating(ctx, source.InstanceID, node, source.LeaseToken); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.CompleteFunctionMigrationSource(ctx, source.InstanceID, source.LeaseToken, retired); !errors.Is(err, state.ErrConflict) {
				t.Fatal("completed without durable fence", err)
			}
			resolution, err := store.FenceFunctionMigrationSource(ctx, source.InstanceID, source.LeaseToken)
			if err != nil || resolution != state.MigrationCommitAborted {
				t.Fatal(resolution, err)
			}
			ins, err := store.MigrationInstanceByID(ctx, source.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			if ins.State != string(state.StateMigrating) || ins.LeaseToken != state.FunctionMigrationRollbackToken(source.LeaseToken) {
				t.Fatal("published source before physical ACK")
			}
			if err := store.MigrateInstanceOwner(ctx, source.InstanceID, node, uuid.NewString(), source.LeaseToken); !errors.Is(err, state.ErrConflict) {
				t.Fatal("late commit passed fence", err)
			}
			for range 2 {
				if err := store.CompleteFunctionMigrationSource(ctx, source.InstanceID, source.LeaseToken, retired); err != nil {
					t.Fatal("completion retry", err)
				}
			}
			ins, err = store.MigrationInstanceByID(ctx, source.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			want := string(state.StateRunning)
			if retired {
				want = string(state.StateStopped)
			}
			if ins.State != want || ins.WakeID != source.SourceWakeID || ins.NodeID != node || ins.LeaseToken != "" {
				t.Fatal("wrong completion", ins)
			}
			if err := store.RenewMigrationLease(ctx, source.LeaseToken, node, time.Now().Add(-time.Minute)); err != nil {
				t.Fatal(err)
			}
			if err := store.ReserveMigrationLease(ctx, state.MigrationLease{InstanceID: source.InstanceID, LeaseToken: state.FunctionMigrationTokenPrefix + uuid.NewString(), SourceNodeID: node, CreatedAt: time.Now(), LeaseExpiresAt: time.Now().Add(time.Minute)}); !errors.Is(err, state.ErrConflict) {
				t.Fatal("replaced expired source before cleanup", err)
			}
			if err := store.DeleteMigrationLease(ctx, source.LeaseToken); err != nil {
				t.Fatal(err)
			}
			if err := store.MarkInstanceMigrating(ctx, source.InstanceID, node, source.LeaseToken); !errors.Is(err, state.ErrConflict) {
				t.Fatal("late mark reopened cleaned source", err)
			}
			if _, err := store.GetFunctionMigrationSource(ctx, source.InstanceID, source.LeaseToken); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("source survived lease cleanup", err)
			}
		}
	}
}

func TestPgStoreFunctionMigrationSourceCommitRollbackRace(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	node := resolveDefaultLocal(t, ctx, store)
	peer, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "source-peer-" + uuid.NewString(), TargetURL: "tcp://10.0.0.2:7000", VPCPUs: 8, MemMB: 8192, MaxConcurrency: 100, AdmissionCeilingMB: 7000, VCPUBudget: api.VCPUSlots, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		source := seedPgFunctionMigrationSource(t, store, node)
		if err := store.MarkInstanceMigrating(ctx, source.InstanceID, node, source.LeaseToken); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		var commitErr, fenceErr error
		var resolution state.MigrationCommitResolution
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			commitErr = store.MigrateInstanceOwner(ctx, source.InstanceID, node, peer.ID, source.LeaseToken)
		}()
		go func() {
			defer wg.Done()
			<-start
			resolution, fenceErr = store.FenceFunctionMigrationSource(ctx, source.InstanceID, source.LeaseToken)
		}()
		close(start)
		wg.Wait()
		if fenceErr != nil {
			t.Fatal(fenceErr)
		}
		if commitErr == nil {
			if resolution != state.MigrationCommitObsolete {
				t.Fatal("commit and source rollback both won", resolution)
			}
		} else if !errors.Is(commitErr, state.ErrConflict) || resolution != state.MigrationCommitAborted {
			t.Fatal("ownership remained uncertain", commitErr, resolution)
		}
	}
}

func TestPgStoreFunctionMigrationSourceCompletionAtCapacity(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "source-tight-" + uuid.NewString(), TargetURL: "tcp://10.0.0.3:7000", VPCPUs: 2, MemMB: 1024, MaxConcurrency: 10, AdmissionCeilingMB: 256 + api.PerVMOverheadMB, VCPUBudget: api.VCPUSlots, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	source := seedPgFunctionMigrationSource(t, store, node.ID)
	if _, err := store.FenceFunctionMigrationSource(ctx, source.InstanceID, source.LeaseToken); err != nil {
		t.Fatal(err)
	}
	// A lost completion ACK must not reserve the same VM's memory twice.
	for range 2 {
		if err := store.CompleteFunctionMigrationSource(ctx, source.InstanceID, source.LeaseToken, false); err != nil {
			t.Fatal("source counted twice during completion retry", err)
		}
	}
}

func TestPgStoreFunctionMigrationSourceExpiredMark(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	node := resolveDefaultLocal(t, ctx, store)
	source := seedPgFunctionMigrationSource(t, store, node)
	if err := store.RenewMigrationLease(ctx, source.LeaseToken, node, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkInstanceMigrating(ctx, source.InstanceID, node, source.LeaseToken); !errors.Is(err, state.ErrConflict) {
		t.Fatal("expired mark succeeded", err)
	}
}
