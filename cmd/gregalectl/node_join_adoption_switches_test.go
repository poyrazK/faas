package main

// adr: 20261017 vmmd restart adoption — the per-node adoption switches reach
// the restarted vmmd and the rollout's adoption check with the same values,
// and they are off unless a host turns them on.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var adoptionSwitchEnv = []string{"FAAS_VMMD_ADOPTION_RECORD", "FAAS_VMMD_ADOPT_RUNNING_VMS", "FAAS_VMMD_ADOPT_ADMITTED_RUNTIMES"}

func TestNodeJoinRendersTheAdoptionSwitches(t *testing.T) {
	dropin := nodeJoinTask(t, "Install the vmmd restart adoption switches")
	copyArgs, ok := dropin["ansible.builtin.copy"].(map[string]any)
	if !ok || copyArgs["dest"] != "/etc/systemd/system/faas-vmmd.service.d/zz-faas-adoption.conf" {
		t.Fatalf("adoption drop-in task = %v", dropin)
	}
	content, _ := copyArgs["content"].(string)
	check := nodeJoinTask(t, "Ask the candidate vmmd whether it adopts every live guest")
	checkEnv, ok := check["environment"].(map[string]any)
	if !ok {
		t.Fatal("adoption-check runs without the adoption switches in its environment")
	}
	for _, name := range adoptionSwitchEnv {
		var rendered string
		for _, line := range strings.Split(content, "\n") {
			if value, ok := strings.CutPrefix(line, "Environment="+name+"="); ok {
				rendered = value
			}
		}
		if rendered == "" {
			t.Fatalf("drop-in does not set %s:\n%s", name, content)
		}
		// The check must judge with exactly what the restarted vmmd gets.
		if got, _ := checkEnv[name].(string); got != rendered {
			t.Errorf("adoption-check %s = %q, drop-in renders %q", name, got, rendered)
		}
	}
	if nodeJoinTask(t, "Validate the vmmd restart adoption switches") == nil {
		t.Fatal("node_join renders the switches without validating their order")
	}
	// The switches are written before the drain decision asks the candidate.
	playbook := nodeJoinPlaybook(t)
	if strings.Index(playbook, "zz-faas-adoption.conf") > strings.Index(playbook, "- adoption-check") {
		t.Fatal("the adoption drop-in is rendered after the adoption check")
	}
}

func TestVMMDServiceAdoptionSwitchesDefaultOff(t *testing.T) {
	role := filepath.Join("..", "..", "deploy", "ansible", "roles", "vmmd_service")
	defaults, err := os.ReadFile(filepath.Join(role, "defaults", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"vmmd_adoption_record: false", "vmmd_adopt_running_vms: false", "vmmd_adopt_admitted_runtimes: false"} {
		if !strings.Contains(string(defaults), "\n"+v+"\n") {
			t.Errorf("vmmd_service defaults lack %q", v)
		}
	}
	// Production step 1: the fleet writes adoption blocks, but adoption is a
	// per-host canary, never a fleet-wide group var.
	fleet, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "group_vars", "compute_nodes", "vmmd_adoption.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fleet), "\nvmmd_adoption_record: true\n") {
		t.Error("compute_nodes group vars do not turn adoption records on")
	}
	for _, v := range []string{"vmmd_adopt_running_vms:", "vmmd_adopt_admitted_runtimes:"} {
		if strings.Contains(string(fleet), "\n"+v) {
			t.Errorf("compute_nodes group vars set %s fleet-wide", v)
		}
	}
	template, err := os.ReadFile(filepath.Join(role, "templates", "99-faas-adoption.conf.j2"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range adoptionSwitchEnv {
		if !strings.Contains(string(template), "Environment="+name+"=") {
			t.Errorf("role drop-in does not set %s", name)
		}
	}
}
