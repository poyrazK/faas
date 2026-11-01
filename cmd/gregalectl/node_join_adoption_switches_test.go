package main

// adr: 20261017 vmmd restart adoption — the per-node adoption switches reach
// the restarted vmmd and the rollout's adoption check with the same values,
// and they are off unless a host turns them on.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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
	// The drain is skipped only when the serving vmmd hands its guests off.
	if nodeJoinTask(t, "Read whether the serving vmmd hands its guests off") == nil {
		t.Fatal("node_join does not read whether the serving vmmd adopts")
	}
	decide, _ := nodeJoinTask(t, "Decide whether this rollout restarts vmmd without draining")["ansible.builtin.set_fact"].(map[string]any)
	if skip, _ := decide["faas_join_skip_drain"].(string); !strings.Contains(skip, "faas_join_serving_vmmd_adopts.stdout") {
		t.Errorf("faas_join_skip_drain = %q ignores whether the serving vmmd hands off", skip)
	}
	// After a no-drain restart, only guests the check already found gone
	// are excused from the adoption report.
	report, _ := nodeJoinTask(t, "Wait for the restarted vmmd to report a clean adoption")["ansible.builtin.command"].(map[string]any)
	argv, _ := report["argv"].([]any)
	gone := false
	for i, a := range argv {
		if a == "--already-gone" && i+1 < len(argv) {
			next, _ := argv[i+1].(string)
			gone = strings.Contains(next, "faas_join_adoption_check.stdout") && strings.Contains(next, ".gone")
		}
	}
	if !gone {
		t.Errorf("adoption-report argv %v does not pass the check's gone guests", argv)
	}
	// The switches are written before the drain decision asks the candidate.
	playbook := nodeJoinPlaybook(t)
	if strings.Index(playbook, "zz-faas-adoption.conf") > strings.Index(playbook, "- adoption-check") {
		t.Fatal("the adoption drop-in is rendered after the adoption check")
	}
}

// A no-drain restart must not take its row out of service: the scheduler
// evacuates an unavailable node, and on production-us (rc.312, rc.313) the
// release install parked every guest on fsn-2 just before vmmd restarted.
func TestNodeJoinNoDrainInstallKeepsTheRowActive(t *testing.T) {
	const want = "--defer-activation={{ 'false' if (faas_join_skip_drain | default(false) | bool) else 'true' }}"
	for _, name := range []string{
		"Install the verified release, keeping a drained row drained",
		"Retry staged release installation after SBoM baseline initialization",
	} {
		install, _ := nodeJoinTask(t, name)["ansible.builtin.command"].(map[string]any)
		argv, _ := install["argv"].([]any)
		var flags []string
		for _, a := range argv {
			if s, _ := a.(string); strings.HasPrefix(s, "--defer-activation") {
				flags = append(flags, s)
			}
		}
		if len(flags) != 1 || flags[0] != want {
			t.Errorf("%s: activation flags %q, want only %q", name, flags, want)
		}
	}
	// The install comes after the no-drain decision it reads.
	playbook := nodeJoinPlaybook(t)
	if strings.Index(playbook, "faas_join_skip_drain: >-") > strings.Index(playbook, "release\n          - install") {
		t.Fatal("the release install runs before the no-drain decision")
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
	// Production: the fleet writes adoption blocks (step 1), and adoption is
	// on for the named canary nodes (step 2), never a fleet-wide switch.
	fleet, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "group_vars", "compute_nodes", "vmmd_adoption.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var vars map[string]any
	if err := yaml.Unmarshal(fleet, &vars); err != nil {
		t.Fatal(err)
	}
	if vars["vmmd_adoption_record"] != true {
		t.Error("compute_nodes group vars do not turn adoption records on")
	}
	if adopt, _ := vars["vmmd_adopt_running_vms"].(string); !strings.Contains(adopt, "in vmmd_adoption_canary_nodes") {
		t.Errorf("vmmd_adopt_running_vms = %v, want it limited to vmmd_adoption_canary_nodes", vars["vmmd_adopt_running_vms"])
	}
	canary, _ := vars["vmmd_adoption_canary_nodes"].([]any)
	if len(canary) == 0 {
		t.Errorf("vmmd_adoption_canary_nodes = %v, want named canary nodes", vars["vmmd_adoption_canary_nodes"])
	}
	seen := map[string]bool{}
	for _, n := range canary {
		name, _ := n.(string)
		if !strings.HasSuffix(name, ".faas") || seen[name] {
			t.Errorf("vmmd_adoption_canary_nodes = %v, want distinct canonical node names", canary)
		}
		seen[name] = true
	}
	if _, set := vars["vmmd_adopt_admitted_runtimes"]; set {
		t.Error("compute_nodes group vars set vmmd_adopt_admitted_runtimes")
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
