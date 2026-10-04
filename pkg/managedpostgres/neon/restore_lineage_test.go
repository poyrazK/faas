// adr: 583
package neon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestRestoreValidatesProviderLineageOnCreateAndRecovery(t *testing.T) {
	point := time.Date(2026, 9, 5, 11, 0, 0, 123456000, time.UTC)
	faults := []struct {
		name string
		edit func(*branch)
		want error
	}{
		{"valid", func(*branch) {}, nil},
		{"equivalent_timezone", func(b *branch) {
			b.ParentTimestamp = point.In(time.FixedZone("source", 3*3600)).Format(time.RFC3339Nano)
		}, nil},
		{"wrong_parent", func(b *branch) { b.ParentID = "br-other-123" }, managedpostgres.ErrConflict},
		{"wrong_point", func(b *branch) { b.ParentTimestamp = point.Add(time.Microsecond).Format(time.RFC3339Nano) }, managedpostgres.ErrConflict},
		{"wrong_project", func(b *branch) { b.ProjectID = "other-project-123" }, managedpostgres.ErrConflict},
		{"self_parent", func(b *branch) { b.ID = b.ParentID }, managedpostgres.ErrConflict},
		{"schema_only", func(b *branch) { b.InitSource = "parent-schema" }, managedpostgres.ErrConflict},
		{"missing_parent", func(b *branch) { b.ParentID = "" }, managedpostgres.ErrUnavailable},
		{"missing_point", func(b *branch) { b.ParentTimestamp = "" }, managedpostgres.ErrUnavailable},
		{"missing_project", func(b *branch) { b.ProjectID = "" }, managedpostgres.ErrUnavailable},
		{"missing_init_source", func(b *branch) { b.InitSource = "" }, managedpostgres.ErrUnavailable},
		{"malformed_point", func(b *branch) { b.ParentTimestamp = "invalid" }, managedpostgres.ErrUnavailable},
	}
	for _, path := range []string{"created", "existing", "ambiguous_response"} {
		for _, fault := range faults {
			t.Run(path+"/"+fault.name, func(t *testing.T) {
				request := managedpostgres.RestoreRequest{ResourceID: "44444444-4444-4444-4444-444444444444", SourceResourceID: "quiet-river-12345678/br-source-123",
					Spec: testDatabaseSpec(), PointInTime: point, IdempotencyKey: "restore-test"}
				var provider *Provider
				posts := 0
				provider = testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/api/v2/projects/quiet-river-12345678/branches" {
						t.Errorf("unexpected path: %s", r.URL.Path)
						w.WriteHeader(http.StatusNotFound)
						return
					}
					target := branch{ID: "br-stage-123", ProjectID: "quiet-river-12345678", Name: provider.restoreBranchName(request.ResourceID), ParentID: "br-source-123",
						ParentTimestamp: point.Format(time.RFC3339Nano), InitSource: "parent-data", CurrentState: "init"}
					fault.edit(&target)
					switch r.Method {
					case http.MethodGet:
						branches := []branch{}
						if path == "existing" || posts > 0 {
							branches = append(branches, target)
						}
						writeResponse(t, w, http.StatusOK, map[string]any{"branches": branches})
					case http.MethodPost:
						posts++
						var payload createBranchRequest
						if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
							t.Error(err)
						}
						if payload.Branch.ParentID != "br-source-123" || payload.Branch.InitSource != "parent-data" || payload.Branch.ParentTimestamp != point.Format(time.RFC3339Nano) {
							t.Errorf("restore did not request exact data fork: %+v", payload.Branch)
						}
						if path == "ambiguous_response" {
							// The provider accepted the fork but its response was lost.
							writeResponse(t, w, http.StatusBadGateway, nil)
							return
						}
						writeResponse(t, w, http.StatusCreated, map[string]any{"branch": target})
					default:
						t.Errorf("unexpected method: %s", r.Method)
						w.WriteHeader(http.StatusMethodNotAllowed)
					}
				}))
				observed, err := provider.Restore(t.Context(), request)
				if !errors.Is(err, fault.want) {
					t.Fatalf("restore error = %v, want %v", err, fault.want)
				}
				wantPosts := 1
				if path == "existing" {
					wantPosts = 0
				}
				if posts != wantPosts {
					t.Fatalf("create calls = %d, want %d", posts, wantPosts)
				}
				if err != nil {
					if observed.ProviderResourceID != "" || observed.RestoreLineage != nil {
						t.Fatal("rejected fork returned an adoptable identity")
					}
					return
				}
				if observed.ProviderResourceID != "quiet-river-12345678/br-stage-123" || observed.RestoreLineage == nil || observed.RestoreLineage.SourceResourceID != request.SourceResourceID || !observed.RestoreLineage.PointInTime.Equal(point) {
					t.Fatalf("restore did not report actual lineage: %+v", observed)
				}
			})
		}
	}
}

