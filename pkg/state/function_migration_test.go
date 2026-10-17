package state

// adr: 1575

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/quick"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func functionSourceFixture(t *testing.T) (*MemStore, FunctionMigrationSource) {
	t.Helper()
	ctx := t.Context()
	m := NewMemStore()
	node, err := m.ComputeNodeByName(ctx, DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	account, err := m.CreateAccount(ctx, uuid.NewString()+"@function-source.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, App{AccountID: account.ID, Slug: "source-" + uuid.NewString(), NodeID: node.ID, RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	ins, err := m.CreateInstance(ctx, app.ID, "", string(StateRunning), 128, node.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if ins.WakeID == "" {
		ins.WakeID = uuid.NewString()
		m.instances[ins.ID] = ins
	}
	source := FunctionMigrationSource{InstanceID: ins.ID, LeaseToken: FunctionMigrationTokenPrefix + uuid.NewString(), SourceNodeID: node.ID, SourceWakeID: ins.WakeID}
	if err := m.ReserveMigrationLease(ctx, MigrationLease{InstanceID: ins.ID, LeaseToken: source.LeaseToken, SourceNodeID: node.ID,
		CreatedAt: time.Now().Add(-time.Hour), LeaseExpiresAt: time.Now().Add(time.Minute), Pending: true}); err != nil {
		t.Fatal(err)
	}
	if err := m.RegisterFunctionMigrationSource(ctx, source, "capture/mem", "capture/vmstate"); err != nil {
		t.Fatal(err)
	}
	if err := m.CompleteMigrationLease(ctx, source.LeaseToken, "capture/mem", "capture/vmstate"); err != nil {
		t.Fatal(err)
	}
	return m, source
}

func TestFunctionMigrationSourceRollbackFence(t *testing.T) {
	for _, phaseTwo := range []bool{false, true} {
		for _, retired := range []bool{false, true} {
			t.Run(map[bool]string{true: "phase-two", false: "before-phase-two"}[phaseTwo]+map[bool]string{true: "/retired", false: "/recovered"}[retired], func(t *testing.T) {
				m, source := functionSourceFixture(t)
				ctx := t.Context()
				if phaseTwo {
					if err := m.PrepareFunctionMigrationSource(ctx, source.InstanceID, source.LeaseToken); err != nil {
						t.Fatal(err)
					}
					if err := m.MarkInstanceMigrating(ctx, source.InstanceID, source.SourceNodeID, source.LeaseToken); err != nil {
						t.Fatal(err)
					}
				}
				if err := m.CompleteFunctionMigrationSource(ctx, source.InstanceID, source.LeaseToken, retired); !errors.Is(err, ErrConflict) {
					t.Fatal("completed without rollback fence", err)
				}
				resolution, err := m.FenceFunctionMigrationSource(ctx, source.InstanceID, source.LeaseToken)
				if err != nil || resolution != MigrationCommitAborted {
					t.Fatal(resolution, err)
				}
				if err := m.PrepareFunctionMigrationSource(ctx, source.InstanceID, source.LeaseToken); err != nil {
					t.Fatal("late capture completion", err)
				}
				if err := m.MigrateInstanceOwner(ctx, source.InstanceID, source.SourceNodeID, uuid.NewString(), source.LeaseToken); !errors.Is(err, ErrConflict) {
					t.Fatal("late commit passed rollback fence", err)
				}
				if err := m.MarkInstanceMigrating(ctx, source.InstanceID, source.SourceNodeID, source.LeaseToken); !errors.Is(err, ErrConflict) {
					t.Fatal("late initial mark passed rollback fence", err)
				}
				ins, _ := m.MigrationInstanceByID(ctx, source.InstanceID)
				if ins.State != string(StateMigrating) || ins.LeaseToken != FunctionMigrationRollbackToken(source.LeaseToken) {
					t.Fatal("published source before physical confirmation", ins)
				}
				for range 2 {
					if err := m.CompleteFunctionMigrationSource(ctx, source.InstanceID, source.LeaseToken, retired); err != nil {
						t.Fatal(err)
					}
				}
				ins, _ = m.MigrationInstanceByID(ctx, source.InstanceID)
				want := string(StateRunning)
				if retired {
					want = string(StateStopped)
				}
				if ins.State != want || ins.WakeID != source.SourceWakeID || ins.NodeID != source.SourceNodeID || ins.LeaseToken != "" {
					t.Fatal("incorrect source completion", ins)
				}
				if err := m.DeleteMigrationLease(ctx, source.LeaseToken); err != nil {
					t.Fatal(err)
				}
				if err := m.MarkInstanceMigrating(ctx, source.InstanceID, source.SourceNodeID, source.LeaseToken); !errors.Is(err, ErrConflict) {
					t.Fatal("late mark reopened completed rollback", err)
				}
				if _, err := m.GetFunctionMigrationSource(ctx, source.InstanceID, source.LeaseToken); !errors.Is(err, ErrNotFound) {
					t.Fatal("source receipt survived parent cleanup", err)
				}
			})
		}
	}
}

