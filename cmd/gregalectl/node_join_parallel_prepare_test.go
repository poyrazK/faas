package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// ADR-1360: prepare runs on every serving node at once, so it must never
// converge roles or claim a bootstrap contract it did not converge, while a
// stale-contract activation must not redo the pre-stage it already finished.
func TestNodeJoinParallelPrepareContract(t *testing.T) {
	type task struct {
		Name   string `yaml:"name"`
		When   any    `yaml:"when"`
		Assert *struct {
			That []string `yaml:"that"`
		} `yaml:"ansible.builtin.assert"`
		IncludeRole *struct {
			Name      string `yaml:"name"`
			TasksFrom string `yaml:"tasks_from"`
		} `yaml:"ansible.builtin.include_role"`
	}
	type play struct {
		Name     string `yaml:"name"`
		PreTasks []task `yaml:"pre_tasks"`
		Tasks    []task `yaml:"tasks"`
	}
	read := func(name string) []play {
		t.Helper()
		body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", name))
		if err != nil {
			t.Fatal(err)
		}
		var plays []play
		if err := yaml.Unmarshal(body, &plays); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		return plays
	}
	when := func(tk task) string {
		switch v := tk.When.(type) {
		case string:
			return v
		case []any:
			parts := make([]string, 0, len(v))
			for _, p := range v {
				parts = append(parts, p.(string))
			}
			return strings.Join(parts, " && ")
		}
		return ""
	}
	const prepare, notPrepare, notActivate = "== 'prepare'", "!= 'prepare'", "!= 'activate'"

	ended := 0
	for _, p := range read("bootstrap.yml") {
		for _, tk := range p.PreTasks {
			if strings.HasPrefix(tk.Name, "End managed") {
				ended++
				if w := when(tk); !strings.Contains(w, "faas_join_bootstrap_contract_current") || !strings.Contains(w, prepare) {
					t.Fatalf("bootstrap.yml %q must end the host when the contract is current or the phase is prepare: %q", tk.Name, w)
				}
			}
		}
	}
	if ended != 3 {
		t.Fatalf("bootstrap.yml fast-path end_host tasks = %d, want 3", ended)
	}

	tasks := map[string]task{}
	for _, p := range read("node_join.yml") {
		for _, tk := range p.Tasks {
			tasks[tk.Name] = tk
		}
	}
	gate, ok := tasks["Require an existing managed host for non-disruptive rollout preparation"]
	if !ok || gate.Assert == nil || len(gate.Assert.That) != 1 || gate.Assert.That[0] != "faas_join_managed_host | bool" {
		t.Fatalf("prepare must require an existing managed host, not a current contract: %+v", gate.Assert)
	}
	if w := when(tasks["Persist the exact bootstrap source contract"]); !strings.Contains(w, notPrepare) {
		t.Fatalf("preparation must not record the bootstrap contract: %q", w)
	}
	grype, ok := tasks["Refresh the Grype vulnerability database during preparation"]
	if !ok || grype.IncludeRole == nil || grype.IncludeRole.Name != "supply_chain_scanners" || grype.IncludeRole.TasksFrom != "grype_db" || !strings.Contains(when(grype), prepare) {
		t.Fatalf("preparation must refresh the Grype database: %+v %q", grype.IncludeRole, when(grype))
	}
	for _, name := range []string{
		"Create the release-bound runtime-base pre-stage directory",
		"Extract the signed candidate for runtime-base pre-staging",
		"Inspect the active vmmd binary before runtime pre-stage",
		"Render fresh-host runtime configuration before pre-stage",
		"Bootstrap fresh-host vmmd from the verified release candidate",
		"Install the runtime-base pre-stage one-shot",
		"Reload systemd for runtime-base pre-staging",
		"Pre-stage release-bound runtime bases before draining the node",
	} {
		w := when(tasks[name])
		if !strings.Contains(w, notActivate) {
			t.Fatalf("activation from the top must skip the finished pre-stage task %q: %q", name, w)
		}
		// Ansible stops at the first false condition; the skipped vmmd
		// inspection leaves no .stat to read on activation from the top.
		if strings.Contains(w, "faas_join_active_vmmd") && strings.Index(w, notActivate) > strings.Index(w, "faas_join_active_vmmd") {
			t.Fatalf("%q must check the phase before faas_join_active_vmmd: %q", name, w)
		}
	}
	if _, err := os.Stat(filepath.Join("..", "..", "deploy", "ansible", "roles", "supply_chain_scanners", "tasks", "grype_db.yml")); err != nil {
		t.Fatalf("Grype database tasks file: %v", err)
	}
}

func TestProbeJoinBootstrapContract(t *testing.T) {
	old := ansiblePlaybookRunner
	t.Cleanup(func() { ansiblePlaybookRunner = old })
	const want = "sha256:" + "ab"
	for _, tc := range []struct {
		name     string
		recorded string
		runErr   error
		current  bool
		wantErr  bool
	}{
		{name: "current", recorded: want + "\n", current: true},
		{name: "stale", recorded: "sha256:cd\n"},
		{name: "never converged", recorded: "\n"},
		{name: "probe failed", runErr: errors.New("unreachable"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotArgs []string
			ansiblePlaybookRunner = func(_ context.Context, _ string, args []string) error {
				gotArgs = args
				if tc.runErr != nil {
					return tc.runErr
				}
				for i, a := range args {
					if a == "-e" && i+1 < len(args) && strings.HasPrefix(args[i+1], "faas_join_contract_probe_out=") {
						return os.WriteFile(strings.TrimPrefix(args[i+1], "faas_join_contract_probe_out="), []byte(tc.recorded), 0o600)
					}
				}
				return errors.New("probe output path missing")
			}
			current, err := probeJoinBootstrapContract(t.Context(), "/ansible", []string{"-i", "hosts.ini"}, "fsn-2", want, t.TempDir())
			if (err != nil) != tc.wantErr || current != tc.current {
				t.Fatalf("probe = %v, %v; want current=%v err=%v", current, err, tc.current, tc.wantErr)
			}
			if joined := strings.Join(gotArgs, " "); !strings.Contains(joined, "--limit fsn-2") || !strings.HasSuffix(joined, filepath.Join("/ansible", "node_join_contract_probe.yml")) {
				t.Fatalf("probe args = %v", gotArgs)
			}
		})
	}
}
