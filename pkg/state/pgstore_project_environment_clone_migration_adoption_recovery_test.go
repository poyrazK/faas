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

func TestPgCloneMigrationAdoptionRecoveryOriginalEvidenceAndHandoff(t *testing.T) {
	s, pool, l := pgWorkloadRetirementFixture(t, "deployed")
	ctx := t.Context()
	in, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	w := in.Plan.Workloads[0]
	source := resolveDefaultLocal(t, ctx, s)
	destination := cloneAdoptionDestination(t, s)
	seed := func(dep string) {
		t.Helper()
		ins, err := s.CreateInstance(ctx, w.AppID, dep, string(state.StateRunning), 128, source, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		at := time.Now().UTC().Truncate(time.Microsecond)
		lease := state.MigrationLease{InstanceID: ins.ID, LeaseToken: uuid.NewString(), SourceNodeID: source, CreatedAt: at, LeaseExpiresAt: at.Add(time.Minute)}
		if err = s.ReserveMigrationLease(ctx, lease); err != nil {
			t.Fatal(err)
		}
		if err = s.MarkInstanceMigrating(ctx, ins.ID, source, lease.LeaseToken); err != nil {
			t.Fatal(err)
		}
		if err = s.CompleteMigrationLease(ctx, lease.LeaseToken, "private-mem-"+ins.ID, "private-state-"+ins.ID); err != nil {
			t.Fatal(err)
		}
		if err = s.RecordCloneCheckpointMigrationAdoption(ctx, ins.ID, w.AppID, dep, destination, "private-mem-"+ins.ID, "private-state-"+ins.ID, lease.LeaseToken); err != nil {
			t.Fatal(err)
		}
		if err = s.DeleteMigrationLease(ctx, lease.LeaseToken); err != nil {
			t.Fatal(err)
		}
		if err = s.DeleteInstance(ctx, ins.ID); err != nil {
			t.Fatal(err)
		}
	}
	seed(w.TargetDeploymentID)
	seed(w.TargetDeploymentID)
	extra := uuid.NewString()
	foreign := uuid.NewString()
	for index, d := range []struct{ id, reason string }{{extra, "environment-clone:" + l.Operation.ID}, {foreign, "environment-clone:" + uuid.NewString()}} {
		_, err = pool.Exec(ctx, `INSERT INTO deployments SELECT (jsonb_populate_record(NULL::deployments,to_jsonb(d)||jsonb_build_object('id',$1::text,'reason',$2::text,'revision',d.revision+$4::integer))).* FROM deployments d WHERE d.id=$3`, d.id, d.reason, w.TargetDeploymentID, index+1)
		if err != nil {
			t.Fatal(err)
		}
		seed(d.id)
	}
	seed(w.SourceDeploymentID)
	// Source holdings are gone; extra namespace evidence survives catalog loss.
	if _, err = pool.Exec(ctx, "DELETE FROM deployments WHERE id=ANY($1::uuid[])", []string{extra, foreign}); err != nil {
		t.Fatal(err)
	}
	if page, err := s.ListProjectEnvironmentCloneMigrationAdoptionsForRetirement(ctx, l, "", 1); !errors.Is(err, state.ErrConflict) || page != nil {
		t.Fatal("planned journal exposed authority", err)
	}
	in, _, err = s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	all, err := s.ListProjectEnvironmentCloneMigrationAdoptionsForRetirement(ctx, l, "", api.CheckpointWorkloadMigrationAdoptionRecoveryBatchMax)
	if err != nil || len(all) != 3 {
		t.Fatal("missing original destination evidence or foreign evidence included", err)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].DispatchID < all[j].DispatchID })
	cursor := ""
	for _, want := range all {
		page, err := s.ListProjectEnvironmentCloneMigrationAdoptionsForRetirement(ctx, l, cursor, 1)
		if err != nil || len(page) != 1 || !reflect.DeepEqual(page[0], want) {
			t.Fatal("unstable original dispatch continuation", err)
		}
		if want.SourceLease.SourceNodeID != source || want.DestinationNodeID != destination || want.SourceLease.LeaseToken == "" || want.DispatchedAt.IsZero() {
			t.Fatal("lost original ownership")
		}
		raw, err := json.Marshal(page[0])
		if err != nil {
			t.Fatal(err)
		}
		for _, private := range []string{want.SourceLease.LeaseToken, want.SourceLease.MemStorageKey, want.SourceLease.VMStateStorageKey} {
			if strings.Contains(string(raw), private) {
				t.Fatal("private capability entered JSON")
			}
		}
		cursor = want.DispatchID
	}
	if page, err := s.ListProjectEnvironmentCloneMigrationAdoptionsForRetirement(ctx, l, cursor, 1); err != nil || len(page) != 0 {
		t.Fatal("foreign dispatch leaked", err)
	}
	all[0].SourceLease.LeaseToken = "adapter-mutation"
	replay, err := s.ListProjectEnvironmentCloneMigrationAdoptionsForRetirement(ctx, l, "", api.CheckpointWorkloadMigrationAdoptionRecoveryBatchMax)
	if err != nil || replay[0].SourceLease.LeaseToken == "adapter-mutation" {
		t.Fatal("adapter changed retained evidence", err)
	}
	if err = s.FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l, pgWorkloadRetirementProof(in.Plan)); !errors.Is(err, state.ErrConflict) {
		t.Fatal("recovery read accepted as native closure", err)
	}
	for _, bad := range []state.ProjectEnvironmentCloneLease{
		func() state.ProjectEnvironmentCloneLease { x := l; x.Token = uuid.NewString(); return x }(),
		func() state.ProjectEnvironmentCloneLease { x := l; x.Operation.Revision++; return x }(),
		func() state.ProjectEnvironmentCloneLease { x := l; x.Operation.AccountID = uuid.NewString(); return x }(),
		func() state.ProjectEnvironmentCloneLease { x := l; x.Operation.ProjectID = uuid.NewString(); return x }(),
	} {
		if page, err := s.ListProjectEnvironmentCloneMigrationAdoptionsForRetirement(ctx, bad, "", 1); err == nil || page != nil {
			t.Fatal("foreign worker read private authority")
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
	if page, err := s.ListProjectEnvironmentCloneMigrationAdoptionsForRetirement(ctx, old, "", 1); !errors.Is(err, state.ErrConflict) || page != nil {
		t.Fatal("old worker retained authority", err)
	}
	recovered, err := s.ListProjectEnvironmentCloneMigrationAdoptionsForRetirement(ctx, l, "", api.CheckpointWorkloadMigrationAdoptionRecoveryBatchMax)
	if err != nil || !reflect.DeepEqual(recovered, replay) {
		t.Fatal("worker handoff changed original evidence", err)
	}
}

func TestPgCloneMigrationAdoptionRecoveryBoundsAndEmpty(t *testing.T) {
	s, _, l := pgWorkloadRetirementFixture(t, "unmaterialized")
	ctx := t.Context()
	if _, err := s.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l); err != nil {
		t.Fatal(err)
	}
	for _, a := range []struct {
		cursor string
		limit  int
	}{{"invalid", 1}, {uuid.Nil.String(), 1}, {"", 0}, {"", -1}, {"", api.CheckpointWorkloadMigrationAdoptionRecoveryBatchMax + 1}} {
		if page, err := s.ListProjectEnvironmentCloneMigrationAdoptionsForRetirement(ctx, l, a.cursor, a.limit); !errors.Is(err, state.ErrInvalidArgument) || page != nil {
			t.Fatal("invalid page accepted", err)
		}
	}
	if page, err := s.ListProjectEnvironmentCloneMigrationAdoptionsForRetirement(ctx, l, "", 1); err != nil || page == nil || len(page) != 0 {
		t.Fatal("empty journal not recoverable", err)
	}
}
