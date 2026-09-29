package daemonunitspec

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// storageSelectionFacts returns the two Jinja expressions the storage role
// uses to turn discovered "device|size" lines into disks and sizes.
func storageSelectionFacts(t *testing.T) (disks, sizes string) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "storage", "tasks", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var tasks []map[string]any
	if err := yaml.Unmarshal(body, &tasks); err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task["name"] != "storage — select automatically discovered disks" {
			continue
		}
		facts, _ := task["ansible.builtin.set_fact"].(map[string]any)
		disks, _ = facts["faas_storage_selected_disks"].(string)
		sizes, _ = facts["faas_storage_selected_sizes"].(string)
		if disks == "" || sizes == "" {
			t.Fatalf("storage selection task lost its set_fact expressions: %v", facts)
		}
		return disks, sizes
	}
	t.Fatal("storage role has no 'select automatically discovered disks' task")
	return "", ""
}

// The automatic disk selection must not depend on how a Jinja string literal
// unescapes a backslash. ansible-core 2.16 kept '\\|' as an escaped pipe;
// 2.19+ does not, so the old regex became an alternation and mkfs.xfs was
// handed "/dev/sdb|" on the first fresh host bootstrapped from a current
// controller. When ansible-playbook is installed, evaluate the role's exact
// expressions on it too.
func TestStorageRoleDiskSelectionIsPortableAcrossAnsibleCore(t *testing.T) {
	disks, sizes := storageSelectionFacts(t)
	for name, expr := range map[string]string{"faas_storage_selected_disks": disks, "faas_storage_selected_sizes": sizes} {
		if strings.Contains(expr, `\`) {
			t.Errorf("%s depends on backslash unescaping: %s", name, expr)
		}
		if !strings.Contains(expr, "map('split', '|')") {
			t.Errorf("%s does not split the discovery separator: %s", name, expr)
		}
	}

	playbookBin, err := exec.LookPath("ansible-playbook")
	if err != nil {
		t.Log("ansible-playbook not installed; static checks only")
		return
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "selection.json")
	playbook := map[string]any{
		"hosts":        "localhost",
		"gather_facts": false,
		"vars": map[string]any{
			"storage_blank_disks": map[string]any{
				"stdout_lines": []string{"/dev/sdb|64424509440", "/dev/nvme0n2|107374182400"},
			},
		},
		"tasks": []map[string]any{
			{"ansible.builtin.set_fact": map[string]any{
				"faas_storage_selected_disks": disks,
				"faas_storage_selected_sizes": sizes,
			}},
			{"ansible.builtin.copy": map[string]any{
				"dest":    out,
				"content": "{{ {'disks': faas_storage_selected_disks, 'sizes': faas_storage_selected_sizes} | to_json }}",
			}},
		},
	}
	body, err := yaml.Marshal([]any{playbook})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "playbook.yml")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	// An empty config isolates the run from any ansible.cfg in the tree.
	cfg := filepath.Join(dir, "ansible.cfg")
	if err := os.WriteFile(cfg, []byte("[defaults]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(playbookBin, "-i", "localhost,", "-c", "local", path)
	cmd.Env = append(os.Environ(), "ANSIBLE_CONFIG="+cfg, "ANSIBLE_NOCOLOR=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ansible-playbook: %v\n%s", err, output)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Disks []string `json:"disks"`
		Sizes []int64  `json:"sizes"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	want := []string{"/dev/sdb", "/dev/nvme0n2"}
	if !reflect.DeepEqual(got.Disks, want) || !reflect.DeepEqual(got.Sizes, []int64{64424509440, 107374182400}) {
		t.Fatalf("selection = %+v, want disks %v and sizes [64424509440 107374182400]", got, want)
	}
}
