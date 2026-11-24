package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// The unit-release build runner may share the control plane
// (ADR-20261122170000) only as its own user, with a build-only label, no
// fleet endpoints and unit limits below PostgreSQL and the daemons.
func TestBuildRunnerIsIsolatedFromTheDeploymentRunner(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "build_runner.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var plays []struct {
		Tasks []struct {
			IncludeRole map[string]any `yaml:"ansible.builtin.include_role"`
			Vars        map[string]any `yaml:"vars"`
		} `yaml:"tasks"`
	}
	if err := yaml.Unmarshal(body, &plays); err != nil {
		t.Fatal(err)
	}
	if len(plays) != 1 {
		t.Fatalf("build_runner.yml must be one play, got %d", len(plays))
	}
	var vars map[string]any
	runners := 0
	for _, task := range plays[0].Tasks {
		if task.IncludeRole["name"] == "github_actions_runner" {
			vars = task.Vars
			runners++
		}
	}
	if runners != 1 {
		t.Fatalf("build_runner.yml must include github_actions_runner once, got %d", runners)
	}
	str := func(k string) string { s, _ := vars[k].(string); return s }
	for k, v := range map[string]string{
		"faas_runner_kind":           "build",
		"faas_runner_user":           "faas-builder",
		"faas_runner_group":          "faas-builder",
		"faas_runner_home":           "/var/lib/faas-builder",
		"faas_runner_root":           "/opt/actions-runner-build",
		"faas_runner_cache":          "/var/cache/faas-builder",
		"faas_runner_service":        "faas-build-runner",
		"faas_runner_protect_system": "strict",
	} {
		if str(k) != v {
			t.Errorf("%s = %q, want %q", k, str(k), v)
		}
	}
	for _, k := range []string{"faas_runner_cpu_quota", "faas_runner_memory_high", "faas_runner_memory_max"} {
		if str(k) == "" {
			t.Errorf("%s must be set: the build runner shares the control plane", k)
		}
	}
	for _, k := range []string{"faas_runner_cpu_weight", "faas_runner_io_weight"} {
		if n, ok := vars[k].(int); !ok || n <= 0 || n > 10 {
			t.Errorf("%s = %v, want a weight from 1 to 10", k, vars[k])
		}
	}
	if n, ok := vars["faas_runner_gomaxprocs"].(int); !ok || n <= 0 {
		t.Errorf("faas_runner_gomaxprocs = %v, want a positive bound", vars["faas_runner_gomaxprocs"])
	}
	labels, _ := vars["faas_runner_labels"].([]any)
	if len(labels) != 1 {
		t.Fatalf("faas_runner_labels = %v, want one build label", labels)
	}
	if label, _ := labels[0].(string); !strings.Contains(label, "faas-build") || strings.Contains(label, "faas-fleet") {
		t.Errorf("faas_runner_labels = %v, want one faas-build label", labels)
	}
	if hosts, _ := vars["faas_runner_private_hosts"].([]any); len(hosts) != 0 {
		t.Errorf("the build runner resolves no private fleet endpoints, got %v", hosts)
	}
	// A cancelled build's Go processes must not outlive it on the control
	// plane (2026-11-22): the unit names the cleanup hook for both job ends.
	role := filepath.Join("..", "..", "deploy", "ansible", "roles", "github_actions_runner")
	unit, err := os.ReadFile(filepath.Join(role, "templates", "faas-github-runner.service.j2"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"{% if faas_runner_kind == 'build' %}",
		"Environment=ACTIONS_RUNNER_HOOK_JOB_STARTED={{ faas_runner_job_cleanup_hook }}",
		"Environment=ACTIONS_RUNNER_HOOK_JOB_COMPLETED={{ faas_runner_job_cleanup_hook }}",
	} {
		if !strings.Contains(string(unit), want) {
			t.Errorf("runner unit template is missing %q", want)
		}
	}
	// The runner runs a hook only by its extension; without one every job
	// failed at "Set up runner" (2026-11-22).
	defaults, err := os.ReadFile(filepath.Join(role, "defaults", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^faas_runner_job_cleanup_hook: \S+\.sh$`).Match(defaults) {
		t.Error("faas_runner_job_cleanup_hook must end in .sh: the runner refuses any other hook path")
	}
	hook, err := os.ReadFile(filepath.Join(role, "templates", "faas-build-runner-job-cleanup.sh.j2"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(hook), `pkill -KILL -u "$(id -u)" -f '/pkg/tool/`) || !strings.Contains(string(hook), "exit 0") {
		t.Error("the cleanup hook must kill this account's Go tool processes and never fail the job")
	}
}
