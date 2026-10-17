package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProjectReleaseStagingClientContracts(t *testing.T) {
	empty := ""
	request := StageProjectReleaseRequest{TTLSeconds: 60, Manifest: CheckedProjectReleaseManifest{Project: "shop", Environment: "production", ExpectedActiveReleaseID: &empty, ExpectedFallbackDeployments: map[string]string{}, ServiceTargets: map[string]map[string]string{"api": {}}, Deployments: map[string]string{"api": "11111111-1111-4111-8111-111111111111"}}}
	for _, old := range []bool{false, true} {
		t.Run(map[bool]string{false: "current", true: "old"}[old], func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if !strings.Contains(r.URL.Path, "/release-sets/staging") {
					t.Fatal("legacy fallback route used")
				}
				if r.Method == http.MethodPost {
					var got StageProjectReleaseRequest
					if err := json.NewDecoder(r.Body).Decode(&got); err != nil || got.Manifest.ExpectedActiveReleaseID == nil || got.Manifest.ServiceTargets["api"] == nil || got.TTLSeconds != 60 {
						t.Fatalf("lost manifest: %+v %v", got, err)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				if old {
					w.WriteHeader(404)
					_, _ = w.Write([]byte(`{"status":404,"code":"not_found","title":"Not found"}`))
					return
				}
				_, _ = w.Write([]byte(`{"id":"stage-id","status":"waiting","members":[]}`))
			}))
			defer server.Close()
			client := NewClient(server.URL, "key")
			operations := []func() (ProjectReleaseStage, error){func() (ProjectReleaseStage, error) {
				return client.StageCheckedProjectRelease(testContext(t), "shop", "production", request)
			}, func() (ProjectReleaseStage, error) {
				return client.GetProjectReleaseStage(testContext(t), "shop", "production", "stage-id")
			}, func() (ProjectReleaseStage, error) {
				return client.CancelProjectReleaseStage(testContext(t), "shop", "production", "stage-id")
			}}
			for _, operation := range operations {
				out, err := operation()
				if (err != nil) != old || (!old && out.ID != "stage-id") {
					t.Fatalf("staging client: %+v %v", out, err)
				}
			}
			if calls != 3 {
				t.Fatalf("unsupported staging used fallback: %d calls", calls)
			}
		})
	}
}
