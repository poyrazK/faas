// adr: 569
package neon

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

type readerDeletionFixture struct {
	reader                   *snapshotReaderFixture
	fork                     *forkDeletionFixture
	request                  managedpostgres.SnapshotCopyReaderDeletionRequest
	ops                      []operation
	deletes, absentReads     int
	loseDelete, keepEndpoint bool
	ackFinished, emptyAck    bool
	fault                    string
}

func newReaderDeletionFixture(t *testing.T) *readerDeletionFixture {
	t.Helper()
	reader := newSnapshotReaderFixture(t)
	// Leave real elapsed time for creation and cleanup dispatch pins.
	reader.request.CaptureCreatedAt = reader.request.CaptureCreatedAt.Add(-time.Minute)
	reader.request.RequestedAt = reader.request.RequestedAt.Add(-time.Minute)
	reader.base.rows[0].CreatedAt = reader.request.CaptureCreatedAt.Format(time.RFC3339Nano)
	reader.rows = []endpoint{reader.owned()}
	reader.request.ExpectedEndpointID = "ep-owned"
	reader.request.ExpectedCreatedAt = reader.request.CaptureCreatedAt.Add(time.Minute)
	f := &readerDeletionFixture{reader: reader, request: managedpostgres.SnapshotCopyReaderDeletionRequest{Reader: reader.request,
		RequestedAt: reader.request.ExpectedCreatedAt.Add(time.Second)}}
	f.fork = &forkDeletionFixture{base: reader.base, ops: []operation{}, request: copyReaderCaptureDeletionRequest(f.request)}
	reader.base.p = testProvider(t, http.HandlerFunc(f.serveHTTP))
	return f
}

func (f *readerDeletionFixture) deletionOps() []operation {
	return []operation{{ID: "123e4567-e89b-12d3-a456-426614174050", ProjectID: "project-source", BranchID: "br-target", EndpointID: "ep-owned",
		Action: "suspend_compute", Status: "running", CreatedAt: f.request.RequestedAt.Add(time.Second).Format(time.RFC3339Nano)}}
}

