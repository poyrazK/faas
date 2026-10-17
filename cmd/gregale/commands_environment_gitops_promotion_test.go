package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops/promotion"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

func promotionCLIResponse(t *testing.T) api.PreviewEnvironmentGitPromotionResponse {
	t.Helper()
	sha := strings.Repeat("a", 40)
	out := api.PreviewEnvironmentGitPromotionResponse{Project: "shop", FromEnvironment: "staging", ToEnvironment: "production", ReadOnly: true, ValuesRedacted: true, RequiresTargetQualification: true,
		Origin:          api.EnvironmentGitPromotionOrigin{SourceID: "staging-source", RevisionID: "revision", DefinitionDigest: strings.Repeat("b", 64)},
		Target:          api.EnvironmentGitPromotionTarget{SourceID: "target-source", CommitSHA: sha, DefinitionDigest: strings.Repeat("c", 64), ProposedDigest: strings.Repeat("d", 64)},
		BlockingReasons: []string{"promotion_source_release_unproven"},
	}
	var err error
	out, err = promotion.Finish(out, api.EnvironmentGitOpsPlan{Manager: "target-source", Revision: "candidate:" + sha, CommitSHA: sha, DesiredDigest: out.Target.ProposedDigest, Hash: strings.Repeat("e", 64)})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestGitOpsPromotionPlanJSONThroughProjectsDispatcher(t *testing.T) {
	resetJSONOut(t)
	jsonOutput = true
	out := promotionCLIResponse(t)
	body, _ := json.Marshal(out)
	fake := authedFakeAPI(t, string(body), http.StatusOK)
	var stdout bytes.Buffer
	code := captureStdoutSwap(t, &stdout, func() int {
		return cmdProjects([]string{"environments", "gitops", "promotion-plan", "shop", "production", "--from", "staging", "--target-commit", out.Target.CommitSHA, "--fail-on-blocked"})
	})
	if code != 2 || fake.sawMethod != http.MethodPost || fake.sawPath != "/v1/projects/shop/environments/production/gitops/promotion-plan" || !strings.Contains(stdout.String(), out.PlanHash) {
		t.Fatalf("code=%d request=%s %s output=%s", code, fake.sawMethod, fake.sawPath, stdout.String())
	}
}

func TestGitOpsPromotionExportRequiresReviewedHashAndRefusesBlockedOrUnredactedResult(t *testing.T) {
	for _, mode := range []string{"missing-hash", "blocked", "unredacted", "changed-hash"} {
		t.Run(mode, func(t *testing.T) {
			resetJSONOut(t)
			out := promotionCLIResponse(t)
			if mode == "unredacted" {
				out.TargetPlan.Changes = []api.EnvironmentGitOpsChange{{Before: json.RawMessage(`"PRIVATE_VALUE"`)}}
			}
			body, _ := json.Marshal(out)
			fake := authedFakeAPI(t, string(body), http.StatusOK)
			args := []string{"promotion-plan", "shop", "production", "--from", "staging", "--target-commit", out.Target.CommitSHA, "--format", "patch"}
			if mode != "missing-hash" {
				hash := out.PlanHash
				if mode == "changed-hash" {
					hash = strings.Repeat("f", 64)
				}
				args = append(args, "--expected-plan-hash", hash)
			}
			var stdout bytes.Buffer
			code := captureStdoutSwap(t, &stdout, func() int { return cmdProjectsEnvironmentGitOps(args) })
			if code == 0 || stdout.Len() != 0 || mode == "missing-hash" && fake.sawMethod != "" {
				t.Fatalf("unsafe export: code=%d output=%s", code, stdout.String())
			}
		})
	}
}

func TestGitOpsPromotionExportsReviewedTargetWithoutChangingConfiguration(t *testing.T) {
	for _, format := range []string{"patch", "manifest", "markdown"} {
		t.Run(format, func(t *testing.T) {
			resetJSONOut(t)
			before := api.EnvironmentWorkloadSource{Kind: "image", Image: "registry.example/api@sha256:" + strings.Repeat("1", 64)}
			after := api.EnvironmentWorkloadSource{Kind: "image", Image: "registry.example/api@sha256:" + strings.Repeat("2", 64)}
			definition := api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: "shop", Environment: "production",
				Workloads: map[string]api.EnvironmentWorkload{"api": {Source: &before, Variables: map[string]string{"MODE": "production-private"}, SecretRefs: map[string]string{"TOKEN": "secret:PRODUCTION_TOKEN"}}}}
			base, err := environmentsync.Compile(definition)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(base.Definition)
			path := filepath.Join(t.TempDir(), "production.yaml")
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			w := definition.Workloads["api"]
			w.Source = &after
			definition.Workloads["api"] = w
			proposed, err := environmentsync.Compile(definition)
			if err != nil {
				t.Fatal(err)
			}
			out := promotionCLIResponse(t)
			out.BlockingReasons = []string{}
			out.Target.DefinitionDigest, out.Target.ProposedDigest = base.Digest, proposed.Digest
			out.Changes = []api.EnvironmentGitPromotionChange{{Resource: "workload/api", Before: before, After: after}}
			out.TargetPlan.DesiredDigest = proposed.Digest
			out, err = promotion.Finish(out, out.TargetPlan)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := json.Marshal(out)
			authedFakeAPI(t, string(body), http.StatusOK)
			args := []string{"environments", "gitops", "promotion-plan", "shop", "production", "--from", "staging", "--target-commit", out.Target.CommitSHA,
				"--format", format, "--expected-plan-hash", out.PlanHash}
			if format == "manifest" {
				args = append(args, "--manifest", path)
			}
			var stdout bytes.Buffer
			if code := captureStdoutSwap(t, &stdout, func() int { return cmdProjects(args) }); code != 0 {
				t.Fatalf("export code=%d", code)
			}
			if format == "manifest" {
				var exported api.EnvironmentDefinition
				if json.Unmarshal(stdout.Bytes(), &exported) != nil || exported.Workloads["api"].Source.Image != after.Image || exported.Workloads["api"].Variables["MODE"] != "production-private" || exported.Workloads["api"].SecretRefs["TOKEN"] != "secret:PRODUCTION_TOKEN" {
					t.Fatalf("invalid manifest: %s", stdout.String())
				}
			} else if !strings.Contains(stdout.String(), out.PlanHash) || strings.Contains(stdout.String(), "production-private") || strings.Contains(stdout.String(), "secret:PRODUCTION_TOKEN") {
				t.Fatalf("invalid preview: %s", stdout.String())
			}
			if unchanged, err := os.ReadFile(path); err != nil || !bytes.Equal(raw, unchanged) {
				t.Fatal("export wrote the input manifest", err)
			}
		})
	}
}

