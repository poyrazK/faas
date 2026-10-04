// adr:567
package neon

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

type snapshotCopyFixture struct {
	capture                     *snapshotRestoreFixture
	request                     managedpostgres.SnapshotCopyTargetRequest
	project                     project
	branches                    []branch
	endpoints                   []endpoint
	ops                         []operation
	exists, invisible, losePost bool
	posts, lists, exactReads    int
	fault                       string
}

func newSnapshotCopyFixture(t *testing.T) *snapshotCopyFixture {
	t.Helper()
	capture := newSnapshotRestoreFixture(t)
	capture.rows = []branch{capture.target()}
	capture.request.ExpectedTargetResourceID = "project-source/br-target"
	f := &snapshotCopyFixture{capture: capture}
	f.request = managedpostgres.SnapshotCopyTargetRequest{ResourceID: "independent-target-owner", Capture: capture.request,
		SnapshotCreatedAt: capture.request.Snapshot.PointInTime.Add(time.Minute), CaptureCreatedAt: capture.request.Snapshot.PointInTime.Add(time.Hour - time.Minute)}
	capture.p = testProvider(t, http.HandlerFunc(f.serveHTTP))
	spec := &capture.definition.Spec
	spec.StorageLimitBytes, spec.RestoreWindowSeconds = 1<<30, 3600
	f.project = project{ID: "project-independent", OrganizationID: capture.p.organizationID, RegionID: capture.p.regionID,
		Name: capture.p.snapshotCopyProjectName(f.request.ResourceID), PostgresMajor: spec.PostgresMajor, HistoryRetentionSeconds: spec.RestoreWindowSeconds,
		CreatedAt: f.request.CaptureCreatedAt.Add(time.Second).Format(time.RFC3339Nano), DefaultEndpointSettings: endpointSettingsForSpec(*spec)}
	f.project.Settings.Quota.LogicalSizeBytes = &spec.StorageLimitBytes
	f.branches = []branch{{ID: "br-independent", ProjectID: f.project.ID, Name: "production", Default: true, CurrentState: "ready"}}
	disabled := false
	settings := endpointSettingsForSpec(*spec)
	f.endpoints = []endpoint{{ID: "ep-independent", ProjectID: f.project.ID, RegionID: capture.p.regionID, BranchID: "br-independent", Type: "read_write", CurrentState: "active", Disabled: &disabled,
		MinimumCU: settings.MinimumCU, MaximumCU: settings.MaximumCU, SuspendTimeoutSecond: settings.SuspendTimeoutSecond}}
	f.ops = []operation{{ID: "create-independent", ProjectID: f.project.ID, BranchID: "br-independent", Status: "finished"}}
	return f
}

