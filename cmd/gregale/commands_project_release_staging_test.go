// adr: 1351 — exact candidate service allocation, expiry and routing preservation.
package main_test

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProjectReleaseStagingCLI(t *testing.T) {
	f := newBindingPolicyCLIFixture(t)
	empty := ""
	manifest := api.CheckedProjectReleaseManifest{Project: "shop", Environment: "production", TTLSeconds: 1800, Deployments: map[string]string{"api": releaseCheckDeploymentID}, ExpectedActiveReleaseID: &empty, ExpectedFallbackDeployments: map[string]string{"api": uuid.NewString()}, ExpectedPolicyRevisions: map[string]int64{"api": 1}, ServiceTargets: map[string]map[string]string{"api": {}}}
	file := filepath.Join(t.TempDir(), "manifest.json")
	raw, _ := json.Marshal(manifest)
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	stage := api.ProjectReleaseStage{ID: uuid.NewString(), Project: "shop", Environment: "production", Status: "waiting", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute), Members: []api.ProjectReleaseStageMember{{App: "api", DeploymentID: releaseCheckDeploymentID, Desired: 1}}}
	calls := []string{}
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPost {
			var request api.StageProjectReleaseRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.TTLSeconds != 60 || request.Manifest.Deployments["api"] != releaseCheckDeploymentID {
				t.Fatalf("stage request: %+v %v", request, err)
			}
		} else if !strings.HasSuffix(r.URL.Path, "/staging/"+stage.ID) {
			t.Fatalf("wrong stage path: %s", r.URL.Path)
		}
		if r.Method == http.MethodDelete {
			stage.Status = "cancelled"
		}
		_ = json.NewEncoder(w).Encode(stage)
	})
	for _, action := range []string{"stage", "stage-status", "cancel-stage"} {
		args := []string{"projects", "environments", "release-sets", action, "shop", "production", "--json"}
		if action == "stage" {
			args = append(args, "--file", file, "--ttl-seconds", "60")
		} else {
			args = append(args, "--stage", stage.ID)
		}
		code, out, stderr := main.CaptureBindingCLI(t, f.server.URL, args...)
		if code != 0 {
			t.Fatalf("%s: %d %s %s", action, code, out, stderr)
		}
		var response api.ProjectReleaseStage
		if err := json.Unmarshal([]byte(out), &response); err != nil || response.ID != stage.ID {
			t.Fatalf("stage output: %s %v", out, err)
		}
	}
	if len(calls) != 3 || !strings.HasPrefix(calls[0], "POST ") || !strings.HasPrefix(calls[1], "GET ") || !strings.HasPrefix(calls[2], "DELETE ") || len(f.requests) != 0 {
		t.Fatalf("unexpected stage effects: %v", calls)
	}
}
