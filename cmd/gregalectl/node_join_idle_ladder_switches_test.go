package main

// adr: 20261107122928 idle ladder — the per-node idle ladder switches reach
// the restarted vmmd on every rollout, set nothing while off, accept only
// off, observe or on, and the guarded app pause needs on.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestNodeJoinRendersTheIdleLadderSwitches(t *testing.T) {
	dropin := nodeJoinTask(t, "Install the idle ladder switches")
	copyArgs, ok := dropin["ansible.builtin.copy"].(map[string]any)
	if !ok || copyArgs["dest"] != "/etc/systemd/system/faas-vmmd.service.d/zz-faas-idle-ladder.conf" {
		t.Fatalf("idle ladder drop-in task = %v", dropin)
	}
	content, _ := copyArgs["content"].(string)
	assertGuardedEnv(t, "node_join's drop-in", content, "vmmd_idle_ladder_apps", "FAAS_IDLE_LADDER_APPS")
	if !strings.Contains(content, "{% if (vmmd_idle_ladder | default('off')) != 'off' %}\nEnvironment=FAAS_IDLE_LADDER={{ vmmd_idle_ladder }}\n{% endif %}") {
		t.Fatalf("FAAS_IDLE_LADDER is not set only while the switch is not off:\n%s", content)
	}
	if strings.Contains(content, "FAAS_IDLE_LADDER_COMPRESS") {
		t.Fatal("node_join turns on L3/L4 compression, which needs a metal run first")
	}
	playbook := nodeJoinPlaybook(t)
	for _, want := range []string{
		"(vmmd_idle_ladder | default('off')) in ['off', 'observe', 'on']",
		"not (vmmd_idle_ladder_apps | default(false) | bool) or (vmmd_idle_ladder | default('off')) == 'on'",
	} {
		if nodeJoinTask(t, "Validate the idle ladder switches") == nil || !strings.Contains(playbook, want) {
			t.Fatalf("node_join does not validate %q", want)
		}
	}
	// Written before the drain decision, like the adoption switches, so a
	// no-drain restart starts vmmd with them.
	if strings.Index(playbook, "zz-faas-idle-ladder.conf") > strings.Index(playbook, "- adoption-check") {
		t.Fatal("the idle ladder drop-in is rendered after the adoption check")
	}
}

func TestIdleLadderGroupVarsName(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "group_vars", "compute_nodes", "vmmd_idle_ladder.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var vars map[string]any
	if err := yaml.Unmarshal(raw, &vars); err != nil {
		t.Fatalf("parse vmmd_idle_ladder.yml: %v", err)
	}
	for _, key := range []string{"vmmd_idle_ladder", "vmmd_idle_ladder_apps", "vmmd_idle_ladder_observe_nodes", "vmmd_idle_ladder_on_nodes", "vmmd_idle_ladder_apps_nodes"} {
		if _, ok := vars[key]; !ok {
			t.Errorf("vmmd_idle_ladder.yml has no %s", key)
		}
	}
	mode, _ := vars["vmmd_idle_ladder"].(string)
	for _, want := range []string{"vmmd_idle_ladder_on_nodes", "vmmd_idle_ladder_observe_nodes", "'off'"} {
		if !strings.Contains(mode, want) {
			t.Errorf("vmmd_idle_ladder = %q does not use %s", mode, want)
		}
	}
	if strings.Contains(string(raw), "idle_ladder_compress") && !strings.Contains(string(raw), "stay off") {
		t.Error("vmmd_idle_ladder.yml mentions compression without keeping it off")
	}
}
