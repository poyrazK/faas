package rollback

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

func fixture(t *testing.T) Input {
	t.Helper()
	now := time.Now().UTC()
	spec := api.EnvironmentGitSourceSpec{Provider: "github", Repository: "example/shop", RepositoryID: 123, InstallationID: 42, ManifestPath: "environments/production.yaml", Mode: "enforce"}
	from := api.EnvironmentGitSource{ID: "staging-source", AccountID: "account", ProjectID: "project", EnvironmentID: "staging-id", EnvironmentSlug: "staging", Spec: spec, Generation: 2, IntentVersion: 3, ApprovedRevisionID: "revision", SourceCheckedAt: &now, SourceVerifiedAt: &now}
	to := from
	to.ID, to.EnvironmentID, to.EnvironmentSlug, to.ApprovedRevisionID = "production-source", "production-id", "production", "target-revision"
	definition := api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: "shop", Environment: "staging", Workloads: map[string]api.EnvironmentWorkload{
		"api": {Source: &api.EnvironmentWorkloadSource{Kind: "image", Image: "registry.example/api@sha256:" + strings.Repeat("a", 64)}, Variables: map[string]string{"MODE": "STAGING_PRIVATE"}, SecretRefs: map[string]string{"TOKEN": "secret:STAGING_TOKEN"}},
	}}
	approved, err := environmentsync.Compile(definition)
	if err != nil {
		t.Fatal(err)
	}
	definition.Environment = "production"
	w := definition.Workloads["api"]
	w.Source = &api.EnvironmentWorkloadSource{Kind: "image", Image: "registry.example/api@sha256:" + strings.Repeat("b", 64)}
	w.Variables = map[string]string{"MODE": "PRODUCTION_PRIVATE"}
	w.SecretRefs = map[string]string{"TOKEN": "secret:PRODUCTION_TOKEN"}
	definition.Workloads["api"] = w
	target, err := environmentsync.Compile(definition)
	if err != nil {
		t.Fatal(err)
	}
	from.EnvironmentID, from.EnvironmentSlug = to.EnvironmentID, "production"
	from.Spec = to.Spec
	definition.Environment = "production"
	// Recover a historical manifest, never require it to be current approval.
	old := approved.Definition
	old.Environment = "production"
	approved, err = environmentsync.Compile(old)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(approved.Definition)
	if err != nil {
		t.Fatal(err)
	}
	from.ApprovedRevisionID = "new-revision"
	return Input{Source: from, Revision: api.EnvironmentDesiredRevision{ID: "revision", SourceID: from.ID, CommitSHA: strings.Repeat("c", 40), Digest: approved.Digest, Definition: raw},
		Origin: api.EnvironmentGitPromotionOrigin{SourceID: from.ID, RevisionID: "revision", CommitSHA: strings.Repeat("c", 40), DefinitionDigest: approved.Digest, Generation: 1, IntentVersion: 1, GraphID: "graph", ReleaseSetID: "2ba0aa40-2362-4c0e-aeb1-6381666bb514"}, ServedAt: &now, Proven: true, CurrentReleaseSetID: "new-release",
		TargetCommit: strings.Repeat("d", 40), Target: target, Now: now, Artifacts: []api.EnvironmentGitPromotionArtifact{{Resource: "workload/api", DeploymentID: "old-deployment", Kind: "image", ImageDigest: approved.Definition.Workloads["api"].Source.Image}}}

}

func complete(t *testing.T, in Input) api.PreviewEnvironmentGitRollbackResponse {
	t.Helper()
	out, proposed, err := Prepare(in)
	if err != nil {
		t.Fatal(err)
	}
	plan := api.EnvironmentGitOpsPlan{Manager: in.Source.ID, Revision: "candidate:" + in.TargetCommit, CommitSHA: in.TargetCommit, Generation: in.Source.Generation, ObservedVersion: in.Source.IntentVersion, DesiredDigest: proposed.Digest, Hash: strings.Repeat("e", 64), Changes: []api.EnvironmentGitOpsChange{}}
	out, err = Finish(out, plan)
	if err != nil || Validate(out) != nil {
		t.Fatalf("invalid plan: %v %v", err, Validate(out))
	}
	return out
}

func TestRollbackPreservesTargetConfigurationAndExportsExactReviewedSources(t *testing.T) {
	in := fixture(t)
	out := complete(t, in)
	if len(out.BlockingReasons) != 0 || len(out.Changes) != 1 {
		t.Fatalf("unexpected plan: %+v", out)
	}
	exported, err := ExportManifest(out, in.Target.Definition)
	if err != nil {
		t.Fatal(err)
	}
	var result api.EnvironmentDefinition
	if json.Unmarshal(exported, &result) != nil || result.Environment != "production" || result.Workloads["api"].Variables["MODE"] != "PRODUCTION_PRIVATE" || result.Workloads["api"].SecretRefs["TOKEN"] != "secret:PRODUCTION_TOKEN" || result.Workloads["api"].Source.Image != in.Artifacts[0].ImageDigest {
		t.Fatalf("target settings changed: %s", exported)
	}
	if in.Target.Definition.Workloads["api"].Source.Image == result.Workloads["api"].Source.Image {
		t.Fatal("planning mutated caller target")
	}
	patch, err := ExportPatch(out)
	if err != nil || len(patch.Operations) != 4 || patch.Operations[2].Op != "test" || patch.Operations[3].Op != "replace" {
		t.Fatalf("patch: %+v %v", patch, err)
	}
	encoded, _ := json.Marshal(out)
	patchJSON, _ := json.Marshal(patch)
	for _, value := range []string{"PRODUCTION_PRIVATE", "STAGING_PRIVATE", "secret:PRODUCTION_TOKEN", "secret:STAGING_TOKEN"} {
		if strings.Contains(string(encoded)+string(patchJSON), value) {
			t.Fatal("configuration leaked from preview")
		}
	}
}

