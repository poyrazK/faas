package state

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

type standardRuntimeRefreshTestStore interface {
	standardOperationControlTestStore
	ApplicationStandardRuntimeRefreshStore
}

func standardRuntimeRefreshFixture(t *testing.T, s standardRuntimeRefreshTestStore) (standardApprovalFixture, map[string]Snapshot) {
	t.Helper()
	f := newStandardApprovalFixture(t, s)
	snapshots := map[string]Snapshot{}
	for _, app := range f.apps {
		dep, err := s.CreateDeployment(t.Context(), Deployment{AppID: app.ID, Kind: DeploymentKindImage, Status: DeployLive})
		if err != nil {
			t.Fatal(err)
		}
		snap, err := s.CreateSnapshot(t.Context(), Snapshot{DeploymentID: dep.ID, FCVersion: "1.10.0", StorageKey: "snap/" + dep.ID + "/legacy.mem", MemBytes: 16384, DiskBytes: 4096})
		if err != nil {
			t.Fatal(err)
		}
		snapshots[app.ID] = snap
	}
	var err error
	f.plan, err = s.PreviewApplicationStandardAssignment(t.Context(), f.plan.OrgID, f.owner.Account.ID, f.plan.Request)
	if err != nil || len(f.plan.Blockers) != 0 {
		t.Fatal("review with retained artifacts", f.plan.Blockers, err)
	}
	return f, snapshots
}

func standardRuntimeRefreshLifecycle(t *testing.T, s standardRuntimeRefreshTestStore, snapshotByID func(string) Snapshot) {
	t.Helper()
	f, snaps := standardRuntimeRefreshFixture(t, s)
	if _, err := s.GetApplicationStandardRuntimeRefresh(t.Context(), f.plan.OrgID, f.apps[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("preview queued runtime work", err)
	}
	o, err := s.ApproveApplicationStandardReview(t.Context(), f.plan.OrgID, f.owner.Account.ID, f.plan.ID, f.plan.ApprovalHash)
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
	if err != nil || !validStandardRuntimeRefresh(r) {
		t.Fatal("installed revision has no valid durable handoff", r, err)
	}
	e, err := s.GetApplicationStandardEnrollment(t.Context(), o.OrgID, appID)
	if err != nil || r != newStandardRuntimeRefresh(e) || e.ObservedRevision != 0 {
		t.Fatal("handoff changed intent or fabricated observation", e, err)
	}
	if _, stamped, err := s.AppRuntimeConfigChangedAt(t.Context(), appID); err != nil || stamped {
		t.Fatal("standard installation stamped environment intent", err)
	}
	for id, snap := range snaps {
		if got := snapshotByID(snap.ID); got.Stale != sameStandardUUID(id, appID) || got.StorageKey != snap.StorageKey {
			t.Fatal("snapshot eligibility or source changed outside installed target", got)
		}
	}
	if current, err := s.CheckApplicationStandardRuntimeRefresh(t.Context(), r); err != nil || !current {
		t.Fatal("installed handoff is not current", current, err)
	}
	paused := standardControlOperation(t, s, o, f.owner.Account.ID, ApplicationStandardOperationPause, "paused")
	if _, err := s.CheckApplicationStandardRuntimeRefresh(t.Context(), r); !errors.Is(err, ErrApplicationStandardRefreshDeferred) {
		t.Fatal("paused revision may dispatch runtime work", err)
	}
	_ = standardControlOperation(t, s, paused, f.owner.Account.ID, ApplicationStandardOperationResume, "waiting")
	if current, err := s.CheckApplicationStandardRuntimeRefresh(t.Context(), r); err != nil || !current {
		t.Fatal("resume lost the original handoff", current, err)
	}
	for _, tc := range []struct {
		name   string
		change func(*ApplicationStandardRuntimeRefreshRequest)
		want   error
	}{
		{"foreign organization", func(x *ApplicationStandardRuntimeRefreshRequest) { x.Standard.OrgID = uuid.NewString() }, ErrNotFound},
		{"future revision", func(x *ApplicationStandardRuntimeRefreshRequest) { x.Standard.DesiredRevision++ }, ErrConflict},
		{"changed hash", func(x *ApplicationStandardRuntimeRefreshRequest) {
			x.Standard.EffectiveHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		}, ErrConflict},
		{"invalid revision", func(x *ApplicationStandardRuntimeRefreshRequest) { x.Standard.DesiredRevision = 0 }, ErrInvalidArgument},
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
	m := NewMemStore()
	standardRuntimeRefreshLifecycle(t, m, func(id string) Snapshot {
		m.mu.Lock()
		defer m.mu.Unlock()
		for _, snap := range m.snapshots {
			if snap.ID == id {
				return snap
			}
		}
		t.Fatal("snapshot vanished")
		return Snapshot{}
	})
}

func TestMemApplicationStandardRuntimeRefreshAutomatic(t *testing.T) {
	m := NewMemStore()
	f := standardAutomaticSetup(t, m, false, true)
	app := automaticCreate(t, m, f, "automatic-refresh")
	e := automaticRepair(t, m)
	r, err := m.GetApplicationStandardRuntimeRefresh(t.Context(), app.OrgID, app.ID)
	if err != nil || r != newStandardRuntimeRefresh(e) {
		t.Fatal("automatic enrollment lost runtime handoff", r, err)
	}
	// Superseded requests never reapply an old projection.
	m.mu.Lock()
	newer := m.applicationStandardEnrollments[app.ID]
	newer.DesiredRevision++
	m.applicationStandardEnrollments[app.ID] = newer
	m.mu.Unlock()
	if current, err := m.CheckApplicationStandardRuntimeRefresh(t.Context(), r); err != nil || current {
		t.Fatal("superseded handoff was not harmless", current, err)
	}
}