func (f *snapshotCopyFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/v2/projects/project-source") {
		f.capture.serveHTTP(w, r)
		return
	}
	t := f.capture.t
	const root = "/api/v2/projects/project-independent"
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v2/projects":
		f.lists++
		if r.URL.Query().Get("search") != f.project.Name || r.URL.Query().Get("org_id") != f.capture.p.organizationID || r.URL.Query().Get("limit") != "400" {
			t.Error("project recovery lacks private owner/organization")
		}
		rows := []project{}
		if f.exists && !f.invisible {
			rows = append(rows, f.project)
		}
		cursor := ""
		switch f.fault {
		case "missing_list":
			writeResponse(t, w, http.StatusOK, map[string]any{})
			return
		case "unavailable":
			writeResponse(t, w, http.StatusOK, map[string]any{"projects": rows, "unavailable": []string{"missing-project"}})
			return
		case "unavailable_project_ids":
			writeResponse(t, w, http.StatusOK, map[string]any{"projects": rows, "unavailable_project_ids": []string{"missing-project"}})
			return
		case "cycle":
			cursor = "same-page"
		case "duplicate":
			rows = append(rows, f.project)
		case "pagination":
			if r.URL.Query().Get("cursor") == "" {
				rows, cursor = []project{}, "next-page"
			}
		}
		writeResponse(t, w, http.StatusOK, map[string]any{"projects": rows, "pagination": map[string]any{"cursor": cursor}})
	case r.Method == http.MethodPost && r.URL.Path == "/api/v2/projects":
		f.posts++
		var body createProjectRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body != f.capture.p.projectPayload(f.project.Name, f.capture.definition.Spec) {
			// The payload contains a pointer to the storage setting; compare
			// serialized values below rather than pointer addresses.
			actual, _ := json.Marshal(body)
			want, _ := json.Marshal(f.capture.p.projectPayload(f.project.Name, f.capture.definition.Spec))
			if err != nil || string(actual) != string(want) {
				t.Error("project creation changed frozen configuration")
			}
		}
		f.exists = true
		if f.losePost {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeResponse(t, w, http.StatusCreated, createdProjectResponse{Project: f.project, Operations: []operation{{Status: "finished"}}})
	case r.Method == http.MethodGet && r.URL.Path == root:
		f.exactReads++
		if !f.exists {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeResponse(t, w, http.StatusOK, projectResponse{Project: f.project})
	case r.Method == http.MethodGet && r.URL.Path == root+"/branches":
		writeResponse(t, w, http.StatusOK, branchesResponse{Branches: f.branches})
	case r.Method == http.MethodGet && r.URL.Path == root+"/endpoints":
		writeResponse(t, w, http.StatusOK, endpointsResponse{Endpoints: f.endpoints})
	case r.Method == http.MethodGet && r.URL.Path == root+"/operations":
		writeResponse(t, w, http.StatusOK, operationsResponse{Operations: f.ops})
	default:
		t.Errorf("unexpected target mutation/path: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func TestSnapshotCopyTargetPreparesIndependentProjectAndRecoversLostPost(t *testing.T) {
	for _, mode := range []string{"ready", "pending", "lost", "lost_invisible"} {
		t.Run(mode, func(t *testing.T) {
			f := newSnapshotCopyFixture(t)
			f.losePost = mode == "lost" || mode == "lost_invisible"
			f.invisible = mode == "lost_invisible"
			if mode == "pending" {
				f.ops[0].Status = "running"
			}
			o, err := f.capture.p.PrepareSnapshotCopyTarget(t.Context(), f.capture.definition, f.request)
			if mode == "lost_invisible" {
				if !errors.Is(err, managedpostgres.ErrUnavailable) {
					t.Fatalf("unknown dispatch: %v", err)
				}
				if _, err := f.capture.p.FindSnapshotCopyTarget(t.Context(), f.capture.definition, f.request); !errors.Is(err, managedpostgres.ErrNotFound) || f.posts != 1 {
					t.Fatalf("unknown create was repeated: %v", err)
				}
				f.invisible = false
				o, err = f.capture.p.FindSnapshotCopyTarget(t.Context(), f.capture.definition, f.request)
			}
			if err != nil || o.ProviderResourceID != "project-independent" || o.Spec != f.capture.definition.Spec || o.Prepared != (mode != "pending") || f.posts != 1 || f.exactReads != 1 {
				t.Fatalf("independent preparation: %+v %v", o, err)
			}
			f.request.ExpectedProviderResourceID, f.request.ExpectedCreatedAt = o.ProviderResourceID, o.CreatedAt
			before := f.lists
			f.ops[0].Status = "finished"
			o, err = f.capture.p.FindSnapshotCopyTarget(t.Context(), f.capture.definition, f.request)
			if err != nil || !o.Prepared || f.lists != before || f.posts != 1 || f.capture.posts != 0 {
				t.Fatalf("pinned recovery recreated/finalized source: %+v %v", o, err)
			}
		})
	}
}

func TestSnapshotCopyTargetRejectsChangedConfigOwnershipAndCapture(t *testing.T) {
	for _, fault := range []string{"name", "organization", "region", "major", "created", "storage", "retention", "default_settings", "endpoint_settings", "suspend", "disabled", "branch_parent", "branch_project", "endpoint_project", "operation_project", "operation_error", "capture_time", "capture_finalized"} {
		t.Run(fault, func(t *testing.T) {
			f := newSnapshotCopyFixture(t)
			f.exists = true
			f.request.ExpectedProviderResourceID = f.project.ID
			f.request.ExpectedCreatedAt = f.request.CaptureCreatedAt.Add(time.Second)
			switch fault {
			case "name":
				f.project.Name = "foreign-owner"
			case "organization":
				f.project.OrganizationID = "org-other"
			case "region":
				f.project.RegionID = "aws-us-east-1"
			case "major":
				f.project.PostgresMajor++
			case "created":
				f.project.CreatedAt = f.request.CaptureCreatedAt.Add(2 * time.Second).Format(time.RFC3339Nano)
			case "storage":
				value := int64(123)
				f.project.Settings.Quota.LogicalSizeBytes = &value
			case "retention":
				f.project.HistoryRetentionSeconds++
			case "default_settings":
				f.project.DefaultEndpointSettings.MaximumCU++
			case "endpoint_settings":
				f.endpoints[0].MaximumCU++
			case "suspend":
				f.endpoints[0].SuspendTimeoutSecond = 42
			case "disabled":
				value := true
				f.endpoints[0].Disabled = &value
			case "branch_parent":
				f.branches[0].ParentID = "br-source"
			case "branch_project":
				f.branches[0].ProjectID = "project-source"
			case "endpoint_project":
				f.endpoints[0].ProjectID = "project-source"
			case "operation_project":
				f.ops[0].ProjectID = "project-source"
			case "operation_error":
				f.ops[0].Status = "error"
			case "capture_time":
				f.request.CaptureCreatedAt = f.request.CaptureCreatedAt.Add(time.Second)
			case "capture_finalized":
				f.capture.rows[0].RestoreStatus = "finalized"
			}
			if _, err := f.capture.p.FindSnapshotCopyTarget(t.Context(), f.capture.definition, f.request); err == nil || f.posts != 0 || f.capture.posts != 0 {
				t.Fatalf("accepted %s: %v", fault, err)
			}
		})
	}
}

func TestSnapshotCopyTargetDiscoveryRejectsIncompleteListsAndNeverRecreatesPinnedProject(t *testing.T) {
	for _, fault := range []string{"missing_list", "unavailable", "unavailable_project_ids", "cycle", "duplicate", "pagination", "known_missing", "source_project"} {
		t.Run(fault, func(t *testing.T) {
			f := newSnapshotCopyFixture(t)
			f.exists = true
			f.fault = fault
			if fault == "known_missing" || fault == "source_project" {
				f.request.ExpectedProviderResourceID = f.project.ID
				f.request.ExpectedCreatedAt = f.request.CaptureCreatedAt.Add(time.Second)
				if fault == "known_missing" {
					f.exists = false
				} else {
					f.request.ExpectedProviderResourceID = "project-source"
				}
			}
			o, err := f.capture.p.PrepareSnapshotCopyTarget(t.Context(), f.capture.definition, f.request)
			if fault == "pagination" {
				if err != nil || !o.Prepared || f.lists != 2 {
					t.Fatalf("incomplete pagination: %+v %v", o, err)
				}
			} else if err == nil {
				t.Fatalf("accepted %s", fault)
			}
			if f.posts != 0 || f.capture.posts != 0 {
				t.Fatal("recovery repeated a mutation")
			}
		})
	}
}
