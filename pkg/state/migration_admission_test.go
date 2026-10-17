// adr: 1529
package state_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type migrationAdmissionStore interface {
	state.Store
	state.ComputeNodeUsageBatcher
	state.ComputeNodeCPUUsageBatcher
	state.MigrationAdmissionStore
	state.MigrationLeaseStore
	state.MigrationSourceStore
	state.RuntimeConfigMigrationStore
	state.NodeLifecycleCompletionStore
	state.MigrationCommitRecoveryStore
}

type migrationAdmissionFixture struct {
	s            migrationAdmissionStore
	source, dest state.ComputeNode
	instance     state.Instance
	plan         state.MigrationAdmissionPlan
}

func newMigrationAdmissionFixture(t *testing.T, s migrationAdmissionStore) migrationAdmissionFixture {
	t.Helper()
	app, dep := completionApplication(t, s)
	source, dest := completionNode(t, s, state.NodeLifecycleActive), completionNode(t, s, state.NodeLifecycleActive)
	i, err := s.CreateInstance(t.Context(), app, dep, "running", 128, source.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	lease := state.MigrationLease{InstanceID: i.ID, LeaseToken: uuid.NewString(), SourceNodeID: source.ID, CreatedAt: time.Now(), LeaseExpiresAt: time.Now().Add(time.Minute), Pending: true}
	if err := s.ReserveMigrationLease(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindMigrationSource(t.Context(), state.MigrationSourceBinding{InstanceID: i.ID, SourceNodeID: source.ID, SourceWakeID: i.WakeID, LeaseToken: lease.LeaseToken, NativeGeneration: uuid.NewString(), PhysicalBinding: strings.Repeat("c", 64)}); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteMigrationLease(t.Context(), lease.LeaseToken, "migration/mem", "migration/state"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkInstanceMigrating(t.Context(), i.ID, source.ID, lease.LeaseToken); err != nil {
		t.Fatal(err)
	}
	p := state.MigrationAdmissionPlan{ID: uuid.NewString(), InstanceID: i.ID, SourceNodeID: source.ID, DestinationNodeID: dest.ID, SourceWakeID: i.WakeID, DestinationWakeID: uuid.NewString(), LeaseToken: lease.LeaseToken, MemStorageKey: "migration/mem", VMStateStorageKey: "migration/state", RequestDigest: strings.Repeat("a", 64), DestinationGeneration: dest.LifecycleGeneration, RAMMB: 128 + api.PerVMOverheadMB, CPUMillicores: api.DefaultAppCPUMillicores, VCPU: api.MustLimitsFor(api.PlanPro).VCPU, ExpiresAt: time.Now().Add(30 * time.Second).UTC().Truncate(time.Microsecond)}
	return migrationAdmissionFixture{s: s, source: source, dest: dest, instance: i, plan: p}
}

func (f migrationAdmissionFixture) reserve(t *testing.T) state.MigrationAdmission {
	t.Helper()
	a, err := f.s.ReserveMigrationAdmission(t.Context(), f.plan)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func (f migrationAdmissionFixture) adopt(t *testing.T) {
	t.Helper()
	a, execute, err := f.s.StartMigrationAdoption(t.Context(), f.plan.ID, f.dest.ID, f.plan.RequestDigest)
	if err != nil || !execute || a.Phase != state.MigrationAdopting {
		t.Fatalf("start: %+v %t %v", a, execute, err)
	}
	if err := f.s.AcknowledgeMigrationAdoption(t.Context(), f.plan.ID, f.dest.ID, f.plan.RequestDigest, []byte("original-receipt")); err != nil {
		t.Fatal(err)
	}
}

func (f migrationAdmissionFixture) commit(ctx context.Context) error {
	ctx = state.WithNodeAdmission(ctx, state.NodeAdmission{NodeID: f.dest.ID, Generation: f.plan.DestinationGeneration})
	ctx = state.WithMigrationAdmission(ctx, f.plan.ID)
	return f.s.MigrateInstanceOwnerWithRuntimeConfig(ctx, f.instance.ID, f.source.ID, f.dest.ID, f.plan.LeaseToken, state.RuntimeConfigMigration{ExpectedWakeID: f.plan.SourceWakeID, WakeID: f.plan.DestinationWakeID})
}

func runMigrationAdmissionTests(t *testing.T, newStore func(*testing.T) migrationAdmissionStore) {
	t.Helper()
	t.Run("reservation_blocks_drain_until_unstarted_cancellation", func(t *testing.T) {
		f := newMigrationAdmissionFixture(t, newStore(t))
		f.reserve(t)
		p := f.plan
		p.RequestDigest = strings.Repeat("b", 64)
		if _, err := f.s.ReserveMigrationAdmission(t.Context(), p); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("changed replay: %v", err)
		}
		n := completionTransition(t, f.s, f.dest, state.NodeLifecycleDraining)
		if _, err := f.s.CompleteNodeLifecycle(t.Context(), completionInput(n)); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("drain ignored inbound reservation: %v", err)
		}
		if _, _, err := f.s.StartMigrationAdoption(t.Context(), f.plan.ID, f.dest.ID, f.plan.RequestDigest); !errors.Is(err, state.ErrNodeAdmissionStale) {
			t.Fatalf("closed destination dispatched: %v", err)
		}
		a, err := f.s.RequestMigrationAdmissionCleanup(t.Context(), f.plan.ID, f.dest.ID)
		if err != nil || a.Phase != state.MigrationCleaned || a.CleanupProof != "no_dispatch" {
			t.Fatalf("cancel: %+v %v", a, err)
		}
		if _, err := f.s.CompleteNodeLifecycle(t.Context(), completionInput(n)); err != nil {
			t.Fatal(err)
		}
		replay, err := f.s.ReserveMigrationAdmission(t.Context(), f.plan)
		if err != nil || replay.Phase != state.MigrationCleaned {
			t.Fatalf("terminal replay: %+v %v", replay, err)
		}
	})
	t.Run("unknown_dispatch_retains_capacity_and_source", func(t *testing.T) {
		f := newMigrationAdmissionFixture(t, newStore(t))
		f.plan.ExpiresAt = time.Now().Add(200 * time.Millisecond).UTC().Truncate(time.Microsecond)
		f.reserve(t)
		if _, execute, err := f.s.StartMigrationAdoption(t.Context(), f.plan.ID, f.dest.ID, f.plan.RequestDigest); err != nil || !execute {
			t.Fatalf("start: %t %v", execute, err)
		}
		if _, execute, err := f.s.StartMigrationAdoption(t.Context(), f.plan.ID, f.dest.ID, f.plan.RequestDigest); !errors.Is(err, state.ErrMigrationAdoptionUnresolved) || execute {
			t.Fatalf("unknown effect replayed: %t %v", execute, err)
		}
		if safe, err := f.s.FenceMigrationSourceResume(t.Context(), f.instance.ID, f.plan.LeaseToken); err != nil || safe {
			t.Fatalf("source resumed with unknown destination: %t %v", safe, err)
		}
		if err := f.s.DeleteMigrationLease(t.Context(), f.plan.LeaseToken); err == nil {
			t.Fatal("pending admission lost source lease")
		}
		for name, effect := range map[string]func() error{
			"cancel": func() error {
				return f.s.CancelInstanceMigration(t.Context(), f.instance.ID, f.source.ID, f.plan.LeaseToken)
			},
			"watchdog_reinvite": func() error { return f.s.ReinviteMigratingInstance(t.Context(), f.instance.ID, f.plan.LeaseToken) },
			"watchdog_abort":    func() error { return f.s.AbortMigratingInstance(t.Context(), f.instance.ID, f.plan.LeaseToken) },
			"delete":            func() error { return f.s.DeleteInstance(t.Context(), f.instance.ID) },
		} {
			if err := effect(); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("%s lost unresolved ownership: %v", name, err)
			}
		}
		time.Sleep(250 * time.Millisecond)
		rows, err := f.s.ListMigrationAdmissionsForRecovery(t.Context(), f.dest.ID, time.Now(), "", api.MigrationAdmissionRecoveryBatchMax)
		if err != nil || len(rows) != 1 || rows[0].Phase != state.MigrationAdopting {
			t.Fatalf("expiry lost evidence: %+v %v", rows, err)
		}
		a, err := f.s.RequestMigrationAdmissionCleanup(t.Context(), f.plan.ID, f.dest.ID)
		if err != nil || a.Phase != state.MigrationCleaning {
			t.Fatalf("cleanup: %+v %v", a, err)
		}
		if _, _, err := f.s.StartMigrationAdoption(t.Context(), f.plan.ID, f.dest.ID, f.plan.RequestDigest); err == nil {
			t.Fatal("revoked attempt dispatched")
		}
		if err := f.s.AcknowledgeMigrationAdmissionCleanup(t.Context(), f.plan.ID, f.dest.ID, "retired-original-generation"); err != nil {
			t.Fatal(err)
		}
		if safe, err := f.s.FenceMigrationSourceResume(t.Context(), f.instance.ID, f.plan.LeaseToken); err != nil || !safe {
			t.Fatalf("cleaned source remains fenced: %t %v", safe, err)
		}
		p := f.plan
		p.ID = uuid.NewString()
		p.ExpiresAt = time.Now().Add(time.Second)
		if _, err := f.s.ReserveMigrationAdmission(t.Context(), p); err == nil {
			t.Fatal("delayed reservation followed source resume")
		}
	})
	t.Run("receipt_replay_atomic_ownership_and_commit_resolution", func(t *testing.T) {
		f := newMigrationAdmissionFixture(t, newStore(t))
		f.reserve(t)
		f.adopt(t)
		a, execute, err := f.s.StartMigrationAdoption(t.Context(), f.plan.ID, f.dest.ID, f.plan.RequestDigest)
		if err != nil || execute || !bytes.Equal(a.AdoptionReceipt, []byte("original-receipt")) {
			t.Fatalf("receipt replay: %+v %t %v", a, execute, err)
		}
		if err := f.s.AcknowledgeMigrationAdoption(t.Context(), f.plan.ID, f.dest.ID, f.plan.RequestDigest, []byte("changed")); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("receipt overwritten: %v", err)
		}
		if err := f.commit(t.Context()); err != nil {
			t.Fatal(err)
		}
		a, err = f.s.MigrationAdmissionByID(t.Context(), f.plan.ID)
		if err != nil || a.Phase != state.MigrationCommitted {
			t.Fatalf("reservation not consumed: %+v %v", a, err)
		}
		a, err = f.s.RequestMigrationAdmissionCleanup(t.Context(), f.plan.ID, f.dest.ID)
		if err != nil || a.Phase != state.MigrationCommitted {
			t.Fatalf("committed VM revoked: %+v %v", a, err)
		}
		resolution, err := f.s.ResolveInstanceMigrationCommit(t.Context(), state.MigrationCommitAttempt{InstanceID: f.instance.ID, SourceNodeID: f.source.ID, DestinationNodeID: f.dest.ID, LeaseToken: f.plan.LeaseToken, SourceWakeID: f.plan.SourceWakeID, DestinationWakeID: f.plan.DestinationWakeID})
		if err != nil || resolution != state.MigrationCommitRecovered {
			t.Fatalf("lost commit recovery: %s %v", resolution, err)
		}
		if safe, err := f.s.FenceMigrationSourceResume(t.Context(), f.instance.ID, f.plan.LeaseToken); err != nil || safe {
			t.Fatalf("committed source resumed: %t %v", safe, err)
		}
		used, err := f.s.ComputeNodeUsedMB(t.Context(), f.dest.ID)
		if err != nil || used != int64(f.plan.RAMMB) {
			t.Fatalf("ownership double charged: %d %v", used, err)
		}
	})
	t.Run("cleanup_fences_late_commit_and_new_source_lease", func(t *testing.T) {
		f := newMigrationAdmissionFixture(t, newStore(t))
		f.reserve(t)
		f.adopt(t)
		if _, err := f.s.RequestMigrationAdmissionCleanup(t.Context(), f.plan.ID, f.dest.ID); err != nil {
			t.Fatal(err)
		}
		if err := f.commit(t.Context()); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("late commit escaped cleanup: %v", err)
		}
		if _, err := f.s.ResolveInstanceMigrationCommit(t.Context(), state.MigrationCommitAttempt{InstanceID: f.instance.ID, SourceNodeID: f.source.ID, DestinationNodeID: f.dest.ID, LeaseToken: f.plan.LeaseToken, SourceWakeID: f.plan.SourceWakeID, DestinationWakeID: f.plan.DestinationWakeID}); !errors.Is(err, state.ErrMigrationCommitUnresolved) {
			t.Fatalf("unproven cleanup resumed source: %v", err)
		}
		if err := f.s.ReserveMigrationLease(t.Context(), state.MigrationLease{InstanceID: f.instance.ID, LeaseToken: uuid.NewString(), SourceNodeID: f.source.ID, CreatedAt: time.Now().Add(time.Hour), LeaseExpiresAt: time.Now().Add(2 * time.Hour)}); err == nil {
			t.Fatal("expired source lease replaced with pending destination")
		}
		if err := f.s.AcknowledgeMigrationAdmissionCleanup(t.Context(), f.plan.ID, f.dest.ID, "original-cleanup"); err != nil {
			t.Fatal(err)
		}
		if err := f.s.AcknowledgeMigrationAdmissionCleanup(t.Context(), f.plan.ID, f.dest.ID, "changed-cleanup"); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("cleanup receipt overwritten: %v", err)
		}
	})
	t.Run("ordinary_admission_cannot_spend_inbound_capacity", func(t *testing.T) {
		for _, resource := range []string{"ram", "cpu", "vcpu"} {
			t.Run(resource, func(t *testing.T) {
				f := newMigrationAdmissionFixture(t, newStore(t))
				n := f.dest
				switch resource {
				case "ram":
					n.AdmissionCeilingMB = f.plan.RAMMB
				case "cpu":
					n.VPCPUs = 1
				case "vcpu":
					n.VCPUBudget = f.plan.VCPU
				}
				if _, err := f.s.UpsertComputeNodeFromOperator(t.Context(), n); err != nil {
					t.Fatal(err)
				}
				f.reserve(t)
				if _, err := f.s.CreateInstance(t.Context(), f.instance.AppID, f.instance.DeploymentID, "cold_booting", 128, f.dest.ID, uuid.NewString()); !errors.Is(err, state.ErrNodeCapacity) {
					t.Fatalf("ordinary admission spent reserved capacity: %v", err)
				}
			})
		}
	})
	t.Run("ownership_keeps_admitted_resource_bounds", func(t *testing.T) {
		f := newMigrationAdmissionFixture(t, newStore(t))
		f.plan.RAMMB += 128
		f.plan.CPUMillicores += 1000
		f.plan.VCPU++
		n := f.dest
		n.AdmissionCeilingMB = f.plan.RAMMB
		if _, err := f.s.UpsertComputeNodeFromOperator(t.Context(), n); err != nil {
			t.Fatal(err)
		}
		f.reserve(t)
		f.adopt(t)
		if err := f.commit(t.Context()); err != nil {
			t.Fatal(err)
		}
		used, err := f.s.ComputeNodeUsedMB(t.Context(), f.dest.ID)
		if err != nil || used != int64(f.plan.RAMMB) {
			t.Fatalf("ownership freed admitted RAM: %d %v", used, err)
		}
		bulk, err := f.s.ComputeNodeUsedMBByNode(t.Context(), []string{f.dest.ID})
		if err != nil || bulk[f.dest.ID] != used {
			t.Fatalf("bulk ownership RAM differs: %+v %v", bulk, err)
		}
		cpu, err := f.s.ComputeNodeUsedCPUMillicoresByNode(t.Context(), []string{f.dest.ID})
		if err != nil || cpu[f.dest.ID] != int64(f.plan.CPUMillicores) {
			t.Fatalf("ownership freed admitted CPU: %+v %v", cpu, err)
		}
		if _, err := f.s.CreateInstance(t.Context(), f.instance.AppID, f.instance.DeploymentID, "running", 128, f.dest.ID, uuid.NewString()); !errors.Is(err, state.ErrNodeCapacity) {
			t.Fatal("ordinary admission spent owned reservation", err)
		}
	})
}

func TestMigrationAdmissionMemory(t *testing.T) {
	runMigrationAdmissionTests(t, func(*testing.T) migrationAdmissionStore { return state.NewMemStore() })
}
