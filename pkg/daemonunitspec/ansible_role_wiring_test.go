package daemonunitspec

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
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

// loopItems expands a task's loop to its string items; a task without a
// string loop yields one empty item.
func loopItems(task map[string]any) []string {
	raw, _ := task["loop"].([]any)
	if raw == nil {
		raw, _ = task["with_items"].([]any)
	}
	var items []string
	for _, r := range raw {
		if s, ok := r.(string); ok {
			items = append(items, s)
		}
	}
	if len(items) == 0 {
		return []string{""}
	}
	return items
}

func moduleArgs(task map[string]any, names ...string) map[string]any {
	for _, name := range names {
		if args, ok := task[name].(map[string]any); ok {
			return args
		}
	}
	return nil
}

func withItem(path, item string) string {
	if item == "" {
		return path
	}
	return strings.ReplaceAll(path, "{{ item }}", item)
}

var systemdDropIn = regexp.MustCompile(`^/etc/systemd/system/[^/{}]+\.service\.d/[^/{}]+$`)

// A drop-in written into a directory the role never creates works only on
// hosts where something else made the directory earlier. The first
// from-scratch control plane failed on faas-outboundd.service.d this way.
func TestAnsibleRoleDropInsHaveTheirDirectory(t *testing.T) {
	for role, files := range roleTaskFiles(t) {
		perFile := map[string][]roleTask{}
		roleDirs := map[string]bool{}
		for _, f := range files {
			perFile[f] = flattenRoleTasks(f, loadYAMLList(t, f))
			for _, task := range perFile[f] {
				if args := moduleArgs(task.body, "ansible.builtin.file", "file"); args != nil && args["state"] == "directory" {
					for _, item := range loopItems(task.body) {
						if p, _ := args["path"].(string); p != "" {
							roleDirs[f+"\x00"+withItem(p, item)] = true
						}
					}
				}
			}
		}
		for _, f := range files {
			created := map[string]bool{}
			for _, task := range perFile[f] {
				if args := moduleArgs(task.body, "ansible.builtin.file", "file"); args != nil && args["state"] == "directory" {
					for _, item := range loopItems(task.body) {
						if p, _ := args["path"].(string); p != "" {
							created[withItem(p, item)] = true
						}
					}
				}
				args := moduleArgs(task.body, "ansible.builtin.template", "template", "ansible.builtin.copy", "copy")
				if args == nil {
					continue
				}
				for _, item := range loopItems(task.body) {
					dest, _ := args["dest"].(string)
					dest = withItem(dest, item)
					if !systemdDropIn.MatchString(dest) {
						continue
					}
					dir := filepath.Dir(dest)
					if created[dir] || createdInOtherFile(roleDirs, files, f, dir) {
						continue
					}
					t.Errorf("role %s (%s) task %q writes %s but the role never creates %s first",
						role, filepath.Base(f), task.body["name"], dest, dir)
				}
			}
		}
	}
}

func createdInOtherFile(roleDirs map[string]bool, files []string, self, dir string) bool {
	for _, f := range files {
		if f != self && roleDirs[f+"\x00"+dir] {
			return true
		}
	}
	return false
}

// Notifying a handler that no role or playbook defines fails the play, but
// only on a run where the notifying task changes something. A drop-in that
// never changes on existing hosts hides the missing handler until the first
// fresh host.
func TestAnsibleRoleNotifiesResolveToAHandler(t *testing.T) {
	root := repoRoot(t)
	handlers := map[string]bool{}
	addHandlers := func(items []any) {
		for _, item := range items {
			h, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if name, ok := h["name"].(string); ok {
				handlers[name] = true
			}
			switch listen := h["listen"].(type) {
			case string:
				handlers[listen] = true
			case []any:
				for _, l := range listen {
					if s, ok := l.(string); ok {
						handlers[s] = true
					}
				}
			}
		}
	}
	handlerFiles, err := filepath.Glob(filepath.Join(root, "deploy", "ansible", "roles", "*", "handlers", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range handlerFiles {
		addHandlers(loadYAMLList(t, f))
	}
	playbooks, err := filepath.Glob(filepath.Join(root, "deploy", "ansible", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range playbooks {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var plays []any
		if yaml.Unmarshal(body, &plays) != nil {
			continue // not a playbook (requirements.yml, vars files)
		}
		for _, play := range plays {
			if p, ok := play.(map[string]any); ok {
				if hs, ok := p["handlers"].([]any); ok {
					addHandlers(hs)
				}
			}
		}
	}

	var missing []string
	for role, files := range roleTaskFiles(t) {
		for _, f := range files {
			for _, task := range flattenRoleTasks(f, loadYAMLList(t, f)) {
				var notify []string
				switch n := task.body["notify"].(type) {
				case string:
					notify = []string{n}
				case []any:
					for _, x := range n {
						if s, ok := x.(string); ok {
							notify = append(notify, s)
						}
					}
				}
				for _, name := range notify {
					if strings.Contains(name, "{{") || handlers[name] {
						continue
					}
					missing = append(missing, role+"/"+filepath.Base(f)+": "+name)
				}
			}
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("notify has no handler or listen: %s", m)
	}
}
