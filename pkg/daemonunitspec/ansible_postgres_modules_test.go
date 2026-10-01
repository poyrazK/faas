package daemonunitspec

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

type moduleTask struct {
	role, file string
	body       map[string]any
}

// roleModuleTasks returns every role task that calls a community.postgresql
// module, keyed by the fully qualified module name.
func roleModuleTasks(t *testing.T) map[string][]moduleTask {
	t.Helper()
	out := map[string][]moduleTask{}
	for role, files := range roleTaskFiles(t) {
		for _, f := range files {
			for _, task := range flattenRoleTasks(f, loadYAMLList(t, f)) {
				for key := range task.body {
					if strings.HasPrefix(key, "community.postgresql.") {
						out[key] = append(out[key], moduleTask{role: role, file: filepath.Base(f), body: task.body})
					}
				}
			}
		}
	}
	return out
}

func masksFailure(task map[string]any) bool {
	if v, ok := task["failed_when"]; ok && (v == false || strings.EqualFold(strings.TrimSpace(toString(v)), "false")) {
		return true
	}
	if v, ok := task["ignore_errors"]; ok && (v == true || strings.EqualFold(strings.TrimSpace(toString(v)), "true")) {
		return true
	}
	return false
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// The faas database role and database were created by community.postgresql
// modules that ran with failed_when: false on a host without psycopg2. On the
// first from-scratch control plane neither existed and the play reported ok.
func TestAnsiblePostgresModulesFailLoudlyAndHaveTheirDriver(t *testing.T) {
	root := repoRoot(t)
	rolesUsing := map[string]bool{}
	for module, tasks := range roleModuleTasks(t) {
		for _, task := range tasks {
			rolesUsing[task.role] = true
			if masksFailure(task.body) {
				t.Errorf("%s/%s task %q masks %s failures", task.role, task.file, task.body["name"], module)
			}
		}
	}
	if len(rolesUsing) == 0 {
		t.Fatal("no role uses community.postgresql; the scan is broken")
	}
	for role := range rolesUsing {
		found := false
		_ = filepath.WalkDir(filepath.Join(root, "deploy", "ansible", "roles", role), func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".yml") {
				return err
			}
			body, readErr := os.ReadFile(path)
			if readErr == nil && strings.Contains(string(body), "python3-psycopg2") {
				found = true
			}
			return nil
		})
		if !found {
			t.Errorf("role %s uses community.postgresql modules but never installs python3-psycopg2 on the target", role)
		}
	}
}

// A task registered with failed_when: false can never be `is failed`, so a
// later fallback gated on that is dead code that hides the original failure.
func TestAnsibleNoFallbackGatedOnAMaskedFailure(t *testing.T) {
	for role, files := range roleTaskFiles(t) {
		for _, f := range files {
			masked := map[string]bool{}
			for _, task := range flattenRoleTasks(f, loadYAMLList(t, f)) {
				when := task.body["when"]
				var conds []string
				switch w := when.(type) {
				case string:
					conds = []string{w}
				case []any:
					for _, c := range w {
						conds = append(conds, toString(c))
					}
				}
				for reg := range masked {
					re := regexp.MustCompile(`\b` + regexp.QuoteMeta(reg) + `(\s+is\s+(failed|failure)\b|\.failed\b)`)
					for _, c := range conds {
						if re.MatchString(c) {
							t.Errorf("%s/%s task %q is gated on %q, which failed_when: false makes unreachable",
								role, filepath.Base(f), task.body["name"], c)
						}
					}
				}
				if reg, ok := task.body["register"].(string); ok {
					if v, ok := task.body["failed_when"]; ok && (v == false || strings.EqualFold(strings.TrimSpace(toString(v)), "false")) {
						masked[reg] = true
					}
				}
			}
		}
	}
}

// Every community.postgresql argument must exist in the installed collection.
// postgresql_user's priv option was removed in community.postgresql 3.0 and
// requirements.yml pins >=3.0.0.
func TestAnsiblePostgresModuleArgumentsExist(t *testing.T) {
	docBin, err := exec.LookPath("ansible-doc")
	if err != nil {
		t.Skip("ansible-doc not installed")
	}
	for module, tasks := range roleModuleTasks(t) {
		out, err := exec.Command(docBin, "-j", module).Output()
		if err != nil {
			t.Skipf("%s not installed: %v", module, err)
		}
		var doc map[string]struct {
			Doc struct {
				Options map[string]struct {
					Aliases []string `json:"aliases"`
				} `json:"options"`
			} `json:"doc"`
		}
		if err := json.Unmarshal(out, &doc); err != nil {
			t.Fatalf("decode ansible-doc %s: %v", module, err)
		}
		entry, ok := doc[module]
		if !ok || len(entry.Doc.Options) == 0 {
			t.Fatalf("ansible-doc returned no options for %s", module)
		}
		known := map[string]bool{}
		for name, opt := range entry.Doc.Options {
			known[name] = true
			for _, a := range opt.Aliases {
				known[a] = true
			}
		}
		for _, task := range tasks {
			args, _ := task.body[module].(map[string]any)
			var unknown []string
			for key := range args {
				if !known[key] {
					unknown = append(unknown, key)
				}
			}
			sort.Strings(unknown)
			if len(unknown) > 0 {
				t.Errorf("%s/%s task %q passes %v, which %s does not accept", task.role, task.file, task.body["name"], unknown, module)
			}
		}
	}
}
