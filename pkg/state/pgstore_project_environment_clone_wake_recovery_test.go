//go:build !no_pg

// adr: 590
package state_test

import (
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgCloneWakeRecoveryOriginalOwnershipAndHandoff(t *testing.T) {
	s, pool, l := pgWorkloadRetirementFixture(t, "deployed")
	ctx := t.Context()
	in, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	w := in.Plan.Workloads[0]
	node := resolveDefaultLocal(t, ctx, s)
	expected := map[string]state.ProjectEnvironmentCloneWakeDispatch{}
	record := func(dep, reason, method string) string {
		t.Helper()
		ins, err := s.CreateInstance(ctx, w.AppID, dep, string(state.StateColdBooting), 128, node, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		if err = s.RecordCloneCheckpointWakeDispatch(ctx, ins.ID, ins.AppID, dep, node, method); err != nil {
			t.Fatal(err)
		}
		var at time.Time
		if err = pool.QueryRow(ctx, "SELECT dispatched_at FROM project_environment_clone_wake_dispatches WHERE instance_id=$1", ins.ID).Scan(&at); err != nil {
			t.Fatal(err)
		}
		expected[ins.ID] = state.ProjectEnvironmentCloneWakeDispatch{InstanceID: ins.ID, AppID: ins.AppID, DeploymentID: dep, NodeID: node, WakeID: ins.WakeID, RequestReason: reason, Method: method, DispatchedAt: at.UTC()}
		if _, err = pool.Exec(ctx, "DELETE FROM instances WHERE id=$1", ins.ID); err != nil {
			t.Fatal(err)
		}
		return ins.ID
	}
	reason := "environment-clone:" + l.Operation.ID
	record(w.TargetDeploymentID, reason, "cold_boot")
	record(w.TargetDeploymentID, reason, "restore")
	// A request in the original operation namespace must remain discoverable
	// even when its extra deployment is absent from the frozen workload list.
	makeDeployment := func(why string, offset int) string {
		t.Helper()
		id := uuid.NewString()
		_, err := pool.Exec(ctx, `INSERT INTO deployments SELECT (jsonb_populate_record(NULL::deployments,to_jsonb(d)||jsonb_build_object('id',$1::text,'reason',$2::text,'revision',d.revision+$3::integer))).* FROM deployments d WHERE d.id=$4`, id, why, offset, w.TargetDeploymentID)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	extra := makeDeployment(reason, 1)
	record(extra, reason, "restore")
	if _, err = pool.Exec(ctx, "DELETE FROM deployments WHERE id=$1", extra); err != nil {
		t.Fatal(err)
	}
	foreignReason := "environment-clone:" + uuid.NewString()
	foreign := makeDeployment(foreignReason, 2)
	foreignInstance := record(foreign, foreignReason, "cold_boot")
	delete(expected, foreignInstance)
	// A planned retirement is not a permanent dispatch fence.
	if page, err := s.ListProjectEnvironmentCloneWakeDispatchesForRetirement(ctx, l, "", 1); !errors.Is(err, state.ErrConflict) || page != nil {
		t.Fatal("planned recovery accepted", page, err)
	}
	in, _, err = s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(expected))
	for id := range expected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	cursor := ""
	for _, id := range ids {
		page, err := s.ListProjectEnvironmentCloneWakeDispatchesForRetirement(ctx, l, cursor, 1)
		if err != nil || len(page) != 1 || !reflect.DeepEqual(page[0], expected[id]) {
			t.Fatal("lost original dispatch", page, expected[id], err)
		}
		cursor = page[0].InstanceID
	}
	page, err := s.ListProjectEnvironmentCloneWakeDispatchesForRetirement(ctx, l, cursor, 1)
	if err != nil || len(page) != 0 {
		t.Fatal("foreign dispatch or cursor replay", page, err)
	}
	all, err := s.ListProjectEnvironmentCloneWakeDispatchesForRetirement(ctx, l, "", api.CheckpointWorkloadWakeRecoveryBatchMax)
	if err != nil || len(all) != len(expected) {
		t.Fatal("incomplete inventory", all, err)
	}
	all[0].NodeID = "mutated-adapter-copy"
	replay, err := s.ListProjectEnvironmentCloneWakeDispatchesForRetirement(ctx, l, "", api.CheckpointWorkloadWakeRecoveryBatchMax)
	if err != nil || len(replay) != len(ids) || !reflect.DeepEqual(replay[0], expected[ids[0]]) {
		t.Fatal("adapter mutation changed original inventory", replay, err)
	}
	if err = s.FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l, pgWorkloadRetirementProof(in.Plan)); !errors.Is(err, state.ErrConflict) {
		t.Fatal("reading ownership supplied closure", err)
	}
	for _, bad := range []state.ProjectEnvironmentCloneLease{
		func() state.ProjectEnvironmentCloneLease { x := l; x.Token = uuid.NewString(); return x }(),
		func() state.ProjectEnvironmentCloneLease { x := l; x.Operation.Revision++; return x }(),
		func() state.ProjectEnvironmentCloneLease { x := l; x.Operation.AccountID = uuid.NewString(); return x }(),
		func() state.ProjectEnvironmentCloneLease { x := l; x.Operation.ProjectID = uuid.NewString(); return x }(),
	} {
		if page, err := s.ListProjectEnvironmentCloneWakeDispatchesForRetirement(ctx, bad, "", 1); err == nil || page != nil {
			t.Fatal("foreign lease exposed dispatches", page, err)
		}
	}
	old := l
	if _, err = pool.Exec(ctx, "UPDATE project_environment_clone_operations SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1", l.Operation.ID); err != nil {
		t.Fatal(err)
	}
	l, err = s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if page, err := s.ListProjectEnvironmentCloneWakeDispatchesForRetirement(ctx, old, "", 1); !errors.Is(err, state.ErrConflict) || page != nil {
		t.Fatal("stale worker exposed ownership", page, err)
	}
	recovered, err := s.ListProjectEnvironmentCloneWakeDispatchesForRetirement(ctx, l, "", api.CheckpointWorkloadWakeRecoveryBatchMax)
	if err != nil || !reflect.DeepEqual(recovered, replay) {
		t.Fatal("worker handoff changed original ownership", recovered, err)
	}
}

func TestPgCloneWakeRecoveryBoundsAndEmptyInventory(t *testing.T) {
	s, _, l := pgWorkloadRetirementFixture(t, "unmaterialized")
	ctx := t.Context()
	if _, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l); err != nil {
		t.Fatal(err)
	}
	for _, args := range []struct {
		cursor string
		limit  int
	}{{"invalid", 1}, {uuid.Nil.String(), 1}, {"", 0}, {"", -1}, {"", api.CheckpointWorkloadWakeRecoveryBatchMax + 1}} {
		if page, err := s.ListProjectEnvironmentCloneWakeDispatchesForRetirement(ctx, l, args.cursor, args.limit); !errors.Is(err, state.ErrInvalidArgument) || page != nil {
			t.Fatal("invalid page accepted", page, err)
		}
	}
	page, err := s.ListProjectEnvironmentCloneWakeDispatchesForRetirement(ctx, l, "", 1)
	if err != nil || page == nil || len(page) != 0 {
		t.Fatal("empty original namespace", page, err)
	}
}
