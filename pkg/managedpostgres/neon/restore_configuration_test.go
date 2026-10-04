// adr: 581
package neon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestRestorePinsCapturedEndpointConfiguration(t *testing.T) {
	for _, class := range []managedpostgres.ServiceClass{managedpostgres.ClassDevelopment, managedpostgres.ClassBurstable, managedpostgres.ClassProduction} {
		for _, scaleToZero := range []bool{false, true} {
			for _, responseLost := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/scale_to_zero=%t/lost_response=%t", class, scaleToZero, responseLost), func(t *testing.T) {
					testRestoreCapturedEndpointConfiguration(t, class, scaleToZero, responseLost)
				})
			}
		}
	}
}

func testRestoreCapturedEndpointConfiguration(t *testing.T, class managedpostgres.ServiceClass, scaleToZero, responseLost bool) {
	t.Helper()
	spec := testDatabaseSpec()
	spec.Class, spec.ScaleToZero = class, scaleToZero
	point := time.Date(2026, 9, 5, 11, 0, 0, 123456000, time.UTC)
	var target endpoint
	posts := 0
	var provider *Provider
	provider = testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		projectPath := "/api/v2/projects/quiet-river-12345678"
		if r.Method == http.MethodPost && r.URL.Path == projectPath+"/branches" {
			posts++
			var raw struct {
				Endpoints []map[string]json.RawMessage `json:"endpoints"`
			}
			if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
				t.Error(err)
			}
			if len(raw.Endpoints) != 1 {
				t.Error("restore must create its own endpoint")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			// Decode independently of the request type: omission must fail even
			// when an adapter struct would populate the same Go zero values.
			for _, field := range []string{"type", "autoscaling_limit_min_cu", "autoscaling_limit_max_cu", "suspend_timeout_seconds"} {
				if len(raw.Endpoints[0][field]) == 0 {
					t.Errorf("restore inherited project setting %s", field)
				}
			}
			body, err := json.Marshal(raw.Endpoints[0])
			if err != nil || json.Unmarshal(body, &target) != nil {
				t.Error("invalid endpoint request")
			}
			target.ID, target.BranchID, target.CurrentState = "ep-stage", "br-stage", "active"
			if responseLost {
				writeResponse(t, w, http.StatusBadGateway, nil)
			} else {
				writeResponse(t, w, http.StatusCreated, map[string]any{"branch": branch{ID: "br-stage", ProjectID: "quiet-river-12345678",
					Name: provider.restoreBranchName("logical-target"), ParentID: "br-source", ParentTimestamp: point.Format(time.RFC3339Nano), InitSource: "parent-data", CurrentState: "init"}})
			}
			return
		}
		if r.Method != http.MethodGet {
			t.Errorf("restore modified source resources: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case projectPath:
			// Mutable defaults intentionally disagree with every supported
			// captured profile. The dedicated target endpoint is authoritative.
			writeResponse(t, w, http.StatusOK, map[string]any{"project": project{
				ID: "quiet-river-12345678", RegionID: "aws-eu-central-1", PostgresMajor: spec.PostgresMajor,
				HistoryRetentionSeconds: spec.RestoreWindowSeconds,
				DefaultEndpointSettings: endpointSettings{MinimumCU: 8, MaximumCU: 16, SuspendTimeoutSecond: -1},
				Settings: projectSettings{Quota: struct {
					LogicalSizeBytes *int64 `json:"logical_size_bytes"`
				}{LogicalSizeBytes: &spec.StorageLimitBytes}},
			}})
		case projectPath + "/branches":
			branches := []branch{{ID: "br-source", ProjectID: "quiet-river-12345678", Default: true, CurrentState: "ready"}}
			if posts > 0 {
				branches = append(branches, branch{ID: "br-stage", ProjectID: "quiet-river-12345678", Name: provider.restoreBranchName("logical-target"),
					ParentID: "br-source", ParentTimestamp: point.Format(time.RFC3339Nano), InitSource: "parent-data", CurrentState: "ready"})
			}
			writeResponse(t, w, http.StatusOK, map[string]any{"branches": branches})
		case projectPath + "/endpoints":
			writeResponse(t, w, http.StatusOK, map[string]any{"endpoints": []endpoint{
				{ID: "ep-source", BranchID: "br-source", Type: "read_write", CurrentState: "active", MinimumCU: 8, MaximumCU: 16, SuspendTimeoutSecond: -1}, target,
			}})
		case projectPath + "/connection_uri":
			if r.URL.Query().Get("branch_id") != "br-stage" || r.URL.Query().Get("role_name") != ownerLogin || r.URL.Query().Get("pooled") != "false" {
				t.Errorf("inherited credential restriction crossed target identity: %s", r.URL.RawQuery)
			}
			writeResponse(t, w, http.StatusOK, connectionURIResponse{URI: "postgres://gregale_owner:test@ep-stage.example.test/gregale?sslmode=require"})
		case projectPath + "/operations":
			writeResponse(t, w, http.StatusOK, map[string]any{"operations": []operation{}})
		default:
			t.Errorf("unexpected restore request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	request := managedpostgres.RestoreRequest{ResourceID: "logical-target", SourceResourceID: "quiet-river-12345678/br-source",
		Spec: spec, PointInTime: point, IdempotencyKey: "restore-target"}
	accepted, err := provider.Restore(t.Context(), request)
	if err != nil || accepted.ProviderResourceID != "quiet-river-12345678/br-stage" || posts != 1 {
		t.Fatalf("restore acceptance: %+v, %v; posts %d", accepted, err, posts)
	}
	actual, err := provider.Inspect(t.Context(), accepted.ProviderResourceID)
	if err != nil || actual.Status != managedpostgres.ProviderStatusReady || actual.Spec != spec {
		t.Fatalf("restored endpoint did not retain captured configuration: %+v, %v", actual, err)
	}
	if provider.roles.(*fakeCredentialRoles).restrictions != 1 {
		t.Fatal("ready restore did not restrict inherited runtime credentials")
	}
	if actual.RestoreLineage == nil || actual.RestoreLineage.SourceResourceID != request.SourceResourceID || !actual.RestoreLineage.PointInTime.Equal(point) {
		t.Fatal("configured endpoint lost exact data lineage")
	}
	if _, err := provider.Restore(t.Context(), request); err != nil || posts != 1 {
		t.Fatalf("restore retry created another branch: %v; posts %d", err, posts)
	}
}
