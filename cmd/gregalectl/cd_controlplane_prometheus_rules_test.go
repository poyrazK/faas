package main

import (
	"regexp"
	"strings"
	"testing"
)

// TestCDControlPlaneShipsEveryPrometheusRuleFile pins the release path to the
// rule files prometheus.yml.j2 loads. rc.280's rollout stopped at "Converge
// Prometheus config and rules": the template listed data_repair.rules.yml but
// the bundle and the converge step did not carry it, so promtool check config
// failed on a file it could not find.
func TestCDControlPlaneShipsEveryPrometheusRuleFile(t *testing.T) {
	template := readRepoFile(t, "deploy", "ansible", "roles", "prometheus", "templates", "prometheus.yml.j2")
	_, ruleSection, found := strings.Cut(template, "\nrule_files:\n")
	if !found {
		t.Fatal("prometheus.yml.j2 has no rule_files section")
	}
	var rules []string
	for _, line := range strings.Split(ruleSection, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		name, ok := strings.CutPrefix(trimmed, "- /etc/prometheus/")
		if !ok {
			break // end of the rule_files list
		}
		rules = append(rules, name)
	}
	if len(rules) == 0 {
		t.Fatal("prometheus.yml.j2 lists no rule files")
	}

	workflow := readWorkflow(t, "cd-controlplane.yml")
	filesMatch := regexp.MustCompile(`(?m)^\s*files=\(([^)]*)\)`).FindStringSubmatch(workflow)
	if filesMatch == nil {
		t.Fatal("cd-controlplane.yml converge step has no files=(...) list")
	}
	converged := map[string]bool{}
	for _, name := range strings.Fields(filesMatch[1]) {
		converged[name] = true
	}
	for _, name := range rules {
		if !strings.Contains(workflow, `"${BUNDLE_ROOT}/prometheus/`+name+`"`) {
			t.Errorf("rule file %s is loaded by prometheus.yml.j2 but not bundled by cd-controlplane.yml", name)
		}
		if !converged[name] {
			t.Errorf("rule file %s is loaded by prometheus.yml.j2 but missing from the converge step's files list", name)
		}
		if !strings.Contains(workflow, `"$source_dir/`+name+`"`) {
			t.Errorf("rule file %s is not checked by promtool check rules in the converge step", name)
		}
	}
}
