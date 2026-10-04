// adr: 568
package neon

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestObservedDataResourceDoesNotFollowChangedDefault(t *testing.T) {
	const projectID, originalID, replacementID = "quiet-river-12345678", "br-original-123", "br-replacement-123"
	var provider *Provider
	defaultChanged := false
	rootDeleted := false
	point := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	provider = testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v2/projects/"+projectID)
		switch {
		case r.Method == http.MethodGet && path == "":
			project := provider.projectPayload("captured-source", testDatabaseSpec()).Project
			writeResponse(t, w, http.StatusOK, map[string]any{"project": map[string]any{
				"id": projectID, "region_id": project.RegionID, "pg_version": project.PostgresMajor,
				"history_retention_seconds": project.HistoryRetentionSeconds, "settings": project.Settings}})
		case r.Method == http.MethodGet && path == "/branches":
			writeResponse(t, w, http.StatusOK, branchesResponse{Branches: []branch{
				{ID: originalID, ProjectID: projectID, Default: !defaultChanged, CurrentState: "ready"},
				{ID: replacementID, ProjectID: projectID, Default: defaultChanged, CurrentState: "ready"}}})
		case r.Method == http.MethodGet && path == "/endpoints":
			writeResponse(t, w, http.StatusOK, endpointsResponse{Endpoints: []endpoint{
				{ID: "ep-original", BranchID: originalID, Type: "read_write", CurrentState: "active", MinimumCU: .25, MaximumCU: 2, SuspendTimeoutSecond: 300},
				{ID: "ep-replacement", BranchID: replacementID, Type: "read_write", CurrentState: "active", MinimumCU: 1, MaximumCU: 4, SuspendTimeoutSecond: -1}}})
		case r.Method == http.MethodGet && path == "/operations":
			writeResponse(t, w, http.StatusOK, operationsResponse{})
		case r.Method == http.MethodPost && path == "/branches":
			var request createBranchRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			if request.Branch.ParentID != originalID || request.Branch.ParentTimestamp != point.Format(time.RFC3339Nano) {
				t.Error("fork followed changed default")
			}
			writeResponse(t, w, http.StatusCreated, createdBranchResponse{Branch: branch{ID: "br-stage-123", ProjectID: projectID,
				Name: request.Branch.Name, ParentID: originalID, ParentTimestamp: point.Format(time.RFC3339Nano), InitSource: "parent-data"}})
		case r.Method == http.MethodGet && strings.HasPrefix(path, "/branches/"+originalID+"/roles/"):
			writeResponse(t, w, http.StatusOK, map[string]any{"role": map[string]any{"name": strings.TrimPrefix(path, "/branches/"+originalID+"/roles/")}})
		case r.Method == http.MethodGet && path == "/connection_uri":
			if r.URL.Query().Get("branch_id") != originalID {
				t.Error("credentials followed changed default")
			}
			writeResponse(t, w, http.StatusOK, map[string]any{"uri": "postgres://" + r.URL.Query().Get("role_name") + ":test-password@original.example.test:5432/gregale?sslmode=require"})
		case r.Method == http.MethodDelete && strings.HasPrefix(path, "/branches/"+originalID+"/roles/"):
			writeResponse(t, w, http.StatusOK, roleResponse{})
		case r.Method == http.MethodDelete && path == "":
			rootDeleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected provider request: %s %s", r.Method, path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	observed, err := provider.Inspect(t.Context(), projectID)
	if err != nil || observed.Status != managedpostgres.ProviderStatusReady || observed.DataResourceID != projectID+"/"+originalID || observed.ProviderResourceID != projectID {
		t.Fatalf("root observation = %+v, %v", observed, err)
	}
	defaultChanged = true
	pinned, err := provider.Inspect(t.Context(), observed.DataResourceID)
	if err != nil || pinned.DataResourceID != observed.DataResourceID || pinned.Spec != observed.Spec {
		t.Fatalf("pinned inspection followed changed default: %+v, %v", pinned, err)
	}
	credentials := managedpostgres.CredentialRequest{ProviderResourceID: observed.DataResourceID, IdentityKey: "captured-binding", IdempotencyKey: "binding", Access: managedpostgres.CredentialReadWrite}
	if _, err := provider.IssueCredentials(t.Context(), credentials); err != nil {
		t.Fatal(err)
	}
	if err := provider.RevokeCredentials(t.Context(), credentials); err != nil {
		t.Fatal(err)
	}
	target, err := provider.Restore(t.Context(), managedpostgres.RestoreRequest{ResourceID: "stage-database", SourceResourceID: observed.DataResourceID,
		Spec: observed.Spec, PointInTime: point, IdempotencyKey: "stage-restore"})
	if err != nil || target.DataResourceID != projectID+"/br-stage-123" || target.RestoreLineage.SourceResourceID != observed.DataResourceID {
		t.Fatalf("fork = %+v, %v", target, err)
	}
	if _, err := provider.Delete(t.Context(), managedpostgres.DeleteRequest{ProviderResourceID: observed.ProviderResourceID, IdempotencyKey: "root-delete"}); err != nil || !rootDeleted {
		t.Fatalf("root cleanup lost project ownership: %v", err)
	}
}
