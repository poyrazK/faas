package journeys

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestFunctionsProjectFromGitHub deploys a functions project from its GitHub
// repository and follows it through pushes (ADR-20261108160000): connect a
// repository with two functions, check their settings came from gregale.yaml,
// push a third function, then push a gregale.yaml without one of them and
// check its app is kept and its GitHub check names the delete command.
//
// It needs a fixture repository the account's Gregale GitHub App
// installation can see, dedicated to this journey because the journey
// rewrites its gregale.yaml and functions/ on main: set
// GREGALE_E2E_GITHUB_FUNCTIONS_REPO. It drives GitHub with the `gh` CLI and
// its login. The capability is released per account, so the journey skips
// until `gregale feature list` reports repository-functions enabled.
func TestFunctionsProjectFromGitHub(t *testing.T) {
	tg := needs(t, tierFull, true)
	repo := os.Getenv("GREGALE_E2E_GITHUB_FUNCTIONS_REPO")
	if repo == "" {
		t.Skip("no functions fixture repository: set GREGALE_E2E_GITHUB_FUNCTIONS_REPO")
	}
	if _, err := exec.LookPath("gh"); err != nil {
		t.Skip("the gh CLI is not installed")
	}
	requireCapabilities(t, tg, "repository-functions")
	t.Parallel()

	project := tg.slug("fn")
	apps := map[string]string{}
	for _, name := range []string{"alpha", "beta", "gamma"} {
		// <project>-<name> is the slug the scan gives each function's app.
		apps[name] = tg.app(t, "fn-"+name)
	}
	t.Cleanup(func() {
		if tg.keep && t.Failed() {
			return
		}
		res := tg.run(t, 2*time.Minute, "project", "delete", project, "--yes")
		if res.exit != 0 && !strings.Contains(res.problem(), "not_found") {
			t.Logf("warning: cleanup could not delete project %s (%s)", project, res.problem())
		}
	})
	version := func(name string) string { return tg.runID + "-" + name }
	s := newStory(t)

	s.step("write_two_functions", func(t *testing.T) {
		for _, name := range []string{"alpha", "beta", "gamma"} {
			ghWriteFile(t, repo, "main", "functions/"+name+"/handler.js", functionsRepoHandler(version(name)),
				"e2e journeys "+tg.runID+": "+name)
		}
		ghWriteFile(t, repo, "main", "gregale.yaml", functionsRepoManifest("alpha", "beta"),
			"e2e journeys "+tg.runID+": alpha and beta")
	})

	s.step("deploy_from_repository", func(t *testing.T) {
		tg.deploySlots <- struct{}{}
		defer func() { <-tg.deploySlots }()
		res := tg.exec(t, tg.token, t.TempDir(), deployLimit+5*time.Minute, "deploy",
			"--repo", repo, "--ref", "main", "--project", "--project-slug", project,
			"--wait", "--timeout", deployLimit.String(), "--yes")
		if res.exit != 0 {
			t.Fatalf("repository deploy failed (exit %d): %s\noutput: %s", res.exit, tg.redact(res.problem()),
				tg.redact(tail(string(res.stdout), 2000)))
		}
		t.Logf("metric functions_repo_deploy_s=%d", int(res.elapsed.Seconds()))
		for _, name := range []string{"alpha", "beta"} {
			tg.ok(t, time.Minute, "app", apps[name], "--no-require-authn")
			expectVersion(t, tg.appURL(apps[name]), version(name), 5*time.Minute)
		}
	})

	s.check("settings_from_manifest", func(t *testing.T) {
		doc := object(t, tg.ok(t, time.Minute, "app", apps["alpha"]).stdout)
		if got := str(doc, "type"); got != "function" {
			t.Errorf("%s has type %q, want function", apps["alpha"], got)
		}
		if got := int(num(doc, "idle_timeout_s")); got != 45 {
			t.Errorf("%s has idle_timeout_s=%d, want 45 from gregale.yaml", apps["alpha"], got)
		}
	})

	s.step("push_adds_a_function", func(t *testing.T) {
		ghWriteFile(t, repo, "main", "gregale.yaml", functionsRepoManifest("alpha", "beta", "gamma"),
			"e2e journeys "+tg.runID+": add gamma")
		eventually(t, "the push creates "+apps["gamma"], deployLimit, 15*time.Second, func() (bool, string) {
			res := tg.run(t, time.Minute, "app", apps["gamma"])
			return res.exit == 0, res.problem()
		})
		tg.ok(t, time.Minute, "app", apps["gamma"], "--no-require-authn")
		expectVersion(t, tg.appURL(apps["gamma"]), version("gamma"), deployLimit)
	})

	s.check("push_keeps_a_removed_function", func(t *testing.T) {
		commit := ghWriteFile(t, repo, "main", "gregale.yaml", functionsRepoManifest("alpha", "gamma"),
			"e2e journeys "+tg.runID+": drop beta")
		want := "gregale app delete " + apps["beta"]
		eventually(t, "beta's check on the push names "+want, 10*time.Minute, 15*time.Second, func() (bool, string) {
			summaries := gh(t, "api", "repos/"+repo+"/commits/"+commit+"/check-runs",
				"--jq", `[.check_runs[] | "\(.name): \(.output.summary // "")"] | join(" | ")`)
			return strings.Contains(summaries, want), "checks: " + tail(summaries, 1500)
		})
		expectVersion(t, tg.appURL(apps["beta"]), version("beta"), time.Minute)
	})
}

// functionsRepoManifest lists the named functions, each in functions/<name>.
// alpha sets an idle timeout so the journey can see settings apply.
func functionsRepoManifest(names ...string) string {
	var b strings.Builder
	b.WriteString("functions:\n")
	for _, name := range names {
		fmt.Fprintf(&b, "  - name: %s\n    root: functions/%s\n", name, name)
		if name == "alpha" {
			b.WriteString("    idle_timeout_s: 45\n")
		}
	}
	return b.String()
}

func functionsRepoHandler(version string) string {
	return fmt.Sprintf("export default {\n  async fetch() {\n    return Response.json({ version: %q });\n  },\n};\n", version)
}

// ghWriteFile creates or replaces path on branch and returns the commit SHA.
func ghWriteFile(t *testing.T, repo, branch, path, content, message string) string {
	t.Helper()
	args := []string{"api", "-X", "PUT", "repos/" + repo + "/contents/" + path,
		"-f", "message=" + message, "-f", "branch=" + branch,
		"-f", "content=" + base64.StdEncoding.EncodeToString([]byte(content)), "--jq", ".commit.sha"}
	out, err := exec.Command("gh", "api", "repos/"+repo+"/contents/"+path+"?ref="+branch, "--jq", ".sha").Output()
	if sha := strings.TrimSpace(string(out)); err == nil && sha != "" {
		args = append(args, "-f", "sha="+sha)
	}
	return strings.TrimSpace(gh(t, args...))
}
