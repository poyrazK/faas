package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	read := func(name string) string {
		body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	for _, tc := range []struct {
		file, input string
		jobs        []string
	}{
		{"cd-platform.yml", "hosted_runners", []string{"plan", "verify"}},
		{"cd-controlplane.yml", "hosted_runner", []string{"deploy"}},
		{"cd-compute.yml", "hosted_runner", []string{"preflight"}},
	} {
		workflow := read(tc.file)
		want := fmt.Sprintf(`runs-on: ${{ inputs.%s && 'ubuntu-latest' || fromJSON('["self-hosted","linux","faas-fleet"]') }}`, tc.input)
		for _, job := range tc.jobs {
			if got := jobRunsOn(t, workflow, job); got != want {
				t.Errorf("%s %s: %s, want %s", tc.file, job, got, want)
			}
		}
		if !strings.Contains(workflow, "      "+tc.input+":\n") {
			t.Errorf("%s does not declare the %s escape hatch", tc.file, tc.input)
		}
	}
	if got := jobRunsOn(t, read("cd-compute.yml"), "deploy"); got != "runs-on: [self-hosted, linux, faas-fleet]" {
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
