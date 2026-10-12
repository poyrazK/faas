package daemonunitspec

// adr: 472

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestVMMDRestartHandlersHonourJoinDeferral pins every Ansible handler that
// restarts faas-vmmd to the join deferral guard. node_join.yml converges the
// bootstrap roles before it drains the node, and vmmd quarantines the VMs it
// did not start (ADR-472), so a pre-drain vmmd restart leaves instances that
// can neither migrate nor stop. The rc.252 production-us rollout aborted on
// fsn-3 that way: log_archive's activation handler restarted vmmd at
// 02:24:35 while a worker ran, and the drain never emptied.
func TestVMMDRestartHandlersHonourJoinDeferral(t *testing.T) {
	root := repoRoot(t)
	files, err := filepath.Glob(filepath.Join(root, "deploy", "ansible", "roles", "*", "handlers", "main.yml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no role handlers found: %v", err)
	}
	checked := 0
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var handlers []map[string]any
		if err := yaml.Unmarshal(body, &handlers); err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		for _, h := range handlers {
			// Only the action counts: name and listen merely mention the unit.
			action := make(map[string]any, len(h))
			for k, v := range h {
				if k != "name" && k != "listen" && k != "when" {
					action[k] = v
				}
			}
			raw, err := yaml.Marshal(action)
			if err != nil {
				t.Fatal(err)
			}
			text := string(raw)
			if !strings.Contains(text, "faas-vmmd") || !strings.Contains(text, "restart") {
				continue
			}
			checked++
			when, _ := yaml.Marshal(h["when"])
			if !strings.Contains(string(when), "faas_join_defer_service_handlers") {
				rel, _ := filepath.Rel(root, file)
				t.Errorf("%s: handler %q restarts faas-vmmd without honouring faas_join_defer_service_handlers", rel, h["name"])
			}
		}
	}
	if checked == 0 {
		t.Fatal("found no handler that restarts faas-vmmd; the scan is broken")
	}
}
