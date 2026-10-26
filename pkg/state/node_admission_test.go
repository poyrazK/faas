// adr: 1528
package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type nodeAdmissionTestStore = contractTestStore

func selectedNodeContext(ctx context.Context, node state.ComputeNode) context.Context {
	return state.WithNodeAdmission(ctx, state.NodeAdmission{NodeID: node.ID, Generation: node.LifecycleGeneration})
}

func seedAdmissionJob(t *testing.T, s state.Store) (state.Job, state.JobRun) {
	t.Helper()
	account, err := s.CreateAccount(t.Context(), uuid.NewString()+"@admission-job.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.JobCreate(t.Context(), account.ID, "admit-"+uuid.NewString()[:8], "batch", "ghcr.io/onebox-faas/test:latest", []string{"/bin/true"}, 128, 60, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.(state.JobImageMaterializationStore).JobSetImageMaterialization(t.Context(), job.ID, job.ImageRef, "ready", "sha256:"+strings.Repeat("a", 64), "jobs/"+job.ID+".ext4", ""); err != nil {
		t.Fatal(err)
	}
	run, _, err := s.JobRunCreate(t.Context(), job.ID, account.ID, "manual", nil, nil, nil, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	return job, run
}

func runNodeAdmissionTests(t *testing.T, create func(*testing.T) nodeAdmissionTestStore) {
	t.Helper()
	t.Run("all_resident_states_refused_on_closed_nodes", func(t *testing.T) {
		s := create(t)
		app, dep := completionApplication(t, s)
		for _, lifecycle := range []state.NodeLifecycle{state.NodeLifecycleDraining, state.NodeLifecycleForceDraining, state.NodeLifecycleUnavailable, state.NodeLifecycleMaintenance, state.NodeLifecycleRetired} {
			node := completionNode(t, s, state.NodeLifecycleActive)
			ctx := selectedNodeContext(t.Context(), node)
			node = completionTransition(t, s, node, lifecycle)
			for _, current := range []state.State{state.StateWaking, state.StateColdBooting, state.StateRunning, state.StateDraining, state.StateSnapshotting, state.StateMigrating, state.StateWarm, state.StateFrozen} {
				if _, err := s.CreateInstanceWithMode(ctx, app, dep, string(current), 128, node.ID, uuid.NewString(), string(state.InstanceModeNormal)); !errors.Is(err, state.ErrNodeAdmissionStale) {
					t.Fatalf("%s admitted %s: %v", lifecycle, current, err)
				}
			}
			// Old/direct entry points also validate current lifecycle.
			if _, err := s.CreateInstance(t.Context(), app, dep, string(state.StateColdBooting), 128, node.ID, uuid.NewString()); !errors.Is(err, state.ErrNodeAdmissionStale) {
				t.Fatalf("legacy insert on %s: %v", lifecycle, err)
			}
			rows, err := s.ListInstancesOnNodeID(t.Context(), node.ID)
			if err != nil || len(rows) != 0 {
				t.Fatalf("refusal left rows: %+v %v", rows, err)
			}
			if _, err := s.CreateInstance(t.Context(), app, dep, string(state.StateParked), 128, node.ID, uuid.NewString()); err != nil {
				t.Fatalf("nonresident inventory refused: %v", err)
			}
		}
	})
	t.Run("captured_generation_cannot_cross_same_state_cycles", func(t *testing.T) {
		s := create(t)
		app, dep := completionApplication(t, s)
		for _, lifecycle := range []state.NodeLifecycle{state.NodeLifecycleActive, state.NodeLifecycleRecovering} {
			node := completionNode(t, s, lifecycle)
			ctx := selectedNodeContext(t.Context(), node)
			if lifecycle == state.NodeLifecycleActive {
				node = completionTransition(t, s, node, state.NodeLifecycleUnavailable)
			}
			node = completionTransition(t, s, node, lifecycle)
			if _, err := s.CreateInstanceWithMode(ctx, app, dep, "cold_booting", 128, node.ID, uuid.NewString(), "normal"); !errors.Is(err, state.ErrNodeAdmissionStale) {
				t.Fatalf("obsolete %s generation accepted: %v", lifecycle, err)
			}
			if _, err := s.CreateInstanceWithMode(selectedNodeContext(t.Context(), node), app, dep, "cold_booting", 128, node.ID, uuid.NewString(), "normal"); err != nil {
				t.Fatalf("fresh %s generation refused: %v", lifecycle, err)
			}
		}
	})
	t.Run("metadata_preserves_admission_and_invalid_bindings_fail_closed", func(t *testing.T) {
		s := create(t)
		app, dep := completionApplication(t, s)
		node := completionNode(t, s, state.NodeLifecycleActive)
		ctx := selectedNodeContext(t.Context(), node)
		if err := s.HeartbeatComputeNode(t.Context(), node.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateInstance(ctx, app, dep, "cold_booting", 128, node.ID, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
		for _, binding := range []state.NodeAdmission{{NodeID: node.ID, Generation: 0}, {NodeID: node.ID, Generation: -1}, {NodeID: uuid.NewString(), Generation: node.LifecycleGeneration}} {
			bad := state.WithNodeAdmission(t.Context(), binding)
			if _, err := s.CreateInstance(bad, app, dep, "cold_booting", 128, node.ID, uuid.NewString()); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("invalid binding accepted: %+v %v", binding, err)
			}
		}
		missing := state.NodeAdmission{NodeID: uuid.NewString(), Generation: 1}
		if _, err := s.CreateInstance(state.WithNodeAdmission(t.Context(), missing), app, dep, "cold_booting", 128, missing.NodeID, uuid.NewString()); !errors.Is(err, state.ErrNodeAdmissionStale) {
			t.Fatalf("missing bound node: %v", err)
		}
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := s.CreateInstance(cancelled, app, dep, "cold_booting", 128, node.ID, uuid.NewString()); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled admission: %v", err)
		}
	})
	t.Run("existing_residency_can_finish_but_parked_cannot_readmit", func(t *testing.T) {
		s := create(t)
		app, dep := completionApplication(t, s)
		node := completionNode(t, s, state.NodeLifecycleActive)
		resident, err := s.CreateInstance(t.Context(), app, dep, "cold_booting", 128, node.ID, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		parked, err := s.CreateInstance(t.Context(), app, dep, "parked", 128, node.ID, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		node = completionTransition(t, s, node, state.NodeLifecycleDraining)
		if err := s.UpdateInstanceStateIf(t.Context(), resident.ID, "cold_booting", "running"); err != nil {
			t.Fatalf("existing boot cannot finish: %v", err)
		}
		if err := s.UpdateInstanceStateIf(t.Context(), parked.ID, "parked", "waking"); !errors.Is(err, state.ErrNodeAdmissionStale) {
			t.Fatalf("parked row readmitted on draining node: %v", err)
		}
		if row, err := s.InstanceByID(t.Context(), parked.ID); err != nil || row.State != "parked" {
			t.Fatalf("partial readmission: %+v %v", row, err)
		}
		if _, err := s.CompleteNodeLifecycle(t.Context(), completionInput(node)); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("drain ignored admitted boot: %v", err)
		}
	})
	t.Run("migration_destination_generation_is_transactional", func(t *testing.T) {
		s := create(t)
		app, dep := completionApplication(t, s)
		for _, withConfig := range []bool{false, true} {
			source := completionNode(t, s, state.NodeLifecycleActive)
			destination := completionNode(t, s, state.NodeLifecycleActive)
			ins, err := s.CreateInstance(t.Context(), app, dep, "running", 128, source.ID, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			lease := uuid.NewString()
			if err := s.MarkInstanceMigrating(t.Context(), ins.ID, source.ID, lease); err != nil {
				t.Fatal(err)
			}
			ctx := selectedNodeContext(t.Context(), destination)
			destination = completionTransition(t, s, destination, state.NodeLifecycleUnavailable)
			commit := func(ctx context.Context) error {
				if withConfig {
					return s.MigrateInstanceOwnerWithRuntimeConfig(ctx, ins.ID, source.ID, destination.ID, lease, state.RuntimeConfigMigration{ExpectedWakeID: ins.WakeID, WakeID: uuid.NewString()})
				}
				return s.MigrateInstanceOwner(ctx, ins.ID, source.ID, destination.ID, lease)
			}
			if err := commit(t.Context()); !errors.Is(err, state.ErrNodeAdmissionStale) {
				t.Fatalf("legacy destination write accepted: %v", err)
			}
			destination = completionTransition(t, s, destination, state.NodeLifecycleActive)
			if err := commit(ctx); !errors.Is(err, state.ErrNodeAdmissionStale) {
				t.Fatalf("migration crossed destination cycle: %v", err)
			}
			row, err := s.InstanceByID(t.Context(), ins.ID)
			if err != nil || row.NodeID != source.ID || row.State != "migrating" || row.WakeID != ins.WakeID {
				t.Fatalf("refusal partially moved source: %+v %v", row, err)
			}
			if err := commit(selectedNodeContext(t.Context(), destination)); err != nil {
				t.Fatal(err)
			}
			row, err = s.InstanceByID(t.Context(), ins.ID)
			if err != nil || row.NodeID != destination.ID || row.State != "running" {
				t.Fatalf("fresh migration failed: %+v %v", row, err)
			}
		}
	})
	t.Run("job_claim_and_legacy_job_insert_refuse_stale_nodes", func(t *testing.T) {
		s := create(t)
		job, run := seedAdmissionJob(t, s)
		node := completionNode(t, s, state.NodeLifecycleActive)
		ctx := selectedNodeContext(t.Context(), node)
		node = completionTransition(t, s, node, state.NodeLifecycleMaintenance)
		id := uuid.NewString()
		if _, err := s.CreateJobInstance(t.Context(), uuid.NewString(), job.ID, run.ID, 0, "cold_booting", 128, node.ID, uuid.NewString()); !errors.Is(err, state.ErrNodeAdmissionStale) {
			t.Fatalf("legacy job admission accepted: %v", err)
		}
		claim := func(ctx context.Context) (state.Instance, error) {
			return s.CreateAndClaimJobInstance(ctx, id, job.ID, run.ID, 0, "cold_booting", 128, node.ID, id, uuid.NewString(), time.Now().Add(time.Minute), node.ID)
		}
		if _, err := claim(ctx); !errors.Is(err, state.ErrNodeAdmissionStale) {
			t.Fatalf("closed job admission: %v", err)
		}
		node = completionTransition(t, s, node, state.NodeLifecycleActive)
		if _, err := claim(ctx); !errors.Is(err, state.ErrNodeAdmissionStale) {
			t.Fatalf("job crossed lifecycle cycle: %v", err)
		}
		task, err := s.JobTaskGet(t.Context(), run.ID, 0)
		if err != nil || task.Status != "queued" || task.InstanceID != nil || task.LeaseToken != nil || task.StartedAt != nil {
			t.Fatalf("failed admission claimed job: %+v %v", task, err)
		}
		if _, err := s.InstanceByID(t.Context(), id); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("failed admission leaked instance: %v", err)
		}
		if _, err := claim(selectedNodeContext(t.Context(), node)); err != nil {
			t.Fatal(err)
		}
		task, err = s.JobTaskGet(t.Context(), run.ID, 0)
		if err != nil || task.Status != "claimed" || task.InstanceID == nil || *task.InstanceID != id {
			t.Fatalf("fresh job claim failed: %+v %v", task, err)
		}
	})
}

// checkReservationAllowance pins ADR-909 amendment 1 on one store: without a
// placement binding the node holds Σ(ram_mb + overhead) up to its ceiling;
// a binding's reservation allowance raises that bound, clamped to
// api.MeasuredAdmissionMaxOvercommitPercent.
func checkReservationAllowance(t *testing.T, s state.Store) {
	t.Helper()
	const ceilingMB, ramMB = 1024, 248
	perInstance := ramMB + api.PerVMOverheadMB
	node, err := s.CreateComputeNode(t.Context(), state.ComputeNode{
		Name: "allowance-" + uuid.NewString(), Lifecycle: state.NodeLifecycleActive,
		TargetURL: "unix:///run/faas/vmmd.sock", VPCPUs: 4, VCPUBudget: 160,
		MemMB: 8192, AdmissionCeilingMB: ceilingMB, MaxConcurrency: 64,
	})
	if err != nil {
		t.Fatal(err)
	}
	app, dep := completionApplication(t, s)
	bound := func(percent int) context.Context {
		return state.WithNodeAdmission(t.Context(), state.NodeAdmission{
			NodeID: node.ID, Generation: node.LifecycleGeneration, ReservationPercent: percent,
		})
	}
	fill := func(ctx context.Context, step string) {
		t.Helper()
		for {
			_, err := s.CreateInstanceWithMode(ctx, app, dep, string(state.StateWaking), ramMB, node.ID, uuid.NewString(), string(state.InstanceModeNormal))
			if errors.Is(err, state.ErrNodeCapacity) {
				return
			}
			if err != nil {
				t.Fatalf("%s: %v", step, err)
			}
		}
	}
	used := func() int64 {
		t.Helper()
		mb, err := s.ComputeNodeUsedMB(t.Context(), node.ID)
		if err != nil {
			t.Fatal(err)
		}
		return mb
	}
	fill(t.Context(), "unbound")
	if got, want := used(), int64(ceilingMB/perInstance*perInstance); got != want {
		t.Fatalf("unbound admissions hold %d MB, want %d (the ceiling)", got, want)
	}
	fill(bound(0), "zero allowance")
	if got, want := used(), int64(ceilingMB/perInstance*perInstance); got != want {
		t.Fatalf("a zero allowance grew the node to %d MB, want %d", got, want)
	}
	capMB := ceilingMB * api.MeasuredAdmissionMaxOvercommitPercent / 100
	fill(bound(10*api.MeasuredAdmissionMaxOvercommitPercent), "clamped allowance")
	if got, want := used(), int64(capMB/perInstance*perInstance); got != want {
		t.Fatalf("an oversized allowance holds %d MB, want %d (clamped to %d%%)", got, want, api.MeasuredAdmissionMaxOvercommitPercent)
	}
	_, err = s.CreateInstanceWithMode(bound(api.MeasuredAdmissionMaxOvercommitPercent), app, dep, string(state.StateWaking), ramMB, node.ID, uuid.NewString(), string(state.InstanceModeNormal))
	if !errors.Is(err, state.ErrNodeCapacity) || !strings.Contains(err.Error(), "cap_mb=") {
		t.Fatalf("admission past the allowance = %v, want ErrNodeCapacity naming the cap", err)
	}
}

func TestNodeReservationAllowanceMemory(t *testing.T) {
	checkReservationAllowance(t, state.NewMemStore())
}

func TestNodeAdmissionMemory(t *testing.T) {
	runNodeAdmissionTests(t, func(*testing.T) nodeAdmissionTestStore { return state.NewMemStore() })
}
