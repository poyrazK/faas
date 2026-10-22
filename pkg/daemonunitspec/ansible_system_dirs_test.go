package daemonunitspec

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// systemDirs are shared with services that run as their own users. A role
// that narrows them (root:faas 0770 on /usr/local/bin) leaves prometheus,
// alertmanager and node_exporter unable to exec after their next restart.
var systemDirs = map[string]bool{
	"/usr/local/bin": true,
	"/usr/local":     true,
	"/usr/bin":       true,
	"/usr/sbin":      true,
}

// TestAnsibleRolesKeepSystemDirsWorldExecutable fails when any role's file
// task manages a shared system directory with anything but root:root 0755.
func TestAnsibleRolesKeepSystemDirsWorldExecutable(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "*", "tasks", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no ansible role task files found")
	}
	checked := 0
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var tasks []map[string]any
		if err := yaml.Unmarshal(raw, &tasks); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		for _, task := range tasks {
			for _, module := range []string{"ansible.builtin.file", "file"} {
				args, ok := task[module].(map[string]any)
				if !ok {
					continue
				}
				for _, path := range taskPaths(args, task["loop"]) {
					if !systemDirs[path] {
						continue
					}
					checked++
					if args["owner"] != "root" || args["group"] != "root" || args["mode"] != "0755" {
						t.Errorf("%s: task %q manages %s as %v:%v %v; want root:root 0755",
							filepath.Base(filepath.Dir(filepath.Dir(file))), task["name"], path, args["owner"], args["group"], args["mode"])
					}
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no role manages /usr/local/bin; the firecracker role's guard task is gone")
	}
}

// taskPaths returns the literal paths a file task manages: its path, or each
// loop item when the path is the "{{ item }}" template.
func taskPaths(args map[string]any, loop any) []string {
	path, _ := args["path"].(string)
	if path != "{{ item }}" {
		return []string{path}
	}
	items, _ := loop.([]any)
	var paths []string
	for _, item := range items {
		if s, ok := item.(string); ok {
			paths = append(paths, s)
		}
	}
	return paths
}
