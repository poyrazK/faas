package daemonunitspec

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestAlloyKeepsTheShipperContract pins ADR-20261023185953. Alloy replaces
// Promtail, and the parts of the contract that are easy to lose in a
// translation are fixed here:
//   - the same stages in the same order;
//   - only the validated identity fields become labels (ADR-143);
//   - DEBUG stays local, and the stored line is the daemon's own;
//   - mTLS with a server-name override;
//   - the job label the dashboards select;
//   - usage reporting off, GA components only, and the HTTP server (which
//     includes a config UI) never on a wildcard address.
func TestAlloyKeepsTheShipperContract(t *testing.T) {
	roles := filepath.Join(repoRoot(t), "deploy", "ansible", "roles")
	read := func(rel string) string {
		body, err := os.ReadFile(filepath.Join(roles, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	full := read("alloy/templates/config.alloy.j2")
	start := strings.Index(full, `loki.process "faas" {`)
	if start < 0 {
		t.Fatal(`config.alloy.j2 has no loki.process "faas" block`)
	}
	config := full[start:]
	if end := strings.Index(config[1:], "\nloki.") + 1; end > 0 {
		config = config[:end]
	}
	for _, want := range []string{`format_as_json = true`, `replacement  = "faas-journal"`} {
		if !strings.Contains(full, want) {
			t.Errorf("config.alloy.j2 missing %q", want)
		}
	}
	config += full[strings.Index(full, `loki.write "loki" {`):]

	stages := regexp.MustCompile(`(?m)^\s+stage\.(\w+) \{`).FindAllStringSubmatch(config, -1)
	var order []string
	for _, m := range stages {
		order = append(order, m[1])
	}
	if got := strings.Join(order, ","); got != "json,json,drop,labels,output" {
		t.Errorf("loki.process stages = %s, want json,json,drop,labels,output (Promtail's pipeline)", got)
	}
	labels := config[strings.Index(config, "stage.labels {"):]
	labels = labels[:strings.Index(labels, "stage.output")]
	keys := regexp.MustCompile(`(?m)^\s+(\w+)\s+= ""`).FindAllStringSubmatch(labels, -1)
	var got []string
	for _, k := range keys {
		got = append(got, k[1])
	}
	if strings.Join(got, ",") != "account_id,app_id,daemon,level" {
		t.Errorf("stage.labels promotes %v; only account_id, app_id, daemon and level may become labels (ADR-143)", got)
	}
	for _, want := range []string{
		`value               = "DEBUG"`,
		`source = "message"`,
		`server_name = "{{ alloy_loki_server_name }}"`,
		`cert_file   = "{{ alloy_loki_client_cert_file }}"`,
		`tenant_id           = "{{ alloy_loki_tenant_id }}"`,
	} {
		if !strings.Contains(config, want) {
			t.Errorf("config.alloy.j2 missing %q", want)
		}
	}

	unit := serviceSection(read("alloy/templates/alloy.service.j2"))
	for _, want := range []string{"--disable-reporting", "--stability.level=generally-available", "Slice=faas-obs.slice", "MemoryMax={{ alloy_memory_max }}"} {
		if !strings.Contains(unit, want) {
			t.Errorf("alloy.service missing %q", want)
		}
	}

	defaults := readRoleDefaults(t, filepath.Join(roles, "alloy"))
	if yamlScalar(defaults["alloy_listen_port"]) != "9080" {
		t.Errorf("alloy_listen_port = %v; apid's discovery scrapes host:9080", defaults["alloy_listen_port"])
	}
	if !strings.Contains(read("alloy/tasks/main.yml"), "alloy_effective_listen_address not in ['0.0.0.0', '::']") {
		t.Error("the alloy role no longer rejects a wildcard listen address")
	}
	if v := yamlScalar(defaults["alloy_release_sha256"]); len(v) != 64 {
		t.Errorf("alloy_release_sha256 = %q, want a SHA-256", v)
	}
}

// TestAlloyShipsHostSecurityEvidence pins ADR-20261107203000: the security
// stream ships the records an intruder with root would erase, every counted
// event has an alert and every alerted event has a selector, the audit keys
// the selectors read are ones host_hardening's rules set, and the audit log
// is readable by the group Alloy runs in.
func TestAlloyShipsHostSecurityEvidence(t *testing.T) {
	root := repoRoot(t)
	roles := filepath.Join(root, "deploy", "ansible", "roles")
	read := func(rel string) string {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	config := read("deploy/ansible/roles/alloy/templates/config.alloy.j2")
	for _, want := range []string{
		`loki.source.journal "security"`,
		`loki.source.journal "kernel"`,
		`loki.source.file "audit"`,
		`tail_from_end = true`,
		`source      = "journal"`,
		`source      = "kernel"`,
		`source      = "auditd"`,
		`job         = "faas-security"`,
		`name              = "security_events_total"`,
		`prefix            = "faas_"`,
		`max_idle_duration = "8760h"`,
		`forward_to = [loki.write.loki.receiver]`,
	} {
		if !strings.Contains(config, want) {
			t.Errorf("config.alloy.j2 missing %q", want)
		}
	}

	counted := map[string]bool{}
	for _, m := range regexp.MustCompile(`template = "([a-z_]+)"`).FindAllStringSubmatch(config, -1) {
		counted[m[1]] = true
	}
	alerted := map[string]bool{}
	rules := read("deploy/ansible/roles/prometheus/files/faas.rules.yml")
	for _, m := range regexp.MustCompile(`increase\(faas_security_events_total\{event=~"([a-z_|]+)"\}`).FindAllStringSubmatch(rules, -1) {
		for _, e := range strings.Split(m[1], "|") {
			if alerted[e] {
				t.Errorf("event %s is in more than one faas_host_security alert", e)
			}
			alerted[e] = true
		}
	}
	if len(alerted) == 0 {
		t.Fatal("faas.rules.yml has no faas_host_security alert on faas_security_events_total")
	}
	for e := range counted {
		if !alerted[e] && e != "ssh_login" {
			t.Errorf("Alloy counts event %s but no faas_host_security alert reads it", e)
		}
	}
	for e := range alerted {
		if !counted[e] {
			t.Errorf("a faas_host_security alert reads event %s, which no Alloy selector sets", e)
		}
	}
	runbook := read("docs/runbooks/FaasHostSecurityEvent.md")
	for e := range counted {
		if !strings.Contains(runbook, "`"+e+"`") {
			t.Errorf("FaasHostSecurityEvent.md does not explain event %s", e)
		}
	}

	hardening := readRoleDefaults(t, filepath.Join(roles, "host_hardening"))
	ruleKeys := map[string]bool{}
	if list, ok := hardening["faas_hardening_auditd_rules"].([]any); ok {
		for _, r := range list {
			if m := regexp.MustCompile(`-k (\S+)`).FindStringSubmatch(yamlScalar(r)); m != nil {
				ruleKeys[m[1]] = true
			}
		}
	}
	for _, m := range regexp.MustCompile(`audit_key=~?\\"([a-z_|]+)\\"`).FindAllStringSubmatch(config, -1) {
		for _, k := range strings.Split(m[1], "|") {
			if !ruleKeys[k] {
				t.Errorf("an Alloy selector reads audit key %s, which no host_hardening audit rule sets", k)
			}
		}
	}

	alloy := readRoleDefaults(t, filepath.Join(roles, "alloy"))
	dropped, _ := alloy["alloy_security_audit_drop_types"].([]any)
	for _, d := range dropped {
		switch yamlScalar(d) {
		case "SYSCALL", "EXECVE", "CONFIG_CHANGE", "DAEMON_END", "DAEMON_ABORT", "USER_AUTH", "USER_LOGIN", "USER_CMD":
			t.Errorf("alloy_security_audit_drop_types drops %s, which carries the event itself", yamlScalar(d))
		}
	}
	if g := yamlScalar(hardening["faas_hardening_auditd_log_group"]); g != "adm" {
		t.Errorf("faas_hardening_auditd_log_group = %q; Alloy reads the audit log as a member of adm", g)
	}
	if !strings.Contains(read("deploy/ansible/roles/alloy/templates/alloy.service.j2"), "SupplementaryGroups=systemd-journal adm") {
		t.Error("alloy.service no longer runs with the adm group that reads the audit log")
	}
}
