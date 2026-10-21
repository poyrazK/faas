package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestSelfHostedSparseCheckoutsUseTheirOwnDirectory: actions/checkout's
// non-cone sparse mode runs `git config core.sparseCheckout true` in the
// repository's own config. A later full checkout in the same directory runs
// `git sparse-checkout disable`, which turns it off only in the worktree
// config, and then drops the worktree config: the old setting comes back and
// the checkout keeps only the old patterns. A self-hosted runner's default
// workspace outlives its jobs. On 2026-10-21 the release-units preflight left
// scripts/ci as the pattern on both control-plane runners, and the next edge
// lane's full checkout deleted scripts/unit-release-verify.sh before the
// canary was staged. A sparse checkout on a self-hosted runner goes in a
// directory of its own.
func TestSelfHostedSparseCheckoutsUseTheirOwnDirectory(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no workflows found")
	}
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var workflow struct {
			Jobs map[string]struct {
				RunsOn any `yaml:"runs-on"`
				Steps  []struct {
					Uses string         `yaml:"uses"`
					With map[string]any `yaml:"with"`
				} `yaml:"steps"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(body, &workflow); err != nil {
			t.Errorf("%s: %v", filepath.Base(file), err)
			continue
		}
		for name, job := range workflow.Jobs {
			if githubHosted(job.RunsOn) {
				continue
			}
			for i, step := range job.Steps {
				if !strings.HasPrefix(step.Uses, "actions/checkout@") {
					continue
				}
				if _, sparse := step.With["sparse-checkout"]; !sparse {
					continue
				}
				if path, _ := step.With["path"].(string); strings.TrimSpace(strings.TrimPrefix(path, "./")) == "" || path == "." {
					t.Errorf("%s job %s step %d: a sparse checkout on a self-hosted runner needs a path of its own", filepath.Base(file), name, i+1)
				}
			}
		}
	}
}

// githubHosted reports whether runs-on names only a GitHub-hosted image,
// whose workspace is gone after the job. An expression may pick a
// self-hosted runner.
func githubHosted(runsOn any) bool {
	label, ok := runsOn.(string)
	if !ok {
		return false
	}
	for _, image := range []string{"ubuntu-", "windows-", "macos-"} {
		if strings.HasPrefix(label, image) {
			return true
		}
	}
	return false
}
