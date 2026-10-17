package promotion

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
	raw, _ := json.Marshal(approved.Definition)
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
	return Input{From: from, To: to, Revision: api.EnvironmentDesiredRevision{ID: "revision", SourceID: from.ID, CommitSHA: strings.Repeat("c", 40), Digest: approved.Digest, Definition: raw}, TargetCommit: strings.Repeat("d", 40), Target: target, Now: now, ReleaseID: "release",
		Evidence:  &api.EnvironmentWorkloadActivationEvidence{GraphID: "graph", PlanHash: strings.Repeat("f", 64), SourceID: from.ID, EnvironmentID: from.EnvironmentID, RevisionID: from.ApprovedRevisionID, DefinitionDigest: approved.Digest, Generation: from.Generation, IntentVersion: from.IntentVersion, ArtifactsPrepared: true, Qualified: true, Activated: true, Serving: true},
		Artifacts: []api.EnvironmentGitPromotionArtifact{{Resource: "workload/api", DeploymentID: "deployment", Kind: "image", ImageDigest: approved.Definition.Workloads["api"].Source.Image}}}
}

func complete(t *testing.T, in Input) api.PreviewEnvironmentGitPromotionResponse {
	t.Helper()
	out, proposed, err := Prepare(in)
	if err != nil {
		t.Fatal(err)
	}
	plan := api.EnvironmentGitOpsPlan{Manager: in.To.ID, Revision: "candidate:" + in.TargetCommit, CommitSHA: in.TargetCommit, Generation: in.To.Generation, ObservedVersion: in.To.IntentVersion, DesiredDigest: proposed.Digest, Hash: strings.Repeat("e", 64), Changes: []api.EnvironmentGitOpsChange{}}
	out, err = Finish(out, plan)
	if err != nil || Validate(out) != nil {
		t.Fatalf("invalid plan: %v %v", err, Validate(out))
	}
	return out
}

func TestPromotionPreservesTargetConfigurationAndExportsExactReviewedSources(t *testing.T) {
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

func TestPromotionBlocksUnprovenAndUnsupportedInputs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Input)
		code   string
	}{
		{"repository transfer", func(in *Input) { in.To.Spec.RepositoryID++ }, "promotion_repository_identity_mismatch"},
		{"provider mismatch", func(in *Input) { in.To.Spec.Provider = "gitlab" }, "promotion_repository_identity_mismatch"},
		{"missing evidence", func(in *Input) { in.Evidence = nil }, "promotion_source_release_unproven"},
		{"stale graph", func(in *Input) { in.Evidence.IntentVersion++ }, "promotion_source_release_unproven"},
		{"not serving", func(in *Input) { in.Evidence.Serving = false }, "promotion_source_release_unproven"},
		{"not prepared", func(in *Input) { in.Evidence.ArtifactsPrepared = false }, "promotion_source_release_unproven"},
		{"graph error", func(in *Input) { in.Evidence.GraphErrorCode = "private_native_error" }, "promotion_source_release_unproven"},
		{"missing release", func(in *Input) { in.ReleaseID = "" }, "promotion_source_release_unproven"},
		{"source outage", func(in *Input) { in.From.SourceErrorCode = "PRIVATE_UPSTREAM" }, "promotion_source_unverified"},
		{"stale poll", func(in *Input) {
			old := in.Now.Add(-2 * api.EnvironmentGitSourceStaleAfter)
			in.From.SourceVerifiedAt = &old
		}, "promotion_source_unverified"},
		{"replaced artifact", func(in *Input) { in.Artifacts[0].ImageDigest = "sha256:" + strings.Repeat("f", 64) }, "workload/api: promotion_artifact_provenance_mismatch"},
		{"invalid artifact reference redacted", func(in *Input) {
			in.Artifacts[0].ImageDigest = "PRIVATE_CREDENTIAL@registry.example/api@sha256:" + strings.Repeat("a", 64)
		}, "workload/api: promotion_artifact_provenance_mismatch"},
		{"invalid target reference redacted", func(in *Input) {
			definition := in.Target.Definition
			workload := definition.Workloads["api"]
			workload.Source.Image = "PRIVATE_CREDENTIAL@registry.example/api@sha256:" + strings.Repeat("a", 64)
			definition.Workloads["api"] = workload
			compiled, err := environmentsync.Compile(definition)
			if err != nil {
				t.Fatal(err)
			}
			in.Target = compiled
		}, "workload/api: promotion_target_image_contract_required"},
		{"duplicate artifact", func(in *Input) { in.Artifacts = append(in.Artifacts, in.Artifacts[0]) }, "promotion_artifact_ambiguous"},
		{"source build", func(in *Input) {
			var d api.EnvironmentDefinition
			_ = json.Unmarshal(in.Revision.Definition, &d)
			w := d.Workloads["api"]
			w.Source = &api.EnvironmentWorkloadSource{Kind: "source", Directory: "."}
			d.Workloads["api"] = w
			compiled, err := environmentsync.Compile(d)
			if err != nil {
				t.Fatal(err)
			}
			in.Revision.Digest = compiled.Digest
			in.Revision.Definition, _ = json.Marshal(compiled.Definition)
		}, "workload/api: promotion_build_artifact_unproven"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := fixture(t)
			tc.mutate(&in)
			out := complete(t, in)
			if !slices.Contains(out.BlockingReasons, tc.code) {
				t.Fatalf("missing %s: %v", tc.code, out.BlockingReasons)
			}
			if _, err := ExportPatch(out); err == nil {
				t.Fatal("blocked patch exported")
			}
			encoded, _ := json.Marshal(out)
			if strings.Contains(string(encoded), "PRIVATE") {
				t.Fatal("invalid source or private configuration leaked from preview")
			}
		})
	}
}

