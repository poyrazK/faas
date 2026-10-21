package state_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

type standardRuntimeRefreshTestStore = state.ContractTestStore

func standardRuntimeRefreshFixture(t *testing.T, s standardRuntimeRefreshTestStore) (state.StandardApprovalFixture, map[string]state.Snapshot) {
	t.Helper()
	f := state.NewStandardApprovalFixture(t, s)
	snapshots := map[string]state.Snapshot{}
	for _, app := range *f.Apps() {
		dep, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive})
		if err != nil {
			t.Fatal(err)
		}
		snap, err := s.CreateSnapshot(t.Context(), state.Snapshot{DeploymentID: dep.ID, FCVersion: "1.10.0", StorageKey: "snap/" + dep.ID + "/legacy.mem", MemBytes: 16384, DiskBytes: 4096})
		if err != nil {
			t.Fatal(err)
		}
		snapshots[app.ID] = snap
	}
	var err error
	(*f.Plan()), err = s.PreviewApplicationStandardAssignment(t.Context(), (*f.Plan()).OrgID, (*f.Owner()).Account.ID, (*f.Plan()).Request)
	if err != nil || len((*f.Plan()).Blockers) != 0 {
		t.Fatal("review with retained artifacts", (*f.Plan()).Blockers, err)
	}
	return f, snapshots
}

func standardRuntimeRefreshLifecycle(t *testing.T, s standardRuntimeRefreshTestStore, snapshotByID func(string) state.Snapshot) {
	t.Helper()
	f, snaps := standardRuntimeRefreshFixture(t, s)
	if _, err := s.GetApplicationStandardRuntimeRefresh(t.Context(), (*f.Plan()).OrgID, (*f.Apps())[0].ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("preview queued runtime work", err)
	}
	o, err := s.ApproveApplicationStandardReview(t.Context(), (*f.Plan()).OrgID, (*f.Owner()).Account.ID, (*f.Plan()).ID, (*f.Plan()).ApprovalHash)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.ClaimApplicationStandardOperation(t.Context(), "refresh-installer")
	if err != nil {
		t.Fatal(err)
	}
	o, err = s.MaterializeNextApplicationStandardTarget(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	appID := o.Targets[0].AppID
	r, err := s.GetApplicationStandardRuntimeRefresh(t.Context(), o.OrgID, appID)
	if err != nil || !state.ValidStandardRuntimeRefresh(r) {
		t.Fatal("installed revision has no valid durable handoff", r, err)
	}
	e, err := s.GetApplicationStandardEnrollment(t.Context(), o.OrgID, appID)
	if err != nil || r != state.NewStandardRuntimeRefresh(e) || e.ObservedRevision != 0 {
		t.Fatal("handoff changed intent or fabricated observation", e, err)
	}
	if _, stamped, err := s.AppRuntimeConfigChangedAt(t.Context(), appID); err != nil || stamped {
		t.Fatal("standard installation stamped environment intent", err)
	}
	for id, snap := range snaps {
		if got := snapshotByID(snap.ID); got.Stale != state.SameStandardUUID(id, appID) || got.StorageKey != snap.StorageKey {
			t.Fatal("snapshot eligibility or source changed outside installed target", got)
		}
	}
	if current, err := s.CheckApplicationStandardRuntimeRefresh(t.Context(), r); err != nil || !current {
		t.Fatal("installed handoff is not current", current, err)
	}
	paused := state.StandardControlOperation(t, s, o, (*f.Owner()).Account.ID, state.ApplicationStandardOperationPause, "paused")
	if _, err := s.CheckApplicationStandardRuntimeRefresh(t.Context(), r); !errors.Is(err, state.ErrApplicationStandardRefreshDeferred) {
		t.Fatal("paused revision may dispatch runtime work", err)
	}
	_ = state.StandardControlOperation(t, s, paused, (*f.Owner()).Account.ID, state.ApplicationStandardOperationResume, "waiting")
	if current, err := s.CheckApplicationStandardRuntimeRefresh(t.Context(), r); err != nil || !current {
		t.Fatal("resume lost the original handoff", current, err)
	}
	for _, tc := range []struct {
		name   string
		change func(*state.ApplicationStandardRuntimeRefreshRequest)
		want   error
	}{
		{"foreign organization", func(x *state.ApplicationStandardRuntimeRefreshRequest) { x.Standard.OrgID = uuid.NewString() }, state.ErrNotFound},
		{"future revision", func(x *state.ApplicationStandardRuntimeRefreshRequest) { x.Standard.DesiredRevision++ }, state.ErrConflict},
		{"changed hash", func(x *state.ApplicationStandardRuntimeRefreshRequest) {
			x.Standard.EffectiveHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		}, state.ErrConflict},
		{"invalid revision", func(x *state.ApplicationStandardRuntimeRefreshRequest) { x.Standard.DesiredRevision = 0 }, state.ErrInvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := r
			tc.change(&copy)
			if _, err := s.CheckApplicationStandardRuntimeRefresh(t.Context(), copy); !errors.Is(err, tc.want) {
				t.Fatal(err)
			}
		})
	}
}

func TestMemApplicationStandardRuntimeRefresh(t *testing.T) {
	m := state.NewMemStore()
	standardRuntimeRefreshLifecycle(t, m, func(id string) state.Snapshot {
		(*m.Mu()).Lock()
		defer (*m.Mu()).Unlock()
		for _, snap := range *m.Snapshots() {
			if snap.ID == id {
				return snap
			}
		}
		t.Fatal("snapshot vanished")
		return state.Snapshot{}
	})
}

func TestMemApplicationStandardRuntimeRefreshAutomatic(t *testing.T) {
	m := state.NewMemStore()
	f := state.StandardAutomaticSetup(t, m, false, true)
	app := state.AutomaticCreate(t, m, f, "automatic-refresh")
	e := state.AutomaticRepair(t, m)
	r, err := m.GetApplicationStandardRuntimeRefresh(t.Context(), app.OrgID, app.ID)
	if err != nil || r != state.NewStandardRuntimeRefresh(e) {
		t.Fatal("automatic enrollment lost runtime handoff", r, err)
	}
	(*
	// Superseded requests never reapply an old projection.
	m.Mu()).Lock()
	newer := (*m.ApplicationStandardEnrollments())[app.ID]
	newer.DesiredRevision++
	(*m.ApplicationStandardEnrollments())[app.ID] = newer
	(*m.Mu()).Unlock()
	if current, err := m.CheckApplicationStandardRuntimeRefresh(t.Context(), r); err != nil || current {
		t.Fatal("superseded handoff was not harmless", current, err)
	}
}