func TestGitOpsBuiltArtifactPromotionMarkdownShowsReviewedProvenance(t *testing.T) {
	resetJSONOut(t)
	out := promotionCLIResponse(t)
	id := "018f22b4-1d51-7d55-a5d8-fdc06ec7c663"
	hash, commit := strings.Repeat("f", 64), strings.Repeat("b", 40)
	before := api.EnvironmentWorkloadSource{Kind: "source", Directory: "apps/api"}
	after := before
	after.BuildArtifactID, after.BuildArtifactHash = id, hash
	out.BlockingReasons = nil
	out.Changes = []api.EnvironmentGitPromotionChange{{Resource: "workload/api", Before: before, After: after}}
	out.Artifacts = []api.EnvironmentGitPromotionArtifact{{Resource: "workload/api", DeploymentID: "deployment", BuildID: id, Kind: "github", BuildArtifactID: id, BuildArtifactHash: hash, ArtifactDigest: "sha256:" + hash, BuildCommitSHA: commit}}
	var err error
	out, err = promotion.Finish(out, out.TargetPlan)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(out)
	authedFakeAPI(t, string(body), http.StatusOK)
	var stdout bytes.Buffer
	code := captureStdoutSwap(t, &stdout, func() int {
		return cmdProjects([]string{"environments", "gitops", "promotion-plan", "shop", "production", "--from", "staging", "--target-commit", out.Target.CommitSHA, "--format", "markdown"})
	})
	if code != 0 {
		t.Fatal("Markdown preview failed", code)
	}
	for _, value := range []string{id, hash, commit, "apps/api", "Read-only", "fresh qualification"} {
		if !strings.Contains(stdout.String(), value) {
			t.Fatal("Markdown omitted reviewed code provenance", value, stdout.String())
		}
	}
}