func TestPromotionHashFencesReleaseIntentAndManifestWithoutClockChurn(t *testing.T) {
	in := fixture(t)
	out := complete(t, in)
	in.Now = in.Now.Add(time.Second)
	if complete(t, in).PlanHash != out.PlanHash {
		t.Fatal("observation clock churned plan hash")
	}
	for _, mutate := range []func(*Input){func(in *Input) { in.ReleaseID = "replacement" }, func(in *Input) { in.To.IntentVersion++ }, func(in *Input) { in.Artifacts[0].DeploymentID = "replacement" }, func(in *Input) { in.To.Generation++ }} {
		in := fixture(t)
		mutate(&in)
		if complete(t, in).PlanHash == out.PlanHash {
			t.Fatal("changed authority retained hash")
		}
	}
	changed := fixture(t)
	w := changed.Target.Definition.Workloads["api"]
	w.Variables["MODE"] = "locally-edited"
	changed.Target.Definition.Workloads["api"] = w
	if _, err := ExportManifest(out, changed.Target.Definition); err == nil {
		t.Fatal("changed local base accepted")
	}
	out.TargetPlan.Changes = []api.EnvironmentGitOpsChange{{Before: json.RawMessage(`"PRIVATE"`)}}
	if Validate(out) == nil {
		t.Fatal("unredacted response accepted")
	}
}

func TestPromotionRefusesExportWhileTargetOverridesPreventConvergence(t *testing.T) {
	out := complete(t, fixture(t))
	plan := out.TargetPlan
	plan.Changes = []api.EnvironmentGitOpsChange{{Resource: "workload/api", Path: "source", Action: "overridden"}}
	out, err := Finish(out, plan)
	if err != nil || !slices.Contains(out.BlockingReasons, "workload/api/source: promotion_target_override_active") {
		t.Fatalf("active target override omitted: %+v %v", out, err)
	}
	if _, err := ExportPatch(out); err == nil {
		t.Fatal("target override did not block export")
	}
}

func TestBuiltArtifactPromotionExportsCodeWithoutSourceConfiguration(t *testing.T) {
	for _, kind := range []string{"source", "dockerfile", "function"} {
		t.Run(kind, func(t *testing.T) {
			in := fixture(t)
			var definition api.EnvironmentDefinition
			_ = json.Unmarshal(in.Revision.Definition, &definition)
			w := definition.Workloads["api"]
			w.App = "shared-api"
			w.Source = &api.EnvironmentWorkloadSource{Kind: kind, Directory: "apps/api"}
			if kind == "function" {
				w.Source.Runtime = "node22"
			}
			definition.Workloads["api"] = w
			origin, err := environmentsync.Compile(definition)
			if err != nil {
				t.Fatal(err)
			}
			in.Revision.Definition, _ = json.Marshal(origin.Definition)
			in.Revision.Digest, in.Evidence.DefinitionDigest = origin.Digest, origin.Digest
			target := in.Target.Definition.Workloads["api"]
			target.App, target.Source = w.App, w.Source
			in.Target.Definition.Workloads["api"] = target
			in.Target, err = environmentsync.Compile(in.Target.Definition)
			if err != nil {
				t.Fatal(err)
			}
			in.Artifacts = []api.EnvironmentGitPromotionArtifact{{Resource: "workload/api", DeploymentID: "deployment", BuildID: "build", Kind: "github",
				BuildArtifactID: "018f22b4-1d51-7d55-a5d8-fdc06ec7c663", BuildArtifactHash: strings.Repeat("a", 64), ArtifactDigest: "sha256:" + strings.Repeat("b", 64), BuildCommitSHA: strings.Repeat("c", 40)}}
			out := complete(t, in)
			if len(out.BlockingReasons) != 0 {
				t.Fatal(out.BlockingReasons)
			}
			encoded, err := ExportManifest(out, in.Target.Definition)
			if err != nil {
				t.Fatal(err)
			}
			var proposed api.EnvironmentDefinition
			_ = json.Unmarshal(encoded, &proposed)
			if proposed.Workloads["api"].Source.BuildArtifactID != in.Artifacts[0].BuildArtifactID || proposed.Workloads["api"].Variables["MODE"] != "PRODUCTION_PRIVATE" || proposed.Workloads["api"].SecretRefs["TOKEN"] != "secret:PRODUCTION_TOKEN" {
				t.Fatal("code or target config changed")
			}
			in.Artifacts[0].BuildArtifactHash = strings.Repeat("e", 64)
			if complete(t, in).PlanHash == out.PlanHash {
				t.Fatal("producer replacement retained the review hash")
			}
			delete(in.Target.Definition.Workloads, "api")
			in.Target, _ = environmentsync.Compile(in.Target.Definition)
			if len(complete(t, in).BlockingReasons) == 0 {
				t.Fatal("missing target contract accepted")
			}
		})
	}
}
