package main_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"gopkg.in/yaml.v3"
)

// The generated shell script invokes the real compare command through this
// subprocess, keeping the workflow test independent of a released CLI binary.
func TestCICompareSubprocess(t *testing.T) {
	if os.Getenv("GREGALE_CI_TEST_SUBPROCESS") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" && i+2 < len(os.Args) && os.Args[i+1] == "test" {
			os.Exit(main.CmdTest(os.Args[i+2:]))
		}
	}
	os.Exit(2)
}

func TestCIWorkflowComparesFailedSuiteReports(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	main.CaptureSuiteTestOutput(t)
	manifest := main.WriteSuiteTestManifest(t, root, main.TestManifest{Version: 1,
		Scenarios: map[string]main.TestScenario{"audit": {Project: "audit-app", Source: ".", Command: []string{"true"}, Simulation: []string{"sh", "-c", "exit ${GREGALE_CI_TEST_FAILURE:-0}"}}},
		Suites:    map[string]main.TestSuite{"smoke": {Scenarios: []main.TestSuiteMember{{Scenario: "audit", Engine: "simulated"}}}},
	})
	if code, problem := main.RunWithStderr(t, func() int {
		return main.CmdTest([]string{"ci", "init", "--manifest", manifest, "--suite", "smoke", "--engine", "simulated"})
	}); code != 0 {
		t.Fatalf("generate: %d %s", code, problem)
	}
	workflow, err := os.ReadFile(filepath.Join(root, main.DefaultTestCIWorkflow))
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `yaml:"name"`
				If   string `yaml:"if"`
				Run  string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(workflow, &parsed); err != nil {
		t.Fatal(err)
	}
	var condition, script string
	for _, step := range parsed.Jobs["scenarios"].Steps {
		if step.Name == "Compare with branch baseline" {
			condition, script = step.If, step.Run
		}
	}
	if condition != "!cancelled() && github.event_name == 'pull_request'" || script == "" {
		t.Fatalf("comparison step: condition %q", condition)
	}
	baseline := filepath.Join(root, "baseline.json")
	t.Setenv("GREGALE_CI_TEST_FAILURE", "0")
	if code := main.CmdTest([]string{"--manifest", manifest, "--suite", "smoke", "--report", baseline}); code != 0 {
		t.Fatalf("baseline: %d", code)
	}
	runner := filepath.Join(root, "runner")
	if err := os.MkdirAll(filepath.Join(runner, "gregale"), 0700); err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(runner, "gregale-test-results.json")
	t.Setenv("GREGALE_CI_TEST_FAILURE", "1")
	if code := main.CmdTest([]string{"--manifest", manifest, "--suite", "smoke", "--report", candidate}); code != 1 {
		t.Fatalf("candidate: %d", code)
	}
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	gh := `#!/bin/sh
set -eu
case "$2" in
  list) printf '123\t2026-01-01T00:00:00Z\n' ;;
  download) mkdir -p "$RUNNER_TEMP/gregale-baseline"; cp "$TEST_BASELINE" "$RUNNER_TEMP/gregale-baseline/gregale-test-results.json" ;;
  *) exit 2 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(gh), 0700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	shim := "#!/bin/sh\nexec \"$TEST_EXECUTABLE\" -test.run '^TestCICompareSubprocess$' -- \"$@\"\n"
	if err := os.WriteFile(filepath.Join(runner, "gregale", "gregale"), []byte(shim), 0700); err != nil {
		t.Fatal(err)
	}
	summary := filepath.Join(root, "summary.md")
	if err := os.WriteFile(summary, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GREGALE_CI_TEST_SUBPROCESS", "1")
	t.Setenv("TEST_EXECUTABLE", executable)
	t.Setenv("TEST_BASELINE", baseline)
	t.Setenv("RUNNER_TEMP", runner)
	t.Setenv("GITHUB_REPOSITORY", "example/repo")
	t.Setenv("GITHUB_STEP_SUMMARY", summary)
	t.Setenv("GREGALE_CI_WORKFLOW_FILE", "gregale-test.yml")
	t.Setenv("GREGALE_CI_BASE_BRANCH", "main")
	t.Setenv("GREGALE_CI_BASELINE_MAX_AGE_DAYS", "0")
	t.Setenv("GREGALE_CI_BUDGET", filepath.Join(root, main.DefaultTestCIBudget))
	command := exec.Command("bash", "-c", script)
	output, err := command.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("expected comparison to fail: %v %s", err, output)
	}
	for _, path := range []string{summary, filepath.Join(runner, "gregale-test-comparison.md"), filepath.Join(runner, "gregale-test-comparison.html")} {
		body, err := os.ReadFile(path)
		if err != nil || len(body) == 0 || (strings.HasSuffix(path, ".md") && !strings.Contains(string(body), "FAILED")) {
			t.Errorf("missing failed comparison in %s: %v", path, err)
		}
	}
}
