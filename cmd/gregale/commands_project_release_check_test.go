package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestProjectReleaseCLIExactRequestsAndReceipts(t *testing.T) {
	for _, mode := range []string{"check", "publish", "wrong-deployment", "unchecked-server", "blocked"} {
		t.Run(mode, func(t *testing.T) {
			resetJSONOut(t)
			dep, app, project := uuid.NewString(), uuid.NewString(), uuid.NewString()
			empty := ""
			req := api.PublishProjectReleaseSetRequest{TTLSeconds: 1800, Deployments: map[string]string{"shop-api": dep}, ExpectedActiveReleaseID: &empty}
			body, _ := json.Marshal(req)
			file := filepath.Join(t.TempDir(), "release.json")
			if err := os.WriteFile(file, body, 0600); err != nil {
				t.Fatal(err)
			}
			report := api.ProjectReleaseCheckResponse{ProjectID: project, Environment: "production", ExpectedActiveReleaseID: "", TTLSeconds: 1800, Members: []api.ProjectReleaseSetMemberResponse{{AppID: app, DeploymentID: dep}}, Passed: true, CheckedAt: time.Now().UTC(), Checks: []api.BindingCheckReport{{App: "shop-api", DeploymentID: dep, Scope: "production", Passed: true}}}
			report.GraphDigest = api.ProjectReleaseGraphDigest(report)
			release := api.ProjectReleaseSetResponse{ID: uuid.NewString(), ProjectID: project, AccountID: uuid.NewString(), Environment: "production", Active: true, TTLSeconds: 1800, Members: report.Members, BindingsCheck: &report}
			if mode == "wrong-deployment" {
				release.Members = []api.ProjectReleaseSetMemberResponse{{AppID: app, DeploymentID: uuid.NewString()}}
			}
			if mode == "unchecked-server" {
				release.BindingsCheck = nil
			}
			if mode == "blocked" {
				report.Passed = false
				report.Blockers = []api.BindingCheckFinding{{Code: "verification_missing", Message: "Verify the selected deployment."}}
			}
			f := authedFakeAPI(t, "", 200)
			calls := 0
			f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost {
					t.Errorf("method=%s", r.Method)
				}
				var got api.PublishProjectReleaseSetRequest
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil || got.ExpectedActiveReleaseID == nil || *got.ExpectedActiveReleaseID != "" || got.Deployments["shop-api"] != dep {
					t.Errorf("request: %+v %v", got, err)
				}
				if mode == "check" || mode == "blocked" {
					if r.URL.Path != "/v1/projects/shop/environments/production/release-sets/check" {
						t.Errorf("path=%s", r.URL.Path)
					}
					_ = json.NewEncoder(w).Encode(report)
				} else {
					w.WriteHeader(201)
					_ = json.NewEncoder(w).Encode(release)
				}
			})
			previous := osStdout
			var output bytes.Buffer
			osStdout = &output
			t.Cleanup(func() { osStdout = previous })
			jsonOutput = true
			publish := mode != "check" && mode != "blocked"
			exit := cmdProjectReleaseWrite([]string{"shop", "production", "--file", file, "--expected-active", "none"}, publish)
			if (mode == "check" || mode == "publish") && exit != 0 {
				t.Fatalf("exit=%d", exit)
			}
			if mode != "check" && mode != "publish" && exit == 0 {
				t.Fatal("invalid receipt passed")
			}
			if calls != 1 {
				t.Fatalf("calls=%d; check/publish must never create verification tasks", calls)
			}
		})
	}
}
