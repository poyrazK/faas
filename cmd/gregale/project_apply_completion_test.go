package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProjectDeploymentCompletionGuards(t *testing.T) {
	pending := api.NewDeploymentConfigurationStatus("prepared", "awaiting_activation")
	activated := api.NewDeploymentConfigurationStatus("activated", "")
	conflict := api.NewDeploymentConfigurationStatus("conflict", "cron_changed")
	for _, tc := range []struct {
		name     string
		dep      api.DeploymentResponse
		accepted *api.DeploymentConfigurationStatus
		done     bool
		phase    string
		failed   bool
	}{
		{"legacy", api.DeploymentResponse{Status: statusLive}, nil, true, "", false},
		{"ready", api.DeploymentResponse{Status: statusLive, Configuration: &activated}, &pending, true, "", false},
		{"pending", api.DeploymentResponse{Status: statusLive, Configuration: &pending}, nil, false, "configuration", false},
		{"lost-diagnostic", api.DeploymentResponse{Status: statusLive}, &pending, false, "configuration", false},
		{"conflicted", api.DeploymentResponse{Status: statusLive, Configuration: &conflict}, nil, true, "configuration", true},
		{"rolling", api.DeploymentResponse{Status: statusLive, RolloutState: "rolling_out", TrafficPercent: 0, Configuration: &activated}, nil, false, "rollout", false},
		{"traffic", api.DeploymentResponse{Status: statusLive, RolloutState: "complete", TrafficPercent: 50}, nil, false, "rollout", false},
		{"aborted", api.DeploymentResponse{Status: statusLive, RolloutState: "aborted"}, nil, true, "rollout", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			done, failure, phase := projectDeploymentCompletion(tc.dep, tc.accepted)
			if done != tc.done || phase != tc.phase || (failure != "") != tc.failed {
				t.Fatalf("done=%v failure=%q phase=%q", done, failure, phase)
			}
		})
	}
}

func TestProjectApplyWaitActivationAndIndependentConflict(t *testing.T) {
	var reads atomic.Int32
	pending := api.NewDeploymentConfigurationStatus("prepared", "awaiting_activation")
	activated := api.NewDeploymentConfigurationStatus("activated", "")
	conflict := api.NewDeploymentConfigurationStatus("conflict", "quota_changed")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("wait mutated server: %s %s", r.Method, r.URL.Path)
		}
		dep := api.DeploymentResponse{ID: "healthy", AppID: "app-healthy", Status: statusLive, RolloutState: "complete", TrafficPercent: 100, Configuration: &pending}
		switch r.URL.Path {
		case "/v1/deployments/healthy":
			if reads.Add(1) > 1 {
				dep.Configuration = &activated
			}
		case "/v1/deployments/blocked":
			dep.ID, dep.AppID, dep.Configuration = "blocked", "app-blocked", &conflict
		default:
			t.Errorf("unexpected read %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(dep)
	}))
	defer srv.Close()
	apply := api.ApplyResponse{Builds: []api.AppliedBuild{{Slug: "healthy", AppID: "app-healthy", DeploymentID: "healthy", Configuration: &pending}, {Slug: "blocked", AppID: "app-blocked", DeploymentID: "blocked", Configuration: &pending}}}
	got, timeout := waitForProjectApply(t.Context(), NewClient(srv.URL, "token"), apply, 5*time.Second)
	if timeout || got.Builds[0].Error != "" || got.Builds[0].Configuration.Crons.Status != "activated" || !strings.Contains(got.Builds[1].Error, "quota_changed") {
		t.Fatalf("result=%+v timeout=%v", got.Builds, timeout)
	}
	if summarizeProjectApply(got).buildsFailed != 1 {
		t.Fatalf("conflict did not fail CI: %+v", got)
	}
}