func TestFunctionMigrationSourceMarkRequiresFreshCapture(t *testing.T) {
	m, source := functionSourceFixture(t)
	ctx := t.Context()
	if err := m.MarkInstanceMigrating(ctx, source.InstanceID, source.SourceNodeID, source.LeaseToken); !errors.Is(err, ErrConflict) {
		t.Fatal("marked before capture ACK", err)
	}
	if err := m.PrepareFunctionMigrationSource(ctx, source.InstanceID, source.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err := m.RenewMigrationLease(ctx, source.LeaseToken, source.SourceNodeID, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := m.MarkInstanceMigrating(ctx, source.InstanceID, source.SourceNodeID, source.LeaseToken); !errors.Is(err, ErrConflict) {
		t.Fatal("marked expired capture", err)
	}
}

func TestFunctionMigrationSourceCommitRollbackRace(t *testing.T) {
	// Exercise the ownership CAS and rollback lock in both scheduling orders.
	property := func(peerFirst bool) bool {
		m, source := functionSourceFixture(t)
		ctx := context.Background()
		if err := m.PrepareFunctionMigrationSource(ctx, source.InstanceID, source.LeaseToken); err != nil {
			t.Fatal(err)
		}
		if err := m.MarkInstanceMigrating(ctx, source.InstanceID, source.SourceNodeID, source.LeaseToken); err != nil {
			t.Fatal(err)
		}
		peer := uuid.NewString()
		var wg sync.WaitGroup
		var commitErr, fenceErr error
		var resolution MigrationCommitResolution
		start := make(chan struct{})
		commit := func() {
			defer wg.Done()
			<-start
			if !peerFirst {
				time.Sleep(time.Microsecond)
			}
			commitErr = m.MigrateInstanceOwner(ctx, source.InstanceID, source.SourceNodeID, peer, source.LeaseToken)
		}
		fence := func() {
			defer wg.Done()
			<-start
			if peerFirst {
				time.Sleep(time.Microsecond)
			}
			resolution, fenceErr = m.FenceFunctionMigrationSource(ctx, source.InstanceID, source.LeaseToken)
		}
		wg.Add(2)
		go commit()
		go fence()
		close(start)
		wg.Wait()
		ins, _ := m.MigrationInstanceByID(ctx, source.InstanceID)
		if fenceErr != nil {
			return false
		}
		if commitErr == nil {
			return resolution == MigrationCommitObsolete && ins.NodeID == peer
		}
		return errors.Is(commitErr, ErrConflict) && resolution == MigrationCommitAborted && ins.NodeID == source.SourceNodeID && ins.LeaseToken == FunctionMigrationRollbackToken(source.LeaseToken)
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 80}); err != nil {
		t.Fatal(err)
	}
}

func TestFunctionMigrationSourceStaleWakeAndExpiry(t *testing.T) {
	m, source := functionSourceFixture(t)
	ctx := t.Context()
	entries, err := m.ListExpiredFunctionMigrationSources(ctx, time.Now().Add(2*time.Minute), 1)
	if err != nil || len(entries) != 1 || entries[0].LeaseToken != source.LeaseToken {
		t.Fatal(entries, err)
	}
	// Expired source ownership cannot be overwritten by a fresh prepare.
	if err := m.ReserveMigrationLease(ctx, MigrationLease{InstanceID: source.InstanceID, LeaseToken: uuid.NewString(), CreatedAt: time.Now().Add(time.Hour), LeaseExpiresAt: time.Now().Add(time.Minute)}); !errors.Is(err, ErrConflict) {
		t.Fatal("replaced unresolved source", err)
	}
	ins := m.instances[source.InstanceID]
	ins.WakeID = uuid.NewString()
	m.instances[ins.ID] = ins
	resolution, err := m.FenceFunctionMigrationSource(ctx, ins.ID, source.LeaseToken)
	if err != nil || resolution != MigrationCommitRetained {
		t.Fatal("stale source acquired a replacement", resolution, err)
	}
	if current := m.instances[ins.ID]; current.State != ins.State || current.WakeID != ins.WakeID || current.LeaseToken != ins.LeaseToken {
		t.Fatal("stale source mutated a replacement")
	}
}
