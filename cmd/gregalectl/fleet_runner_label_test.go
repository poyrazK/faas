package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// runnerLabelCheck returns the runner role's fleet-label assertion.
func runnerLabelCheck(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "roles", "github_actions_runner", "tasks", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var tasks []map[string]any
	if err := yaml.Unmarshal(body, &tasks); err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task["name"] != "github_actions_runner — require a supported host" {
			continue
		}
		assert, _ := task["ansible.builtin.assert"].(map[string]any)
		that, _ := assert["that"].([]any)
		for _, c := range that {
			if s, _ := c.(string); strings.Contains(s, "faas_runner_labels") && strings.Contains(s, "faas-fleet") {
				return s
			}
		}
	}
	t.Fatal("runner role lost its fleet-label assertion")
	return ""
}

// Each fleet's trusted runner carries exactly one fleet label, so it can only
// run that fleet's rollouts (cd-* select the label from deploy_environment).
// A runner labeled for two fleets would accept the other fleet's jobs.
func TestFleetRunnerCarriesExactlyOneFleetLabel(t *testing.T) {
	check := runnerLabelCheck(t)
	playbookBin, err := exec.LookPath("ansible-playbook")
	if err != nil {
		t.Skip("ansible-playbook not installed")
	}
	for _, tc := range []struct {
		labels []string
		ok     bool
	}{
		{[]string{"faas-fleet"}, true},
		{[]string{"faas-fleet-us"}, true},
		{[]string{"faas-fleet", "gpu"}, true},
		{[]string{"faas-fleet", "faas-fleet-us"}, false},
		{[]string{}, false},
		{[]string{"self-hosted-fleet"}, false},
		{[]string{"faas-fleet-US"}, false},
	} {
		dir := t.TempDir()
		play := []map[string]any{{
			"hosts":        "localhost",
			"gather_facts": false,
			"vars":         map[string]any{"faas_runner_labels": tc.labels},
			"tasks":        []map[string]any{{"ansible.builtin.assert": map[string]any{"that": []string{check}}}},
		}}
		body, err := yaml.Marshal(play)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "play.yml")
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		cfg := filepath.Join(dir, "ansible.cfg")
		if err := os.WriteFile(cfg, []byte("[defaults]\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(playbookBin, "-i", "localhost,", "-c", "local", path)
		cmd.Env = append(os.Environ(), "ANSIBLE_CONFIG="+cfg, "ANSIBLE_NOCOLOR=1")
		out, err := cmd.CombinedOutput()
		if (err == nil) != tc.ok {
			t.Errorf("labels %v: accepted=%v, want %v\n%s", tc.labels, err == nil, tc.ok, out)
		}
	}
}