func TestProjectApplyWaitTimeoutRetainsLiveConfiguration(t *testing.T) {
	pending := api.NewDeploymentConfigurationStatus("prepared", "awaiting_activation")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/deployments/dep" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "dep", Status: statusLive, Configuration: &pending})
	}))
	defer srv.Close()
	apply := api.ApplyResponse{Builds: []api.AppliedBuild{{Slug: "demo", DeploymentID: "dep", BuildID: "build", Configuration: &pending}}}
	got, timeout := waitForProjectApply(t.Context(), NewClient(srv.URL, "token"), apply, 40*time.Millisecond)
	row := got.Builds[0]
	if !timeout || row.DeploymentStatus != statusLive || row.BuildStatus != api.BuildStatusSucceeded || row.Configuration == nil || row.Configuration.Crons.Status != "pending" || !strings.Contains(row.Error, "configuration wait timed out") {
		t.Fatalf("result=%+v timeout=%v", row, timeout)
	}
}

func TestProjectApplyWaitRolloutThenConfiguration(t *testing.T) {
	var reads atomic.Int32
	pending := api.NewDeploymentConfigurationStatus("prepared", "awaiting_rollout")
	activated := api.NewDeploymentConfigurationStatus("activated", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("wait issued a mutation")
		}
		dep := api.DeploymentResponse{ID: "dep", Status: statusLive, RolloutState: "rolling_out", Configuration: &pending}
		switch reads.Add(1) {
		case 1:
		case 2:
			dep.RolloutState, dep.TrafficPercent = "complete", 100
		default:
			dep.RolloutState, dep.TrafficPercent, dep.Configuration = "complete", 100, &activated
		}
		_ = json.NewEncoder(w).Encode(dep)
	}))
	defer srv.Close()
	got, timeout := waitForProjectApply(t.Context(), NewClient(srv.URL, "token"), api.ApplyResponse{Builds: []api.AppliedBuild{{DeploymentID: "dep"}}}, 6*time.Second)
	if timeout || got.Builds[0].Error != "" || reads.Load() != 3 || got.Builds[0].Configuration.Crons.Status != "activated" {
		t.Fatalf("result=%+v timeout=%v reads=%d", got, timeout, reads.Load())
	}
}

func TestProjectDeployCompletionExitCodes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		flags  []string
		status string
		exit   int
		reads  bool
	}{
		{"conflict", nil, "conflict", 1, true},
		{"timeout", []string{"--timeout", "1"}, "prepared", 3, true},
		{"no-wait", []string{"--no-wait"}, "prepared", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			var reads, submits atomic.Int32
			pending := api.NewDeploymentConfigurationStatus("prepared", "awaiting_activation")
			config := api.NewDeploymentConfigurationStatus(tc.status, "cron_changed")
			deployReliabilityCLI(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/projects/scan":
					_ = json.NewEncoder(w).Encode(api.PlanResponse{CanApply: true, PlanToken: "plan", Workloads: []api.PlanWorkload{{Name: "demo"}}})
				case "/v1/projects":
					submits.Add(1)
					_ = json.NewEncoder(w).Encode(api.ApplyResponse{Builds: []api.AppliedBuild{{Slug: "demo", AppID: "app", DeploymentID: "dep", Configuration: &pending}}})
				case "/v1/deployments/dep":
					reads.Add(1)
					if r.Method != http.MethodGet {
						t.Error("completion issued a mutation")
					}
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "dep", AppID: "app", Status: statusLive, Configuration: &config})
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			})
			archive := writeDeploySourceArchive(t, map[string]string{"package.json": "{}"})
			args := append([]string{"--name", "completion-project", "--tarball", archive, "--project", "--yes", "--json"}, tc.flags...)
			code := cmdDeployTarball(args)
			if code != tc.exit || (reads.Load() > 0) != tc.reads || submits.Load() != 1 {
				t.Fatalf("exit=%d reads=%d submits=%d", code, reads.Load(), submits.Load())
			}
			var receipt api.ApplyResponse
			if err := json.Unmarshal(osStdout.(*bytes.Buffer).Bytes(), &receipt); err != nil {
				t.Fatal(err)
			}
			if len(receipt.Builds) != 1 || receipt.Builds[0].Configuration == nil {
				t.Fatalf("lost JSON receipt: %+v", receipt)
			}
			if tc.name == "no-wait" && receipt.Builds[0].Configuration.Crons.Status != "pending" {
				t.Fatalf("no-wait=%+v", receipt)
			}
		})
	}
}
