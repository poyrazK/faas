package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestProjectInspectionReportsGraphDifferencesWithoutValues(t *testing.T) {
	main.ResetJSONOut(t)
	snapshot := api.ProjectEnvironmentStateResponse{
		ProjectSlug: "shop", Environment: "production", ReleaseSetStatus: "active",
		Configuration:    api.ProjectEnvironmentConfigResponse{Version: 8, Values: json.RawMessage(`{"password":"config-value"}`)},
		ActiveReleaseSet: &api.ProjectReleaseSetResponse{ID: "release-a", Members: []api.ProjectReleaseSetMemberResponse{{AppID: "app-a", DeploymentID: "dep-a"}}},
		Workloads: []api.ProjectEnvironmentStateWorkloadResponse{{AppID: "app-a", WorkloadSlug: "api",
			Release:   api.ProjectEnvironmentReleaseWorkloadResponse{DeploymentID: "dep-b", Status: "live"},
			Variables: []api.ProjectEnvironmentVariableResponse{{Key: "TOKEN", Value: "variable-value"}},
			Secrets:   []api.ProjectEnvironmentSecretResponse{{Key: "SECRET_NAME", ValueHash: "secret-fingerprint"}},
		}},
	}
	for _, asJSON := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "json"}[asJSON], func(t *testing.T) {
			f := main.AuthedFakeAPI(t, "", http.StatusOK)
			var requests []string
			(*f.Srv()).Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("inspection wrote state: %s", r.Method)
				}
				requests = append(requests, r.URL.Path)
				if strings.HasSuffix(r.URL.Path, "/state") {
					_ = json.NewEncoder(w).Encode(snapshot)
					return
				}
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
			})
			previous := (*main.OsStdout)
			var out bytes.Buffer
			(*main.OsStdout) = &out
			(*main.JsonOutput) = asJSON
			t.Cleanup(func() { (*main.OsStdout) = previous; (*main.JsonOutput) = false })
			if code := main.CmdProjects([]string{"environments", "inspect", "shop", "production"}); code != 0 {
				t.Fatalf("exit = %d", code)
			}
			for _, want := range []string{"release-a", "dep-a", "dep-b", "release_selection_differs", "promotion_history_unavailable"} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("missing %q in %s", want, out.String())
				}
			}
			for _, forbidden := range []string{"config-value", "variable-value", "secret-fingerprint", "SECRET_NAME", "healthy"} {
				if strings.Contains(out.String(), forbidden) {
					t.Errorf("inspection exposed %q in %s", forbidden, out.String())
				}
			}
			if len(requests) != 2 || requests[0] != "/v1/projects/shop/environments/production/state" || requests[1] != "/v1/projects/shop/environments/production/promotions" {
				t.Fatalf("requests = %+v", requests)
			}
			if asJSON {
				var got main.ProjectEnvironmentInspection
				if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.RuntimeHealth != "not_checked" || len(got.Issues) != 2 {
					t.Fatalf("JSON = %+v, %v", got, err)
				}
			}
		})
	}
}

func TestProjectInspectionContextAndMissingGraph(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if _, err := main.SaveProjectContext(dir, main.LocalProjectContext{Version: 1, Project: "shop", Environment: "staging"}); err != nil {
		t.Fatal(err)
	}
	project, environment, err := main.ProjectInspectionTarget(nil)
	if err != nil || project != "shop" || environment != "staging" {
		t.Fatalf("linked target = %s/%s %v", project, environment, err)
	}
	project, environment, err = main.ProjectInspectionTarget([]string{"other", "production"})
	if err != nil || project != "other" || environment != "production" {
		t.Fatalf("explicit target = %s/%s %v", project, environment, err)
	}
	for _, args := range [][]string{{"shop"}, {"Bad Slug", "production"}, {"shop", ""}} {
		if _, _, err := main.ProjectInspectionTarget(args); err == nil {
			t.Fatalf("invalid target accepted: %+v", args)
		}
	}
	in := api.ProjectEnvironmentStateResponse{ReleaseSetStatus: "none"}
	if got := main.BuildProjectEnvironmentInspection(in); got.ReleaseSetStatus != "none" || len(got.Issues) != 0 {
		t.Fatalf("no graph = %+v", got)
	}
	in.ReleaseSetStatus = ""
	if got := main.BuildProjectEnvironmentInspection(in); got.ReleaseSetStatus != "unknown" || len(got.Issues) != 1 {
		t.Fatalf("older server = %+v", got)
	}
	in.ReleaseSetStatus = "active"
	in.ActiveReleaseSet = &api.ProjectReleaseSetResponse{Members: []api.ProjectReleaseSetMemberResponse{{AppID: "removed", DeploymentID: "old"}}}
	in.Workloads = []api.ProjectEnvironmentStateWorkloadResponse{{AppID: "new", WorkloadSlug: "new-api"}}
	got := main.BuildProjectEnvironmentInspection(in)
	if len(got.Issues) != 2 || got.Issues[0].Code != "release_member_missing" || got.Issues[1].Code != "release_members_detached" {
		t.Fatalf("membership drift = %+v", got.Issues)
	}
}

func TestProjectReleaseSetsCLIUsesPagination(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, `{"items":[],"next_before":"next"}`, http.StatusOK)
	if code := main.CmdProjects([]string{"environments", "release-sets", "shop", "production", "--limit", "2", "--before", "cursor"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if (*f.SawMethod()) != http.MethodGet || (*f.SawPath()) != "/v1/projects/shop/environments/production/release-sets" || (*f.SawQuery()) != "before=cursor&limit=2" {
		t.Fatalf("request = %s %s?%s", (*f.SawMethod()), (*f.SawPath()), (*f.SawQuery()))
	}
	if code := main.CmdProjectsEnvironmentReleaseSets([]string{"shop", "production", "--limit", "101"}); code == 0 {
		t.Fatal("invalid limit accepted")
	}
}

func TestProjectInspectionStateFailureReturnsError(t *testing.T) {
	main.ResetJSONOut(t)
	f := main.AuthedFakeAPI(t, `{"status":503,"title":"Unavailable"}`, http.StatusServiceUnavailable)
	if code := main.CmdProjectsEnvironmentInspect([]string{"shop", "production"}); code == 0 {
		t.Fatal("state failure succeeded")
	}
	if !strings.HasSuffix((*f.SawPath()), "/state") {
		t.Fatalf("unexpected request: %s", (*f.SawPath()))
	}
}
