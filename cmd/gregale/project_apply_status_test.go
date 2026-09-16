package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func projectApplyPlan(actions ...string) api.PlanResponse {
	plan := api.PlanResponse{CanApply: true, PlanToken: "token"}
	for i, action := range actions {
		plan.Workloads = append(plan.Workloads, api.PlanWorkload{
			Name:   "app-" + string(rune('a'+i)),
			Action: action,
		})
	}
	return plan
}

func projectApplyBuild(slug, deploymentID, buildID, buildError string) api.AppliedBuild {
	return api.AppliedBuild{
		Slug:         slug,
		AppID:        "id-" + slug,
		DeploymentID: deploymentID,
		BuildID:      buildID,
		Error:        buildError,
	}
}

func TestSummarizeProjectApply_AllSuccess(t *testing.T) {
	plan := projectApplyPlan("create", "update")
	apply := api.ApplyResponse{
		ProjectID: "project-1",
		Apps:      []api.ApplyResponseApp{{Slug: "app-a"}, {Slug: "app-b"}},
		Builds: []api.AppliedBuild{
			projectApplyBuild("app-a", "dep-a", "build-a", ""),
			projectApplyBuild("app-b", "dep-b", "build-b", ""),
		},
	}

	status := summarizeProjectApply(apply)
	if status.appsReconciled != 2 || status.buildsQueued != 2 || status.buildsFailed != 0 {
		t.Fatalf("unexpected success status: %+v", status)
	}

	var out bytes.Buffer
	if code := renderProjectApplyResult(&out, plan, apply); code != 0 {
		t.Fatalf("all-success apply exit = %d, want 0", code)
	}
	text := out.String()
	for _, want := range []string{
		"Created project project-1",
		"app-a: deployment=dep-a build=build-a",
		"app-b: deployment=dep-b build=build-b",
		"Summary: apps reconciled=2, builds queued=2, builds failed=0",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("success output missing %q:\n%s", want, text)
		}
	}
}

func TestRenderProjectApplyResult_PartialFailure(t *testing.T) {
	plan := projectApplyPlan("create", "update")
	apply := api.ApplyResponse{
		ProjectID: "project-1",
		Apps:      []api.ApplyResponseApp{{Slug: "app-a"}, {Slug: "app-b"}},
		Builds: []api.AppliedBuild{
			projectApplyBuild("app-a", "dep-a", "build-a", ""),
			projectApplyBuild("app-b", "", "", "enqueue failed"),
		},
	}

	var out bytes.Buffer
	if code := renderProjectApplyResult(&out, plan, apply); code != 1 {
		t.Fatalf("partial apply exit = %d, want 1", code)
	}
	text := out.String()
	if strings.Contains(text, "Created project") {
		t.Fatalf("partial apply led with an unqualified success:\n%s", text)
	}
	for _, want := range []string{
		"Project project-1 applied with failures",
		"app-a: deployment=dep-a build=build-a",
		"app-b: enqueue failed",
		"Summary: apps reconciled=2, builds queued=1, builds failed=1",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("partial output missing %q:\n%s", want, text)
		}
	}
}

func TestRenderProjectApplyResult_AllFailed(t *testing.T) {
	plan := projectApplyPlan("create", "update")
	apply := api.ApplyResponse{
		ProjectID: "project-1",
		Apps:      []api.ApplyResponseApp{{Slug: "app-a"}, {Slug: "app-b"}},
		Builds: []api.AppliedBuild{
			projectApplyBuild("app-a", "", "", "stage failed"),
			projectApplyBuild("app-b", "", "", "enqueue failed"),
		},
	}

	var out bytes.Buffer
	if code := renderProjectApplyResult(&out, plan, apply); code != 1 {
		t.Fatalf("all-failed apply exit = %d, want 1", code)
	}
	text := out.String()
	for _, want := range []string{
		"Project project-1 applied with failures",
		"app-a: stage failed",
		"app-b: enqueue failed",
		"Summary: apps reconciled=2, builds queued=0, builds failed=2",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("all-failed output missing %q:\n%s", want, text)
		}
	}
}

func TestRenderProjectApplyResult_EmptyBuildsForChangedWorkloadFails(t *testing.T) {
	plan := projectApplyPlan("update")
	apply := api.ApplyResponse{
		ProjectID: "project-1",
		Apps:      []api.ApplyResponseApp{{Slug: "app-a"}},
	}

	var out bytes.Buffer
	if code := renderProjectApplyResult(&out, plan, apply); code != 1 {
		t.Fatalf("empty-build apply exit = %d, want 1", code)
	}
	text := out.String()
	if strings.Contains(text, "Created project") {
		t.Fatalf("empty-build apply led with an unqualified success:\n%s", text)
	}
	for _, want := range []string{
		"missing build results: 1 workload(s) were expected but not returned",
		"Summary: apps reconciled=1, builds queued=0, builds failed=1",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("empty-build output missing %q:\n%s", want, text)
		}
	}
}

