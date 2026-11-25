package main_test

import (
	"bytes"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Prod hunt r49: the post-deploy "Changes since v1" led with lifecycle
// fields (status superseded -> live, traffic 0 -> 100), the upload URL, and
// the whole build_plan twice to show one changed hash.
func TestReleaseSummaryShowsOnlyReleaseChanges(t *testing.T) {
	var out bytes.Buffer
	main.RenderDeploymentReleaseSummary(&out, api.DeploymentSummaryResponse{
		Previous: &api.DeploymentResponse{ID: "d1", Revision: 1},
		Changes: []api.DeploymentChange{
			{Field: "status", Before: "superseded", After: "live"},
			{Field: "image_digest", Before: "sha256:old", After: "sha256:new"},
			{Field: "source_url", Before: "local-tar://upload-session/abc", After: ""},
			{Field: "source_sha256", Before: "aaa", After: "bbb"},
			{Field: "build_plan",
				Before: map[string]any{"entrypoint": "npm start", "port": 3000, "source_sha256": "aaa"},
				After:  map[string]any{"entrypoint": "node server.js", "port": 3000, "source_sha256": "bbb"}},
			{Field: "traffic_percent", Before: 0, After: 100},
			{Field: "canary_preset", Before: "none", After: "adaptive"},
		},
		RollbackTargetID: "d1",
	}, "my-app")
	got := out.String()
	for _, want := range []string{"image_digest", "source_sha256", `build_plan.entrypoint "npm start" -> "node server.js"`, "canary_preset"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary lacks %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"superseded", "traffic_percent", "local-tar://", "build_plan.port", "build_plan.source_sha256", "{"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("summary still shows %q:\n%s", unwanted, got)
		}
	}
}

// Only lifecycle fields changed: the release itself did not.
func TestReleaseSummaryLifecycleOnlyIsNone(t *testing.T) {
	var out bytes.Buffer
	main.RenderDeploymentReleaseSummary(&out, api.DeploymentSummaryResponse{
		Previous: &api.DeploymentResponse{ID: "d1", Revision: 1},
		Changes:  []api.DeploymentChange{{Field: "status", Before: "superseded", After: "live"}},
	}, "my-app")
	if !strings.Contains(out.String(), "Changes since v1: none") {
		t.Fatalf("summary = %q, want no changes", out.String())
	}
}

// A switch between a git source and a local upload stays listed, with the
// upload named rather than its internal URL; an unparsable build_plan still
// reports that it changed.
func TestReleaseSummarySourceSwitchAndOpaqueBuildPlan(t *testing.T) {
	var out bytes.Buffer
	main.RenderDeploymentReleaseSummary(&out, api.DeploymentSummaryResponse{
		Previous: &api.DeploymentResponse{ID: "d1", Revision: 1},
		Changes: []api.DeploymentChange{
			{Field: "source_url", Before: "https://github.com/acme/app", After: "local-tar://upload-session/abc"},
			{Field: "build_plan", Before: "v1", After: "v2"},
		},
	}, "my-app")
	got := out.String()
	for _, want := range []string{`"https://github.com/acme/app" -> local upload`, "build_plan         changed"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "local-tar://") {
		t.Errorf("summary names the upload URL:\n%s", got)
	}
}