func TestRestoreRecoveryDoesNotFollowChangedDefaultBranch(t *testing.T) {
	var provider *Provider
	provider = testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("restore recovery created a second branch")
		}
		writeResponse(t, w, http.StatusOK, map[string]any{"branches": []branch{
			{ID: "br-new-default", Default: true},
			{ID: "br-stage", ProjectID: "quiet-river-12345678", Name: provider.restoreBranchName("logical-target"), ParentID: "br-old-default", ParentTimestamp: "2026-09-05T11:00:00Z", InitSource: "parent-data"},
		}})
	}))
	_, err := provider.Restore(context.Background(), managedpostgres.RestoreRequest{ResourceID: "logical-target", SourceResourceID: "quiet-river-12345678",
		Spec: testDatabaseSpec(), PointInTime: time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC), IdempotencyKey: "restore-target"})
	if !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("changed default branch silently adopted old fork: %v", err)
	}
	// A captured exact branch remains independent of the mutable default.
	observed, err := provider.Restore(context.Background(), managedpostgres.RestoreRequest{ResourceID: "logical-target", SourceResourceID: "quiet-river-12345678/br-old-default",
		Spec: testDatabaseSpec(), PointInTime: time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC), IdempotencyKey: "restore-target"})
	if err != nil || observed.RestoreLineage == nil || observed.RestoreLineage.SourceResourceID != "quiet-river-12345678/br-old-default" {
		t.Fatalf("pinned branch recovery = %+v, %v", observed, err)
	}
}

func TestBranchLineageRequiresObservedTimestampAndDataInitialization(t *testing.T) {
	for _, initSource := range []string{"", "parent-schema", "schema-only", "import", "parent-data"} {
		t.Run(initSource, func(t *testing.T) {
			lineage, err := observedBranchLineage("quiet-river-12345678", branch{ID: "br-stage", ProjectID: "quiet-river-12345678", ParentID: "br-source", InitSource: initSource})
			if err != nil || lineage != nil {
				t.Fatal("missing timestamp was treated as verified data lineage")
			}
		})
	}
}

