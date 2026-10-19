// adr: 1344 — the warm standby records the primary's release (§8 "Takeover
// speed"), because a dead primary cannot be asked which release to deploy.
package daemonunitspec

import (
	"path/filepath"
	"strings"
	"testing"
)

func standbyRoleTasks(t *testing.T, file string) map[string]roleTask {
	t.Helper()
	tasks := flattenRoleTasks(file, loadYAMLList(t, filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "control_plane_standby", "tasks", file)))
	byName := map[string]roleTask{}
	for _, task := range tasks {
		if name, ok := task.body["name"].(string); ok {
			byName[name] = task
		}
	}
	return byName
}

func TestStandbyRecordsThePrimarysRelease(t *testing.T) {
	sync := standbyRoleTasks(t, "sync.yml")
	for _, name := range []string{
		"control_plane_standby — find the primary's current release",
		"control_plane_standby — read the primary's release manifest",
		"control_plane_standby — read the primary's per-unit versions",
	} {
		task, ok := sync[name]
		if !ok {
			t.Errorf("sync has no task %q", name)
			continue
		}
		if task.body["delegate_to"] != "{{ faas_postgres_primary_inventory_host }}" {
			t.Errorf("%q does not read the primary", name)
		}
	}
	current, _ := sync["control_plane_standby — find the primary's current release"].body["ansible.builtin.stat"].(map[string]any)
	if current["path"] != "/opt/faas/current" || current["follow"] != false {
		t.Errorf("release lookup must read the /opt/faas/current link itself: %v", current)
	}
	record := sync["control_plane_standby — record the primary's release"]
	dest, _ := record.body["ansible.builtin.copy"].(map[string]any)
	if dest["dest"] != "{{ faas_standby_stage_dir }}/release.json" || dest["mode"] != "0600" {
		t.Errorf("release record = %v", dest)
	}
	vars, _ := record.body["vars"].(map[string]any)
	fields, _ := vars["control_plane_standby_release"].(map[string]any)
	for _, key := range []string{"primary", "release_dir", "git_sha", "manifest_hash", "unit_versions"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("release record misses %q", key)
		}
	}
	// A primary without a release keeps the previous record instead of
	// failing the converge or writing an empty one.
	if !strings.Contains(whenClauses(record), "control_plane_standby_release_manifest.content is defined") {
		t.Errorf("release record is written without a manifest: %s", whenClauses(record))
	}

	takeover := standbyRoleTasks(t, "takeover.yml")
	read, _ := takeover["control_plane_standby — read the primary's recorded release"].body["ansible.builtin.slurp"].(map[string]any)
	if read["src"] != "{{ faas_standby_stage_dir }}/release.json" {
		t.Errorf("takeover reads the release from %v", read["src"])
	}
	next, _ := takeover["control_plane_standby — next steps"].body["ansible.builtin.debug"].(map[string]any)
	msg, _ := next["msg"].(string)
	for _, want := range []string{"release.git_sha", "release.manifest_hash", "release.unit_versions"} {
		if !strings.Contains(msg, want) {
			t.Errorf("takeover's next steps do not print %s", want)
		}
	}
}