func (f *readerDeletionFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	const root = "/api/v2/projects/project-source"
	switch {
	case r.Method == http.MethodDelete && r.URL.Path == root+"/endpoints/ep-owned":
		f.deletes++
		if len(r.URL.Query()) != 0 || len(f.reader.rows) != 1 {
			f.reader.base.t.Error("cleanup changed selectors or deleted unknown endpoint")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		actual := f.reader.rows[0]
		f.ops = f.deletionOps()
		if !f.keepEndpoint {
			f.reader.rows = []endpoint{}
		}
		if f.loseDelete {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if f.emptyAck {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		ack := append([]operation{}, f.ops...)
		if f.ackFinished {
			ack[0].Status = "finished"
		}
		if f.fault == "ack_endpoint" {
			actual.ID = "ep-other"
		}
		writeResponse(f.reader.base.t, w, http.StatusOK, map[string]any{"endpoint": actual, "operations": ack})
	case r.Method == http.MethodGet && r.URL.Path == root+"/endpoints/ep-owned" && len(f.reader.rows) == 0:
		f.absentReads++
		if f.fault == "reappears" && f.absentReads > 1 {
			writeResponse(f.reader.base.t, w, http.StatusOK, map[string]any{"endpoint": f.reader.owned()})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, root+"/operations/"):
		id := strings.TrimPrefix(r.URL.Path, root+"/operations/")
		for _, op := range f.ops {
			if op.ID == id {
				if f.fault == "returned_id" {
					op.ID = "123e4567-e89b-12d3-a456-426614174099"
				}
				writeResponse(f.reader.base.t, w, http.StatusOK, map[string]any{"operation": op})
				return
			}
		}
		f.fork.serveHTTP(w, r)
	case strings.Contains(r.URL.Path, "/snapshots"):
		f.reader.base.t.Error("cleanup required disposed source snapshot")
		w.WriteHeader(http.StatusNotFound)
	case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/endpoints"):
		f.reader.serveHTTP(w, r)
	default:
		f.fork.serveHTTP(w, r)
	}
}

func (f *readerDeletionFixture) finishCaptureDeletion() {
	f.reader.base.rows = []branch{}
	f.fork.ops = f.fork.deletionOps()
	for i := range f.fork.ops {
		f.fork.ops[i].Status = "finished"
	}
}

func TestSnapshotCopyReaderDeletionRequiresExactChainAndIndependentAbsence(t *testing.T) {
	for _, mode := range []string{"deleted", "present"} {
		t.Run(mode, func(t *testing.T) {
			f := newReaderDeletionFixture(t)
			f.keepEndpoint = mode == "present"
			f.ackFinished = true
			o, err := f.reader.base.p.DeleteSnapshotCopyReader(t.Context(), f.reader.base.definition, f.request)
			if err != nil || o.Done || len(o.OperationIDs) != 1 || f.deletes != 1 {
				t.Fatalf("ack completed cleanup: %+v %v", o, err)
			}
			f.request.OperationIDs = o.OperationIDs
			o, err = f.reader.base.p.ObserveSnapshotCopyReaderDeletion(t.Context(), f.reader.base.definition, f.request)
			if err != nil || o.Done || f.deletes != 1 || f.fork.lists != 0 {
				t.Fatalf("pending/read-only recovery: %+v %v", o, err)
			}
			f.ops[0].Status = "finished"
			o, err = f.reader.base.p.ObserveSnapshotCopyReaderDeletion(t.Context(), f.reader.base.definition, f.request)
			if err != nil || o.Done != (mode == "deleted") || f.deletes != 1 {
				t.Fatalf("terminal chain bypassed absence: %+v %v", o, err)
			}
			f.reader.rows = []endpoint{}
			o, err = f.reader.base.p.ObserveSnapshotCopyReaderDeletion(t.Context(), f.reader.base.definition, f.request)
			if err != nil || !o.Done || f.deletes != 1 || f.fork.deletes != 0 || f.reader.posts != 0 {
				t.Fatalf("exact cleanup proof: %+v %v", o, err)
			}
		})
	}
}

func TestSnapshotCopyReaderDeletionLostAckCannotAdoptAutosuspend(t *testing.T) {
	for _, mode := range []string{"lost", "empty_ack", "lost_present"} {
		t.Run(mode, func(t *testing.T) {
			f := newReaderDeletionFixture(t)
			f.loseDelete, f.emptyAck, f.keepEndpoint = mode != "empty_ack", mode == "empty_ack", mode == "lost_present"
			if _, err := f.reader.base.p.DeleteSnapshotCopyReader(t.Context(), f.reader.base.definition, f.request); !errors.Is(err, managedpostgres.ErrUnavailable) || f.deletes != 1 {
				t.Fatalf("unknown deletion: %v", err)
			}
			f.ops[0].Status = "finished"
			o, err := f.reader.base.p.ObserveSnapshotCopyReaderDeletion(t.Context(), f.reader.base.definition, f.request)
			if !errors.Is(err, managedpostgres.ErrUnavailable) || o.Done || f.deletes != 1 || f.fork.lists != 0 {
				t.Fatalf("unacknowledged suspend supplied deletion authority: %+v %v", o, err)
			}
			f.finishCaptureDeletion()
			o, err = f.reader.base.p.ObserveSnapshotCopyReaderDeletion(t.Context(), f.reader.base.definition, f.request)
			if err != nil || o.Done != (mode != "lost_present") || len(o.CaptureOperationIDs) != 2 || f.deletes != 1 {
				t.Fatalf("capture deletion assumed endpoint cascade: %+v %v", o, err)
			}
			f.request.CaptureOperationIDs = o.CaptureOperationIDs
			f.reader.rows = []endpoint{}
			o, err = f.reader.base.p.ObserveSnapshotCopyReaderDeletion(t.Context(), f.reader.base.definition, f.request)
			if err != nil || !o.Done || f.deletes != 1 {
				t.Fatalf("pinned capture recovery: %+v %v", o, err)
			}
		})
	}
}

func TestSnapshotCopyReaderDeletionRejectsForeignEndpointAndNativeCapture(t *testing.T) {
	for _, fault := range []string{"endpoint", "branch", "name", "type", "created", "capture_time", "capture_default", "capture_finalized", "organization", "ack_endpoint"} {
		t.Run(fault, func(t *testing.T) {
			f := newReaderDeletionFixture(t)
			switch fault {
			case "endpoint":
				f.request.Reader.ExpectedEndpointID = "ep-source"
			case "branch":
				f.reader.rows[0].BranchID = "br-source"
			case "name":
				f.reader.rows[0].Name = "other-owner"
			case "type":
				f.reader.rows[0].Type = "read_write"
			case "created":
				f.reader.rows[0].CreatedAt = f.request.Reader.ExpectedCreatedAt.Add(time.Second).Format(time.RFC3339Nano)
			case "capture_time":
				f.reader.base.rows[0].CreatedAt = f.request.Reader.CaptureCreatedAt.Add(time.Second).Format(time.RFC3339Nano)
			case "capture_default":
				f.reader.base.rows[0].Default = true
			case "capture_finalized":
				f.reader.base.rows[0].RestoreStatus = "finalized"
			case "organization":
				f.reader.base.fault = "organization"
			case "ack_endpoint":
				f.fault = fault
			}
			o, err := f.reader.base.p.DeleteSnapshotCopyReader(t.Context(), f.reader.base.definition, f.request)
			if err == nil || o.Done || fault != "ack_endpoint" && f.deletes != 0 {
				t.Fatalf("foreign cleanup accepted: %+v %v", o, err)
			}
		})
	}
}

func TestSnapshotCopyReaderDeletionRejectsSubstitutedOperationChains(t *testing.T) {
	for _, fault := range []string{"project", "branch", "endpoint", "action", "old", "future", "precision", "returned_id", "missing", "duplicates", "oversize", "failed", "skipped", "reappears"} {
		t.Run(fault, func(t *testing.T) {
			f := newReaderDeletionFixture(t)
			f.reader.rows = []endpoint{}
			f.ops = f.deletionOps()
			f.ops[0].Status = "finished"
			f.request.OperationIDs = []string{f.ops[0].ID}
			pending := false
			switch fault {
			case "project":
				f.ops[0].ProjectID = "project-other"
			case "branch":
				f.ops[0].BranchID = "br-source"
			case "endpoint":
				f.ops[0].EndpointID = "ep-source"
			case "action":
				f.ops[0].Action = "start_compute"
			case "old":
				f.ops[0].CreatedAt = f.request.RequestedAt.Add(-time.Second).Format(time.RFC3339Nano)
			case "future":
				f.ops[0].CreatedAt = time.Now().Add(time.Hour).Format(time.RFC3339Nano)
			case "precision":
				f.ops[0].CreatedAt = f.request.RequestedAt.Add(time.Nanosecond).Format(time.RFC3339Nano)
			case "returned_id", "reappears":
				f.fault = fault
			case "missing":
				f.ops = nil
			case "duplicates":
				f.request.OperationIDs = append(f.request.OperationIDs, f.request.OperationIDs[0])
			case "oversize":
				f.request.OperationIDs = make([]string, api.PostgresCopyReaderMaxOperations+1)
			case "failed", "skipped":
				f.ops[0].Status, pending = fault, true
			}
			o, err := f.reader.base.p.ObserveSnapshotCopyReaderDeletion(t.Context(), f.reader.base.definition, f.request)
			if o.Done || !pending && err == nil || pending && err != nil || f.deletes != 0 {
				t.Fatalf("invalid operation chain accepted: %+v %v", o, err)
			}
		})
	}
}

func TestSnapshotCopyReaderDeletionRequiresQualifiedCaptureAbsence(t *testing.T) {
	for _, fault := range []string{"missing_chain", "pending", "foreign_branch", "endpoint_remains"} {
		t.Run(fault, func(t *testing.T) {
			f := newReaderDeletionFixture(t)
			f.finishCaptureDeletion()
			f.reader.rows = []endpoint{}
			switch fault {
			case "missing_chain":
				f.fork.ops = nil
			case "pending":
				f.fork.ops[1].Status = "running"
			case "foreign_branch":
				f.fork.ops[1].BranchID = "br-source"
			case "endpoint_remains":
				f.reader.rows = []endpoint{f.reader.owned()}
			}
			o, err := f.reader.base.p.ObserveSnapshotCopyReaderDeletion(t.Context(), f.reader.base.definition, f.request)
			if o.Done || fault != "endpoint_remains" && err == nil || f.deletes != 0 || f.fork.deletes != 0 {
				t.Fatalf("capture proof bypassed exact endpoint: %+v %v", o, err)
			}
		})
	}
}
