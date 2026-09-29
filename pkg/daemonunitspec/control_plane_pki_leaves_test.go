package daemonunitspec

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/onebox-faas/faas/pkg/pki"
)

const cpLeafAssertTask = "control_plane_service — assert per-daemon leaves exist (Tier 1)"

func issuedLeaves(boxRole string) map[string]bool {
	out := map[string]bool{}
	for _, r := range pki.RolesForBox(boxRole) {
		out[r.Directory+"/"+r.Filename+".crt"] = true
	}
	return out
}

// The control-plane role asserted the egress server leaf that
// `gregalectl pki init --box-role control-plane` never issues, so the first
// from-scratch split-box control plane could not pass bootstrap. Every leaf
// the role asserts for a box role must be one pki.RolesForBox issues for it.
func TestControlPlaneLeafAssertsMatchIssuedPKI(t *testing.T) {
	path := filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "control_plane_service", "tasks", "main.yml")
	var task map[string]any
	for _, rt := range flattenRoleTasks(path, loadYAMLList(t, path)) {
		if rt.body["name"] == cpLeafAssertTask {
			task = rt.body
		}
	}
	if task == nil {
		t.Fatalf("control_plane_service lost %q", cpLeafAssertTask)
	}
	vars, _ := task["vars"].(map[string]any)
	dirs, _ := vars["faas_pki_leaves"].([]any)
	files, _ := vars["faas_pki_leaf_files"].([]any)
	if len(dirs) == 0 || len(dirs) != len(files) {
		t.Fatalf("leaf lists are malformed: %v / %v", dirs, files)
	}
	when, _ := task["when"].(string)
	all := issuedLeaves("single-box")
	cp := issuedLeaves("control-plane")
	for i := range dirs {
		leaf := dirs[i].(string) + "/" + files[i].(string)
		if !all[leaf] {
			t.Errorf("role asserts %s, which pki.Roles never issues", leaf)
		}
		if !cp[leaf] && !(strings.Contains(when, "faas_box_role") && strings.Contains(when, "'"+dirs[i].(string)+"'")) {
			t.Errorf("role asserts %s on a control-plane box, but pki init --box-role control-plane does not issue it", leaf)
		}
	}

	playbookBin, err := exec.LookPath("ansible-playbook")
	if err != nil {
		t.Log("ansible-playbook not installed; static checks only")
		return
	}
	for _, box := range []string{"control-plane", "single-box"} {
		t.Run(box, func(t *testing.T) {
			issued := issuedLeaves(box)
			for _, leaf := range assertedLeaves(t, playbookBin, task, box) {
				if !issued[leaf] {
					t.Errorf("role asserts %s on a %s box; pki.RolesForBox(%q) does not issue it", leaf, box, box)
				}
			}
		})
	}
}

// assertedLeaves runs the role task's loop, vars and when on localhost and
// returns the leaves it would stat for the given box role.
func assertedLeaves(t *testing.T, playbookBin string, task map[string]any, boxRole string) []string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "leaves.json")
	vars, _ := task["vars"].(map[string]any)
	play := map[string]any{
		"hosts":        "localhost",
		"gather_facts": false,
		"vars":         map[string]any{"faas_box_role": boxRole, "asserted": []any{}},
		"tasks": []map[string]any{
			{
				"ansible.builtin.set_fact": map[string]any{"asserted": "{{ asserted + [item.0 ~ '/' ~ item.1] }}"},
				"loop":                     task["loop"],
				"vars":                     vars,
				"when":                     task["when"],
			},
			{"ansible.builtin.copy": map[string]any{"dest": out, "content": "{{ asserted | to_json }}"}},
		},
	}
	if task["when"] == nil {
		delete(play["tasks"].([]map[string]any)[0], "when")
	}
	body, err := yaml.Marshal([]any{play})
	if err != nil {
		t.Fatal(err)
	}
	pb := filepath.Join(dir, "playbook.yml")
	if err := os.WriteFile(pb, body, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "ansible.cfg")
	if err := os.WriteFile(cfg, []byte("[defaults]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(playbookBin, "-i", "localhost,", "-c", "local", pb)
	cmd.Env = append(os.Environ(), "ANSIBLE_CONFIG="+cfg, "ANSIBLE_NOCOLOR=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ansible-playbook: %v\n%s", err, output)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var leaves []string
	if err := json.Unmarshal(raw, &leaves); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	sort.Strings(leaves)
	if len(leaves) == 0 {
		t.Fatalf("no leaves asserted for %s", boxRole)
	}
	return leaves
}
