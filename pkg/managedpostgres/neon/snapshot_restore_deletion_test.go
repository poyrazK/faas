// adr: 590
package neon

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

type forkDeletionFixture struct {
	base                               *snapshotRestoreFixture
	request                            managedpostgres.SnapshotRestoreDeletionRequest
	ops                                []operation
	deletes, lists, exactOps           int
	loseDelete, disappear, ackFinished bool
	fault                              string
}

func newForkDeletionFixture(t *testing.T) *forkDeletionFixture {
	t.Helper()
	b := newSnapshotRestoreFixture(t)
	f := &forkDeletionFixture{base: b, ops: []operation{}, disappear: true}
	b.p = testProvider(t, http.HandlerFunc(f.serveHTTP))
	b.rows = []branch{b.target()}
	r := b.request
	r.ExpectedTargetResourceID = "project-source/br-target"
	snapshotAt, _ := time.Parse(time.RFC3339Nano, b.snapshot.CreatedAt)
	targetAt, _ := time.Parse(time.RFC3339Nano, b.rows[0].CreatedAt)
	f.request = managedpostgres.SnapshotRestoreDeletionRequest{Restore: r, SnapshotCreatedAt: snapshotAt, TargetCreatedAt: targetAt}
	return f
}

func (f *forkDeletionFixture) deletionOps() []operation {
	return []operation{{ID: "123e4567-e89b-12d3-a456-426614174000", ProjectID: "project-source", BranchID: "br-target", Action: "suspend_compute", Status: "running", CreatedAt: f.request.TargetCreatedAt.Add(time.Second).Format(time.RFC3339Nano)},
		{ID: "123e4567-e89b-12d3-a456-426614174001", ProjectID: "project-source", BranchID: "br-target", Action: "delete_timeline", Status: "running", CreatedAt: f.request.TargetCreatedAt.Add(time.Second).Format(time.RFC3339Nano)}}
}

