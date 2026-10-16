package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestServiceAdditionCLIRequiresAcknowledgementAndKeepsReservationIdentity(t *testing.T) {
	for _, scenario := range []string{"accepted", "old_server", "changed_plan"} {
		t.Run(scenario, func(t *testing.T) {
			posts := 0
			safe := &api.ProjectSafeRelease{Status: "building", PreviousReleaseID: "baseline", TTLSeconds: 1800, Deadline: time.Now().Add(time.Hour)}
			if scenario != "old_server" {
				safe.AddServices = true
				safe.Additions = []api.ProjectReleaseAddition{{Slug: "metrics", RootDir: "metrics", Fingerprint: strings.Repeat("a", 64)}}
			}
			deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get(api.ProjectAddServicesHeader) != "required" || r.Header.Get(api.ProjectSafeReleaseHeader) != "atomic" {
					t.Error("service addition intent was not sent")
				}
				switch r.URL.Path {
				case "/v1/projects/scan":
					_ = json.NewEncoder(w).Encode(api.PlanResponse{CanApply: true, PlanToken: "addition-plan", SafeRelease: safe})
				case "/v1/projects/safe-deploy":
					posts++
					checkpoint := *safe
					checkpoint.Additions = append([]api.ProjectReleaseAddition(nil), safe.Additions...)
					checkpoint.Additions[0].AppID = "metrics-app"
					if scenario == "changed_plan" {
						checkpoint.Additions[0].Fingerprint = strings.Repeat("b", 64)
					}
					_ = json.NewEncoder(w).Encode(api.ApplyResponse{ProjectID: "project", SubmissionAttemptID: r.Header.Get(api.DeploymentAttemptHeader), PlanResponse: api.PlanResponse{SafeRelease: &checkpoint}, Builds: []api.AppliedBuild{{Slug: "metrics", AppID: "metrics-app", DeploymentID: "candidate", BuildID: "build"}}})
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			})
			archive := writeDeploySourceArchive(t, map[string]string{"package.json": "{}"})
			code := cmdDeployTarball([]string{"--project", "--project-slug", "shop", "--atomic", "--add-services", "--tarball", archive, "--yes", "--no-wait"})
			if scenario == "accepted" {
				if code != 0 || posts != 1 {
					t.Fatalf("accepted addition: code=%d posts=%d", code, posts)
				}
			} else if code == 0 || scenario == "old_server" && posts != 0 || scenario == "changed_plan" && posts != 1 {
				t.Fatalf("unsafe acknowledgement: code=%d posts=%d", code, posts)
			}
		})
	}
}

func TestServiceAdditionCLIRejectsIncompatibleFlagsBeforeNetwork(t *testing.T) {
	for _, args := range [][]string{
		{"--add-services"},
		{"--project", "--atomic", "--add-services", "--migrate", "--verify"},
		{"--project", "--atomic", "--add-services", "--workers", "--verify"},
		{"--project", "--atomic", "--add-services", "--config-file", "missing.json", "--verify"},
	} {
		deployReliabilityCLI(t, func(http.ResponseWriter, *http.Request) { t.Fatal("incompatible additions reached server") })
		if cmdDeployTarball(args) == 0 {
			t.Fatal("invalid combination succeeded", args)
		}
	}
}
