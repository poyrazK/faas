package daemonunitspec

import (
	"path/filepath"
	"regexp"
	"testing"
)

// TestPostgresRoleTurnsJITOff pins H9-159. The production-us control plane
// ran PostgreSQL's JIT defaults: the wake timeline's boot lookup was compiled
// with LLVM on every call and ran past a 20 s statement timeout, against 1.0 s
// with jit=off. The role must write `jit = off` over Ubuntu's commented
// default, without touching the jit_*_cost settings, and reload PostgreSQL.
func TestPostgresRoleTurnsJITOff(t *testing.T) {
	roleDir := filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "postgres")
	var found bool
	for _, task := range flattenRoleTasks("tasks/main.yml", loadYAMLList(t, filepath.Join(roleDir, "tasks", "main.yml"))) {
		args, ok := task.body["ansible.builtin.lineinfile"].(map[string]any)
		if !ok || args["line"] != "jit = off" {
			continue
		}
		found = true
		pattern, _ := args["regexp"].(string)
		re, err := regexp.Compile(pattern)
		if err != nil {
			t.Fatalf("jit task regexp %q: %v", pattern, err)
		}
		for line, want := range map[string]bool{
			"#jit = on\t\t\t\t# allow JIT compilation": true,
			"jit = on":                       true,
			"jit=off":                        true,
			"#jit_above_cost = 100000":       false,
			"jit_inline_above_cost = 500000": false,
			"#jit_provider = 'llvmjit'":      false,
		} {
			if got := re.MatchString(line); got != want {
				t.Errorf("jit task regexp %q matches %q = %v, want %v", pattern, line, got, want)
			}
		}
		if args["path"] != "{{ postgres_config_dir }}/postgresql.conf" {
			t.Errorf("jit task writes %v, want postgresql.conf", args["path"])
		}
		if task.body["notify"] != "reload postgresql" {
			t.Errorf("jit task notifies %v, want reload postgresql", task.body["notify"])
		}
	}
	if !found {
		t.Fatal("postgres role has no lineinfile task writing `jit = off`")
	}
}
