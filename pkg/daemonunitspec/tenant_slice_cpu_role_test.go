package daemonunitspec

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestTenantPlanSubSlicesShareOneWeight pins ADR-20261109172421 §3: a busy
// host shares CPU by instance size, not by plan, so every per-plan tenant
// sub-slice gets the same CPUWeight.
//
// adr: 20261109172421
func TestTenantPlanSubSlicesShareOneWeight(t *testing.T) {
	roleDir := filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "systemd_slices")
	if got := yamlScalar(readRoleDefaults(t, roleDir)["faas_tenant_plan_slice_cpu_weight"]); got != "16" {
		t.Errorf("faas_tenant_plan_slice_cpu_weight default = %q, want 16", got)
	}

	var want []string
	for _, plan := range api.Plans {
		want = append(want, strings.TrimPrefix(plan.SliceName(), "tenant-"))
	}
	slices.Sort(want)

	var plans []string
	var dest, content string
	var validated bool
	for _, task := range flattenRoleTasks("tasks/main.yml", loadYAMLList(t, filepath.Join(roleDir, "tasks", "main.yml"))) {
		switch name, _ := task.body["name"].(string); name {
		case "systemd_slices — write per-plan tenant sub-slices (CPUWeight only)":
			args, _ := task.body["ansible.builtin.copy"].(map[string]any)
			dest, _ = args["dest"].(string)
			content, _ = args["content"].(string)
			plans = loopItems(task.body)
		case "systemd_slices — validate the tenant sub-slice weight":
			validated = true
		}
	}
	slices.Sort(plans)
	if !slices.Equal(plans, want) {
		t.Errorf("sub-slices written for %v, want one per plan %v", plans, want)
	}
	if dest != "/etc/systemd/system/faas-tenant-{{ item }}.slice" {
		t.Errorf("sub-slice dest = %q", dest)
	}
	if !strings.Contains(content, "CPUWeight={{ faas_tenant_plan_slice_cpu_weight }}") || strings.Contains(content, "item.weight") {
		t.Errorf("sub-slices must all take faas_tenant_plan_slice_cpu_weight:\n%s", content)
	}
	if !validated {
		t.Error("systemd_slices has no task validating faas_tenant_plan_slice_cpu_weight")
	}
}

// TestNodeJoinCapsTheTenantSliceBelowTheHost pins ADR-20261109172421 §4: the
// platform's CPU is protected at the tenant slice. node_join.yml writes
// CPUQuota = (host threads - faas_tenant_cpu_reserve_threads) x 100%, at
// least one thread, into the same drop-in as the memory fence.
//
// adr: 20261109172421
func TestNodeJoinCapsTheTenantSliceBelowTheHost(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "ansible", "node_join.yml"))
	if err != nil {
		t.Fatal(err)
	}
	play := string(body)
	for _, want := range []string{
		"faas_tenant_cpu_reserve_threads | default(2) | string is match('^[0-9]+$')",
		"{{ ([1, (ansible_processor_vcpus | int)\n                  - (faas_tenant_cpu_reserve_threads | default(2) | int)] | max) * 100 }}",
		"CPUQuota={{ faas_join_tenant_cpu_quota_pct }}%",
	} {
		if !strings.Contains(play, want) {
			t.Errorf("node_join.yml is missing the tenant CPU quota contract %q", want)
		}
	}
	derive := strings.Index(play, "- name: Derive the tenant slice CPU quota")
	quota := strings.Index(play, "CPUQuota={{ faas_join_tenant_cpu_quota_pct }}%")
	fence := strings.Index(play, "MemoryMax={{ faas_node_sizing.tenant_slice_max_mb }}M")
	reload := strings.Index(play, "- name: Reload systemd so the re-fenced tenant slice takes effect")
	if derive < 0 || quota < 0 || fence < 0 || reload < 0 {
		t.Fatalf("tenant CPU quota contract incomplete: derive=%d quota=%d fence=%d reload=%d", derive, quota, fence, reload)
	}
	// One drop-in carries both fences, written after the quota is derived
	// and live before the reload that precedes vmmd's start.
	if !(derive < fence && fence < quota && quota < reload) {
		t.Errorf("want derive < memory fence < CPU quota < reload: %d %d %d %d", derive, fence, quota, reload)
	}
}
