package daemonunitspec

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func postgresCapacityTasks(t *testing.T) []map[string]any {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "postgres_capacity", "tasks", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var tasks []map[string]any
	if err := yaml.Unmarshal(body, &tasks); err != nil {
		t.Fatal(err)
	}
	return tasks
}

// A restart that depends on a handler notification is lost when a run
// writes postgresql.conf and then fails before the flush: every later run sees
// an unchanged file and never restarts, and admission fails forever. This
// happened on the first from-scratch gregale-prod bootstrap. The restart must
// be decided from the running postmaster.
func TestPostgresCapacityRestartFollowsTheRunningPostmaster(t *testing.T) {
	tasks := postgresCapacityTasks(t)
	index := map[string]int{}
	byName := map[string]map[string]any{}
	for i, task := range tasks {
		name, _ := task["name"].(string)
		index[name] = i
		byName[name] = task
		if _, ok := task["notify"]; ok {
			t.Errorf("task %q restarts through a handler notification", name)
		}
	}
	if _, err := os.Stat(filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "postgres_capacity", "handlers")); err == nil {
		t.Error("postgres_capacity still ships handlers; the restart must not depend on notification")
	}

	const (
		target  = "postgres capacity — derive the live postmaster target"
		running = "postgres capacity — read running postmaster settings"
		restart = "postgres capacity — restart postgresql to activate postmaster settings"
		live    = "postgres capacity — read live postmaster settings"
		verify  = "postgres capacity — verify live ordinary-client capacity"
		maxConn = "postgres capacity — set max_connections"
		reserve = "postgres capacity — reserve operator connections"
	)
	order := []string{maxConn, reserve, target, running, restart, live, verify}
	for i, name := range order {
		if _, ok := index[name]; !ok {
			t.Fatalf("postgres_capacity lost task %q", name)
		}
		if i > 0 && index[order[i-1]] >= index[name] {
			t.Errorf("task %q must run before %q", order[i-1], name)
		}
	}
	when, _ := byName[restart]["when"].(string)
	if !strings.Contains(when, "postgres_capacity_running.stdout") || !strings.Contains(when, "postgres_capacity_live_target") {
		t.Errorf("restart condition %q does not compare the running postmaster with the target", when)
	}
	svc, _ := byName[restart]["ansible.builtin.service"].(map[string]any)
	if svc["state"] != "restarted" {
		t.Errorf("restart task state = %v, want restarted", svc["state"])
	}
	facts, _ := byName[target]["ansible.builtin.set_fact"].(map[string]any)
	targetExpr, _ := facts["postgres_capacity_live_target"].(string)
	checks, _ := byName[verify]["ansible.builtin.assert"].(map[string]any)
	that, _ := checks["that"].([]any)
	if len(that) != 1 || !strings.Contains(that[0].(string), "postgres_capacity_live_target") {
		t.Errorf("admission check %v does not use the same target as the restart", that)
	}

	playbookBin, err := exec.LookPath("ansible-playbook")
	if err != nil {
		t.Log("ansible-playbook not installed; static checks only")
		return
	}
	for _, tc := range []struct {
		name, running string
		wantRestart   bool
	}{
		{"file written but postmaster never restarted", "100:3\n", true},
		{"postmaster already on target", "160:5\n", false},
		{"only the reserve differs", "160:3", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := evalRestartCondition(t, playbookBin, targetExpr, when, tc.running); got != tc.wantRestart {
				t.Fatalf("restart = %v, want %v", got, tc.wantRestart)
			}
		})
	}
}

// evalRestartCondition runs the role's exact target and restart expressions
// for max_connections 160 and 5 reserved connections.
func evalRestartCondition(t *testing.T, playbookBin, targetExpr, when, running string) bool {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "restart")
	playbook := map[string]any{
		"hosts":        "localhost",
		"gather_facts": false,
		"vars": map[string]any{
			"postgres_capacity_max_connections":            "160",
			"faas_postgres_superuser_reserved_connections": 5,
			"postgres_capacity_running":                    map[string]any{"stdout": running},
		},
		"tasks": []map[string]any{
			{"ansible.builtin.set_fact": map[string]any{"postgres_capacity_live_target": targetExpr}},
			{"ansible.builtin.copy": map[string]any{
				"dest":    out,
				"content": "{{ 'yes' if (" + when + ") else 'no' }}",
			}},
		},
	}
	body, err := yaml.Marshal([]any{playbook})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "playbook.yml")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "ansible.cfg")
	if err := os.WriteFile(cfg, []byte("[defaults]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(playbookBin, "-i", "localhost,", "-c", "local", path)
	cmd.Env = append(os.Environ(), "ANSIBLE_CONFIG="+cfg, "ANSIBLE_NOCOLOR=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ansible-playbook: %v\n%s", err, output)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(got)) == "yes"
}