func (f *forkDeletionFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	const root = "/api/v2/projects/project-source"
	switch {
	case r.Method == http.MethodDelete && r.URL.Path == root+"/branches/br-target":
		f.deletes++
		if len(r.URL.Query()) != 0 {
			f.base.t.Error("cleanup changed physical selectors")
		}
		actual := f.base.rows[0]
		f.ops = f.deletionOps()
		if f.disappear {
			f.base.rows = []branch{}
		}
		if f.loseDelete {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		ack := append([]operation{}, f.ops...)
		if f.ackFinished {
			for i := range ack {
				ack[i].Status = "finished"
			}
		}
		writeResponse(f.base.t, w, http.StatusOK, map[string]any{"branch": actual, "operations": ack})
	case r.Method == http.MethodGet && r.URL.Path == root+"/operations":
		f.lists++
		if r.URL.Query().Get("limit") != "1000" {
			f.base.t.Error("operation pagination did not use documented limit")
		}
		if f.fault == "missing_list" {
			writeResponse(f.base.t, w, http.StatusOK, map[string]any{})
			return
		}
		ops := f.ops
		cursor := ""
		if f.fault == "pagination" && r.URL.Query().Get("cursor") == "" {
			ops = []operation{{ID: "unrelated-operation", ProjectID: "project-source", BranchID: "br-unrelated", Status: "finished"}}
			cursor = "next-page"
		}
		if f.fault == "cycle" {
			ops = []operation{{ID: "loop-operation", ProjectID: "project-source", BranchID: "br-unrelated", Status: "finished"}}
			cursor = "same-page"
		}
		if f.fault == "terminal" {
			cursor = "last-operation"
			if r.URL.Query().Get("cursor") != "" {
				ops = []operation{}
			}
		}
		writeResponse(f.base.t, w, http.StatusOK, map[string]any{"operations": ops, "pagination": map[string]any{"cursor": cursor}})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, root+"/operations/"):
		f.exactOps++
		id := strings.TrimPrefix(r.URL.Path, root+"/operations/")
		for _, op := range f.ops {
			if op.ID == id {
				if f.fault == "returned_id" {
					op.ID = "123e4567-e89b-12d3-a456-426614174099"
				}
				writeResponse(f.base.t, w, http.StatusOK, map[string]any{"operation": op})
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	default:
		f.base.serveHTTP(w, r)
	}
}

func TestSnapshotRestoreDeletionAcceptsRetainedTerminalCursor(t *testing.T) {
	f := newForkDeletionFixture(t)
	f.base.rows = []branch{}
	f.ops = f.deletionOps()
	for i := range f.ops {
		f.ops[i].Status = "finished"
	}
	f.fault = "terminal"
	actual, err := f.base.p.ObserveSnapshotRestoreDeletion(t.Context(), f.base.definition, f.request)
	if err != nil || !actual.Done || f.lists != 2 || f.deletes != 0 {
		t.Fatalf("terminal cursor deletion proof: %+v err=%v lists=%d deletes=%d", actual, err, f.lists, f.deletes)
	}
}

func TestSnapshotRestoreDeletionRequiresIndependentFinishedOperationsAndAbsence(t *testing.T) {
	for _, mode := range []string{"pending", "lost_reply", "branch_present"} {
		t.Run(mode, func(t *testing.T) {
			f := newForkDeletionFixture(t)
			f.ackFinished = true
			f.loseDelete = mode == "lost_reply"
			f.disappear = mode != "branch_present"
			actual, err := f.base.p.DeleteSnapshotRestore(t.Context(), f.base.definition, f.request)
			if mode == "lost_reply" {
				if !errors.Is(err, managedpostgres.ErrUnavailable) {
					t.Fatalf("lost reply: %v", err)
				}
				actual, err = f.base.p.ObserveSnapshotRestoreDeletion(t.Context(), f.base.definition, f.request)
			}
			if err != nil || actual.Done || len(actual.OperationIDs) != 2 || f.deletes != 1 {
				t.Fatalf("ACK completed deletion: %+v %v", actual, err)
			}
			f.request.OperationIDs = actual.OperationIDs
			before := f.lists
			actual, err = f.base.p.ObserveSnapshotRestoreDeletion(t.Context(), f.base.definition, f.request)
			if err != nil || actual.Done || f.lists != before || f.deletes != 1 {
				t.Fatalf("pending exact operation observation: %+v %v", actual, err)
			}
			for i := range f.ops {
				f.ops[i].Status = "finished"
			}
			actual, err = f.base.p.ObserveSnapshotRestoreDeletion(t.Context(), f.base.definition, f.request)
			if err != nil || actual.Done != (mode != "branch_present") {
				t.Fatalf("operation/absence proof: %+v %v", actual, err)
			}
			f.base.rows = []branch{}
			actual, err = f.base.p.ObserveSnapshotRestoreDeletion(t.Context(), f.base.definition, f.request)
			if err != nil || !actual.Done || f.deletes != 1 {
				t.Fatalf("terminal exact identity: %+v %v", actual, err)
			}
		})
	}
}

func TestSnapshotRestoreDeletionRejectsSubstitutedTargetsAndOperationProof(t *testing.T) {
	for _, fault := range []string{"snapshot", "name", "default", "time", "finalized", "project", "branch", "action", "failed", "missing_ids", "returned_id", "missing_operation", "missing_list", "cycle", "future_op"} {
		t.Run(fault, func(t *testing.T) {
			f := newForkDeletionFixture(t)
			f.ops = f.deletionOps()
			for i := range f.ops {
				f.ops[i].Status = "finished"
			}
			expectedPending := false
			switch fault {
			case "snapshot":
				f.base.rows[0].RestoredFrom = "snap-other"
			case "name":
				f.base.rows[0].Name = "other-owner"
			case "default":
				f.base.rows[0].Default = true
			case "time":
				f.base.rows[0].CreatedAt = f.request.TargetCreatedAt.Add(time.Second).Format(time.RFC3339Nano)
			case "finalized":
				f.base.rows[0].RestoreStatus = "finalized"
			default:
				f.base.rows = []branch{}
				f.request.OperationIDs = []string{f.ops[0].ID, f.ops[1].ID}
				switch fault {
				case "project":
					f.ops[1].ProjectID = "project-other"
				case "branch":
					f.ops[1].BranchID = "br-source"
				case "action":
					f.ops[1].Action = "create_timeline"
				case "failed":
					f.ops[1].Status = "failed"
					expectedPending = true
				case "missing_ids":
					f.request.OperationIDs = nil
					f.ops = []operation{}
				case "returned_id":
					f.fault = fault
				case "missing_operation":
					f.ops = f.ops[:1]
				case "missing_list", "cycle":
					f.request.OperationIDs = nil
					f.fault = fault
				case "future_op":
					f.ops[1].CreatedAt = time.Now().Add(time.Hour).Format(time.RFC3339Nano)
				}
			}
			actual, err := f.base.p.ObserveSnapshotRestoreDeletion(t.Context(), f.base.definition, f.request)
			if actual.Done || !expectedPending && err == nil || expectedPending && err != nil || f.deletes != 0 {
				t.Fatalf("accepted %s proof: %+v %v", fault, actual, err)
			}
		})
	}
}

func TestSnapshotRestoreDeletionDiscoveryTraversesPagesAndNeverCreates(t *testing.T) {
	f := newForkDeletionFixture(t)
	f.base.rows = []branch{}
	f.ops = f.deletionOps()
	for i := range f.ops {
		f.ops[i].Status = "finished"
	}
	f.fault = "pagination"
	actual, err := f.base.p.ObserveSnapshotRestoreDeletion(t.Context(), f.base.definition, f.request)
	if err != nil || !actual.Done || f.lists != 2 || f.deletes != 0 || f.base.posts != 0 {
		t.Fatalf("paged deletion recovery: %+v %v", actual, err)
	}
	f.request.Restore.ExpectedTargetResourceID = ""
	before := f.base.exactReads
	if _, err := f.base.p.DeleteSnapshotRestore(t.Context(), f.base.definition, f.request); !errors.Is(err, managedpostgres.ErrInvalid) || f.base.exactReads != before || f.deletes != 0 {
		t.Fatalf("unknown identity deleted: %v", err)
	}
	f.request.Restore.ExpectedTargetResourceID = "project-source/br-target"
	f.request.OperationIDs = []string{"invalid-uuid"}
	if _, err := f.base.p.DeleteSnapshotRestore(t.Context(), f.base.definition, f.request); !errors.Is(err, managedpostgres.ErrInvalid) || f.base.exactReads != before || f.deletes != 0 {
		t.Fatalf("invalid operation selector reached API: %v", err)
	}
}
