package dbsched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/dbarchive"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/pgextension"
)

type rehearsalVMM struct {
	*requestedDrillVMM
	t      *testing.T
	source string
	point  time.Time
	reason string
}

func (f *rehearsalVMM) CheckLatestArchiveRestore(_ context.Context, node Node, id string, upper time.Time, major int) (dbarchive.RestoreCheck, error) {
	if id != f.source || node.ID != "db-a" || major != 16 || !f.point.Before(upper) {
		f.t.Errorf("archive check: %s on %s through %v, PostgreSQL %d", id, node.ID, upper, major)
	}
	if f.reason != "" {
		return dbarchive.RestoreCheck{Outcome: dbarchive.RestoreUnverified, Reason: f.reason}, nil
	}
	return dbarchive.RestoreCheck{Outcome: dbarchive.RestoreCovered, RecoveryPoint: &f.point, Timeline: 2}, nil
}

func (f *rehearsalVMM) Start(ctx context.Context, node Node, vm VM) error {
	if vm.DatabaseID == f.source {
		f.t.Error("rehearsal booted the source")
	}
	return f.fakeVMM.Start(ctx, node, vm)
}

func (f *rehearsalVMM) Control(ctx context.Context, node Node, id string, req dbguest.Request) (dbguest.Response, error) {
	if id == f.source {
		f.t.Errorf("rehearsal sent %s to the source", req.Op)
	}
	if req.Op == dbguest.OpExtensions {
		if req.DiagnosticDatabase != "app" {
			f.t.Errorf("extension preflight inspected %q", req.DiagnosticDatabase)
		}
		return dbguest.Response{State: dbguest.StateReady, Extensions: &pgextension.Catalog{Installed: []pgextension.Installed{}, Available: []pgextension.Available{}}}, nil
	}
	resp, err := f.fakeVMM.Control(ctx, node, id, req)
	if req.Op == dbguest.OpCheckDrill {
		f.mu.Lock()
		resp.Major = f.runMajor[id]
		f.mu.Unlock()
	}
	return resp, err
}

