package daemonunitspec

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// privateSecretOwnerCheck matches an owner or group comparison in a
// failed_when or assert expression, e.g. `.stat.pw_name != "faas"`.
var privateSecretOwnerCheck = regexp.MustCompile(`\.stat\.(pw_name|gr_name)\s*(==|!=)\s*"([^"]+)"`)

// TestRoleSecretAssertsAgreeWithPrivateBucket reproduces production-us on
// 2026-10-05 (rc.238). The shared ADR-127 assert requires every PRIVATE
// per-daemon secret to be 0400 root:root, and githubd_service separately
// required /etc/faas/secrets/githubd/app.pem to be owned by faas. No file can
// satisfy both, so control-plane convergence failed whenever the key existed.
// CI never saw it because both asserts skip an absent file. A role-specific
// assert on a PRIVATE path must not demand a non-root owner or group.
func TestRoleSecretAssertsAgreeWithPrivateBucket(t *testing.T) {
	root := repoRoot(t)
	private := map[string]bool{}
	shared := filepath.Join(root, "deploy", "ansible", "roles", "_shared", "tasks", "sealed_env_split_perm_asserts.yml")
	for _, task := range flattenRoleTasks(shared, loadYAMLList(t, shared)) {
		name, _ := task.body["name"].(string)
		if !strings.Contains(name, "stat PRIVATE") {
			continue
		}
		items, _ := task.body["loop"].([]any)
		for _, item := range items {
			if m, ok := item.(map[string]any); ok {
				if p, ok := m["path"].(string); ok {
					private[p] = true
				}
			}
		}
	}
	if !private["/etc/faas/secrets/githubd/app.pem"] {
		t.Fatalf("PRIVATE bucket paths not found in %s (got %d); did the task name change?", shared, len(private))
	}

	checked := 0
	for role, files := range roleTaskFiles(t) {
		for _, f := range files {
			tasks := flattenRoleTasks(f, loadYAMLList(t, f))
			// register name -> PRIVATE path it stats
			registered := map[string]string{}
			for _, task := range tasks {
				for _, key := range []string{"ansible.builtin.stat", "stat"} {
					args, ok := task.body[key].(map[string]any)
					if !ok {
						continue
					}
					p, _ := args["path"].(string)
					reg, _ := task.body["register"].(string)
					if private[p] && reg != "" {
						registered[reg] = p
					}
				}
			}
			for _, task := range tasks {
				name, _ := task.body["name"].(string)
				expr := fmt.Sprint(task.body["failed_when"])
				if args, ok := task.body["ansible.builtin.assert"].(map[string]any); ok {
					expr += " " + fmt.Sprint(args["that"])
				}
				for reg, p := range registered {
					if !strings.Contains(expr, reg+".stat.") {
						continue
					}
					checked++
					for _, m := range privateSecretOwnerCheck.FindAllStringSubmatch(expr, -1) {
						if m[3] != "root" {
							t.Errorf("%s: %q requires %s %s %q on PRIVATE secret %s; the shared ADR-127 assert requires root:root",
								role, name, m[1], m[2], m[3], p)
						}
					}
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no role-specific assert on a PRIVATE secret was found; did the githubd app.pem assert move?")
	}
}
