package main

// adr: 20261017 connection survival — the per-node instance bridge switches
// reach the restarted vmmd on every rollout, set nothing while off, and the
// bridge unit cannot be turned on without the unprivileged bridge.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var bridgeSwitches = []struct{ variable, env string }{
	{"vmmd_bridge_unprivileged", "FAAS_BRIDGE_UNPRIVILEGED"},
	{"vmmd_instance_bridge_sessions", "FAAS_INSTANCE_BRIDGE_SESSIONS"},
	{"vmmd_instance_bridge_unit", "FAAS_INSTANCE_BRIDGE_UNIT"},
}

// assertGuardedEnv checks that body sets env=1 only inside an if on variable.
func assertGuardedEnv(t *testing.T, where, body, variable, env string) {
	t.Helper()
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "Environment="+env+"=1" {
			continue
		}
		if i == 0 || !strings.Contains(lines[i-1], "{% if "+variable+" ") {
			t.Errorf("%s sets %s outside an if on %s", where, env, variable)
		}
		return
	}
	t.Errorf("%s does not set %s:\n%s", where, env, body)
}

func TestNodeJoinRendersTheBridgeSwitches(t *testing.T) {
	dropin := nodeJoinTask(t, "Install the instance bridge switches")
	copyArgs, ok := dropin["ansible.builtin.copy"].(map[string]any)
	if !ok || copyArgs["dest"] != "/etc/systemd/system/faas-vmmd.service.d/zz-faas-bridge.conf" {
		t.Fatalf("bridge drop-in task = %v", dropin)
	}
	content, _ := copyArgs["content"].(string)
	for _, sw := range bridgeSwitches {
		assertGuardedEnv(t, "node_join's drop-in", content, sw.variable, sw.env)
	}
	check := nodeJoinTask(t, "Validate the instance bridge switches")
	if check == nil || !strings.Contains(nodeJoinPlaybook(t), "not (vmmd_instance_bridge_unit | default(false) | bool) or (vmmd_bridge_unprivileged | default(false) | bool)") {
		t.Fatal("node_join renders the bridge switches without requiring the unprivileged bridge for units")
	}
	// Written before the drain decision, like the adoption switches, so a
	// no-drain restart starts vmmd with them.
	playbook := nodeJoinPlaybook(t)
	if strings.Index(playbook, "zz-faas-bridge.conf") > strings.Index(playbook, "- adoption-check") {
		t.Fatal("the bridge drop-in is rendered after the adoption check")
	}
}

func TestVMMDServiceBridgeSwitchesDefaultOff(t *testing.T) {
	role := filepath.Join("..", "..", "deploy", "ansible", "roles", "vmmd_service")
	defaults, err := os.ReadFile(filepath.Join(role, "defaults", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	template, err := os.ReadFile(filepath.Join(role, "templates", "99-faas-bridge.conf.j2"))
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := os.ReadFile(filepath.Join(role, "tasks", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, sw := range bridgeSwitches {
		if !strings.Contains(string(defaults), "\n"+sw.variable+": false\n") {
			t.Errorf("vmmd_service defaults lack %s: false", sw.variable)
		}
		assertGuardedEnv(t, "the role drop-in", string(template), sw.variable, sw.env)
	}
	if !strings.Contains(string(tasks), "not (vmmd_instance_bridge_unit | bool) or (vmmd_bridge_unprivileged | bool)") {
		t.Error("vmmd_service does not require the unprivileged bridge for bridge units")
	}
}