func TestUpgradeRehearsalUsesPrivateCopyAndResumes(t *testing.T) {
	ctx := t.Context()
	store := storeFactories(t)["pg"](t).(*PgStore)
	seedNodes(t, store, Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 4096,
		VolumeCapacityBytes: 20 << 30, Lifecycle: NodeActive, Majors: []int{16, 17, 18}})
	vmm := &rehearsalVMM{t: t, requestedDrillVMM: &requestedDrillVMM{fakeVMM: newFakeVMM()}}
	vmm.archive = true
	s := New(store, vmm, Config{}, nil)
	t.Cleanup(s.restoreWG.Wait)
	t.Cleanup(s.upgradeWG.Wait)
	source := provisionMajor(t, s, 16)
	vmm.source = source.DatabaseID
	sweep := func() {
		t.Helper()
		if err := s.advanceRequestedDrills(ctx, store); err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct {
		name, archiveReason, upgradeCode, failedPhase, errorCode string
		loseMark                                                 bool
	}{
		{name: "success"},
		{name: "upgrade refused", upgradeCode: dbguest.ErrCodeUpgradeRefused, failedPhase: "upgrade", errorCode: dbguest.ErrCodeUpgradeRefused},
		{name: "verification failed", loseMark: true, failedPhase: "verify", errorCode: dbguest.ErrCodeDrillMarkMissing},
		{name: "unverified archive", archiveReason: "timeline_unverified", failedPhase: "restore", errorCode: "timeline_unverified"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vmm.point = time.Now().UTC().Truncate(time.Microsecond)
			vmm.recoveredTo = vmm.point.Add(-time.Millisecond)
			vmm.upgradeCode, vmm.reason = tc.upgradeCode, tc.archiveReason
			key := uuid.NewString()
			d, err := s.RequestUpgradeRehearsal(ctx, source.DatabaseID, key, 18, "app")
			if err != nil || d.Result != DrillQueued || d.RequestID != key || d.Rehearsal.TargetMajor != 18 || !d.TargetAt.IsZero() {
				t.Fatalf("queue = %+v, %v", d, err)
			}
			if got, err := s.RequestUpgradeRehearsal(ctx, source.DatabaseID, key, 18, "app"); err != nil || got.ID != d.ID {
				t.Fatalf("requeue = %+v, %v", got, err)
			}
			if _, err := s.RequestUpgradeRehearsal(ctx, source.DatabaseID, key, 17, "app"); !errors.Is(err, ErrConflict) {
				t.Fatalf("same key with another target: %v", err)
			}
			if _, err := s.RequestRestoreDrill(ctx, source.DatabaseID, uuid.NewString()); !errors.Is(err, ErrConflict) {
				t.Fatalf("concurrent restore drill: %v", err)
			}
			if _, err := s.GetRestoreDrill(ctx, source.DatabaseID, d.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("rehearsal exposed as a restore drill: %v", err)
			}
			if _, err := s.GetUpgradeRehearsal(ctx, uuid.NewString(), d.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("rehearsal exposed to another source: %v", err)
			}

			if tc.archiveReason == "" {
				hold := make(chan struct{})
				vmm.restoreHold = hold
				sweep()
				copy, err := store.Get(ctx, d.ID)
				close(hold)
				if err != nil || copy.State != stateRehearsalRestore || copy.RestoreSource != source.DatabaseID || copy.RestoreTimeline != 2 {
					t.Fatalf("private copy was not isolated from general restarts: %+v, %v", copy, err)
				}
				s.restoreWG.Wait()
				// Simulate a restart before the restore handoff update commits.
				if _, err := store.pool.Exec(ctx, "UPDATE database_upgrade_rehearsals SET restore_started=false WHERE drill_id=$1", mustUUID(d.ID)); err != nil {
					t.Fatal(err)
				}
				sweep()
				sweep() // restored copy accepted
				sweep() // mark written on copy
				sweep() // pg_upgrade started
				s.upgradeWG.Wait()
				if tc.loseMark {
					vmm.mu.Lock()
					delete(vmm.marks, d.ID)
					vmm.mu.Unlock()
				}
				if tc.upgradeCode == "" {
					sweep() // upgrade committed by the normal upgrade worker
				}
				vmm.failDrop = true
				if err := s.advanceRequestedDrills(ctx, store); err == nil {
					t.Fatal("expected a retryable archive cleanup failure")
				}
				pending, err := s.GetUpgradeRehearsal(ctx, source.DatabaseID, d.ID)
				if err != nil || pending.PendingOutcome == "" || !pending.FinishedAt.IsZero() {
					t.Fatalf("cleanup pending = %+v, %v", pending, err)
				}
			}
			sweep()
			got, err := s.GetUpgradeRehearsal(ctx, source.DatabaseID, d.ID)
			want := DrillOK
			if tc.errorCode != "" {
				want = DrillFailed
			}
			if err != nil || got.Result != want || got.ErrorCode != tc.errorCode || got.FinishedAt.IsZero() {
				t.Fatalf("terminal = %+v, %v, want %s/%s", got, err, want, tc.errorCode)
			}
			if tc.failedPhase != "" && got.Rehearsal.Phase != tc.failedPhase {
				t.Fatalf("failed phase = %s, want %s", got.Rehearsal.Phase, tc.failedPhase)
			}
			if tc.archiveReason == "" && (got.Rehearsal.RecoveryTimeline != 2 || !got.TargetAt.Equal(vmm.point) || !got.RecoveredTo.Equal(vmm.recoveredTo)) {
				t.Fatalf("lost recovery evidence: %+v", got)
			}
			if _, err := store.Get(ctx, d.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("copy survived cleanup: %v", err)
			}
			if _, err := store.q.GetDatabaseUpgradeRehearsal(ctx, store.pool, mustUUID(d.ID)); err != nil {
				t.Fatalf("upgrade evidence survived neither copy nor archive cleanup: %v", err)
			}
			if rows, err := store.q.DatabaseRestoreDrillsByNode(ctx, store.pool); err != nil || len(rows) != 0 {
				t.Fatalf("rehearsal was counted as a restore drill: %+v, %v", rows, err)
			}
		})
	}
	after, err := store.Get(ctx, source.DatabaseID)
	if err != nil || after.State != StateParked || after.Major != 16 || !after.LastBackupAt.Equal(source.LastBackupAt) {
		t.Fatalf("rehearsal changed the source: %+v, %v", after, err)
	}
	vmm.mu.Lock()
	defer vmm.mu.Unlock()
	if len(vmm.restoreBoots) != 3 || countCalls(vmm.calls, "mark_drill") != 3 || countCalls(vmm.calls, "drop_archive") != 3 {
		t.Fatalf("unexpected repeated work: restores = %v, calls = %v", vmm.restoreBoots, vmm.calls)
	}
	for _, fallback := range vmm.untilEnd {
		if fallback {
			t.Fatal("rehearsal accepted recovery short of the verified target")
		}
	}
}
