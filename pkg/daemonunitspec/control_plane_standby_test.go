// adr: 1344 — staging the control plane's host files on the warm standby
// and installing them on a takeover (§8).
package daemonunitspec

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestControlPlaneStandbySyncsWhatATakeoverNeeds(t *testing.T) {
	root := repoRoot(t)
	// The standby stages the shared control-plane state list, which the
	// off-host backup seals too.
	roleDefaults, err := os.ReadFile(filepath.Join(root, "deploy", "ansible", "roles", "control_plane_standby", "defaults", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`faas_standby_sync_trees: "{{ faas_cp_state_trees }}"`,
		`faas_standby_sync_files: "{{ faas_cp_state_files }}"`,
		`faas_standby_sync_exclude: "{{ faas_cp_state_exclude }}"`,
	} {
		if !strings.Contains(string(roleDefaults), want) {
			t.Errorf("standby does not stage the shared state list: missing %s", want)
		}
	}
	body, err := os.ReadFile(filepath.Join(root, "deploy", "ansible", "vars", "cp_state_files.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var defaults struct {
		Trees   []string `yaml:"faas_cp_state_trees"`
		Files   []string `yaml:"faas_cp_state_files"`
		Exclude []string `yaml:"faas_cp_state_exclude"`
	}
	if err := yaml.Unmarshal(body, &defaults); err != nil {
		t.Fatal(err)
	}
	// Bootstrap regenerates the HMAC keys when they are missing, and a new
	// CA means re-issuing every node's certificates.
	for _, want := range []string{"/var/lib/faas/audit-hmac.key", "/var/lib/faas/recovery-hmac.key", "/etc/faas/sealed.env"} {
		if !slices.Contains(defaults.Files, want) {
			t.Errorf("standby does not stage %s", want)
		}
	}
	for _, want := range []string{"/etc/faas/secrets", "/etc/faas/tls"} {
		if !slices.Contains(defaults.Trees, want) {
			t.Errorf("standby does not stage %s", want)
		}
	}
	if !slices.Contains(defaults.Exclude, "/etc/faas/secrets/host.age") {
		t.Error("the primary's per-host age identity is copied to the standby")
	}

	sync := flattenRoleTasks("sync.yml", loadYAMLList(t, filepath.Join(root, "deploy", "ansible", "roles", "control_plane_standby", "tasks", "sync.yml")))
	for _, task := range sync {
		name, _ := task.body["name"].(string)
		if _, ok := task.body["ansible.builtin.slurp"]; ok && task.body["no_log"] != true {
			t.Errorf("%q logs secret contents", name)
		}
		if args, ok := task.body["ansible.builtin.copy"].(map[string]any); ok {
			if _, content := args["content"]; content && task.body["no_log"] != true {
				t.Errorf("%q logs secret contents", name)
			}
			if args["owner"] != "root" || args["mode"] != "0600" {
				t.Errorf("%q stages a file readable beyond root: %v", name, args)
			}
		}
		if args, ok := task.body["ansible.builtin.file"].(map[string]any); ok && args["state"] == "directory" {
			if args["owner"] != "root" || args["mode"] != "0700" {
				t.Errorf("%q creates a staging directory readable beyond root: %v", name, args)
			}
		}
	}
}

func TestControlPlaneStandbyTakeover(t *testing.T) {
	root := repoRoot(t)
	takeover := flattenRoleTasks("takeover.yml", loadYAMLList(t, filepath.Join(root, "deploy", "ansible", "roles", "control_plane_standby", "tasks", "takeover.yml")))
	if len(takeover) == 0 {
		t.Fatal("takeover.yml has no tasks")
	}
	first := takeover[0].body
	args, _ := first["ansible.builtin.command"].(map[string]any)
	if !strings.Contains(strings.Join(anyStrings(args["argv"]), " "), "pg_is_in_recovery()") || !strings.Contains(first["failed_when"].(string), "!= 'f'") {
		t.Errorf("takeover must first refuse a host whose database is still a standby: %v", first)
	}
	var install map[string]any
	pathCheck, firstRootWrite := -1, -1
	for i, task := range takeover {
		name, _ := task.body["name"].(string)
		if name == "control_plane_standby — install the files with the primary's ownership" {
			install = task.body
		}
		if name == "control_plane_standby — refuse staged paths outside the control-plane state list" {
			pathCheck = i
		}
		_, group := task.body["ansible.builtin.group"]
		_, user := task.body["ansible.builtin.user"]
		_, file := task.body["ansible.builtin.file"]
		_, shell := task.body["ansible.builtin.shell"]
		if firstRootWrite < 0 && (group || user || file || shell) {
			firstRootWrite = i
		}
	}
	// A restored bundle is encrypted, not signed: its index must not choose
	// arbitrary root-owned paths (CodeQL triage 2026-10-25).
	if pathCheck < 0 || firstRootWrite < 0 || pathCheck > firstRootWrite {
		t.Errorf("takeover must refuse unlisted staged paths before creating users or installing files (check at %d, first write at %d)", pathCheck, firstRootWrite)
	} else {
		check := takeover[pathCheck].body
		vars, _ := check["vars"].(map[string]any)
		expr, _ := vars["control_plane_standby_unlisted"].(string)
		for _, want := range []string{"faas_standby_sync_files", "faas_standby_sync_trees", "faas_standby_sync_exclude", "control_plane_standby_index.directories", `\.\.?(/|$)`} {
			if !strings.Contains(expr, want) {
				t.Errorf("path check does not consider %q:\n%s", want, expr)
			}
		}
		loaded := false
		for _, task := range takeover[:pathCheck] {
			args, _ := task.body["ansible.builtin.include_vars"].(map[string]any)
			if f, _ := args["file"].(string); strings.HasSuffix(f, "/vars/cp_state_files.yml") {
				loaded = true
			}
		}
		if !loaded {
			t.Error("takeover must load vars/cp_state_files.yml before checking staged paths")
		}
	}
	script, _ := install["ansible.builtin.shell"].(string)
	for _, want := range []string{"umask 077", "base64 -d", "chown", "chmod", "mv -T"} {
		if !strings.Contains(script, want) {
			t.Errorf("file install missing %q:\n%s", want, script)
		}
	}

	plays := loadYAMLList(t, filepath.Join(root, "deploy", "ansible", "standby_takeover.yml"))
	play := plays[0].(map[string]any)
	if play["hosts"] != "postgres_standby" {
		t.Errorf("takeover runs on %v, want the promoted standby", play["hosts"])
	}
}

func anyStrings(v any) []string {
	var out []string
	for _, item := range v.([]any) {
		out = append(out, item.(string))
	}
	return out
}
