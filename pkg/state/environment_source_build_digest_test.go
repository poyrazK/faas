// adr: 20261017190804, 435 — derived build identity retains reviewed source provenance.
package state_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestFrozenGitSourceAcceptsDerivedBuildDigest(t *testing.T) {
	buildID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	archive := state.EnvironmentWorkloadSourceArtifact{RevisionID: "revision", CommitSHA: strings.Repeat("a", 40),
		DefinitionDigest: strings.Repeat("b", 64), BuildID: buildID.String(), Path: "/review/source.tgz",
		SHA256: strings.Repeat("c", 64), Bytes: 512, LogPath: "/review/build.log"}
	frozen := state.EnvironmentWorkloadRuntime{SourceID: "source", EnvironmentID: "environment", RevisionID: archive.RevisionID,
		DefinitionDigest: archive.DefinitionDigest, Generation: 1, Resource: "workload/api", PlanHash: strings.Repeat("d", 64),
		AppID: "app", Scope: "production", AppType: state.AppTypeApp, Runtime: map[string]json.RawMessage{},
		Source: &api.EnvironmentWorkloadSource{Kind: "dockerfile", Directory: "app", Dockerfile: "Dockerfile"}, SourceArchive: &archive}
	raw, err := json.Marshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	base := state.Deployment{AppID: frozen.AppID, Scope: frozen.Scope, Kind: state.DeploymentKindGitHub, CommitSHA: archive.CommitSHA,
		SourcePath: archive.Path, SourceSHA256: archive.SHA256, SourceBytes: archive.Bytes, SourceRoot: "app", BuildID: archive.BuildID,
		LogPath: archive.LogPath, EnvironmentWorkloadRuntime: string(raw)}
	for _, tc := range []struct {
		name   string
		mutate func(*state.Deployment)
		valid  bool
	}{
		{"before imaging", func(*state.Deployment) {}, true},
		{"produced manifest", func(d *state.Deployment) { d.ImageDigest = "sha256:" + strings.Repeat("e", 64) }, true},
		{"registry reference", func(d *state.Deployment) { d.ImageDigest = "registry.example/api@sha256:" + strings.Repeat("e", 64) }, false},
		{"malformed digest", func(d *state.Deployment) { d.ImageDigest = "sha256:short" }, false},
		{"uppercase digest", func(d *state.Deployment) { d.ImageDigest = "sha256:" + strings.Repeat("E", 64) }, false},
		{"changed archive", func(d *state.Deployment) { d.SourceSHA256 = strings.Repeat("f", 64) }, false},
		{"changed path", func(d *state.Deployment) { d.SourcePath += ".other" }, false},
		{"changed commit", func(d *state.Deployment) { d.CommitSHA = strings.Repeat("f", 40) }, false},
		{"changed build", func(d *state.Deployment) { d.BuildID = uuid.NewString() }, false},
		{"changed root", func(d *state.Deployment) { d.SourceRoot = "other" }, false},
		{"changed kind", func(d *state.Deployment) { d.Kind = state.DeploymentKindImage }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dep := base
			tc.mutate(&dep)
			got, err := dep.ScopedWorkloadRuntime()
			if (err == nil) != tc.valid || tc.valid && (got == nil || *got.SourceArchive != archive) {
				t.Fatalf("frozen source: runtime=%+v err=%v; want valid=%v with unchanged archive", got, err, tc.valid)
			}
		})
	}
}