func TestRollbackFencesHistoricalProofAndHash(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Input)
		reason string
	}{
		{"not served", func(in *Input) { in.Proven = false }, "rollback_release_unproven"},
		{"missing receipt time", func(in *Input) { in.ServedAt = nil }, "rollback_release_unproven"},
		{"suspended", func(in *Input) { in.Source.Suspended = true }, "rollback_source_inactive"},
		{"source outage", func(in *Input) { in.Source.SourceErrorCode = "PRIVATE_UPSTREAM" }, "rollback_source_unverified"},
		{"artifact missing", func(in *Input) { in.Artifacts = nil }, "workload/api: rollback_artifact_provenance_mismatch"},
		{"duplicate artifact", func(in *Input) { in.Artifacts = append(in.Artifacts, in.Artifacts[0]) }, "rollback_artifact_ambiguous"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := fixture(t)
			tc.mutate(&in)
			out := complete(t, in)
			if !slices.Contains(out.BlockingReasons, tc.reason) {
				t.Fatalf("missing blocker %s: %+v", tc.reason, out)
			}
			if _, err := ExportPatch(out); err == nil {
				t.Fatal("blocked export")
			}
		})
	}
	in := fixture(t)
	first := complete(t, in)
	in.Now = in.Now.Add(time.Second)
	second := complete(t, in)
	if first.PlanHash != second.PlanHash {
		t.Fatal("clock churn")
	}
	in.CurrentReleaseSetID = "changed"
	if complete(t, in).PlanHash == first.PlanHash {
		t.Fatal("current release not bound")
	}
	first.Origin.ReleaseSetID = "changed"
	if Validate(first) == nil {
		t.Fatal("tampered origin accepted")
	}
	in = fixture(t)
	out := complete(t, in)
	changed := in.Target.Definition
	w := changed.Workloads["api"]
	w.Variables = map[string]string{"MODE": "CHANGED"}
	changed.Workloads["api"] = w
	if _, err := ExportManifest(out, changed); err == nil {
		t.Fatal("changed local manifest exported")
	}
	in = fixture(t)
	in.Origin.SourceID = "foreign"
	if _, _, err := Prepare(in); err == nil {
		t.Fatal("cross-source history accepted")
	}
}

func TestRollbackSignedBuildReferencePreservesConfigAndRejectsRecipes(t *testing.T) {
	in := fixture(t)
	var d api.EnvironmentDefinition
	_ = json.Unmarshal(in.Revision.Definition, &d)
	w := d.Workloads["api"]
	w.App = "shop-api"
	w.Source = &api.EnvironmentWorkloadSource{Kind: "source", Directory: "apps/api"}
	d.Workloads["api"] = w
	old, err := environmentsync.Compile(d)
	if err != nil {
		t.Fatal(err)
	}
	in.Revision.Definition, _ = json.Marshal(old.Definition)
	in.Revision.Digest = old.Digest
	in.Origin.DefinitionDigest = old.Digest
	d = in.Target.Definition
	w = d.Workloads["api"]
	w.App = "shop-api"
	w.Source = &api.EnvironmentWorkloadSource{Kind: "source", Directory: "apps/api"}
	d.Workloads["api"] = w
	in.Target, err = environmentsync.Compile(d)
	if err != nil {
		t.Fatal(err)
	}
	in.Artifacts = []api.EnvironmentGitPromotionArtifact{{Resource: "workload/api", DeploymentID: "old", Kind: "source", BuildID: "build", BuildArtifactID: "f3ae3221-22db-4896-8400-d3619fa0647b", BuildArtifactHash: strings.Repeat("a", 64), ArtifactDigest: "sha256:" + strings.Repeat("b", 64), BuildCommitSHA: strings.Repeat("c", 40)}}
	out := complete(t, in)
	if len(out.BlockingReasons) != 0 || len(out.Changes) != 1 {
		t.Fatalf("build rollback %+v", out)
	}
	encoded, err := ExportManifest(out, in.Target.Definition)
	if err != nil {
		t.Fatal(err)
	}
	var got api.EnvironmentDefinition
	_ = json.Unmarshal(encoded, &got)
	if got.Workloads["api"].Source.BuildArtifactID != in.Artifacts[0].BuildArtifactID || got.Workloads["api"].Variables["MODE"] != "PRODUCTION_PRIVATE" {
		t.Fatal("source or target config changed")
	}
	d = in.Target.Definition
	w = d.Workloads["api"]
	copy := *w.Source
	copy.Directory = "other"
	w.Source = &copy
	d.Workloads["api"] = w
	in.Target, err = environmentsync.Compile(d)
	if err != nil {
		t.Fatal(err)
	}
	out = complete(t, in)
	if len(out.BlockingReasons) == 0 {
		t.Fatal("incompatible recipe accepted")
	}
}
