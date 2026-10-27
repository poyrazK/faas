package journeys

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestGitHubDeploys binds an app to the fixture repository (set
// GREGALE_E2E_GITHUB_REPO, e.g. poyrazK/gregale-e2e-fixture, which the
// account's Gregale GitHub App installation can see) and checks
// push-to-deploy, the Checks status on the pushed commit, source review of
// that commit, and a pull-request preview from open to close (rows 5, 16,
// 27). It drives GitHub with the `gh` CLI and its login.
//
// The fixture's main branch alternates between two fixed versions, gh-a and
// gh-b, and pull requests always carry pr-preview, so after the first runs
// every build is a build-cache hit.
func TestGitHubDeploys(t *testing.T) {
	tg := needs(t, tierFull, true)
	repo := os.Getenv("GREGALE_E2E_GITHUB_REPO")
	if repo == "" {
		t.Skip("no fixture repository: set GREGALE_E2E_GITHUB_REPO (scripts/e2e-journeys.sh --github-repo)")
	}
	if _, err := exec.LookPath("gh"); err != nil {
		t.Skip("the gh CLI is not installed")
	}
	t.Parallel()
	slug, base := tg.deployProbe(t, "github")
	s := newStory(t)

	s.step("bind", func(t *testing.T) {
		// Branch-to-scope mappings need a project-backed app; a plain app
		// deploys its production branch.
		tg.ok(t, 2*time.Minute, "github", "bind", slug, "--repo", repo, "--branch", "main")
		eventually(t, "the binding is healthy", 2*time.Minute, 5*time.Second, func() (bool, string) {
			res := tg.run(t, time.Minute, "github", "status", slug)
			return res.exit == 0 && strings.Contains(string(res.stdout), repo), res.problem() + tail(string(res.stdout), 300)
		})
	})

	var pushed, commit string
	// A check, not a step: source review and PR previews only need the
	// pushed commit, not its deployment.
	s.check("push_to_deploy", func(t *testing.T) {
		current := strings.TrimSpace(ghFileContent(t, repo, "main", "version.txt"))
		pushed = "gh-a"
		if current == "gh-a" {
			pushed = "gh-b"
		}
		commit = ghPutFile(t, repo, "main", "version.txt", pushed+"\n", "e2e journeys run "+tg.runID)
		t.Logf("pushed %s to %s main as %s", pushed, repo, commit)
		defer func() {
			if t.Failed() {
				// Say whether the push created a deployment and how it ended.
				res := tg.run(t, time.Minute, "deployments", "--app", slug, "--limit", "3")
				t.Logf("deployments after the push: %s", tail(string(res.stdout), 1500))
				status := tg.run(t, time.Minute, "github", "status", slug)
				t.Logf("github status: %s", tail(string(status.stdout), 800))
			}
		}()
		expectVersion(t, base, pushed, 25*time.Minute)
	})
	s.check("checks_status", func(t *testing.T) {
		eventually(t, "a successful Gregale check on the pushed commit", 10*time.Minute, 15*time.Second, func() (bool, string) {
			runs := gh(t, "api", "repos/"+repo+"/commits/"+commit+"/check-runs",
				"--jq", `[.check_runs[] | "\(.app.slug)|\(.name)|\(.status)|\(.conclusion)"] | join(";")`)
			statuses := gh(t, "api", "repos/"+repo+"/commits/"+commit+"/status",
				"--jq", `[.statuses[] | "\(.context)|\(.state)"] | join(";")`)
			all := strings.ToLower(runs + ";" + statuses)
			ok := strings.Contains(all, "gregale") && (strings.Contains(all, "|success") || strings.Contains(all, "completed|success"))
			return ok, "checks: " + runs + " statuses: " + statuses
		})
	})
	s.check("source_review", func(t *testing.T) {
		key := tg.scopedKey(t, "review")
		r := tg.apiCall(http.MethodPost, "/v1/apps/"+slug+"/source-review", map[string]string{"repo": repo, "ref": commit}, key)
		if r.status != http.StatusOK {
			t.Fatalf("source review of %s@%s: %s", repo, commit, r)
		}
		if got := str(jsonBody(t, r), "commit_sha"); got != commit {
			t.Fatalf("source review pinned %s, want %s", got, commit)
		}
	})
	autoPreview := false
	s.check("pull_request_preview", func(t *testing.T) {
		// What production serves before the PR; the preview must not change it.
		before := str(jsonBody(t, get(base+"/")), "version")
		branch := "e2ej-" + tg.runID
		gh(t, "api", "repos/"+repo+"/git/refs", "-f", "ref=refs/heads/"+branch, "-f", "sha="+commit)
		t.Cleanup(func() { ghQuiet("api", "-X", "DELETE", "repos/"+repo+"/git/refs/heads/"+branch) })
		ghPutFile(t, repo, branch, "version.txt", "pr-preview\n", "e2e journeys preview "+tg.runID)
		prURL := strings.TrimSpace(gh(t, "pr", "create", "--repo", repo, "--head", branch, "--base", "main",
			"--title", "e2e journeys preview "+tg.runID, "--body", "Opened and closed by the Gregale journey suite."))
		number, err := strconv.Atoi(prURL[strings.LastIndex(prURL, "/")+1:])
		if err != nil {
			t.Fatalf("gh pr create printed %q", prURL)
		}
		t.Cleanup(func() { ghQuiet("pr", "close", strconv.Itoa(number), "--repo", repo) })
		preview := fmt.Sprintf("pr-%d-%s", number, slug)

		// The GitHub App should create the preview on its own; create it
		// explicitly if it has not appeared, so the serving check still runs.
		appeared := false
		deadline := time.Now().Add(5 * time.Minute)
		for time.Now().Before(deadline) {
			if res := tg.run(t, time.Minute, "preview", "list", "--app", slug); strings.Contains(string(res.stdout), preview) {
				appeared = true
				break
			}
			time.Sleep(15 * time.Second)
		}
		autoPreview = appeared
		if !appeared {
			t.Logf("opening PR #%d created no preview within 5 minutes; creating it with the CLI", number)
			tg.ok(t, 25*time.Minute, "preview", "create", "--app", slug, "--repo", repo, "--ref", branch,
				"--pr-number", strconv.Itoa(number), "--wait")
		}
		tg.ok(t, 25*time.Minute, "preview", "wait", preview, "--timeout", "20m")
		expectVersion(t, tg.appURL(preview), "pr-preview", 5*time.Minute)
		expectVersion(t, base, before, time.Minute)

		gh(t, "pr", "close", strconv.Itoa(number), "--repo", repo)
		eventually(t, "closing the PR removes its preview", 10*time.Minute, 15*time.Second, func() (bool, string) {
			res := tg.run(t, time.Minute, "preview", "list", "--app", slug)
			return res.exit == 0 && !strings.Contains(string(res.stdout), preview), "still listed: " + tail(string(res.stdout), 200)
		})
	})
	// Separate from the lifecycle above so the GitHub App's automatic
	// preview is judged on its own (docs/preview-environments.md: every PR
	// against a connected repository gets one, no CI needed).
	s.check("preview_created_on_pr_open", func(t *testing.T) {
		if !autoPreview {
			t.Fatalf("opening a pull request created no preview within 5 minutes")
		}
	})
}

// gh runs the GitHub CLI and fails the test on error.
func gh(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "gh", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("gh %s: %v: %s", strings.Join(args, " "), err, tail(string(out), 500))
	}
	return string(out)
}

// ghQuiet runs the GitHub CLI for cleanup, ignoring errors.
func ghQuiet(args ...string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_ = exec.CommandContext(ctx, "gh", args...).Run()
}

func ghFileContent(t *testing.T, repo, ref, path string) string {
	t.Helper()
	encoded := gh(t, "api", "repos/"+repo+"/contents/"+path+"?ref="+ref, "--jq", ".content")
	b, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(strings.TrimSpace(encoded), "\n", ""))
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return string(b)
}

// ghPutFile commits content to path on branch and returns the commit SHA.
func ghPutFile(t *testing.T, repo, branch, path, content, message string) string {
	t.Helper()
	sha := strings.TrimSpace(gh(t, "api", "repos/"+repo+"/contents/"+path+"?ref="+branch, "--jq", ".sha"))
	return strings.TrimSpace(gh(t, "api", "-X", "PUT", "repos/"+repo+"/contents/"+path,
		"-f", "message="+message, "-f", "branch="+branch, "-f", "sha="+sha,
		"-f", "content="+base64.StdEncoding.EncodeToString([]byte(content)), "--jq", ".commit.sha"))
}
