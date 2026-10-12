package daemonunitspec

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// Spec §11: every slice that parents VM scopes needs an always-active fence
// pin slice. Without one, any systemd daemon-reload empties the parent's
// cgroup.subtree_control and strips memory.max, cpu.weight and cpu.max from
// every live VM.
func TestSystemdSlicesPinsEveryVMParent(t *testing.T) {
	want := []string{"faas-cp-build"}
	for _, plan := range api.Plans {
		want = append(want, "faas-"+plan.SliceName())
	}
	slices.Sort(want)

	roleDir := filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "systemd_slices")
	var written, started []string
	var content string
	for _, task := range flattenRoleTasks("tasks/main.yml", loadYAMLList(t, filepath.Join(roleDir, "tasks", "main.yml"))) {
		name, _ := task.body["name"].(string)
		switch name {
		case "systemd_slices — write per-VM fence pin slices":
			args, _ := task.body["ansible.builtin.copy"].(map[string]any)
			content, _ = args["content"].(string)
			written = loopItems(task.body)
		case "systemd_slices — start per-VM fence pin slices":
			started = loopItems(task.body)
		}
	}
	slices.Sort(written)
	slices.Sort(started)
	if !slices.Equal(written, want) {
		t.Errorf("pin slices written for %v, want every VM parent %v", written, want)
	}
	if !slices.Equal(started, want) {
		t.Errorf("pin slices started for %v, want every VM parent %v", started, want)
	}
	// One directive per controller the VM scopes need: cpu, memory, pids.
	for _, directive := range []string{"CPUWeight=", "MemoryAccounting=yes", "TasksAccounting=yes"} {
		if !strings.Contains(content, directive) {
			t.Errorf("pin slice content missing %q:\n%s", directive, content)
		}
	}

	unit := UnitVmmd()
	for _, parent := range want {
		pin := parent + "-fence.slice"
		if !slices.Contains(unit.Wants, pin) || !slices.Contains(unit.After, pin) {
			t.Errorf("vmmd unit must Want and start After %s", pin)
		}
	}
}
