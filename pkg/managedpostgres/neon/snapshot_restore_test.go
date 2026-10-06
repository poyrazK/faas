// adr: 590
package neon

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

type snapshotRestoreFixture struct {
	t                             *testing.T
	p                             *Provider
	mu                            sync.Mutex
	definition                    managedpostgres.RestoreSourceDefinition
	request                       managedpostgres.SnapshotRestoreRequest
	snapshot                      snapshot
	rows                          []branch
	posts, lists, exactReads      int
	losePost, invisible, ackReady bool
	fault                         string
}

func newSnapshotRestoreFixture(t *testing.T) *snapshotRestoreFixture {
	t.Helper()
	point := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	f := &snapshotRestoreFixture{t: t, rows: []branch{},
		definition: managedpostgres.RestoreSourceDefinition{Spec: managedpostgres.Spec{Region: "eu-central-1", PostgresMajor: 17,
			Class: managedpostgres.ClassDevelopment, Availability: managedpostgres.AvailabilitySingleZone}, ProviderResourceID: "project-source", DataResourceID: "project-source/br-source"},
		request: managedpostgres.SnapshotRestoreRequest{ResourceID: "private-clone-target", ProviderSnapshotID: "project-source/snapshots/snap-owned",
			Snapshot: managedpostgres.SnapshotCaptureRequest{ResourceID: "operation/source", SourceResourceID: "project-source/br-source", IdempotencyKey: "capture", PointInTime: point}}}
	f.p = testProvider(t, http.HandlerFunc(f.serveHTTP))
	f.snapshot = snapshot{ID: "snap-owned", Name: f.p.snapshotName(f.request.Snapshot.ResourceID), SourceBranchID: "br-source",
		Timestamp: point.Format(time.RFC3339Nano), CreatedAt: point.Add(time.Minute).Format(time.RFC3339Nano), ExpiresAt: json.RawMessage("null"), Manual: true}
	return f
}

func (f *snapshotRestoreFixture) target() branch {
	return branch{ID: "br-target", ProjectID: "project-source", Name: f.p.restoreBranchName(f.request.ResourceID), CurrentState: "ready", RestoreStatus: "restored",
		RestoredFrom: "snap-owned", RestoredAs: "br-source", CreatedAt: f.request.Snapshot.PointInTime.Add(time.Hour - time.Minute).Format(time.RFC3339Nano)}
}

