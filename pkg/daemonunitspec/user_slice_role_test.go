package daemonunitspec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUserSliceYieldsToTheServices pins the ADR-20261023124955 amendment of
// 2026-11-06. Operator and Ansible SSH sessions in user.slice competed as an
// equal with system.slice, PostgreSQL included, and took more CPU than
// PostgreSQL whenever the 2-vCPU control plane was busy. systemd_slices weights
// user.slice down on every host it converges, and the release-time runtime
// base pre-stage runs at the same low weight.
func TestUserSliceYieldsToTheServices(t *testing.T) {
	roles := filepath.Join(repoRoot(t), "deploy", "ansible", "roles")
	defaults := readRoleDefaults(t, filepath.Join(roles, "systemd_slices"))
	for key, want := range map[string]string{
		"faas_user_slice_cpu_weight": "20",
		"faas_user_slice_io_weight":  "20",
	} {
		if got := yamlScalar(defaults[key]); got != want {
			t.Errorf("systemd_slices default %s = %q, want %q", key, got, want)
		}
	}

	var dest, content string
	var validated, reloads bool
	for _, task := range flattenRoleTasks("tasks/main.yml", loadYAMLList(t, filepath.Join(roles, "systemd_slices", "tasks", "main.yml"))) {
		name, _ := task.body["name"].(string)
		switch name {
		case "systemd_slices — weight user.slice below the services":
			args, _ := task.body["ansible.builtin.copy"].(map[string]any)
			dest, _ = args["dest"].(string)
			content, _ = args["content"].(string)
		case "systemd_slices — validate user.slice weights":
			validated = true
		case "systemd_slices — daemon-reload":
			when, _ := task.body["when"].(string)
			reloads = strings.Contains(when, "user_slice is changed")
		}
	}
	if dest != "/etc/systemd/system/user.slice.d/50-faas-priority.conf" {
		t.Errorf("user.slice drop-in written to %q", dest)
	}
	for _, want := range []string{"[Slice]", "CPUWeight={{ faas_user_slice_cpu_weight }}", "IOWeight={{ faas_user_slice_io_weight }}"} {
		if !strings.Contains(content, want) {
			t.Errorf("user.slice drop-in missing %q:\n%s", want, content)
		}
	}
	if !validated {
		t.Error("systemd_slices has no task validating the user.slice weights")
	}
	if !reloads {
		t.Error("a changed user.slice drop-in does not trigger daemon-reload")
	}

	join, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "ansible", "node_join.yml"))
	if err != nil {
		t.Fatal(err)
	}
	unit := string(join)
	start := strings.Index(unit, "dest: /etc/systemd/system/faas-imaged-prestage.service")
	if start < 0 {
		t.Fatal("node_join.yml no longer installs faas-imaged-prestage.service")
	}
	end := strings.Index(unit[start:], "ExecStart=")
	if end < 0 || !strings.Contains(unit[start:start+end], "CPUWeight=20") {
		t.Error("faas-imaged-prestage.service lost CPUWeight=20 before its ExecStart")
	}
}
