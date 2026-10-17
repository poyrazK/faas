package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops/rollback"
)

func rollbackPRCLIFixture(t *testing.T, provider string) (api.PreviewEnvironmentGitRollbackResponse, api.EnvironmentGitSource, []byte) {
	t.Helper()
	p, source, raw := promotionPRCLIFixture(t)
	origin := p.Origin
	origin.SourceID, origin.GraphID, origin.CommitSHA, origin.Provider = source.ID, "served-graph", strings.Repeat("b", 40), provider
	served := time.Unix(1700000000, 0).UTC()
	plan := api.PreviewEnvironmentGitRollbackResponse{Project: p.Project, Environment: p.ToEnvironment, ReadOnly: true, ValuesRedacted: true, RequiresTargetQualification: true,
		Origin: origin, OriginServedAt: &served, CurrentReleaseSetID: "3ba0aa40-2362-4c0e-aeb1-6381666bb514", Target: p.Target, Changes: p.Changes, Artifacts: p.Artifacts}
	var err error
	plan, err = rollback.Finish(plan, p.TargetPlan)
	if err != nil {
		t.Fatal(err)
	}
	source.Spec.Provider = provider
	if provider == "gitlab" {
		source.Spec.InstallationID = 0
	}
	return plan, source, raw
}

func TestProjectsGitOpsRollbackPRDispatcherTextJSONAndFreshRecheck(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		for _, mode := range []string{"text", "json-flag", "global-json", "stale-before-write", "other-release"} {
			t.Run(provider+"/"+mode, func(t *testing.T) {
				resetJSONOut(t)
				if mode == "global-json" {
					jsonOutput = true
				}
				plan, source, raw := rollbackPRCLIFixture(t, provider)
				expectedHash, selectedRelease := plan.PlanHash, plan.Origin.ReleaseSetID
				if mode == "other-release" {
					plan.Origin.ReleaseSetID = plan.CurrentReleaseSetID
					var err error
					plan, err = rollback.Finish(plan, plan.TargetPlan)
					if err != nil {
						t.Fatal(err)
					}
				}
				dir := t.TempDir()
				executable, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				name := "gh"
				if provider == "gitlab" {
					name = "glab"
				}
				script := "#!/bin/sh\nexec \"$GREG_ROLLBACK_TEST_EXE\" -test.run=TestGitOpsRollbackProviderProcess -- \"$@\"\n"
				if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "manifest"), raw, 0600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", dir+":/bin:/usr/bin")
				t.Setenv("GREG_ROLLBACK_TEST_EXE", executable)
				t.Setenv("GREG_ROLLBACK_TEST_DIR", dir)
				t.Setenv("GREG_ROLLBACK_TEST_PROVIDER", provider)
				t.Setenv("FAAS_TOKEN", "test-token")
				t.Setenv("HOME", dir)
				t.Setenv("XDG_CONFIG_HOME", dir)
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					count := requests.Add(1)
					if r.Header.Get("Authorization") != "Bearer test-token" {
						t.Error("missing auth")
					}
					w.Header().Set("Content-Type", "application/json")
					switch {
					case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/rollback-plan"):
						var request api.PreviewEnvironmentGitRollbackRequest
						if json.NewDecoder(r.Body).Decode(&request) != nil || request.ReleaseSetID != selectedRelease || request.ExpectedPlanHash != expectedHash || request.TargetCommitSHA != plan.Target.CommitSHA {
							t.Error("lost explicit rollback selection")
						}
						if mode == "stale-before-write" && count > 2 {
							api.WriteProblem(w, api.NewProblem(http.StatusConflict, "environment_gitops_stale_rollback_plan", "Recovery observation changed", "Select and review a fresh rollback plan."))
							return
						}
						_ = json.NewEncoder(w).Encode(plan)
					case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/gitops"):
						_ = json.NewEncoder(w).Encode(api.EnvironmentGitOpsStatusResponse{Source: source})
					default:
						t.Error("unexpected platform write", r.Method, r.URL.Path)
						http.Error(w, "unexpected", 400)
					}
				}))
				defer server.Close()
				t.Setenv("FAAS_API", server.URL)
				args := []string{"environments", "gitops", "rollback-pr", "shop", "production", "--to-release", selectedRelease, "--target-commit", plan.Target.CommitSHA, "--expected-plan-hash", expectedHash}
				if mode == "json-flag" {
					args = append(args, "--json")
				}
				if mode == "stale-before-write" {
					args = append(args, "--yes")
				}
				var stdout bytes.Buffer
				code := captureStdoutSwap(t, &stdout, func() int { return cmdProjects(args) })
				if mode == "stale-before-write" || mode == "other-release" {
					want := int32(3)
					if mode == "other-release" {
						want = 1
					}
					if code == 0 || requests.Load() != want {
						t.Fatal(code, requests.Load(), stdout.String())
					}
					if _, err := os.Stat(filepath.Join(dir, "unexpected-write")); !os.IsNotExist(err) {
						t.Fatal("stale recovery reached provider write")
					}
					return
				}
				if code != 0 || requests.Load() != 2 {
					t.Fatal(code, requests.Load(), stdout.String())
				}
				if mode != "text" {
					var result struct {
						Status                      string `json:"status"`
						PlanHash                    string `json:"plan_hash"`
						Release                     string `json:"release_set_id"`
						RequiresTargetQualification bool   `json:"requires_target_qualification"`
					}
					if json.Unmarshal(stdout.Bytes(), &result) != nil || result.Status != "preview" || result.Release != selectedRelease || result.PlanHash != expectedHash || !result.RequiresTargetQualification {
						t.Fatal(stdout.String())
					}
				} else if !strings.Contains(stdout.String(), "Use --yes") || !strings.Contains(stdout.String(), selectedRelease) || !strings.Contains(stdout.String(), "does not restore data or replay completed Jobs") {
					t.Fatal(stdout.String())
				}
				if strings.Contains(stdout.String(), "production-private") {
					t.Fatal("target value leaked")
				}
			})
		}
	}
}

