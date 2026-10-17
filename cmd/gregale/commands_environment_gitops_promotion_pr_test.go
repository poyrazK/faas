package main

import (
	"bytes"
	"crypto/sha1" // #nosec G505 -- Git blob object format, not an authorization digest.
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

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops/promotion"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

func promotionPRCLIFixture(t *testing.T) (api.PreviewEnvironmentGitPromotionResponse, api.EnvironmentGitSource, []byte) {
	t.Helper()
	before := api.EnvironmentWorkloadSource{Kind: "image", Image: "registry.example/api@sha256:" + strings.Repeat("1", 64)}
	after := before
	after.Image = "registry.example/api@sha256:" + strings.Repeat("2", 64)
	definition := api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: "shop", Environment: "production", Workloads: map[string]api.EnvironmentWorkload{"api": {Source: &before, Variables: map[string]string{"MODE": "production-private"}}}}
	base, err := environmentsync.Compile(definition)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(base.Definition)
	w := definition.Workloads["api"]
	w.Source = &after
	definition.Workloads["api"] = w
	proposed, err := environmentsync.Compile(definition)
	if err != nil {
		t.Fatal(err)
	}
	out := promotionCLIResponse(t)
	out.BlockingReasons = nil
	out.Origin.Provider, out.Origin.RepositoryID, out.Origin.ReleaseSetID = "github", 42, "2ba0aa40-2362-4c0e-aeb1-6381666bb514"
	out.Target.DefinitionDigest, out.Target.ProposedDigest, out.Target.ManifestPath = base.Digest, proposed.Digest, "production.yaml"
	out.Changes = []api.EnvironmentGitPromotionChange{{Resource: "workload/api", Before: before, After: after}}
	out.TargetPlan.DesiredDigest = proposed.Digest
	out, err = promotion.Finish(out, out.TargetPlan)
	if err != nil {
		t.Fatal(err)
	}
	source := api.EnvironmentGitSource{ID: out.Target.SourceID, EnvironmentSlug: "production", Spec: api.EnvironmentGitSourceSpec{Provider: "github", Repository: "shop/platform", RepositoryID: 42, InstallationID: 12, Ref: "refs/heads/main", ManifestPath: out.Target.ManifestPath, Mode: "report", ApprovalPolicy: "manual"}}
	return out, source, raw
}

func TestProjectsGitOpsPromotionPRDispatcherTextAndJSON(t *testing.T) {
	for _, mode := range []string{"text", "json-flag", "global-json"} {
		t.Run(mode, func(t *testing.T) {
			resetJSONOut(t)
			if mode == "global-json" {
				jsonOutput = true
			}
			plan, source, raw := promotionPRCLIFixture(t)
			dir := t.TempDir()
			// Fake only gh. Gregale's own planner/status requests go through HTTP.
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			script := "#!/bin/sh\nexec \"$GREG_PROMOTION_TEST_EXE\" -test.run=TestGitOpsPromotionProviderProcess -- \"$@\"\n"
			if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "manifest"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+":/bin:/usr/bin")
			t.Setenv("GREG_PROMOTION_TEST_EXE", executable)
			t.Setenv("GREG_PROMOTION_TEST_DIR", dir)
			t.Setenv("FAAS_TOKEN", "test-token")
			t.Setenv("HOME", dir)
			t.Setenv("XDG_CONFIG_HOME", dir)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("missing auth")
				}
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/promotion-plan"):
					var request api.PreviewEnvironmentGitPromotionRequest
					if json.NewDecoder(r.Body).Decode(&request) != nil || request.ExpectedPlanHash != plan.PlanHash {
						t.Error("missing reviewed hash")
					}
					_ = json.NewEncoder(w).Encode(plan)
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/gitops"):
					_ = json.NewEncoder(w).Encode(api.EnvironmentGitOpsStatusResponse{Source: source})
				default:
					t.Error("unexpected API mutation", r.Method, r.URL.Path)
					http.Error(w, "unexpected", 400)
				}
			}))
			defer server.Close()
			t.Setenv("FAAS_API", server.URL)
			args := []string{"environments", "gitops", "promotion-pr", "shop", "production", "--from", "staging", "--target-commit", plan.Target.CommitSHA, "--expected-plan-hash", plan.PlanHash}
			if mode == "json-flag" {
				args = append(args, "--json")
			}
			var stdout bytes.Buffer
			code := captureStdoutSwap(t, &stdout, func() int { return cmdProjects(args) })
			if code != 0 || requests.Load() != 2 {
				t.Fatal(code, requests.Load(), stdout.String())
			}
			if mode != "text" {
				var result struct {
					Status                      string `json:"status"`
					PlanHash                    string `json:"plan_hash"`
					RequiresTargetQualification bool   `json:"requires_target_qualification"`
				}
				if json.Unmarshal(stdout.Bytes(), &result) != nil || result.Status != "preview" || result.PlanHash != plan.PlanHash || !result.RequiresTargetQualification {
					t.Fatal(stdout.String())
				}
			} else if !strings.Contains(stdout.String(), "Use --yes") {
				t.Fatal(stdout.String())
			}
			if strings.Contains(stdout.String(), "production-private") {
				t.Fatal("manifest values leaked")
			}
		})
	}
}

