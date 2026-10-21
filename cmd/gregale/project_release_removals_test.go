package main_test

// adr: 20261021120000

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestServiceRemovalCLIRequiresAcknowledgement(t *testing.T) {
	for _, scenario := range []string{"accepted", "old_server", "changed_plan"} {
		t.Run(scenario, func(t *testing.T) {
			posts := 0
			safe := &api.ProjectSafeRelease{Status: "building", PreviousReleaseID: "baseline", TTLSeconds: 1800, Deadline: time.Now().Add(time.Hour)}
			if scenario != "old_server" {
				safe.RemoveServices = true
				safe.Removals = []api.ProjectReleaseRemoval{{AppID: "legacy-app", Slug: "legacy", RootDir: "legacy", Fingerprint: strings.Repeat("a", 64)}}
			}
			main.DeployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get(api.ProjectRemoveServicesHeader) != "required" || r.Header.Get(api.ProjectSafeReleaseHeader) != "atomic" {
					t.Error("service removal intent was not sent")
				}
				switch r.URL.Path {
				case "/v1/projects/scan":
					_ = json.NewEncoder(w).Encode(api.PlanResponse{CanApply: true, PlanToken: "removal-plan", SafeRelease: safe})
				case "/v1/projects/safe-deploy":
					posts++
					checkpoint := *safe
					if scenario == "changed_plan" {
						checkpoint.Removals = []api.ProjectReleaseRemoval{{AppID: "legacy-app", Slug: "legacy", RootDir: "legacy", Fingerprint: strings.Repeat("b", 64)}}
					}
					_ = json.NewEncoder(w).Encode(api.ApplyResponse{ProjectID: "project", SubmissionAttemptID: r.Header.Get(api.DeploymentAttemptHeader), PlanResponse: api.PlanResponse{SafeRelease: &checkpoint}, Builds: []api.AppliedBuild{{Slug: "web", AppID: "web-app", DeploymentID: "candidate", BuildID: "build"}}})
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			})
			archive := main.WriteDeploySourceArchive(t, map[string]string{"package.json": "{}"})
			code := main.CmdDeployTarball([]string{"--project", "--project-slug", "shop", "--atomic", "--remove-services", "--tarball", archive, "--yes", "--no-wait"})
			if scenario == "accepted" {
				if code != 0 || posts != 1 {
					t.Fatalf("accepted removal: code=%d posts=%d", code, posts)
				}
			} else if code == 0 || scenario == "old_server" && posts != 0 || scenario == "changed_plan" && posts != 1 {
				t.Fatalf("unsafe acknowledgement: code=%d posts=%d", code, posts)
			}
		})
	}
}

func TestServiceRemovalCLIRejectsIncompatibleFlagsBeforeNetwork(t *testing.T) {
	for _, args := range [][]string{
		{"--remove-services"},
		{"--project", "--atomic", "--remove-services", "--add-services"},
		{"--project", "--atomic", "--remove-services", "--migrate", "--verify"},
		{"--project", "--atomic", "--remove-services", "--workers", "--verify"},
	} {
		main.DeployReliabilityCLI(t, func(http.ResponseWriter, *http.Request) { t.Fatal("incompatible removal reached server") })
		if main.CmdDeployTarball(args) == 0 {
			t.Fatal("invalid combination succeeded", args)
		}
	}
}
