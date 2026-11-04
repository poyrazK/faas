package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// `issues resolve --deployment v4` reached the server as "v4" and came back
// "resolution requires fixed_deployment_id"; the CLI now resolves the
// revision like traffic and deployment commands (Hunt 12 H12-50).
func TestIssuesResolveAcceptsRevision(t *testing.T) {
	const depID = "a4c1e57f-9c80-4917-a59b-637661ad172a"
	tests := []struct{ name, ref string }{
		{"revision", "v4"},
		{"uuid", depID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			main.ResetJSONOut(t)
			var got api.IssueActionRequest
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/v1/apps/web/deployments":
					_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: []api.DeploymentResponse{{ID: depID, Revision: 4}, {ID: "11111111-2222-3333-4444-555555555555", Revision: 3}}})
				case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/web/issues/issue-1/actions":
					_ = json.NewDecoder(r.Body).Decode(&got)
					_ = json.NewEncoder(w).Encode(api.Issue{ID: "issue-1", State: "resolved"})
				default:
					t.Logf("unexpected %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(srv.Close)
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			_, _, restore := main.SwapIO(t)
			code := main.Run([]string{"issues", "resolve", "--app", "web", "issue-1", "--deployment", tt.ref})
			restore()
			if code != 0 || got.Action != "resolve" || got.FixedDeploymentID != depID {
				t.Fatalf("exit %d, action %+v; want resolve with %s", code, got, depID)
			}
		})
	}
}