// Invoked only as a provider-CLI subprocess of the dispatcher test above.
func TestGitOpsPromotionProviderProcess(t *testing.T) {
	dir := os.Getenv("GREG_PROMOTION_TEST_DIR")
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
	endpoint := args[len(args)-1]
	if !strings.Contains(strings.Join(args, " "), "--method GET") || !strings.Contains(strings.Join(args, " "), "--hostname github.com") {
		os.Exit(4)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "manifest"))
	// This is the known Git blob hash, calculated independently with Git's format.
	// The production adapter will verify it before parsing the manifest.
	blob := promotionTestBlob(raw)
	sha, tree := strings.Repeat("a", 40), strings.Repeat("d", 40)
	status := 200
	var body any
	switch {
	case endpoint == "repos/shop/platform":
		body = map[string]any{"id": 42, "full_name": "shop/platform"}
	case strings.HasSuffix(endpoint, "/git/ref/heads/main"):
		body = map[string]any{"ref": "refs/heads/main", "object": map[string]string{"sha": sha, "type": "commit"}}
	case strings.Contains(endpoint, "/git/ref/heads/gregale-promote-"):
		status = 404
		body = map[string]string{"message": "missing"}
	case strings.HasSuffix(endpoint, "/git/commits/"+sha):
		body = map[string]any{"sha": sha, "tree": map[string]string{"sha": tree}}
	case strings.HasSuffix(endpoint, "/git/trees/"+tree):
		body = map[string]any{"sha": tree, "tree": []map[string]string{{"path": "production.yaml", "mode": "100644", "type": "blob", "sha": blob}}}
	case strings.HasSuffix(endpoint, "/git/blobs/"+blob):
		body = map[string]string{"sha": blob, "encoding": "base64", "content": base64.StdEncoding.EncodeToString(raw)}
	case strings.Contains(endpoint, "/pulls?"):
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

func TestProjectsGitOpsPromotionPRRejectsInvalidFlagsBeforeRequests(t *testing.T) {
	for _, extra := range [][]string{nil, {"--expected-plan-hash", "bad"}, {"--expected-plan-hash", strings.Repeat("a", 64), "--repository", "other/repo"}} {
		t.Run(fmt.Sprint(extra), func(t *testing.T) {
			resetJSONOut(t)
			fake := authedFakeAPI(t, `{}`, 200)
			args := append([]string{"environments", "gitops", "promotion-pr", "shop", "production", "--from", "staging", "--target-commit", strings.Repeat("a", 40)}, extra...)
			if code := cmdProjects(args); code == 0 || fake.sawMethod != "" {
				t.Fatal("invalid input reached API", code)
			}
		})
	}
}

// adr: 1550, 1532 — review commands consume exactly one project/environment pair.
func TestGitOpsReviewPRRejectsUnexpectedPositionalsBeforeRequests(t *testing.T) {
	for _, command := range []string{"promotion-pr", "rollback-pr"} {
		for _, positional := range [][]string{nil, {"shop"}, {"shop", "production", "unexpected"}} {
			t.Run(command+fmt.Sprint(positional), func(t *testing.T) {
				resetJSONOut(t)
				fake := authedFakeAPI(t, `{}`, 200)
				args := append([]string{"environments", "gitops", command}, positional...)
				args = append(args, "--target-commit", strings.Repeat("a", 40), "--expected-plan-hash", strings.Repeat("b", 64), "--yes")
				if command == "promotion-pr" {
					args = append(args, "--from", "staging")
				} else {
					args = append(args, "--to-release", "2ba0aa40-2362-4c0e-aeb1-6381666bb514")
				}
				if code := cmdProjects(args); code == 0 || fake.sawMethod != "" {
					t.Fatal("invalid positional count reached platform API", code, fake.sawMethod)
				}
			})
		}
	}
}

// Kept separate from the adapter to ensure provider content identity is checked.
func promotionTestBlob(raw []byte) string {
	return fmt.Sprintf("%x", sha1.Sum(append([]byte(fmt.Sprintf("blob %d\x00", len(raw))), raw...))) //nolint:gosec // Git object identity.
}