// Provider fixtures run in child processes and never use real credentials/network.
func TestGitOpsRollbackProviderProcess(t *testing.T) {
	dir := os.Getenv("GREG_ROLLBACK_TEST_DIR")
	if dir == "" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) < 2 {
		os.Exit(3)
	}
	provider := os.Getenv("GREG_ROLLBACK_TEST_PROVIDER")
	host := "github.com"
	if provider == "gitlab" {
		host = "gitlab.com"
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--method GET") {
		_ = os.WriteFile(filepath.Join(dir, "unexpected-write"), []byte("provider mutation attempted"), 0600)
		os.Exit(4)
	}
	if !strings.Contains(joined, "--hostname "+host) {
		os.Exit(4)
	}
	endpoint := args[len(args)-1]
	raw, _ := os.ReadFile(filepath.Join(dir, "manifest"))
	blob := promotionTestBlob(raw)
	sha, tree := strings.Repeat("a", 40), strings.Repeat("d", 40)
	status := 200
	var body any
	switch {
	case endpoint == "repos/shop/platform" || endpoint == "projects/shop%2Fplatform":
		body = map[string]any{"id": 42, "full_name": "shop/platform", "path_with_namespace": "shop/platform"}
	case strings.Contains(endpoint, "gregale-rollback-") && !strings.Contains(endpoint, "?"):
		status = 404
		body = map[string]string{"message": "missing"}
	case strings.HasSuffix(endpoint, "/git/ref/heads/main"):
		body = map[string]any{"ref": "refs/heads/main", "object": map[string]string{"sha": sha, "type": "commit"}}
	case strings.HasSuffix(endpoint, "/repository/branches/main"):
		body = map[string]any{"name": "main", "commit": map[string]string{"id": sha}}
	case strings.HasSuffix(endpoint, "/git/commits/"+sha):
		body = map[string]any{"sha": sha, "tree": map[string]string{"sha": tree}}
	case strings.HasSuffix(endpoint, "/git/trees/"+tree):
		body = map[string]any{"sha": tree, "tree": []map[string]string{{"path": "production.yaml", "mode": "100644", "type": "blob", "sha": blob}}}
	case strings.HasSuffix(endpoint, "/git/blobs/"+blob):
		body = map[string]string{"sha": blob, "encoding": "base64", "content": base64.StdEncoding.EncodeToString(raw)}
	case strings.Contains(endpoint, "/repository/files/production.yaml?"):
		body = map[string]string{"file_path": "production.yaml", "blob_id": blob, "encoding": "base64", "content": base64.StdEncoding.EncodeToString(raw), "commit_id": sha, "last_commit_id": sha}
	case strings.Contains(endpoint, "/repository/tree?"):
		body = []map[string]string{{"path": "production.yaml", "type": "blob", "mode": "100644", "id": blob}}
	case strings.Contains(endpoint, "/pulls?") || strings.Contains(endpoint, "/merge_requests?"):
		body = []any{}
	default:
		os.Exit(5)
	}
	fmt.Printf("HTTP/2.0 %d response\nContent-Type: application/json\n\n", status)
	_ = json.NewEncoder(os.Stdout).Encode(body)
	if status == 404 {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestProjectsGitOpsRollbackPRRejectsInvalidFlagsBeforeRequests(t *testing.T) {
	for _, mode := range []string{"release", "noncanonical-release", "hash", "commit", "repository-override"} {
		t.Run(mode, func(t *testing.T) {
			resetJSONOut(t)
			fake := authedFakeAPI(t, `{}`, 200)
			release, hash, commit := "2ba0aa40-2362-4c0e-aeb1-6381666bb514", strings.Repeat("b", 64), strings.Repeat("a", 40)
			switch mode {
			case "release":
				release = "latest"
			case "noncanonical-release":
				release = strings.ToUpper(release)
			case "hash":
				hash = ""
			case "commit":
				commit = "main"
			}
			args := []string{"environments", "gitops", "rollback-pr", "shop", "production", "--to-release", release, "--target-commit", commit, "--expected-plan-hash", hash, "--yes"}
			if mode == "repository-override" {
				args = append(args, "--repository", "other/repo")
			}
			if code := cmdProjects(args); code == 0 || fake.sawMethod != "" {
				t.Fatal("invalid input reached platform API", code)
			}
		})
	}
}