func (f *snapshotRestoreFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	const root = "/api/v2/projects/project-source"
	switch {
	case r.Method == http.MethodGet && r.URL.Path == root:
		value := project{ID: "project-source", OrganizationID: "org-gregale-12345678", RegionID: "aws-eu-central-1", PostgresMajor: f.definition.Spec.PostgresMajor}
		switch f.fault {
		case "project":
			value.ID = "project-other"
		case "organization":
			value.OrganizationID = "org-other"
		case "region":
			value.RegionID = "aws-us-east-1"
		case "major":
			value.PostgresMajor = 18
		case "placement_changed":
			if f.exactReads > 0 {
				value.PostgresMajor = 18
			}
		}
		writeResponse(f.t, w, http.StatusOK, projectResponse{Project: value})
	case r.Method == http.MethodGet && r.URL.Path == root+"/snapshots":
		writeResponse(f.t, w, http.StatusOK, map[string]any{"snapshots": []snapshot{f.snapshot}})
	case r.Method == http.MethodGet && r.URL.Path == root+"/branches":
		f.lists++
		q := r.URL.Query()
		if q.Get("search") != f.p.restoreBranchName(f.request.ResourceID) || q.Get("limit") != "10000" {
			f.t.Error("discovery did not scope the private target name")
		}
		if f.fault == "missing_list" {
			writeResponse(f.t, w, http.StatusOK, map[string]any{})
			return
		}
		rows := f.rows
		if f.invisible {
			rows = []branch{}
		}
		next := ""
		if f.fault == "pagination" && q.Get("cursor") == "" {
			rows = []branch{}
			next = "next-page"
		}
		if f.fault == "cursor_cycle" {
			next = "same-page"
			rows = []branch{}
		}
		if f.fault == "duplicate_pages" && q.Get("cursor") == "" {
			next = "next-page"
		}
		writeResponse(f.t, w, http.StatusOK, map[string]any{"branches": rows, "pagination": map[string]any{"next": next}})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, root+"/branches/"):
		f.exactReads++
		id := strings.TrimPrefix(r.URL.Path, root+"/branches/")
		for _, row := range f.rows {
			if row.ID == id {
				writeResponse(f.t, w, http.StatusOK, map[string]any{"branch": row})
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	case r.Method == http.MethodPost && r.URL.Path == root+"/snapshots/snap-owned/restore":
		f.posts++
		var payload map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || len(payload) != 3 || string(payload["finalize_restore"]) != "false" || string(payload["target_branch_id"]) != `"br-source"` || string(payload["name"]) != `"`+f.p.restoreBranchName(f.request.ResourceID)+`"` || len(r.URL.Query()) != 0 {
			f.t.Error("restore did not explicitly preview the owned snapshot into the reserved target")
		}
		actual := f.target()
		if f.ackReady {
			actual.CurrentState = "init"
		}
		f.rows = append(f.rows, actual)
		if f.losePost {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		accepted := actual
		if f.ackReady {
			accepted.CurrentState = "ready"
		}
		writeResponse(f.t, w, http.StatusOK, map[string]any{"branch": accepted, "operations": []any{map[string]any{"status": "running"}}})
	default:
		f.t.Errorf("unexpected snapshot restore mutation/path: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func TestSnapshotRestoreCreatesOwnedPreviewAndIndependentlyObservesCompletion(t *testing.T) {
	for _, mode := range []string{"ready", "pending", "lost_post", "lost_post_invisible"} {
		t.Run(mode, func(t *testing.T) {
			f := newSnapshotRestoreFixture(t)
			f.ackReady = mode == "pending"
			f.losePost = mode == "lost_post" || mode == "lost_post_invisible"
			f.invisible = mode == "lost_post_invisible"
			actual, err := f.p.RestoreSnapshot(t.Context(), f.definition, f.request)
			if mode == "lost_post_invisible" {
				if !errors.Is(err, managedpostgres.ErrUnavailable) {
					t.Fatalf("unknown POST outcome: %v", err)
				}
				if _, err := f.p.FindSnapshotRestore(t.Context(), f.definition, f.request); !errors.Is(err, managedpostgres.ErrNotFound) || f.posts != 1 {
					t.Fatalf("missing discovery repeated POST: %v", err)
				}
				f.invisible = false
				actual, err = f.p.FindSnapshotRestore(t.Context(), f.definition, f.request)
			}
			if err != nil || actual.ProviderResourceID != "project-source/br-target" || actual.ProviderSnapshotID != f.request.ProviderSnapshotID || actual.SourceDataResourceID != f.definition.DataResourceID || !actual.PointInTime.Equal(f.request.Snapshot.PointInTime) || actual.Restored != (mode != "pending") || f.posts != 1 {
				t.Fatalf("restore %s: %+v %v", mode, actual, err)
			}
			if mode == "pending" {
				if f.exactReads != 1 {
					t.Fatal("creation acknowledgement substituted for target observation")
				}
				f.rows[0].CurrentState = "ready"
				actual, err = f.p.FindSnapshotRestore(t.Context(), f.definition, f.request)
				if err != nil || !actual.Restored || f.posts != 1 {
					t.Fatalf("asynchronous completion: %+v %v", actual, err)
				}
			}
			f.request.ExpectedTargetResourceID = actual.ProviderResourceID
			before := f.lists
			if _, err := f.p.FindSnapshotRestore(t.Context(), f.definition, f.request); err != nil || f.lists != before || f.posts != 1 {
				t.Fatalf("exact target recovery used mutable name selection: %v", err)
			}
		})
	}
}

func TestSnapshotRestoreRejectsSourceSnapshotAndTargetSubstitution(t *testing.T) {
	for _, fault := range []string{"project", "organization", "region", "major", "snapshot_name", "snapshot_source", "snapshot_point", "snapshot_expiry", "target_snapshot", "target_project", "target_source", "target_name", "target_default", "finalized", "target_time", "missing_lineage", "placement_changed", "missing_list", "cursor_cycle", "duplicate_pages"} {
		t.Run(fault, func(t *testing.T) {
			f := newSnapshotRestoreFixture(t)
			f.rows = []branch{f.target()}
			f.fault = fault
			switch fault {
			case "snapshot_name":
				f.snapshot.Name = "other-owner"
			case "snapshot_source":
				f.snapshot.SourceBranchID = "br-other"
			case "snapshot_point":
				f.snapshot.Timestamp = f.request.Snapshot.PointInTime.Add(time.Second).Format(time.RFC3339Nano)
			case "snapshot_expiry":
				f.snapshot.ExpiresAt, _ = json.Marshal(time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano))
			case "target_snapshot":
				f.rows[0].RestoredFrom = "snap-other"
			case "target_project":
				f.rows[0].ProjectID = "project-other"
			case "target_source":
				f.rows[0].ID = "br-source"
			case "target_name":
				f.request.ExpectedTargetResourceID = "project-source/br-target"
				f.rows[0].Name = "other-owner"
			case "target_default":
				f.rows[0].Default = true
			case "finalized":
				f.rows[0].RestoreStatus = "finalized"
			case "target_time":
				f.rows[0].CreatedAt = f.request.Snapshot.PointInTime.Format(time.RFC3339Nano)
			case "missing_lineage":
				f.rows[0].RestoredFrom = ""
			case "placement_changed":
				f.request.ExpectedTargetResourceID = "project-source/br-target"
			}
			if _, err := f.p.FindSnapshotRestore(t.Context(), f.definition, f.request); err == nil {
				t.Fatalf("accepted %s", fault)
			}
			if f.posts != 0 {
				t.Fatal("readiness/discovery created a target")
			}
		})
	}
}

func TestSnapshotRestoreDiscoveryReadsEveryPageAndNeverRecreatesKnownMissingTarget(t *testing.T) {
	f := newSnapshotRestoreFixture(t)
	f.rows = []branch{f.target()}
	f.fault = "pagination"
	actual, err := f.p.FindSnapshotRestore(t.Context(), f.definition, f.request)
	if err != nil || !actual.Restored || f.lists != 2 || f.posts != 0 {
		t.Fatalf("pagination recovery: %+v %v", actual, err)
	}
	f.request.ExpectedTargetResourceID = "project-source/br-missing"
	if _, err := f.p.RestoreSnapshot(t.Context(), f.definition, f.request); !errors.Is(err, managedpostgres.ErrNotFound) || f.posts != 0 {
		t.Fatalf("known missing target recreated: %v", err)
	}
}

func TestSnapshotRestoreRejectsUnpinnedSelectorsBeforeProviderIO(t *testing.T) {
	for _, fault := range []string{"lifecycle_project", "lifecycle_branch", "snapshot_project", "source_alias", "source_changed", "target_project", "target_source", "target_alias", "logical_region", "unsupported_major", "point", "owner"} {
		t.Run(fault, func(t *testing.T) {
			f := newSnapshotRestoreFixture(t)
			switch fault {
			case "lifecycle_project":
				f.definition.ProviderResourceID = "project-other"
			case "lifecycle_branch":
				f.definition.ProviderResourceID = "project-source/br-other"
			case "snapshot_project":
				f.request.ProviderSnapshotID = "project-other/snapshots/snap-owned"
			case "source_alias":
				f.definition.DataResourceID, f.request.Snapshot.SourceResourceID = "project-source", "project-source"
			case "source_changed":
				f.definition.DataResourceID = "project-source/br-other"
			case "target_project":
				f.request.ExpectedTargetResourceID = "project-other/br-target"
			case "target_source":
				f.request.ExpectedTargetResourceID = "project-source/br-source"
			case "target_alias":
				f.request.ExpectedTargetResourceID = "project-source"
			case "logical_region":
				f.definition.Spec.Region = "us-east-1"
			case "unsupported_major":
				f.definition.Spec.PostgresMajor = 99
			case "point":
				f.request.Snapshot.PointInTime = time.Time{}
			case "owner":
				f.request.ResourceID = ""
			}
			if _, err := f.p.RestoreSnapshot(t.Context(), f.definition, f.request); err == nil || f.posts != 0 || f.lists != 0 || f.exactReads != 0 {
				t.Fatalf("invalid %s reached provider: %v", fault, err)
			}
		})
	}
}
