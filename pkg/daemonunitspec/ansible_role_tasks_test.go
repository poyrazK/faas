package daemonunitspec

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// roleTask is one task from a role's tasks/*.yml, with block/rescue/always
// children flattened in file order.
type roleTask struct {
	file string
	body map[string]any
}

func flattenRoleTasks(file string, items []any) []roleTask {
	var out []roleTask
	for _, item := range items {
		task, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for _, key := range []string{"block", "rescue", "always"} {
			if children, ok := task[key].([]any); ok {
				out = append(out, flattenRoleTasks(file, children)...)
			}
		}
		out = append(out, roleTask{file: file, body: task})
	}
	return out
}

func loadYAMLList(t *testing.T, path string) []any {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var items []any
	if err := yaml.Unmarshal(body, &items); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return items
}

// roleTaskFiles returns role name -> its task files, sorted.
func roleTaskFiles(t *testing.T) map[string][]string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "*", "tasks", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	roles := map[string][]string{}
	for _, f := range files {
		role := filepath.Base(filepath.Dir(filepath.Dir(f)))
		roles[role] = append(roles[role], f)
	}
	return roles
}
