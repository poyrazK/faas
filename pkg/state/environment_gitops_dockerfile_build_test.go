package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A scoped source selection has no place for build arguments or a target, so
// inheriting a live Dockerfile app that uses them must refuse rather than
// rebuild the final stage with default arguments (ADR-20261019142544).
func TestObservedWorkloadSourceRefusesDockerfileBuildOptions(t *testing.T) {
	const commit = "0123456789abcdef0123456789abcdef01234567"
	app := state.GitOpsIntentApp{Type: state.AppTypeApp, Sources: []state.GitOpsSourceBaseline{{
		Kind: state.DeploymentKindGitHub, SourceURL: "github://acme/shop@" + commit, CommitSHA: commit, SourceRoot: "api",
	}}}
	app.Manifest.BuildDockerfile = "Dockerfile"
	source, _, reason := state.ObservedWorkloadSource(app, "acme/shop")
	if reason != "" || source == nil || source.Kind != "dockerfile" || source.Dockerfile != "Dockerfile" {
		t.Fatalf("plain Dockerfile app: source=%+v reason=%q", source, reason)
	}
	for _, options := range []*api.DockerfileBuildOptions{
		{Target: "runtime"},
		{Args: map[string]string{"NODE_ENV": "production"}},
	} {
		app.Manifest.DockerfileBuild = options
		source, _, reason = state.ObservedWorkloadSource(app, "acme/shop")
		if source != nil || reason != "live Dockerfile build arguments or target require a reviewed scoped import" {
			t.Fatalf("options %+v: source=%+v reason=%q", options, source, reason)
		}
	}
	app.Manifest.DockerfileBuild = &api.DockerfileBuildOptions{}
	if source, _, reason = state.ObservedWorkloadSource(app, "acme/shop"); reason != "" || source == nil {
		t.Fatalf("empty options refused: source=%+v reason=%q", source, reason)
	}
}
