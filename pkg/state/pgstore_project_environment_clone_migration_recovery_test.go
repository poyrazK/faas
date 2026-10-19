//go:build !no_pg

// adr: 590
package state_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgCloneMigrationRecoveryOriginalLeaseAndHandoff(t *testing.T) {
	s, pool, l := pgWorkloadRetirementFixture(t, "deployed")
	ctx := t.Context()
	in, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	w := in.Plan.Workloads[0]
	node := resolveDefaultLocal(t, ctx, s)
	// The lease names the node the instance is migrating from, distinct from
	// where it is now scheduled. It must be a real node that has not been
	// closed (ADR-1530 source lease guard).
	sourceNode := pgTestComputeNode(t, ctx, s, true, 0)
	expected := map[string]state.ProjectEnvironmentCloneMigrationHolding{}
	seed := func(dep string, expired, completed bool) state.ProjectEnvironmentCloneMigrationHolding {
		t.Helper()
		ins, err := s.CreateInstance(ctx, w.AppID, dep, string(state.StateRunning), 128, node, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		at := time.Now().UTC().Truncate(time.Microsecond)
		until := at.Add(time.Minute)
		if expired {
			until = at.Add(-time.Minute)
		}
		lease := state.MigrationLease{InstanceID: ins.ID, LeaseToken: uuid.NewString(), SourceNodeID: sourceNode, CreatedAt: at.Add(-2 * time.Minute), LeaseExpiresAt: until, Pending: true}
		if err = s.ReserveMigrationLease(ctx, lease); err != nil {
			t.Fatal(err)
		}
		if completed {
			lease.MemStorageKey = "original-mem-" + ins.ID
			lease.VMStateStorageKey = "original-state-" + ins.ID
			lease.Pending = false
			if err = s.CompleteMigrationLease(ctx, lease.LeaseToken, lease.MemStorageKey, lease.VMStateStorageKey); err != nil {
				t.Fatal(err)
			}
		}
		return state.ProjectEnvironmentCloneMigrationHolding{Lease: lease, AppID: ins.AppID, DeploymentID: dep, WakeID: ins.WakeID, SchedulingNodeID: node}
	}
	pending := seed(w.TargetDeploymentID, false, false)
	expected[pending.Lease.InstanceID] = pending
	completed := seed(w.TargetDeploymentID, true, true)
	expected[completed.Lease.InstanceID] = completed
	source := seed(w.SourceDeploymentID, true, true)
	foreign := uuid.NewString()
	_, err = pool.Exec(ctx, `INSERT INTO deployments SELECT (jsonb_populate_record(NULL::deployments,to_jsonb(d)||jsonb_build_object('id',$1::text,'reason',$2::text,'revision',d.revision+1))).* FROM deployments d WHERE d.id=$3`, foreign, "environment-clone:"+uuid.NewString(), w.TargetDeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	seed(foreign, false, false)
	if rows, err := s.ListProjectEnvironmentCloneMigrationHoldingsForRetirement(ctx, l, "", 1); !errors.Is(err, state.ErrConflict) || rows != nil {
		t.Fatal("planned retirement exposed holdings", err)
	}
	in, _, err = s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "UPDATE instances SET state='stopped' WHERE id=$1", completed.Lease.InstanceID); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(expected))
	for id := range expected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	cursor := ""
	for _, id := range ids {
		page, err := s.ListProjectEnvironmentCloneMigrationHoldingsForRetirement(ctx, l, cursor, 1)
		if err != nil || len(page) != 1 || !reflect.DeepEqual(page[0], expected[id]) {
			t.Fatal("original handoff identity changed", err)
		}
		if page[0].SchedulingNodeID == page[0].Lease.SourceNodeID {
			t.Fatal("scheduler node replaced original source")
		}
		raw, err := json.Marshal(page[0])
		if err != nil {
			t.Fatal(err)
		}
		for _, private := range []string{page[0].Lease.LeaseToken, page[0].Lease.MemStorageKey, page[0].Lease.VMStateStorageKey} {
			if private != "" && strings.Contains(string(raw), private) {
				t.Fatal("private cleanup authority entered JSON output")
			}
		}
		cursor = id
	}
	if page, err := s.ListProjectEnvironmentCloneMigrationHoldingsForRetirement(ctx, l, cursor, 1); err != nil || len(page) != 0 {
		t.Fatal("foreign/source holdings leaked", err)
	}
	// Delayed original completion may add cleanup handles without changing the
	// source identity or renewing an expired/native handoff authority.
	if err = s.CompleteMigrationLease(ctx, pending.Lease.LeaseToken, "delayed-mem", "delayed-state"); err != nil {
		t.Fatal(err)
	}
	pending.Lease.Pending = false
	pending.Lease.MemStorageKey = "delayed-mem"
	pending.Lease.VMStateStorageKey = "delayed-state"
	expected[pending.Lease.InstanceID] = pending
	all, err := s.ListProjectEnvironmentCloneMigrationHoldingsForRetirement(ctx, l, "", api.CheckpointWorkloadMigrationRecoveryBatchMax)
	if err != nil || len(all) != len(ids) {
		t.Fatal("incomplete recovery", err)
	}
	for i, id := range ids {
		if !reflect.DeepEqual(all[i], expected[id]) {
			t.Fatal("lost delayed completion")
		}
	}
	all[0].Lease.SourceNodeID = "adapter-mutation"
	replay, err := s.ListProjectEnvironmentCloneMigrationHoldingsForRetirement(ctx, l, "", api.CheckpointWorkloadMigrationRecoveryBatchMax)
	if err != nil || !reflect.DeepEqual(replay[0], expected[ids[0]]) {
		t.Fatal("adapter mutation changed retained source", err)
	}
	requireCloneMigrationRetained(t, s.DeleteMigrationLease(ctx, completed.Lease.LeaseToken))
	if err = s.FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l, pgWorkloadRetirementProof(in.Plan)); !errors.Is(err, state.ErrConflict) {
		t.Fatal("inventory read or stopped/expired labels accepted as closure", err)
	}
	for _, bad := range []state.ProjectEnvironmentCloneLease{
		func() state.ProjectEnvironmentCloneLease { x := l; x.Token = uuid.NewString(); return x }(),
		func() state.ProjectEnvironmentCloneLease { x := l; x.Operation.Revision++; return x }(),
		func() state.ProjectEnvironmentCloneLease { x := l; x.Operation.AccountID = uuid.NewString(); return x }(),
		func() state.ProjectEnvironmentCloneLease { x := l; x.Operation.ProjectID = uuid.NewString(); return x }(),
	} {
		if page, err := s.ListProjectEnvironmentCloneMigrationHoldingsForRetirement(ctx, bad, "", 1); err == nil || page != nil {
			t.Fatal("foreign/stale lease exposed cleanup authority")
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
	if page, err := s.ListProjectEnvironmentCloneMigrationHoldingsForRetirement(ctx, old, "", 1); !errors.Is(err, state.ErrConflict) || page != nil {
		t.Fatal("expired worker recovered handoff authority", err)
	}
	recovered, err := s.ListProjectEnvironmentCloneMigrationHoldingsForRetirement(ctx, l, "", api.CheckpointWorkloadMigrationRecoveryBatchMax)
	if err != nil || !reflect.DeepEqual(recovered, replay) {
		t.Fatal("worker handoff changed retained leases", err)
	}
	if err = s.DeleteMigrationLease(ctx, source.Lease.LeaseToken); err != nil {
		t.Fatal("source release acquired clone fence", err)
	}
}

func TestPgCloneMigrationRecoveryBoundsAndEmptyInventory(t *testing.T) {
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
	}{{"invalid", 1}, {uuid.Nil.String(), 1}, {"", 0}, {"", -1}, {"", api.CheckpointWorkloadMigrationRecoveryBatchMax + 1}} {
		if page, err := s.ListProjectEnvironmentCloneMigrationHoldingsForRetirement(ctx, l, args.cursor, args.limit); !errors.Is(err, state.ErrInvalidArgument) || page != nil {
			t.Fatal("invalid recovery page accepted", err)
		}
	}
	page, err := s.ListProjectEnvironmentCloneMigrationHoldingsForRetirement(ctx, l, "", 1)
	if err != nil || page == nil || len(page) != 0 {
		t.Fatal("empty original holdings", err)
	}
}
