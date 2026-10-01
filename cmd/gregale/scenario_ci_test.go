package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestCmdTestCIInitCreatesWorkflowAndStarterBudget(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.MkdirAll("app", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("app/server.js", []byte("// local fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := testManifest{
		Version: 1,
		Suites: map[string]testSuite{
			"smoke": {Scenarios: []testSuiteMember{{Scenario: "api", Engine: "local"}}},
		},
		Scenarios: map[string]testScenario{
			"api": {
				Project: "ci-api", Source: "./app",
				Consumers: []testConsumer{{Name: "buyer"}},
				Local: &testLocalAppSpec{
					Command:   []string{"node", "server.js"},
					Readiness: testLocalReadinessSpec{Path: "/health", Status: 200},
				},
				Requests: []testHTTPRequest{{Name: "health", As: "buyer", Method: "GET", Path: "/health", Expect: testHTTPExpect{Status: 200}}},
			},
		},
	}
	body, err := yaml.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("gregale-test.yaml", body, 0o600); err != nil {
		t.Fatal(err)
	}

	oldOut, oldErr := osStdout, osStderr
	var stdout, stderr bytes.Buffer
	osStdout, osStderr = &stdout, &stderr
	t.Cleanup(func() { osStdout, osStderr = oldOut, oldErr })
	if code := cmdTest([]string{"ci", "init", "--manifest", "gregale-test.yaml"}); code != 0 {
		t.Fatalf("test ci init exit = %d; stderr: %s", code, stderr.String())
	}
	workflowPath := filepath.Join(".github", "workflows", "gregale-test.yml")
	workflowBody, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("workflow not created: %v", err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(workflowBody, &document); err != nil {
		t.Fatalf("generated workflow is not valid YAML: %v\n%s", err, workflowBody)
	}
	for _, expected := range []string{
		"actions: read", "contents: read", "actions/checkout@v7", "actions/setup-node@v7",
		"gh run list", "gh run download", "gregale-test-baseline", "--github-summary",
		"workflow_dispatch: {}", "github.event_name == 'workflow_dispatch'",
		`GREGALE_CI_BASELINE_MAX_AGE_DAYS: "30"`,
		"GREGALE_TEST_CONSUMER_BUYER_KEY", "gregale-ci-test-buyer", "gregale-test-results.xml",
	} {
		if !strings.Contains(string(workflowBody), expected) {
			t.Errorf("generated workflow omitted %q:\n%s", expected, workflowBody)
		}
	}
	if document["name"] != testCIWorkflowName {
		t.Fatalf("workflow name = %v, want %q", document["name"], testCIWorkflowName)
	}
	for _, key := range []string{"on", "permissions", "jobs"} {
		if _, ok := document[key]; !ok {
			t.Errorf("generated workflow is missing %q", key)
		}
	}
	budgetPath := filepath.Join(root, defaultTestCIBudget)
	if _, err := readTestComparisonBudget(budgetPath); err != nil {
		t.Fatalf("generated starter budget is invalid: %v", err)
	}
	if !strings.Contains(stdout.String(), "Created .github/workflows/gregale-test.yml") {
		t.Fatalf("success output omitted generated workflow: %s", stdout.String())
	}

	workflowBefore := append([]byte(nil), workflowBody...)
	budgetBefore, err := os.ReadFile(budgetPath)
	if err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	if code := cmdTest([]string{"ci", "init", "--manifest", "gregale-test.yaml"}); code == 0 {
		t.Fatal("test ci init overwrote an existing workflow")
	}
	workflowAfter, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatal(err)
	}
	budgetAfter, err := os.ReadFile(budgetPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(workflowAfter, workflowBefore) || !bytes.Equal(budgetAfter, budgetBefore) {
		t.Fatal("refusing to overwrite an existing workflow changed generated files")
	}

	customBudget := []byte("version: 1\nruns:\n  require_passing: false\n")
	if err := os.WriteFile("custom-budget.yaml", customBudget, 0o600); err != nil {
		t.Fatal(err)
	}
	customWorkflow := filepath.Join(".github", "workflows", "custom-ci.yaml")
	stderr.Reset()
	if code := cmdTest([]string{"ci", "init", "--manifest", "gregale-test.yaml", "--workflow", customWorkflow, "--budget", "custom-budget.yaml", "--baseline-max-age-days", "10"}); code != 0 {
		t.Fatalf("test ci init with existing budget exit = %d; stderr: %s", code, stderr.String())
	}
	customBudgetAfter, err := os.ReadFile("custom-budget.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(customBudgetAfter, customBudget) {
		t.Fatalf("existing budget was modified: %s", customBudgetAfter)
	}
	customWorkflowBody, err := os.ReadFile(customWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(customWorkflowBody), "custom-budget.yaml") {
		t.Fatalf("workflow did not keep the selected budget path:\n%s", customWorkflowBody)
	}
	if !strings.Contains(string(customWorkflowBody), `GREGALE_CI_BASELINE_MAX_AGE_DAYS: "10"`) {
		t.Fatalf("workflow did not keep the selected baseline age limit:\n%s", customWorkflowBody)
	}
}

func TestCmdTestCIInitValidatesSelectionAndBranch(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.MkdirAll("app", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("app/server.js", []byte("// fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := testManifest{
		Version: 1,
		Suites: map[string]testSuite{
			"smoke": {Scenarios: []testSuiteMember{{Scenario: "api", Engine: "local"}}},
			"other": {Scenarios: []testSuiteMember{{Scenario: "api", Engine: "local"}}},
		},
		Scenarios: map[string]testScenario{
			"api": {
				Project: "ci-api", Source: "./app", Local: &testLocalAppSpec{Command: []string{"node", "server.js"}},
				Requests: []testHTTPRequest{{Name: "health", Method: "GET", Path: "/health", Expect: testHTTPExpect{Status: 200}}},
			},
		},
	}
	body, err := yaml.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("gregale-test.yaml", body, 0o600); err != nil {
		t.Fatal(err)
	}
	oldErr := osStderr
	var stderr bytes.Buffer
	osStderr = &stderr
	t.Cleanup(func() { osStderr = oldErr })
	if code := cmdTest([]string{"ci", "init", "--manifest", "gregale-test.yaml"}); code == 0 || !strings.Contains(stderr.String(), "pass --suite NAME") {
		t.Fatalf("ambiguous suite selection exit = %d; stderr: %s", code, stderr.String())
	}
	if code := cmdTest([]string{"ci", "init", "--manifest", "gregale-test.yaml", "--suite", "smoke", "--branch", "main;touch-pwned"}); code == 0 || !strings.Contains(stderr.String(), "Invalid baseline branch") {
		t.Fatalf("invalid branch exit = %d; stderr: %s", code, stderr.String())
	}
	for _, value := range []string{"-1", "31"} {
		stderr.Reset()
		if code := cmdTest([]string{"ci", "init", "--manifest", "gregale-test.yaml", "--suite", "smoke", "--baseline-max-age-days=" + value}); code == 0 || !strings.Contains(stderr.String(), "Invalid baseline age") {
			t.Errorf("invalid baseline age %q exit = %d; stderr: %s", value, code, stderr.String())
		}
	}
	if _, err := os.Stat(filepath.Join(root, defaultTestCIWorkflow)); !os.IsNotExist(err) {
		t.Fatalf("invalid invocation generated a workflow: %v", err)
	}
}

func TestCmdTestCIInitCreatesManualRealVMProfileMatrix(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.MkdirAll("app", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("app/handler.js", []byte("exports.handler = async () => ({statusCode: 200});\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := testManifest{
		Version: 1,
		Suites: map[string]testSuite{
			"acceptance": {Scenarios: []testSuiteMember{{Scenario: "api", Engine: "real-vm"}}},
		},
		Scenarios: map[string]testScenario{
			"api": {
				Project: "ci-vm", Source: "./app", Timeout: "2m",
				Command: []string{"node", "test/assert.mjs"},
			},
		},
	}
	body, err := yaml.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("gregale-test.yaml", body, 0o600); err != nil {
		t.Fatal(err)
	}

	oldOut, oldErr := osStdout, osStderr
	var stdout, stderr bytes.Buffer
	osStdout, osStderr = &stdout, &stderr
	t.Cleanup(func() { osStdout, osStderr = oldOut, oldErr })
	if code := cmdTest([]string{"ci", "init", "--manifest", "gregale-test.yaml", "--suite", "acceptance", "--engine", "real-vm", "--profiles", "cold,restored"}); code == 0 || !strings.Contains(stderr.String(), "requires --max-workload-minutes") {
		t.Fatalf("real-vm generator without a workload guard exit = %d; stderr: %s", code, stderr.String())
	}
	stderr.Reset()
	if code := cmdTest([]string{"ci", "init", "--manifest", "gregale-test.yaml", "--suite", "acceptance", "--engine", "real-vm", "--profiles", "cold,restored", "--max-workload-minutes", "1"}); code == 0 || !strings.Contains(stderr.String(), "up to 2 workload-minutes") {
		t.Fatalf("real-vm generator below the estimated workload exit = %d; stderr: %s", code, stderr.String())
	}
	stderr.Reset()
	vmArgs := []string{"ci", "init", "--manifest", "gregale-test.yaml", "--suite", "acceptance", "--engine", "real-vm", "--profiles", "cold,restored", "--max-workload-minutes", "10", "--baseline-max-age-days", "10"}
	if code := cmdTest(vmArgs); code == 0 || !strings.Contains(stderr.String(), "--baseline-max-age-days") {
		t.Fatalf("real-vm workflow accepted a baseline age option, exit = %d; stderr: %s", code, stderr.String())
	}
	stderr.Reset()
	args := []string{"ci", "init", "--manifest", "gregale-test.yaml", "--suite", "acceptance", "--engine", "real-vm", "--profiles", "cold,restored", "--max-workload-minutes", "10", "--environment", "gregale-acceptance"}
	if code := cmdTest(args); code != 0 {
		t.Fatalf("real-vm test ci init exit = %d; stderr: %s", code, stderr.String())
	}
	workflowPath := filepath.Join(".github", "workflows", "gregale-test.yml")
	workflowBody, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("workflow not created: %v", err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(workflowBody, &document); err != nil {
		t.Fatalf("generated workflow is not valid YAML: %v\n%s", err, workflowBody)
	}
	for _, expected := range []string{
		"workflow_dispatch", "fromJSON", "matrix.profile", "max-parallel: 1", "gregale-acceptance",
		"GREGALE_TEST_API_URL", "GREGALE_TEST_TOKEN", "GREGALE_CI_MAX_WORKLOAD_MINUTES: \"10\"",
		"--preflight", "--max-workload-minutes", "--profile \"$GREGALE_CI_PROFILE\"",
		"actions/setup-node@v7", "gregale-test-${{ matrix.profile }}-${{ github.run_id }}",
	} {
		if !strings.Contains(string(workflowBody), expected) {
			t.Errorf("generated real-vm workflow omitted %q:\n%s", expected, workflowBody)
		}
	}
	events, ok := document["on"].(map[string]any)
	if !ok {
		t.Fatalf("workflow events have unexpected type: %T", document["on"])
	}
	if _, ok := events["pull_request"]; ok {
		t.Fatal("real-vm workflow should not expose its credentialed job to pull requests")
	}
	if document["name"] != testCIVMWorkflowName {
		t.Fatalf("workflow name = %v, want %q", document["name"], testCIVMWorkflowName)
	}
	jobs, ok := document["jobs"].(map[string]any)
	if !ok {
		t.Fatalf("workflow jobs have unexpected type: %T", document["jobs"])
	}
	scenarios, ok := jobs["scenarios"].(map[string]any)
	if !ok {
		t.Fatalf("scenario job has unexpected type: %T", jobs["scenarios"])
	}
	if scenarios["environment"] != "gregale-acceptance" {
		t.Fatalf("workflow environment = %v", scenarios["environment"])
	}
	jobEnv, ok := scenarios["env"].(map[string]any)
	if !ok {
		t.Fatalf("scenario job environment has unexpected type: %T", scenarios["env"])
	}
	if _, ok := jobEnv["FAAS_TOKEN"]; ok {
		t.Fatal("Gregale API token is available to every action in the VM job")
	}
	steps, ok := scenarios["steps"].([]any)
	if !ok {
		t.Fatalf("scenario steps have unexpected type: %T", scenarios["steps"])
	}
	for _, rawStep := range steps {
		step, ok := rawStep.(map[string]any)
		if !ok || step["uses"] == nil {
			continue
		}
		if stepEnv, ok := step["env"].(map[string]any); ok {
			if _, ok := stepEnv["FAAS_TOKEN"]; ok {
				t.Errorf("GitHub token exposed to third-party action %v", step["uses"])
			}
		}
	}
	strategy, ok := scenarios["strategy"].(map[string]any)
	if !ok {
		t.Fatalf("scenario strategy has unexpected type: %T", scenarios["strategy"])
	}
	matrix, ok := strategy["matrix"].(map[string]any)
	if !ok || !strings.Contains(matrix["profile"].(string), "fromJSON") {
		t.Fatalf("scenario profile matrix = %#v", strategy["matrix"])
	}
	if _, err := os.Stat(defaultTestCIBudget); !os.IsNotExist(err) {
		t.Fatalf("real-vm workflow unexpectedly created a comparison budget: %v", err)
	}
	if !strings.Contains(stdout.String(), "manually dispatched") || !strings.Contains(stdout.String(), "10 minutes per profile job") {
		t.Fatalf("real-vm success output omitted workflow configuration: %s", stdout.String())
	}
}

func TestParseTestCIProfilesAndEnvironment(t *testing.T) {
	profiles, err := parseTestCIProfiles("warm,restored")
	if err != nil || strings.Join(profiles, ",") != "warm,restored" {
		t.Fatalf("parsed profiles = %v (%v)", profiles, err)
	}
	if profiles, err := parseTestCIProfiles("all"); err != nil || strings.Join(profiles, ",") != "warm,cold,restored" {
		t.Fatalf("all profiles = %v (%v)", profiles, err)
	}
	for _, invalid := range []string{"", "warm, warm", "warm,warm", "hot", "all,warm"} {
		if _, err := parseTestCIProfiles(invalid); err == nil {
			t.Errorf("parseTestCIProfiles(%q) succeeded", invalid)
		}
	}
	if !validTestCIEnvironment("gregale-acceptance.v1") || validTestCIEnvironment("../unsafe") || validTestCIEnvironment("") {
		t.Fatal("GitHub environment name validation is incorrect")
	}
}

func TestGeneratedTestCIRunCommandAddsLoadFlagAsAContinuation(t *testing.T) {
	command := generatedTestCIRunCommand(true)
	if !strings.HasSuffix(command, `--html "$RUNNER_TEMP/gregale-test-results.html" \
  --load`) {
		t.Fatalf("load command does not end with a valid continued flag:\n%s", command)
	}
}

func TestGeneratedTestCICommandsAreValidBash(t *testing.T) {
	for name, command := range map[string]string{
		"run":           generatedTestCIRunCommand(false),
		"run with load": generatedTestCIRunCommand(true),
		"compare":       generatedTestCICompareCommand(),
		"VM config":     generatedTestCIVMConfigCommand(),
		"VM preflight":  generatedTestCIVMRunCommand(true),
		"VM run":        generatedTestCIVMRunCommand(false),
	} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command("bash", "-n")
			cmd.Stdin = strings.NewReader(command)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("generated bash is invalid: %v\n%s\n%s", err, output, command)
			}
		})
	}
}

func TestGeneratedTestCICompareCommandBaselineBehavior(t *testing.T) {
	t.Run("no successful baseline bootstraps", func(t *testing.T) {
		code, summary, output, _ := runGeneratedTestCICompare(t, map[string]string{
			"FAKE_GH_BASELINE": "",
		})
		if code != 0 || !strings.Contains(summary, "No successful baseline exists") || strings.Contains(output, "::error::") {
			t.Fatalf("bootstrap result = %d, summary %q, output %q", code, summary, output)
		}
	})

	t.Run("unavailable artifact fails", func(t *testing.T) {
		code, summary, output, _ := runGeneratedTestCICompare(t, map[string]string{
			"FAKE_GH_BASELINE":                 "123\t2026-09-30T00:00:00Z",
			"FAKE_GH_DOWNLOAD_EXIT":            "1",
			"GREGALE_CI_BASELINE_MAX_AGE_DAYS": "0",
		})
		if code != 1 || !strings.Contains(summary, "unavailable or expired") || !strings.Contains(output, "::error::") {
			t.Fatalf("missing artifact result = %d, summary %q, output %q", code, summary, output)
		}
	})

	t.Run("artifact without report fails", func(t *testing.T) {
		code, summary, output, _ := runGeneratedTestCICompare(t, map[string]string{
			"FAKE_GH_BASELINE":                 "123\t2026-09-30T00:00:00Z",
			"FAKE_GH_OMIT_FILE":                "1",
			"GREGALE_CI_BASELINE_MAX_AGE_DAYS": "0",
		})
		if code != 1 || !strings.Contains(summary, "did not contain gregale-test-results.json") || !strings.Contains(output, "::error::") {
			t.Fatalf("incomplete artifact result = %d, summary %q, output %q", code, summary, output)
		}
	})

	t.Run("stale baseline fails before download", func(t *testing.T) {
		code, summary, output, _ := runGeneratedTestCICompare(t, map[string]string{
			"FAKE_GH_BASELINE":                 "123\t2000-01-01T00:00:00Z",
			"FAKE_BASELINE_EPOCH":              "0",
			"FAKE_NOW_EPOCH":                   "2678401",
			"GREGALE_CI_BASELINE_MAX_AGE_DAYS": "30",
		})
		if code != 1 || !strings.Contains(summary, "older than the configured 30-day limit") || !strings.Contains(output, "::error::") {
			t.Fatalf("stale baseline result = %d, summary %q, output %q", code, summary, output)
		}
	})

	t.Run("newer manual baseline is selected", func(t *testing.T) {
		code, summary, _, downloadedRunID := runGeneratedTestCICompare(t, map[string]string{
			"FAKE_GH_PUSH_BASELINE":            "123\t2026-09-29T00:00:00Z",
			"FAKE_GH_MANUAL_BASELINE":          "456\t2026-09-30T00:00:00Z",
			"FAKE_GH_DOWNLOAD_EXIT":            "1",
			"GREGALE_CI_BASELINE_MAX_AGE_DAYS": "0",
		})
		if code != 1 || downloadedRunID != "456" || !strings.Contains(summary, "unavailable or expired") {
			t.Fatalf("manual baseline selection = %d, downloaded run %q, summary %q", code, downloadedRunID, summary)
		}
	})
}

func runGeneratedTestCICompare(t *testing.T, overrides map[string]string) (int, string, string, string) {
	t.Helper()
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	ghScript := `#!/bin/bash
set -euo pipefail
case "$2" in
  list)
    event=""
    while (($#)); do
      if [[ "$1" == --event ]]; then event="$2"; shift; fi
      shift
    done
    case "$event" in
      push) baseline="${FAKE_GH_PUSH_BASELINE-${FAKE_GH_BASELINE:-}}" ;;
      workflow_dispatch) baseline="${FAKE_GH_MANUAL_BASELINE-${FAKE_GH_BASELINE:-}}" ;;
      *) exit 98 ;;
    esac
    printf '%s' "$baseline"
    exit "${FAKE_GH_LIST_EXIT:-0}"
    ;;
  download)
    printf '%s' "$3" > "$FAKE_GH_DOWNLOAD_RUN_FILE"
    if [[ "${FAKE_GH_DOWNLOAD_EXIT:-0}" != 0 ]]; then exit "$FAKE_GH_DOWNLOAD_EXIT"; fi
    mkdir -p "$RUNNER_TEMP/gregale-baseline"
    if [[ "${FAKE_GH_OMIT_FILE:-0}" != 1 ]]; then
      printf '{}\n' > "$RUNNER_TEMP/gregale-baseline/gregale-test-results.json"
    fi
    ;;
  *) exit 99 ;;
esac
`
	if err := os.WriteFile(filepath.Join(binDir, "gh"), []byte(ghScript), 0o700); err != nil {
		t.Fatal(err)
	}
	dateScript := `#!/bin/bash
set -euo pipefail
case " $* " in
  *" -d "*) printf '%s\n' "${FAKE_BASELINE_EPOCH:-0}" ;;
  *) printf '%s\n' "${FAKE_NOW_EPOCH:-2000000000}" ;;
esac
`
	if err := os.WriteFile(filepath.Join(binDir, "date"), []byte(dateScript), 0o700); err != nil {
		t.Fatal(err)
	}
	summaryPath := filepath.Join(root, "summary.md")
	runnerTemp := filepath.Join(root, "runner-temp")
	if err := os.MkdirAll(runnerTemp, 0o700); err != nil {
		t.Fatal(err)
	}
	envValues := map[string]string{
		"PATH":                             binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"GITHUB_REPOSITORY":                "example/gregale",
		"GITHUB_STEP_SUMMARY":              summaryPath,
		"GREGALE_CI_WORKFLOW_FILE":         "gregale-test.yml",
		"GREGALE_CI_BASE_BRANCH":           "main",
		"GREGALE_CI_BASELINE_MAX_AGE_DAYS": "30",
		"GREGALE_CI_BUDGET":                "budget.yaml",
		"RUNNER_TEMP":                      runnerTemp,
		"FAKE_GH_BASELINE":                 "123\t2026-09-30T00:00:00Z",
		"FAKE_GH_DOWNLOAD_EXIT":            "0",
		"FAKE_GH_OMIT_FILE":                "0",
		"FAKE_GH_DOWNLOAD_RUN_FILE":        filepath.Join(root, "download-run-id"),
		"FAKE_BASELINE_EPOCH":              "1999999999",
		"FAKE_NOW_EPOCH":                   "2000000000",
	}
	for key, value := range overrides {
		envValues[key] = value
	}
	env := make([]string, 0, len(os.Environ())+len(envValues))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := envValues[key]; !replaced {
			env = append(env, entry)
		}
	}
	for key, value := range envValues {
		env = append(env, key+"="+value)
	}
	cmd := exec.Command("bash", "-c", generatedTestCICompareCommand())
	cmd.Env = env
	outputBytes, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run generated compare script: %v\n%s", err, outputBytes)
		}
		code = exitErr.ExitCode()
	}
	summaryBytes, err := os.ReadFile(summaryPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read comparison summary: %v", err)
	}
	downloadedRunID, err := os.ReadFile(envValues["FAKE_GH_DOWNLOAD_RUN_FILE"])
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read downloaded baseline run id: %v", err)
	}
	return code, string(summaryBytes), string(outputBytes), string(downloadedRunID)
}
