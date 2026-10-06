package daemonunitspec

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// collectDelegateTo returns every delegate_to value in a parsed playbook or
// task file, at any nesting depth (plays, blocks, rescue/always).
func collectDelegateTo(node any, out *[]string) {
	switch v := node.(type) {
	case map[string]any:
		if d, ok := v["delegate_to"]; ok {
			*out = append(*out, fmt.Sprint(d))
		}
		for _, child := range v {
			collectDelegateTo(child, out)
		}
	case []any:
		for _, child := range v {
			collectDelegateTo(child, out)
		}
	}
}

// TestDelegateToGroupIndexIsGuarded reproduces the production-us rc.241
// node join: tenant_egress_client delegated to
// "{{ groups['tenant_egress_gateway'][0] }}" behind a `when` that skips the
// task without that group. ansible-core 2.21 templates delegate_to before
// `when`, so the bare index failed the play on a fleet with no egress
// gateway. Every delegate_to that reads an inventory group must resolve
// when the group is absent or empty.
func TestDelegateToGroupIndexIsGuarded(t *testing.T) {
	root := filepath.Join(repoRoot(t), "deploy", "ansible")
	var files []string
	for _, pattern := range []string{"*.yml", "roles/*/tasks/*.yml", "roles/*/handlers/*.yml"} {
		matches, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, matches...)
	}
	checked := 0
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "delegate_to") {
			continue
		}
		var tree any
		if err := yaml.Unmarshal(body, &tree); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		var delegates []string
		collectDelegateTo(tree, &delegates)
		rel, _ := filepath.Rel(root, f)
		for _, d := range delegates {
			if !strings.Contains(d, "groups[") {
				continue
			}
			checked++
			if !strings.Contains(d, "default(") {
				t.Errorf("%s: delegate_to %q indexes an inventory group without a default; it fails when the group is absent", rel, d)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no group-indexed delegate_to found; did tenant_egress_client move?")
	}
}
