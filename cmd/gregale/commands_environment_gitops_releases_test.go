package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestGitOpsRecoveryCatalogUsesReadOnlyProjectsDispatcher(t *testing.T) {
	now := time.Now().UTC()
	out := api.ListEnvironmentGitReleasesResponse{Project: "shop", Environment: "production", SourceID: "source", TargetCommitSHA: strings.Repeat("a", 40), ObservedAt: now, ReadOnly: true, ValuesRedacted: true, RequiresTargetQualification: true, Releases: []api.EnvironmentGitRecoveryRelease{{ReleaseSetID: "2ba0aa40-2362-4c0e-aeb1-6381666bb514", CreatedAt: now, RetentionStatus: "compatibility_window_elapsed", Eligibility: "blocked", BlockingReasons: []string{"rollback_source_unverified\n\x1b[2J"}}}}
	until := now.Add(time.Hour)
	out.RecoveryRetention = &api.EnvironmentRecoveryRetention{Releases: 3, Seconds: 3600, MaxBytes: 1000}
	out.RetentionBytes = 100
	out.Releases[0].ProtectionStatus = "protected"
	out.Releases[0].ArtifactsProtectedUntil = &until
	out.Releases[0].RetainedBytes = 100
	for _, mode := range []string{"text", "json-flag", "global-json"} {
		t.Run(mode, func(t *testing.T) {
			resetJSONOut(t)
			jsonOutput = mode == "global-json"
			body, _ := json.Marshal(out)
			fake := authedFakeAPI(t, string(body), http.StatusOK)
			args := []string{"environments", "gitops", "releases", "shop", "production", "--target-commit", out.TargetCommitSHA, "--before", out.Releases[0].ReleaseSetID}
			if mode == "json-flag" {
				args = append(args, "--json")
			}
			var stdout bytes.Buffer
			code := captureStdoutSwap(t, &stdout, func() int { return cmdProjects(args) })
			if code != 0 || fake.sawMethod != http.MethodGet || fake.sawPath != "/v1/projects/shop/environments/production/gitops/releases" || fake.sawQuery != "before="+out.Releases[0].ReleaseSetID+"&target_commit_sha="+out.TargetCommitSHA {
				t.Fatalf("code=%d request=%s %s output=%s", code, fake.sawMethod, fake.sawPath, stdout.String())
			}
			if mode == "text" {
				if !strings.Contains(stdout.String(), "Artifact protection: protected (100 charged bytes)") || strings.ContainsAny(stdout.String(), "\x1b\r") || !strings.Contains(stdout.String(), "rollback-plan shop production --to-release "+out.Releases[0].ReleaseSetID) || !strings.Contains(stdout.String(), "do not guarantee artifact retention") {
					t.Fatal("unsafe output", stdout.String())
				}
			} else {
				var got api.ListEnvironmentGitReleasesResponse
				if json.Unmarshal(stdout.Bytes(), &got) != nil || got.TargetCommitSHA != out.TargetCommitSHA || got.RetentionBytes != 100 || got.Releases[0].ArtifactsProtectedUntil == nil {
					t.Fatal("JSON response lost", stdout.String())
				}
			}
		})
	}
}

func TestGitOpsRecoveryCatalogRejectsInvalidTargetsAndUnverifiedResponse(t *testing.T) {
	resetJSONOut(t)
	fake := authedFakeAPI(t, `{}`, 200)
	for _, args := range [][]string{{"releases", "../other", "production", "--target-commit", strings.Repeat("a", 40)}, {"releases", "shop", "production", "--target-commit", "main"}, {"releases", "shop", "production", "--target-commit", strings.Repeat("a", 40), "--before", "wrong"}} {
		if cmdProjectsEnvironmentGitOps(args) == 0 || fake.sawMethod != "" {
			t.Fatal("invalid target reached API")
		}
	}
	var stdout bytes.Buffer
	if code := captureStdoutSwap(t, &stdout, func() int {
		return cmdProjectsEnvironmentGitOps([]string{"releases", "shop", "production", "--target-commit", strings.Repeat("a", 40)})
	}); code == 0 || stdout.Len() != 0 {
		t.Fatal("unverified response printed", code, stdout.String())
	}
}