func TestRenderProjectApplyResult_EmptyBuildsForNoopSucceeds(t *testing.T) {
	plan := projectApplyPlan("noop")
	apply := api.ApplyResponse{
		ProjectID: "project-1",
		Apps:      nil,
	}

	var out bytes.Buffer
	if code := renderProjectApplyResult(&out, plan, apply); code != 0 {
		t.Fatalf("no-op apply exit = %d, want 0", code)
	}
	text := out.String()
	if !strings.Contains(text, "Created project project-1") {
		t.Fatalf("no-op apply missing success line:\n%s", text)
	}
	if !strings.Contains(text, "Summary: apps reconciled=0, builds queued=0, builds failed=0") {
		t.Fatalf("no-op apply missing summary:\n%s", text)
	}
}

func TestRenderProjectApplyResult_UnchangedUpdateWithNoBuildsSucceeds(t *testing.T) {
	// The scan partition labels every existing workload as "update" when it
	// matches by identity; only ApplyResponse.Apps distinguishes an actual
	// reconcile from an unchanged re-apply.
	plan := projectApplyPlan("update")
	apply := api.ApplyResponse{ProjectID: "project-1"}

	var out bytes.Buffer
	if code := renderProjectApplyResult(&out, plan, apply); code != 0 {
		t.Fatalf("unchanged update apply exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "Summary: apps reconciled=0, builds queued=0, builds failed=0") {
		t.Fatalf("unchanged update missing zero-build summary:\n%s", out.String())
	}
}

func TestCmdDeployTarball_ProjectBuildFailuresSetExitCode(t *testing.T) {
	plan := projectApplyPlan("create", "update")
	cases := []struct {
		name       string
		json       bool
		builds     []api.AppliedBuild
		wantCode   int
		wantStdout []string
		wantStderr string
	}{
		{
			name: "partial text",
			builds: []api.AppliedBuild{
				projectApplyBuild("app-a", "dep-a", "build-a", ""),
				projectApplyBuild("app-b", "", "", "enqueue failed"),
			},
			wantCode: 1,
			wantStdout: []string{
				"Project project-1 applied with failures",
				"app-a: deployment=dep-a build=build-a",
				"app-b: enqueue failed",
				"Summary: apps reconciled=2, builds queued=1, builds failed=1",
			},
		},
		{
			name: "all failed json",
			json: true,
			builds: []api.AppliedBuild{
				projectApplyBuild("app-a", "", "", "stage failed"),
				projectApplyBuild("app-b", "", "", "enqueue failed"),
			},
			wantCode:   1,
			wantStderr: "project apply failed: apps reconciled=2, builds queued=0, builds failed=2",
		},
		{
			name:       "empty changed json",
			json:       true,
			builds:     nil,
			wantCode:   1,
			wantStderr: "project apply failed: apps reconciled=2, builds queued=0, builds failed=2",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			apply := api.ApplyResponse{
				PlanResponse: plan,
				ProjectID:    "project-1",
				Apps: []api.ApplyResponseApp{
					{Slug: "app-a", ID: "id-app-a"},
					{Slug: "app-b", ID: "id-app-b"},
				},
				Builds: tc.builds,
			}
			sink := &decomposeSink{
				scanStatus:  200,
				scanBody:    plan,
				applyStatus: 200,
				applyBody:   apply,
			}
			srv := httptest.NewServer(sink)
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")

			var stdout, stderr bytes.Buffer
			oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
			osStdout, osStderr, jsonOutput = &stdout, &stderr, tc.json
			t.Cleanup(func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON })

			code := cmdDeployTarball([]string{
				"--tarball", writeTarball(t),
				"--project-slug", "fixture",
				"--yes",
			})
			if code != tc.wantCode {
				t.Fatalf("cmdDeployTarball exit = %d, want %d\nstdout=%s\nstderr=%s", code, tc.wantCode, stdout.String(), stderr.String())
			}
			for _, want := range tc.wantStdout {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("stdout missing %q:\n%s", want, stdout.String())
				}
			}
			if tc.wantStderr != "" && !strings.Contains(stderr.String(), tc.wantStderr) {
				t.Errorf("stderr missing %q:\n%s", tc.wantStderr, stderr.String())
			}
			if tc.json {
				var got api.ApplyResponse
				if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
					t.Fatalf("JSON output is invalid: %v\n%s", err, stdout.String())
				}
				if len(got.Builds) != len(tc.builds) {
					t.Fatalf("JSON dropped build rows: got %d, want %d", len(got.Builds), len(tc.builds))
				}
			}
		})
	}
}
