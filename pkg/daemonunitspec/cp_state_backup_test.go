// adr: 1344 — off-host backup of the control plane's host-local state (§8).
package daemonunitspec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCPStateBackupRole(t *testing.T) {
	root := repoRoot(t)
	roleDir := filepath.Join(root, "deploy", "ansible", "roles", "cp_state_backup")
	read := func(rel string) string {
		t.Helper()
		body, err := os.ReadFile(filepath.Join(roleDir, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}

	if !strings.Contains(read("defaults/main.yml"), "faas_cp_state_backup_recipients: []") {
		t.Error("the backup must default off until an operator recipient is configured")
	}
	tasks := flattenRoleTasks("main.yml", loadYAMLList(t, filepath.Join(roleDir, "tasks", "main.yml")))
	byName := map[string]roleTask{}
	for _, task := range tasks {
		if name, ok := task.body["name"].(string); ok {
			byName[name] = task
		}
	}
	load, _ := byName["cp_state_backup — load the control-plane state list"].body["ansible.builtin.include_vars"].(map[string]any)
	if load["file"] != "{{ playbook_dir }}/vars/cp_state_files.yml" {
		t.Errorf("backup does not seal the shared state list: %v", load)
	}
	guard, _ := byName["cp_state_backup — require age recipients, never identities"].body["ansible.builtin.assert"].(map[string]any)
	if that := guard["that"].([]any)[0].(string); !strings.Contains(that, "'^age1[0-9a-z]+$'") {
		t.Errorf("recipients are not restricted to age public keys: %s", that)
	}
	timer, _ := byName["cp_state_backup — enable the daily backup"].body["ansible.builtin.systemd_service"].(map[string]any)
	if enabled, _ := timer["enabled"].(string); !strings.Contains(enabled, "faas_cp_state_backup_recipients | length > 0") {
		t.Errorf("the timer is not gated on configured recipients: %v", timer)
	}

	paths := read("templates/cp-state-backup.paths.j2")
	for _, want := range []string{"faas_cp_state_trees + faas_cp_state_files", "faas_cp_state_exclude", "!{{ p }}"} {
		if !strings.Contains(paths, want) {
			t.Errorf("state list template missing %q", want)
		}
	}
	unit := read("templates/faas-cp-state-backup.service.j2")
	for _, want := range []string{"UMask=0077", "ExecStart=/usr/local/lib/faas/faas-cp-state-backup.sh", "FAAS_RCLONE_BIN=/usr/local/lib/faas/faas-rclone-backup-identity.py"} {
		if !strings.Contains(unit, want) {
			t.Errorf("backup unit missing %q", want)
		}
	}
	script := read("files/faas-cp-state-backup.sh")
	seal := strings.Index(script, "backup seal-cp-state")
	check := strings.Index(script, `"$rclone_bin" check`)
	prune := strings.Index(script, `rm -f -- "${bundles[$i]}"`)
	metric := strings.Index(script, "faas_cp_state_backup_last_success_timestamp_seconds $(date +%s)")
	if seal < 0 || check < seal || prune < check || metric < check {
		t.Error("the backup must seal, push and verify before it prunes or records success")
	}

	playbook, err := os.ReadFile(filepath.Join(root, "deploy", "ansible", "bootstrap.yml"))
	if err != nil {
		t.Fatal(err)
	}
	cp := string(playbook)[:strings.Index(string(playbook), "- name: Bootstrap a Gate-B compute-only node")]
	if pb, cs := strings.Index(cp, "    - role: postgres_backup\n"), strings.Index(cp, "    - role: cp_state_backup\n"); pb < 0 || cs < pb {
		t.Error("the control-plane play must run cp_state_backup after postgres_backup")
	}
}

// A rebuild installs the restored state before PostgreSQL exists, so only
// the restore playbook skips the takeover's promotion check.
func TestCPStateRestorePlaybook(t *testing.T) {
	root := repoRoot(t)
	plays := loadYAMLList(t, filepath.Join(root, "deploy", "ansible", "cp_state_restore.yml"))
	play := plays[0].(map[string]any)
	if play["hosts"] != "control_plane" {
		t.Errorf("restore runs on %v, want the new control plane", play["hosts"])
	}
	vars, _ := play["vars"].(map[string]any)
	if vars["control_plane_standby_require_promoted"] != false {
		t.Errorf("restore does not skip the promotion check: %v", vars)
	}
	defaults, err := os.ReadFile(filepath.Join(root, "deploy", "ansible", "roles", "control_plane_standby", "defaults", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(defaults), "control_plane_standby_require_promoted: true") {
		t.Error("a standby takeover must check promotion by default")
	}
	takeover := flattenRoleTasks("takeover.yml", loadYAMLList(t, filepath.Join(root, "deploy", "ansible", "roles", "control_plane_standby", "tasks", "takeover.yml")))
	if when := whenClauses(takeover[0]); when != "control_plane_standby_require_promoted | bool" {
		t.Errorf("takeover promotion check is not gated: %q", when)
	}
}
