// adr: 20261101160300
package main_test

import (
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