func TestDeleteRecoveryRequiresVerifiedRestoreLineage(t *testing.T) {
	point := time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC)
	for _, fault := range []string{"valid", "absent", "wrong_parent", "wrong_point", "missing_point", "schema_only", "missing_request_point"} {
		t.Run(fault, func(t *testing.T) {
			var provider *Provider
			deletes := 0
			provider = testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v2/projects/quiet-river-12345678/branches":
					target := branch{ID: "br-stage", ProjectID: "quiet-river-12345678", Name: provider.restoreBranchName("logical-target"), ParentID: "br-source", ParentTimestamp: point.Format(time.RFC3339), InitSource: "parent-data"}
					switch fault {
					case "wrong_parent":
						target.ParentID = "br-other"
					case "wrong_point":
						target.ParentTimestamp = point.Add(time.Second).Format(time.RFC3339)
					case "missing_point":
						target.ParentTimestamp = ""
					case "schema_only":
						target.InitSource = "parent-schema"
					}
					branches := []branch{target}
					if fault == "absent" {
						branches = nil
					}
					writeResponse(t, w, http.StatusOK, map[string]any{"branches": branches})
				case r.Method == http.MethodGet && r.URL.Path == "/api/v2/projects/quiet-river-12345678/branches/br-stage" && deletes == 1:
					// Empty delete responses require an independently observed absence.
					w.WriteHeader(http.StatusNotFound)
				case r.Method == http.MethodDelete && r.URL.Path == "/api/v2/projects/quiet-river-12345678/branches/br-stage":
					deletes++
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected cleanup request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			request := managedpostgres.DeleteRequest{ResourceID: "logical-target", RestoreSourceResourceID: "quiet-river-12345678/br-source", RestorePointInTime: point, IdempotencyKey: "delete-target"}
			if fault == "missing_request_point" {
				request.RestorePointInTime = time.Time{}
			}
			result, err := provider.Delete(t.Context(), request)
			if fault == "valid" || fault == "absent" {
				if err != nil || !result.Done || deletes != map[string]int{"valid": 1, "absent": 0}[fault] {
					t.Fatalf("verified cleanup = %+v, %v, deletes=%d", result, err, deletes)
				}
			} else if err == nil || result.Done || deletes != 0 {
				t.Fatalf("unverified fork was deleted: %+v, %v, deletes=%d", result, err, deletes)
			}
		})
	}
}

func TestInspectReportsOnlyActualBranchLineage(t *testing.T) {
	for _, fault := range []string{"valid", "different_point", "lsn_only", "schema_only", "wrong_project", "malformed_point"} {
		t.Run(fault, func(t *testing.T) {
			point := "2026-09-05T11:00:00Z"
			provider := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/projects/quiet-river-12345678":
					writeResponse(t, w, http.StatusOK, map[string]any{"project": map[string]any{"id": "quiet-river-12345678", "region_id": "aws-eu-central-1", "pg_version": 17}})
				case "/api/v2/projects/quiet-river-12345678/branches":
					target := map[string]any{"id": "br-stage", "project_id": "quiet-river-12345678", "parent_id": "br-source", "parent_timestamp": point, "init_source": "parent-data", "current_state": "ready"}
					switch fault {
					case "different_point":
						point = "2026-09-05T12:00:00Z"
						target["parent_timestamp"] = point
					case "lsn_only":
						delete(target, "parent_timestamp")
						target["parent_lsn"] = "0/1DE2850"
					case "schema_only":
						target["init_source"] = "parent-schema"
					case "wrong_project":
						target["project_id"] = "other-project"
					case "malformed_point":
						target["parent_timestamp"] = "invalid"
					}
					writeResponse(t, w, http.StatusOK, map[string]any{"branches": []any{target}})
				case "/api/v2/projects/quiet-river-12345678/endpoints":
					writeResponse(t, w, http.StatusOK, map[string]any{"endpoints": []map[string]any{{"id": "ep-stage", "branch_id": "br-stage", "type": "read_write", "current_state": "active"}}})
				case "/api/v2/projects/quiet-river-12345678/connection_uri":
					writeResponse(t, w, http.StatusOK, map[string]any{"uri": "postgres://gregale_owner:secret@owner.example/gregale?sslmode=require"})
				case "/api/v2/projects/quiet-river-12345678/operations":
					writeResponse(t, w, http.StatusOK, map[string]any{"operations": []any{}})
				default:
					t.Errorf("unexpected inspect request: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			observed, err := provider.Inspect(t.Context(), "quiet-river-12345678/br-stage")
			switch fault {
			case "wrong_project", "malformed_point":
				if err == nil || observed.ProviderResourceID != "" {
					t.Fatal("invalid actual metadata was accepted")
				}
			case "lsn_only", "schema_only":
				if err != nil || observed.RestoreLineage != nil {
					t.Fatal("provider invented timestamp data lineage")
				}
			default:
				if err != nil || observed.RestoreLineage == nil || observed.RestoreLineage.PointInTime.Format(time.RFC3339) != point {
					t.Fatalf("inspection did not report actual point: %+v, %v", observed, err)
				}
			}
		})
	}
}
