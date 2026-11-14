package journeys

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestEnvironmentBranchPRPreviews previews a pull request as an environment
// branch of production (ADR-20261113121342, docs/environment-branches.md
// "Pull request previews"), through the GitHub App on the fixture
// repository:
//   - a project deployed from a base branch of the fixture, with previews in
//     branch mode;
//   - opening a pull request makes branch pr-<N>, which serves the pull
//     request's code while production keeps serving the base;
//   - a push to the pull request reaches the same branch URL, and rebuilds
//     only the service whose files changed (slice 2);
//   - closing the pull request starts the branch's 24 hour grace.
//
// It needs the fixture repository (GREGALE_E2E_GITHUB_REPO) and skips while
// the target has environment branches or branch previews off.
func TestEnvironmentBranchPRPreviews(t *testing.T) {
	tg := needs(t, tierFull, true)
	repo := os.Getenv("GREGALE_E2E_GITHUB_REPO")
	if repo == "" {
		t.Skip("no fixture repository: set GREGALE_E2E_GITHUB_REPO (scripts/e2e-journeys.sh --github-repo)")
	}
	for _, tool := range []string{"gh", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("the %s CLI is not installed", tool)
		}
	}
	t.Parallel()
	project := tg.slug("brpr")
	switch res := tg.runWithEnv(t, branchesCLI, time.Minute, "environment", "branch", project, "--list"); {
	case res.problemCode() == "environment_branch_unavailable":
		t.Skip("environment branches are off on this target")
	case res.exit != 0 && res.problemCode() == "":
		t.Skipf("this CLI cannot branch environments: %s", tg.redact(tail(res.problem(), 300)))
	}
	app, api := tg.app(t, "brpr"), tg.app(t, "brprapi")
	base, head := "e2ej-brpr-base-"+tg.runID, "e2ej-brpr-head-"+tg.runID
	var number int
	environment := func() string { return "pr-" + strconv.Itoa(number) }
	t.Cleanup(func() {
		if number > 0 {
			ghQuiet("pr", "close", strconv.Itoa(number), "--repo", repo)
		}
		ghQuiet("api", "-X", "DELETE", "repos/"+repo+"/git/refs/heads/"+head)
		ghQuiet("api", "-X", "DELETE", "repos/"+repo+"/git/refs/heads/"+base)
		if tg.keep && t.Failed() {
			return
		}
		if number > 0 {
			tg.runWithEnv(t, branchesCLI, 2*time.Minute, "environment", "delete", project, environment(), "--yes")
		}
		res := tg.run(t, 2*time.Minute, "project", "delete", project, "--yes")
		if res.exit != 0 && !strings.Contains(res.problem(), "not_found") {
			t.Logf("warning: cleanup could not delete project %s (%s)", project, res.problem())
		}
	})
	production := tg.appURL(app)
	var branchURL string
	s := newStory(t)

	s.step("base_branch", func(t *testing.T) {
		// The base branch holds the probe as a two-service project: web calls
		// api, so a preview of web brings api along, each in its own
		// directory so a change to one leaves the other's build alone.
		files := map[string]string{
			"compose.yaml": "services:\n  " + app + ":\n    build: ./web\n    ports: [\"8080:8080\"]\n    depends_on: [" + api + "]\n" +
				"  " + api + ":\n    build: ./api\n    ports: [\"8080:8080\"]\n",
			"web/version.txt": "brpr-base\n",
			"api/version.txt": "brpr-api\n",
		}
		src := filepath.Join("testdata", "probe-node")
		entries, err := os.ReadDir(src)
		if err != nil {
			t.Fatalf("read probe source: %v", err)
		}
		for _, e := range entries {
			b, err := os.ReadFile(filepath.Join(src, e.Name()))
			if err != nil {
				t.Fatalf("read probe source: %v", err)
			}
			files["web/"+e.Name()], files["api/"+e.Name()] = string(b), string(b)
		}
		ghBranchWithFiles(t, repo, "main", base, files, "e2e journeys branch previews "+tg.runID)
	})

	s.step("deploy_project_from_base", func(t *testing.T) {
		tg.deploySlots <- struct{}{}
		defer func() { <-tg.deploySlots }()
		res := tg.exec(t, tg.token, t.TempDir(), deployLimit+5*time.Minute, "deploy",
			"--repo", repo, "--ref", base, "--project", "--project-slug", project,
			"--wait", "--timeout", deployLimit.String(), "--yes")
		if res.exit != 0 {
			t.Fatalf("repository deploy failed (exit %d): %s\noutput: %s", res.exit, tg.redact(res.problem()),
				tg.redact(tail(string(res.stdout), 2000)))
		}
		for _, slug := range []string{app, api} {
			tg.ok(t, time.Minute, "app", slug, "--no-require-authn")
		}
		expectVersion(t, production, "brpr-base", 5*time.Minute)
	})

	s.step("branch_previews_on", func(t *testing.T) {
		// github setup binds the repository and sets the project's policy; it
		// writes its workflow into a scratch Git repository.
		dir := t.TempDir()
		if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v: %s", err, out)
		}
		res := tg.exec(t, tg.token, dir, 3*time.Minute, "github", "setup", app, "--repo", repo,
			"--production-branch", base, "--preview", "--preview-mode", "branch")
		if res.problemCode() == "environment_branch_unavailable" {
			t.Skip("branch previews are off on this target")
		}
		if res.exit != 0 {
			t.Fatalf("github setup failed (exit %d): %s\noutput: %s", res.exit, tg.redact(res.problem()),
				tg.redact(tail(string(res.stdout), 2000)))
		}
	})

	s.step("open_pull_request", func(t *testing.T) {
		commit := strings.TrimSpace(gh(t, "api", "repos/"+repo+"/git/ref/heads/"+base, "--jq", ".object.sha"))
		gh(t, "api", "repos/"+repo+"/git/refs", "-f", "ref=refs/heads/"+head, "-f", "sha="+commit)
		ghPutFile(t, repo, head, "web/version.txt", "brpr-one\n", "e2e journeys branch preview "+tg.runID)
		prURL := strings.TrimSpace(gh(t, "pr", "create", "--repo", repo, "--head", head, "--base", base,
			"--title", "e2e journeys branch preview "+tg.runID, "--body", "Opened and closed by the Gregale journey suite."))
		n, err := strconv.Atoi(prURL[strings.LastIndex(prURL, "/")+1:])
		if err != nil {
			t.Fatalf("gh pr create printed %q", prURL)
		}
		number = n
	})

	s.step("branch_serves_the_pull_request", func(t *testing.T) {
		eventually(t, "the GitHub App makes "+environment(), 5*time.Minute, 10*time.Second, func() (bool, string) {
			res := tg.runWithEnv(t, branchesCLI, time.Minute, "environment", "branch", project, environment(), "--status")
			if res.exit != 0 {
				return false, res.problem()
			}
			doc := object(t, res.stdout)
			branchURL = strings.TrimSuffix(branchWorkload(doc, app, "url"), "/")
			return str(doc, "branch.parent") == "production" && branchURL != "", compact(doc)
		})
		if branchURL == production {
			t.Fatalf("the branch serves at production's URL %s", branchURL)
		}
		expectVersion(t, branchURL, "brpr-one", 15*time.Minute)
		expectVersion(t, production, "brpr-base", time.Minute)
	})

	status := func(t *testing.T) map[string]any {
		t.Helper()
		return object(t, tg.runWithEnvOK(t, branchesCLI, time.Minute, "environment", "branch", project, environment(), "--status").stdout)
	}
	s.step("push_rebuilds_only_the_changed_service", func(t *testing.T) {
		// The first push built both services into the branch; api's build
		// must be live before the second push can keep it.
		var apiDeployment string
		eventually(t, "api's branch build is live", 15*time.Minute, 10*time.Second, func() (bool, string) {
			doc := status(t)
			apiDeployment = branchWorkload(doc, api, "deployment_id")
			return branchWorkload(doc, api, "status") == "live" && apiDeployment != "", compact(doc)
		})
		ghPutFile(t, repo, head, "web/version.txt", "brpr-two\n", "e2e journeys branch preview push "+tg.runID)
		expectVersion(t, branchURL, "brpr-two", 15*time.Minute)
		expectVersion(t, production, "brpr-base", time.Minute)
		if got := branchWorkload(status(t), api, "deployment_id"); got != apiDeployment {
			t.Fatalf("a web-only push replaced api's branch deployment %s with %s; want it kept", apiDeployment, got)
		}
	})

	s.step("close_starts_the_grace", func(t *testing.T) {
		gh(t, "pr", "close", strconv.Itoa(number), "--repo", repo)
		eventually(t, environment()+" expires within the grace", 3*time.Minute, 10*time.Second, func() (bool, string) {
			doc := status(t)
			expires, err := time.Parse(time.RFC3339, str(doc, "branch.expires_at"))
			return err == nil && expires.Before(time.Now().Add(25*time.Hour)), compact(doc)
		})
	})
}

// branchWorkload is one field of the branch status's workload named slug,
// or "".
func branchWorkload(doc map[string]any, slug, key string) string {
	for _, workload := range asSlice(field(doc, "branch.workloads")) {
		if str(workload, "workload") == slug {
			return str(workload, key)
		}
	}
	return ""
}
