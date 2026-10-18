// adr: 1530
package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func sourceRecord(t *testing.T, f migrationAdmissionFixture) state.MigrationSource {
	t.Helper()
	r, err := f.s.MigrationSourceByLease(t.Context(), f.plan.LeaseToken)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func runMigrationSourceTests(t *testing.T, create func(*testing.T) migrationAdmissionStore) {
	t.Helper()
	t.Run("binding_and_receipts_are_immutable", func(t *testing.T) {
		f := newMigrationAdmissionFixture(t, create(t))
		r := sourceRecord(t, f)
		if replay, err := f.s.BindMigrationSource(t.Context(), r.MigrationSourceBinding); err != nil || replay != r {
			t.Fatalf("binding replay: %+v %v", replay, err)
		}
		b := r.MigrationSourceBinding
		b.NativeGeneration = uuid.NewString()
		if _, err := f.s.BindMigrationSource(t.Context(), b); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("binding was replaceable: %v", err)
		}
		if _, err := f.s.RequestMigrationSourceRetirement(t.Context(), r.LeaseToken, r.SourceNodeID); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("early retirement destroyed source: %v", err)
		}
		if err := f.s.DeleteMigrationLease(t.Context(), r.LeaseToken); err == nil {
			t.Fatal("bound source lost its lease")
		}
		if err := f.s.CompleteMigrationLease(t.Context(), r.LeaseToken, "wrong/mem", "wrong/state"); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("source lost original cleanup handles: %v", err)
		}
		if err := f.s.AcknowledgeMigrationSource(t.Context(), r.MigrationSourceBinding, state.MigrationSourceResumed, "unchecked-resume"); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("resume without fence: %v", err)
		}
	})
	t.Run("bounded_lease_recovery_includes_terminal_snapshot_cleanup", func(t *testing.T) {
		f := newMigrationAdmissionFixture(t, create(t))
		r := sourceRecord(t, f)
		rows, err := f.s.ListMigrationSourceLeasesForRecovery(t.Context(), f.source.ID, time.Now(), "", 1)
		if err != nil || len(rows) != 0 {
			t.Fatalf("unexpired bound capture recovered early: %+v %v", rows, err)
		}
		f.reserve(t)
		f.adopt(t)
		if err := f.commit(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := f.s.AcknowledgeMigrationSource(t.Context(), r.MigrationSourceBinding, state.MigrationSourceRetired, "original-resource-removal"); err != nil {
			t.Fatal(err)
		}
		rows, err = f.s.ListMigrationSourceLeasesForRecovery(t.Context(), f.source.ID, time.Now(), "", 1)
		if err != nil || len(rows) != 1 || rows[0].MemStorageKey != f.plan.MemStorageKey {
			t.Fatalf("terminal source lost pending snapshot cleanup: %+v %v", rows, err)
		}
		if err := f.s.ReserveMigrationLease(t.Context(), state.MigrationLease{InstanceID: r.InstanceID, SourceNodeID: r.SourceNodeID, LeaseToken: uuid.NewString(), CreatedAt: time.Now().Add(time.Hour), LeaseExpiresAt: time.Now().Add(2 * time.Hour)}); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("expired lease replacement erased source handles: %v", err)
		}
		if err := f.s.DeleteMigrationLease(t.Context(), r.LeaseToken); err != nil {
			t.Fatal(err)
		}
		rows, err = f.s.ListMigrationSourceLeasesForRecovery(t.Context(), f.source.ID, time.Now(), "", 1)
		if err != nil || len(rows) != 0 {
			t.Fatalf("finished lease replayed effects: %+v %v", rows, err)
		}
	})
	t.Run("parked_rollback_requires_retirement_instead_of_resume", func(t *testing.T) {
		f := newMigrationAdmissionFixture(t, create(t))
		r := sourceRecord(t, f)
		if err := f.s.CancelInstanceMigration(t.Context(), f.instance.ID, f.source.ID, f.plan.LeaseToken); err != nil {
			t.Fatal(err)
		}
		if safe, err := f.s.FenceMigrationSourceResume(t.Context(), r.InstanceID, r.LeaseToken); err != nil || !safe {
			t.Fatalf("source cancellation fence: %t %v", safe, err)
		}
		if err := f.s.AcknowledgeMigrationSource(t.Context(), r.MigrationSourceBinding, state.MigrationSourceResumed, "unpublished-resume"); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("parked acquired unaccounted RAM: %v", err)
		}
		if used, err := f.s.ComputeNodeUsedMB(t.Context(), f.source.ID); err != nil || used < int64(r.RAMMB) {
			t.Fatalf("parked rollback lost source charge: %d %v", used, err)
		}
		if _, err := f.s.RequestMigrationSourceRetirement(t.Context(), r.LeaseToken, r.SourceNodeID); err != nil {
			t.Fatal(err)
		}
		if err := f.s.AcknowledgeMigrationSource(t.Context(), r.MigrationSourceBinding, state.MigrationSourceRetired, "original-stopped"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("resume_fences_delayed_source_marker", func(t *testing.T) {
		f := newMigrationAdmissionFixture(t, create(t))
		r := sourceRecord(t, f)
		if err := f.s.UpdateInstanceState(t.Context(), r.InstanceID, string(state.StateRunning)); err != nil {
			t.Fatal(err)
		}
		if safe, err := f.s.FenceMigrationSourceResume(t.Context(), r.InstanceID, r.LeaseToken); err != nil || !safe {
			t.Fatalf("source resume fence: %t %v", safe, err)
		}
		if err := f.s.MarkInstanceMigrating(t.Context(), r.InstanceID, r.SourceNodeID, r.LeaseToken); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("delayed marker crossed resume fence: %v", err)
		}
		if err := f.s.AcknowledgeMigrationSource(t.Context(), r.MigrationSourceBinding, state.MigrationSourceResumed, "original-still-running"); err != nil {
			t.Fatal(err)
		}
		if err := f.s.DeleteMigrationLease(t.Context(), r.LeaseToken); err != nil {
			t.Fatal(err)
		}
		if err := f.s.MarkInstanceMigrating(t.Context(), r.InstanceID, r.SourceNodeID, r.LeaseToken); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("delayed marker erased resumed ownership: %v", err)
		}
		i, err := f.s.InstanceByID(t.Context(), r.InstanceID)
		if err != nil || i.State != string(state.StateRunning) {
			t.Fatalf("resume lost its serving owner: %+v %v", i, err)
		}
	})
	t.Run("commit_preserves_source_capacity_and_blocks_maintenance", func(t *testing.T) {
		f := newMigrationAdmissionFixture(t, create(t))
		r := sourceRecord(t, f)
		f.reserve(t)
		f.adopt(t)
		if err := f.commit(t.Context()); err != nil {
			t.Fatal(err)
		}
		if sourceRecord(t, f).Phase != state.MigrationSourceRetiring {
			t.Fatal("ownership committed without source retirement")
		}
		used, err := f.s.ComputeNodeUsedMB(t.Context(), f.source.ID)
		if err != nil || used != int64(r.RAMMB) {
			t.Fatalf("source resources disappeared: %d %v", used, err)
		}
		cpu, err := f.s.ComputeNodeUsedCPUMillicoresByNode(t.Context(), []string{f.source.ID})
		if err != nil || cpu[f.source.ID] != int64(r.CPUMillicores) {
			t.Fatalf("source CPU disappeared: %v %v", cpu, err)
		}
		n := completionTransition(t, f.s, f.source, state.NodeLifecycleDraining)
		if _, err := f.s.CompleteNodeLifecycle(t.Context(), completionInput(n)); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("maintenance passed unretired source: %v", err)
		}
		if err := f.s.NodeSetLifecycle(t.Context(), n.ID, n.Lifecycle, state.NodeLifecycleRetired); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("direct retirement passed source: %v", err)
		}
		if safe, err := f.s.FenceMigrationSourceResume(t.Context(), r.InstanceID, r.LeaseToken); err != nil || safe {
			t.Fatalf("committed source resumed: %t %v", safe, err)
		}
		b := r.MigrationSourceBinding
		b.PhysicalBinding = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
		if err := f.s.AcknowledgeMigrationSource(t.Context(), b, state.MigrationSourceRetired, "wrong-binding"); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("wrong proof released capacity: %v", err)
		}
		if err := f.s.AcknowledgeMigrationSource(t.Context(), r.MigrationSourceBinding, state.MigrationSourceRetired, "original-physical-retirement"); err != nil {
			t.Fatal(err)
		}
		if err := f.s.AcknowledgeMigrationSource(t.Context(), r.MigrationSourceBinding, state.MigrationSourceRetired, "original-physical-retirement"); err != nil {
			t.Fatal(err)
		}
		if err := f.s.AcknowledgeMigrationSource(t.Context(), r.MigrationSourceBinding, state.MigrationSourceRetired, "changed-proof"); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("retirement proof changed: %v", err)
		}
		if used, err := f.s.ComputeNodeUsedMB(t.Context(), f.source.ID); err != nil || used != 0 {
			t.Fatalf("retired source remains charged: %d %v", used, err)
		}
		if err := f.s.DeleteMigrationLease(t.Context(), r.LeaseToken); err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.CompleteNodeLifecycle(t.Context(), completionInput(n)); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("pending_source_contends_with_ordinary_admission", func(t *testing.T) {
		for _, resource := range []string{"ram", "cpu", "vcpu"} {
			t.Run(resource, func(t *testing.T) {
				f := newMigrationAdmissionFixture(t, create(t))
				r := sourceRecord(t, f)
				f.reserve(t)
				f.adopt(t)
				if err := f.commit(t.Context()); err != nil {
					t.Fatal(err)
				}
				n := f.source
				switch resource {
				case "ram":
					n.AdmissionCeilingMB = r.RAMMB
				case "cpu":
					n.VPCPUs = 1
				case "vcpu":
					n.VCPUBudget = r.VCPU
				}
				if _, err := f.s.UpsertComputeNodeFromOperator(t.Context(), n); err != nil {
					t.Fatal(err)
				}
				if _, err := f.s.CreateInstance(t.Context(), f.instance.AppID, f.instance.DeploymentID, "running", 128, f.source.ID, uuid.NewString()); !errors.Is(err, state.ErrNodeCapacity) {
					t.Fatalf("ordinary admission stole %s: %v", resource, err)
				}
			})
		}
	})
	t.Run("cancel_fences_late_commit_and_releases_only_original", func(t *testing.T) {
		f := newMigrationAdmissionFixture(t, create(t))
		r := sourceRecord(t, f)
		f.reserve(t)
		if _, err := f.s.RequestMigrationAdmissionCleanup(t.Context(), f.plan.ID, f.dest.ID); err != nil {
			t.Fatal(err)
		}
		if safe, err := f.s.FenceMigrationSourceResume(t.Context(), r.InstanceID, r.LeaseToken); err != nil || !safe {
			t.Fatalf("source resume fence: %t %v", safe, err)
		}
		if err := f.s.UpdateInstanceState(t.Context(), r.InstanceID, string(state.StateRunning)); err != nil {
			t.Fatal(err)
		}
		if err := f.s.AcknowledgeMigrationSource(t.Context(), r.MigrationSourceBinding, state.MigrationSourceResumed, "original-resumed"); err != nil {
			t.Fatal(err)
		}
		if err := f.commit(t.Context()); err == nil {
			t.Fatal("commit followed resume")
		}
		if err := f.s.DeleteMigrationLease(t.Context(), r.LeaseToken); err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.RequestMigrationSourceRetirement(t.Context(), r.LeaseToken, r.SourceNodeID); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("cancelled source later retired: %v", err)
		}
	})
	t.Run("protected_capacity_retains_source_until_physical_proof", func(t *testing.T) {
		f := newMigrationAdmissionFixture(t, create(t))
		if _, err := f.s.SetServiceCapacityProtection(t.Context(), true); err != nil {
			t.Fatal(err)
		}
		f.reserve(t)
		f.adopt(t)
		before, err := f.s.ServiceCapacityPlacement(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if err := f.commit(t.Context()); err != nil {
			t.Fatal(err)
		}
		after, err := f.s.ServiceCapacityPlacement(t.Context())
		if err != nil || after.Nodes[f.source.ID] != before.Nodes[f.source.ID] {
			t.Fatalf("ownership movement freed protected source headroom: %+v %+v %v", before, after, err)
		}
		r := sourceRecord(t, f)
		if err := f.s.AcknowledgeMigrationSource(t.Context(), r.MigrationSourceBinding, state.MigrationSourceRetired, "source-physical-proof"); err != nil {
			t.Fatal(err)
		}
		retired, err := f.s.ServiceCapacityPlacement(t.Context())
		if err != nil || retired.Nodes[f.source.ID].Slots <= after.Nodes[f.source.ID].Slots {
			t.Fatalf("retirement did not release protected headroom: %+v %v", retired, err)
		}
	})
	t.Run("retiring_records_recover_immediately_and_keep_fair_cursor", func(t *testing.T) {
		f := newMigrationAdmissionFixture(t, create(t))
		f.reserve(t)
		f.adopt(t)
		if err := f.commit(t.Context()); err != nil {
			t.Fatal(err)
		}
		rows, err := f.s.ListMigrationSourcesForRecovery(t.Context(), f.source.ID, time.Now(), "", 1)
		if err != nil || len(rows) != 1 || rows[0].Phase != state.MigrationSourceRetiring {
			t.Fatalf("unexpired committed source not recovered: %+v %v", rows, err)
		}
		rows, err = f.s.ListMigrationSourcesForRecovery(t.Context(), f.source.ID, time.Now(), f.plan.LeaseToken, 1)
		if err != nil || len(rows) != 0 {
			t.Fatalf("cursor did not advance: %+v %v", rows, err)
		}
		rows, err = f.s.ListMigrationSourcesForRecovery(t.Context(), f.dest.ID, time.Now().Add(time.Hour), "", 1)
		if err != nil || len(rows) != 0 {
			t.Fatalf("another node recovered source: %+v %v", rows, err)
		}
	})
	t.Run("resume_and_retirement_are_mutually_fenced", func(t *testing.T) {
		for _, resumeFirst := range []bool{false, true} {
			t.Run(map[bool]string{false: "retirement_first", true: "resume_first"}[resumeFirst], func(t *testing.T) {
				f := newMigrationAdmissionFixture(t, create(t))
				if err := f.s.CancelInstanceMigration(t.Context(), f.instance.ID, f.source.ID, f.plan.LeaseToken); err != nil {
					t.Fatal(err)
				}
				if resumeFirst {
					if err := f.s.UpdateInstanceState(t.Context(), f.instance.ID, string(state.StateRunning)); err != nil {
						t.Fatal(err)
					}
					if safe, err := f.s.FenceMigrationSourceResume(t.Context(), f.instance.ID, f.plan.LeaseToken); err != nil || !safe {
						t.Fatalf("resume fence: %t %v", safe, err)
					}
					if _, err := f.s.RequestMigrationSourceRetirement(t.Context(), f.plan.LeaseToken, f.source.ID); !errors.Is(err, state.ErrConflict) {
						t.Fatalf("retirement crossed resume fence: %v", err)
					}
				} else {
					if _, err := f.s.RequestMigrationSourceRetirement(t.Context(), f.plan.LeaseToken, f.source.ID); err != nil {
						t.Fatal(err)
					}
					if safe, err := f.s.FenceMigrationSourceResume(t.Context(), f.instance.ID, f.plan.LeaseToken); err != nil || safe {
						t.Fatalf("resume crossed retirement fence: %t %v", safe, err)
					}
				}
			})
		}
	})
}

func TestMigrationSourceMemory(t *testing.T) {
	runMigrationSourceTests(t, func(*testing.T) migrationAdmissionStore { return state.NewMemStore() })
}
