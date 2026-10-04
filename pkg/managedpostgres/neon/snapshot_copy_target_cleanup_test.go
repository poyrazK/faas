// adr: 583
package neon

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

type copyCleanupFixture struct {
	*snapshotCopyFixture
	deletedRows               []project
	deletes                   int
	loseDelete, pendingDelete bool
	cleanupFault              string
}

func newCopyCleanupFixture(t *testing.T) *copyCleanupFixture {
	t.Helper()
	f := &copyCleanupFixture{snapshotCopyFixture: newSnapshotCopyFixture(t), deletedRows: []project{}}
	f.exists = true
	f.capture.p = testProvider(t, http.HandlerFunc(f.serveCleanupHTTP))
	f.request.ExpectedProviderResourceID = f.project.ID
	f.request.ExpectedCreatedAt = f.request.CaptureCreatedAt.Add(time.Second)
	return f
}

func (f *copyCleanupFixture) serveCleanupHTTP(w http.ResponseWriter, r *http.Request) {
	t := f.capture.t
	if r.URL.Path == "/api/v2/projects" && r.Method == http.MethodGet && r.URL.Query().Get("recoverable") == "true" {
		if r.URL.Query().Get("search") != f.project.Name || r.URL.Query().Get("org_id") != f.capture.p.organizationID || r.URL.Query().Get("limit") != "400" {
			t.Error("cleanup discovery is not owner scoped")
		}
		rows := f.deletedRows
		cursor := ""
		switch f.cleanupFault {
		case "missing_list":
			writeResponse(t, w, http.StatusOK, map[string]any{})
			return
		case "unavailable":
			writeResponse(t, w, http.StatusOK, map[string]any{"projects": rows, "unavailable_project_ids": []string{"unavailable"}})
			return
		case "pagination":
			if r.URL.Query().Get("cursor") == "" {
				rows = []project{}
				cursor = "next-page"
			}
		case "duplicate":
			rows = append(append([]project{}, rows...), rows...)
		case "cycle":
			cursor = "same-page"
		}
		writeResponse(t, w, http.StatusOK, map[string]any{"projects": rows, "pagination": map[string]any{"cursor": cursor}})
		return
	}
	if r.URL.Path == "/api/v2/projects/project-independent" && r.Method == http.MethodDelete {
		f.deletes++
		if !f.pendingDelete {
			f.exists = false
			f.deletedRows = []project{f.project}
		}
		if f.loseDelete {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeResponse(t, w, http.StatusOK, projectResponse{Project: f.project})
		return
	}
	if r.URL.Path == "/api/v2/projects/project-independent" && r.Method == http.MethodGet && f.cleanupFault == "changed_creation" {
		changed := f.project
		changed.CreatedAt = f.request.CaptureCreatedAt.Add(2 * time.Second).Format(time.RFC3339Nano)
		writeResponse(t, w, http.StatusOK, projectResponse{Project: changed})
		return
	}
	f.snapshotCopyFixture.serveHTTP(w, r)
}

func TestSnapshotCopyTargetCleanupIndependentDeletionProofAndLostReply(t *testing.T) {
	for _, mode := range []string{"normal", "lost", "pending", "drift"} {
		t.Run(mode, func(t *testing.T) {
			f := newCopyCleanupFixture(t)
			f.loseDelete = mode == "lost"
			f.pendingDelete = mode == "pending"
			if mode == "drift" {
				f.project.HistoryRetentionSeconds = 0
				f.project.DefaultEndpointSettings.MaximumCU = 999
				f.endpoints = []endpoint{}
			}
			o, err := f.capture.p.DeleteSnapshotCopyTarget(t.Context(), f.capture.definition, f.request)
			if mode == "lost" {
				if !errors.Is(err, managedpostgres.ErrUnavailable) {
					t.Fatalf("lost ACK: %v", err)
				}
			} else if err != nil || o.Deleted {
				t.Fatalf("ACK supplied terminal proof: %+v %v", o, err)
			}
			if f.deletes != 1 || f.capture.posts != 0 || f.capture.exactReads != 0 {
				t.Fatal("cleanup mutated/read live source or repeated DELETE")
			}
			o, err = f.capture.p.ObserveSnapshotCopyTargetDeletion(t.Context(), f.capture.definition, f.request)
			if err != nil || o.Deleted != (mode != "pending") || f.deletes != 1 {
				t.Fatalf("independent terminal read: %+v %v", o, err)
			}
			if mode != "pending" {
				f.cleanupFault = "pagination"
				o, err = f.capture.p.DeleteSnapshotCopyTarget(t.Context(), f.capture.definition, f.request)
				if err != nil || !o.Deleted || f.deletes != 1 {
					t.Fatalf("deleted identity was mutated again: %+v %v", o, err)
				}
			}
		})
	}
}

func TestSnapshotCopyTargetCleanupRecoversUnknownOwnedIdentityAndRejectsFalseAbsence(t *testing.T) {
	f := newCopyCleanupFixture(t)
	f.request.ExpectedProviderResourceID = ""
	// An unknown dispatch supplies no creation pin, but still carries the
	// exact owner name and captured source/snapshot timestamps.
	f.request.ExpectedCreatedAt = time.Time{}
	f.cleanupFault = "changed_creation"
	if _, err := f.capture.p.DiscoverSnapshotCopyTargetForCleanup(t.Context(), f.capture.definition, f.request); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("discovery accepted a changed exact creation identity: %v", err)
	}
	f.cleanupFault = ""
	o, err := f.capture.p.DiscoverSnapshotCopyTargetForCleanup(t.Context(), f.capture.definition, f.request)
	if err != nil || o.Deleted || o.ProviderResourceID != f.project.ID || f.deletes != 0 {
		t.Fatalf("unfinished ownership discovery: %+v %v", o, err)
	}
	f.request.ExpectedProviderResourceID, f.request.ExpectedCreatedAt = o.ProviderResourceID, o.CreatedAt
	f.exists = false
	if _, err := f.capture.p.ObserveSnapshotCopyTargetDeletion(t.Context(), f.capture.definition, f.request); !errors.Is(err, managedpostgres.ErrUnavailable) {
		t.Fatalf("404 alone proved deletion: %v", err)
	}
	f.deletedRows = []project{f.project}
	unknown := f.request
	unknown.ExpectedProviderResourceID, unknown.ExpectedCreatedAt = "", time.Time{}
	if o, err := f.capture.p.DiscoverSnapshotCopyTargetForCleanup(t.Context(), f.capture.definition, unknown); err != nil || !o.Deleted || o.ProviderResourceID != f.project.ID || f.deletes != 0 {
		t.Fatalf("unknown dispatch did not recover its deleted identity: %+v %v", o, err)
	}
	for _, fault := range []string{"missing_list", "unavailable", "duplicate", "cycle", "foreign_identity", "foreign_created", "recovered"} {
		t.Run(fault, func(t *testing.T) {
			f.cleanupFault = fault
			f.deletedRows = []project{f.project}
			f.exists = false
			switch fault {
			case "foreign_identity":
				f.deletedRows[0].ID = "another-project"
			case "foreign_created":
				f.deletedRows[0].CreatedAt = f.request.CaptureCreatedAt.Format(time.RFC3339Nano)
			case "recovered":
				f.exists = true
			}
			if _, err := f.capture.p.ObserveSnapshotCopyTargetDeletion(t.Context(), f.capture.definition, f.request); err == nil || f.deletes != 0 {
				t.Fatalf("accepted %s terminal proof: %v", fault, err)
			}
		})
	}
}
