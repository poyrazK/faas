// adr: 20261101160300
package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"gopkg.in/yaml.v3"
)

// --wait-for deploys a branch push only after the named CI workflow's run
// for that commit succeeds, and only for a push to this repository
// (ADR-20261101160300, amendment 1).
func TestRenderGithubSetupWorkflowWaitsForCI(t *testing.T) {
	workflow := main.RenderGithubSetupWorkflow("api", "acme/api", "main", "standard", map[string]string{
		"staging": "staging",
	}, "", false, "CI")
	var parsed struct {
		On struct {
			WorkflowRun struct {
				Workflows []string `yaml:"workflows"`
				Types     []string `yaml:"types"`
				Branches  []string `yaml:"branches"`
			} `yaml:"workflow_run"`
			Push struct {
				Branches []string `yaml:"branches"`
				Tags     []string `yaml:"tags"`
			} `yaml:"push"`
		} `yaml:"on"`
		Jobs map[string]struct {
			If    string `yaml:"if"`
			Steps []struct {
				With map[string]string `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(workflow), &parsed); err != nil {
		t.Fatalf("workflow is invalid YAML: %v\n%s", err, workflow)
	}
	run := parsed.On.WorkflowRun
	if strings.Join(run.Workflows, ",") != "CI" || strings.Join(run.Types, ",") != "completed" || strings.Join(run.Branches, ",") != "main,staging" {
		t.Fatalf("workflow_run trigger = %+v, want CI completions on main and staging", run)
	}
	if len(parsed.On.Push.Branches) != 0 || strings.Join(parsed.On.Push.Tags, ",") != "v*" {
		t.Fatalf("push trigger = %+v, want release tags only", parsed.On.Push)
	}
	deploy := parsed.Jobs["deploy"]
	for _, want := range []string{
		"github.event.workflow_run.conclusion == 'success'",
		"github.event.workflow_run.event == 'push'",
		"github.event.workflow_run.head_repository.full_name == github.repository",
	} {
		if !strings.Contains(deploy.If, want) {
			t.Errorf("deploy condition lacks %q: %s", want, deploy.If)
		}
	}
	if got := deploy.Steps[0].With["ref"]; got != "${{ github.event.workflow_run.head_sha || github.sha }}" {
		t.Fatalf("deploy ref = %q, want the CI run's commit", got)
	}
	if !strings.Contains(workflow, "format('refs/heads/{0}', github.event.workflow_run.head_branch) || github.ref) == 'refs/heads/staging' && 'staging'") {
		t.Fatalf("environment mapping ignores the CI run's branch:\n%s", workflow)
	}
}

func TestValidGithubSetupWorkflowName(t *testing.T) {
	for name, want := range map[string]bool{"CI": true, "Build & test": true, "": false, "ci\nname": false, strings.Repeat("x", 256): false} {
		if got := main.ValidGithubSetupWorkflowName(name); got != want {
			t.Errorf("validGithubSetupWorkflowName(%q) = %v, want %v", name, got, want)
		}
	}
}

// --wait-for names a workflow of the repository that runs on pushes to every
// deploy branch; anything else would never start a workflow_run, so setup
// refuses it (ADR-20261101160300, amendment 2).
func TestGithubSetupCheckWaitFor(t *testing.T) {
	root := t.TempDir()
	workflows := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(workflows, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"ci.yml":      "name: CI\non:\n  push:\n    branches: [main, 'release/**', '!release/old']\n  pull_request:\n",
		"lint.yaml":   "name: Lint\non: [pull_request]\n",
		"tags.yml":    "name: Tags\non:\n  push:\n    tags: ['v*']\n",
		"any.yml":     "on: push\n",
		"ignore.yml":  "name: Ignore\non:\n  push:\n    branches-ignore: ['staging']\n",
		"gregale.yml": "name: Gregale deploy\non: push\n",
		"broken.yml":  ":\n  - [",
		"notes.md":    "name: CI\n",
	} {
		if err := os.WriteFile(filepath.Join(workflows, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	generated := filepath.Join(".github", "workflows", "gregale.yml")
	for _, tc := range []struct {
		name, waitFor string
		branches      []string
		wantErr       string
	}{
		{name: "CI runs on the deploy branches", waitFor: "CI", branches: []string{"main", "release/2026"}},
		{name: "unnamed workflow by path", waitFor: ".github/workflows/any.yml", branches: []string{"main"}},
		{name: "branches-ignore leaves main", waitFor: "Ignore", branches: []string{"main"}},
		{name: "typo", waitFor: "ci", branches: []string{"main"}, wantErr: "found: .github/workflows/any.yml, CI, Ignore, Lint, Tags"},
		{name: "branch outside the filter", waitFor: "CI", branches: []string{"main", "staging"}, wantErr: "does not run on pushes to staging"},
		{name: "negated branch", waitFor: "CI", branches: []string{"release/old"}, wantErr: "does not run on pushes to release/old"},
		{name: "pull requests only", waitFor: "Lint", branches: []string{"main"}, wantErr: "does not run on pushes to main"},
		{name: "tags only", waitFor: "Tags", branches: []string{"main"}, wantErr: "does not run on pushes to main"},
		{name: "ignored branch", waitFor: "Ignore", branches: []string{"staging"}, wantErr: "does not run on pushes to staging"},
		{name: "the generated workflow itself", waitFor: "Gregale deploy", branches: []string{"main"}, wantErr: "is named \"Gregale deploy\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := main.GithubSetupCheckWaitFor(root, generated, tc.waitFor, tc.branches)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("GithubSetupCheckWaitFor = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("GithubSetupCheckWaitFor = %v, want an error containing %q", err, tc.wantErr)
			}
		})
	}
	if err := main.GithubSetupCheckWaitFor(t.TempDir(), generated, "CI", []string{"main"}); err == nil || !strings.Contains(err.Error(), "found: none") {
		t.Fatalf("repository without workflows = %v, want found: none", err)
	}
}
