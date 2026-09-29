package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fleetRunner selects the trusted runner label for the target environment.
// Each fleet has its own label so a runner inside one fleet's network never
// picks up another fleet's rollout.
const fleetRunner = `fromJSON(inputs.deploy_environment == 'production-us' && '["self-hosted","linux","faas-fleet-us"]' || '["self-hosted","linux","faas-fleet"]')`

func readWorkflow(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// jobRunsOn returns the runs-on line of a top-level job in a workflow.
func jobRunsOn(t *testing.T, workflow, job string) string {
	t.Helper()
	start := strings.Index(workflow, "\n  "+job+":\n")
	if start < 0 {
		t.Fatalf("job %q not found", job)
	}
	for _, line := range strings.Split(workflow[start+1:], "\n")[1:] {
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") {
			break // next job
		}
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "runs-on:") {
			return trimmed
		}
	}
	t.Fatalf("job %q has no runs-on", job)
	return ""
}

// Rollout coordination jobs run on the dedicated fleet runner by default: on
// a busy CI day each GitHub-hosted job queued 8-14 minutes, and a rollout ran
// five of them in sequence. Hosted runners stay available as an explicit
// escape hatch; node rollouts always need the fleet runner's private network.
func TestCDCoordinationJobsDefaultToFleetRunner(t *testing.T) {
	read := func(name string) string { return readWorkflow(t, name) }
	for _, tc := range []struct {
		file, input string
		jobs        []string
	}{
		{"cd-platform.yml", "hosted_runners", []string{"plan", "verify"}},
		{"cd-controlplane.yml", "hosted_runner", []string{"deploy"}},
		{"cd-compute.yml", "hosted_runner", []string{"preflight"}},
	} {
		workflow := read(tc.file)
		want := fmt.Sprintf(`runs-on: ${{ inputs.%s && 'ubuntu-latest' || %s }}`, tc.input, fleetRunner)
		for _, job := range tc.jobs {
			if got := jobRunsOn(t, workflow, job); got != want {
				t.Errorf("%s %s: %s, want %s", tc.file, job, got, want)
			}
		}
		if !strings.Contains(workflow, "      "+tc.input+":\n") {
			t.Errorf("%s does not declare the %s escape hatch", tc.file, tc.input)
		}
	}
	if got := jobRunsOn(t, read("cd-compute.yml"), "deploy"); got != "runs-on: ${{ "+fleetRunner+" }}" {
		t.Errorf("cd-compute node rollout must always use the fleet runner: %s", got)
	}
	platform := read("cd-platform.yml")
	calls := strings.Count(platform, "uses: ./.github/workflows/cd-")
	if passed := strings.Count(platform, "hosted_runner: ${{ inputs.hosted_runners }}"); passed != calls {
		t.Errorf("cd-platform passes hosted_runner to %d of %d stage calls", passed, calls)
	}
	if strings.Contains(read("cd-controlplane.yml"), "gh api") {
		t.Error("cd-controlplane uses the gh CLI, which the fleet runner does not install")
	}
}

// The rollout stages read the target fleet's secrets from the GitHub
// environment named by deploy_environment and run on that fleet's runner
// label. A stage that hard-codes `production`, or a cd-platform call that
// drops the input, would deploy one fleet's release with the other fleet's
// hosts, keys and database.
func TestCDStagesTargetTheSelectedEnvironment(t *testing.T) {
	platform := readWorkflow(t, "cd-platform.yml")
	calls := strings.Count(platform, "uses: ./.github/workflows/cd-")
	if passed := strings.Count(platform, "deploy_environment: ${{ inputs.deploy_environment }}"); passed != calls {
		t.Errorf("cd-platform passes deploy_environment to %d of %d stage calls", passed, calls)
	}
	for _, name := range []string{"cd-platform.yml", "cd-controlplane.yml", "cd-compute.yml"} {
		workflow := readWorkflow(t, name)
		if !strings.Contains(workflow, "      deploy_environment:\n") {
			t.Errorf("%s does not declare deploy_environment", name)
		}
		envs := 0
		for _, line := range strings.Split(workflow, "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "environment:") {
				continue
			}
			envs++
			if trimmed != "environment: ${{ inputs.deploy_environment }}" {
				t.Errorf("%s: %q does not follow deploy_environment", name, trimmed)
			}
		}
		if envs == 0 {
			t.Errorf("%s has no environment-scoped job", name)
		}
		for _, line := range strings.Split(workflow, "\n") {
			if strings.Contains(line, "faas-fleet") && strings.Contains(line, "runs-on") && !strings.Contains(line, fleetRunner) {
				t.Errorf("%s: runner label not derived from deploy_environment: %s", name, strings.TrimSpace(line))
			}
		}
	}
	compute := readWorkflow(t, "cd-compute.yml")
	if !strings.Contains(compute, "FLEET_RUNNER_LABEL: ${{ inputs.deploy_environment == 'production-us' && 'faas-fleet-us' || 'faas-fleet' }}") ||
		!strings.Contains(compute, `index($label)`) {
		t.Error("cd-compute's online-runner probe does not look for the selected fleet's label")
	}
}

// Jobs on the self-hosted fleet runner cannot pip-install into the system
// interpreter: Ubuntu 24.04 ships no pip and marks python3 externally
// managed (PEP 668). cd-controlplane moved onto the fleet runner and failed
// its first run at `python3 -m pip install`. Python tools go into a per-job
// venv, and the runner role installs python3-venv so the venv can be made.
func TestFleetRunnerJobsInstallPythonToolsInAVenv(t *testing.T) {
	for _, name := range []string{"cd-platform.yml", "cd-controlplane.yml", "cd-compute.yml", "pki-renew.yml"} {
		for i, line := range strings.Split(readWorkflow(t, name), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.Contains(trimmed, "pip install") && !strings.Contains(trimmed, "-venv/bin/") {
				t.Errorf("%s:%d installs Python packages outside a venv: %s", name, i+1, trimmed)
			}
		}
	}
	controlPlane := readWorkflow(t, "cd-controlplane.yml")
	for _, want := range []string{
		`python3 -m venv "$RUNNER_TEMP/ansible-venv"`,
		`echo "$RUNNER_TEMP/ansible-venv/bin" >> "$GITHUB_PATH"`,
	} {
		if !strings.Contains(controlPlane, want) {
			t.Errorf("cd-controlplane renderer install lost %q", want)
		}
	}
	role, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "roles", "github_actions_runner", "tasks", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(role), "      - python3-venv\n") {
		t.Error("github_actions_runner does not install python3-venv")
	}
}
