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

// TestCDControlPlaneTargetGateSkipsEveryComputeJob pins the control-plane
// target gate's compute set to the HTTP-SD jobs prometheus.yml.j2 renders on a
// control plane. Those targets live on compute nodes, which a platform rollout
// moves only after the control-plane stage, so a missing name fails the
// release on a node it has not reached yet (gatewayd-public-edge and
// cloudflared were missing when the app edge moved onto compute nodes).
func TestCDControlPlaneTargetGateSkipsEveryComputeJob(t *testing.T) {
	template := readRepoFile(t, "deploy", "ansible", "roles", "prometheus", "templates", "prometheus.yml.j2")
	_, controlPlane, found := strings.Cut(template, "{% if (faas_box_role | default('')) == 'control-plane' %}")
	if !found {
		t.Fatal("prometheus.yml.j2 has no control-plane scrape block")
	}
	jobs := regexp.MustCompile(`(?m)^\s+- job_name: "([^"]+)"\n\s+http_sd_configs:`).FindAllStringSubmatch(controlPlane, -1)
	if len(jobs) == 0 {
		t.Fatal("prometheus.yml.j2 renders no HTTP-SD compute jobs")
	}

	workflow := readWorkflow(t, "cd-controlplane.yml")
	setMatch := regexp.MustCompile(`compute=\{([^}]*)\}`).FindStringSubmatch(workflow)
	if setMatch == nil {
		t.Fatal("cd-controlplane.yml target gate has no compute={...} set")
	}
	skipped := map[string]bool{}
	for _, name := range strings.Split(setMatch[1], ",") {
		skipped[strings.Trim(name, `"`)] = true
	}
	for _, job := range jobs {
		if !skipped[job[1]] {
			t.Errorf("compute scrape job %s is gated by the control-plane stage", job[1])
		}
	}
}

// TestCDControlPlaneRestartsOptionalDaemonsBeforeTargetGate: the target gate
// requires every control-plane target up, including the optional daemons. The
// rollout must restart them on the new release first; otherwise one failed
// start (rc.300's mcp-bridged) fails every later release at the gate, before
// the step that would have started the fixed daemon (rc.301).
func TestCDControlPlaneRestartsOptionalDaemonsBeforeTargetGate(t *testing.T) {
	workflow := readWorkflow(t, "cd-controlplane.yml")
	gate := strings.Index(workflow, "- name: Converge Prometheus config and rules")
	if gate < 0 {
		t.Fatal("cd-controlplane.yml has no Prometheus converge step")
	}
	for _, step := range []string{
		"- name: Restart configured optional S3 gateway",
		"- name: Restart configured optional live-query hub",
		"- name: Restart configured optional hosted management MCP server",
	} {
		at := strings.Index(workflow, step)
		if at < 0 {
			t.Errorf("cd-controlplane.yml is missing %q", step)
			continue
		}
		if at > gate {
			t.Errorf("%q runs after the Prometheus target gate", step)
		}
	}
}
