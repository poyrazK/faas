package main_test

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"gopkg.in/yaml.v3"
)

// activatePreparedStartTask is where --activate-prepared starts node_join.yml
// when the node's bootstrap contract is current (ADR-20261025095348).
const activatePreparedStartTask = "Verify the prepared rollout marker"

// --activate-prepared skips every task before its start task, in every play,
// so a result those tasks register is undefined in a current-contract
// activation. A later task that reads one must register it again after the
// start task. Reading it through `default` hid the gap: rc.354 skipped the
// adoption check and drained both nodes because the existing compute-node
// lookup and the active vmmd stat ran only before the start task
// (ADR-20261118060000).
func TestNodeJoinActivatePreparedNeverReadsResultsItSkipped(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "node_join.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var plays []map[string]any
	if err := yaml.Unmarshal(body, &plays); err != nil {
		t.Fatal(err)
	}
	var tasks []map[string]any
	var walk func(items any)
	walk = func(items any) {
		list, _ := items.([]any)
		for _, item := range list {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if block, ok := m["block"]; ok {
				// The block's own when reads results too.
				header := map[string]any{}
				for k, v := range m {
					if k != "block" && k != "rescue" && k != "always" {
						header[k] = v
					}
				}
				tasks = append(tasks, header)
				walk(block)
				walk(m["rescue"])
				walk(m["always"])
				continue
			}
			tasks = append(tasks, m)
		}
	}
	for _, play := range plays {
		walk(play["tasks"])
	}
	start := -1
	for i, tk := range tasks {
		if tk["name"] == activatePreparedStartTask {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("node_join.yml has no %q task", activatePreparedStartTask)
	}
	// Results a later task may still read after a skip, and why.
	allowed := map[string]string{
		// The per-node database login block (faas_compute_db_cert_logins,
		// off in production) loops over it; a current-contract activation
		// with the switch on fails on the undefined loop instead of
		// silently changing behaviour.
		"faas_join_db_cert_staged": "per-node database logins are off in production",
	}
	skipped := map[string]bool{}
	for _, tk := range tasks[:start] {
		if reg, ok := tk["register"].(string); ok && allowed[reg] == "" {
			skipped[reg] = true
		}
	}
	for _, tk := range tasks[start:] {
		rest := map[string]any{}
		for k, v := range tk {
			if k != "name" && k != "register" {
				rest[k] = v
			}
		}
		text, err := yaml.Marshal(rest)
		if err != nil {
			t.Fatal(err)
		}
		for reg := range skipped {
			if m := regexp.MustCompile(`\b` + regexp.QuoteMeta(reg) + `\.\w+`).Find(text); m != nil {
				t.Errorf("%q reads %q, which --activate-prepared skipped; register it again after %q", tk["name"], m, activatePreparedStartTask)
			}
		}
		if reg, ok := tk["register"].(string); ok {
			delete(skipped, reg)
		}
		for _, key := range []string{"ansible.builtin.set_fact", "set_fact"} {
			if facts, ok := tk[key].(map[string]any); ok {
				for name := range facts {
					delete(skipped, name)
				}
			}
		}
	}
}
