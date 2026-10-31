package daemonunitspec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readGrafanaRole(t *testing.T, rel string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "ansible", "roles", "grafana", rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// grafanaTask returns the YAML of the task named name, up to the next task,
// and its offset in tasks.
func grafanaTask(t *testing.T, tasks, name string) (string, int) {
	t.Helper()
	i := strings.Index(tasks, "- name: "+name+"\n")
	if i < 0 {
		t.Fatalf("grafana task %q is missing", name)
	}
	task := tasks[i:]
	if j := strings.Index(task[1:], "\n- name:"); j >= 0 {
		task = task[:j+1]
	}
	return task, i
}

// TestGrafanaKeepsAdminPasswordPrivate pins a fix the obs-stack-smoke
// workflow found: the grafana package resets everything under /etc/grafana
// to root:grafana 0640 when it is installed or upgraded. The role puts an
// existing admin password file back to grafana:grafana 0400 after the
// install and before its own check, without reading it.
func TestGrafanaKeepsAdminPasswordPrivate(t *testing.T) {
	tasks := readGrafanaRole(t, "tasks/main.yml")
	_, install := grafanaTask(t, tasks, "grafana — apt install grafana-server")
	keep, keepAt := grafanaTask(t, tasks, "grafana — keep the admin password file private after package installs")
	_, check := grafanaTask(t, tasks, "grafana — require private admin password file (production)")
	if !(install < keepAt && keepAt < check) {
		t.Fatalf("the restore must run after the package install and before the check (install %d, restore %d, check %d)", install, keepAt, check)
	}
	for _, want := range []string{"ansible.builtin.file:", "owner: grafana", "group: grafana", `mode: "0400"`, "no_log: true"} {
		if !strings.Contains(keep, want) {
			t.Errorf("restore task lacks %q:\n%s", want, keep)
		}
	}
}

// TestGrafanaAppliesProvisionedAdminPassword pins the second fix the
// obs-stack-smoke workflow found. grafana-server ignores GF_*__FILE; only
// Grafana's Docker entrypoint converts it, so every Grafana this role created
// kept admin/admin. The password now goes through grafana.ini's $__file{}
// expansion, and a reconcile step sets it, through stdin only, on a database
// created before.
func TestGrafanaAppliesProvisionedAdminPassword(t *testing.T) {
	ini := readGrafanaRole(t, "templates/grafana.ini.j2")
	if !strings.Contains(ini, "admin_password = $__file{{ '{' }}{{ gv_admin_password_file }}{{ '}' }}") {
		t.Error("grafana.ini.j2 must set admin_password through $__file{gv_admin_password_file}")
	}
	if unit := readGrafanaRole(t, "templates/grafana-server.service.j2"); strings.Contains(unit, "__FILE") {
		t.Error("grafana-server.service.j2 sets a GF_*__FILE variable, which grafana-server ignores")
	}
	tasks := readGrafanaRole(t, "tasks/main.yml")
	start, _ := grafanaTask(t, tasks, "grafana — enable + start service (production only)")
	if !strings.Contains(start, "daemon_reload: true") {
		t.Errorf("the first start must reload systemd so it runs the role's unit:\n%s", start)
	}
	set, _ := grafanaTask(t, tasks, "grafana — set the provisioned admin password")
	for _, want := range []string{"reset-admin-password", "--password-from-stdin", "stdin:", "become_user: grafana", "no_log: true", "gv_admin_login.status == 401"} {
		if !strings.Contains(set, want) {
			t.Errorf("reconcile task lacks %q:\n%s", want, set)
		}
	}
	argv := set[strings.Index(set, "argv:"):strings.Index(set, "stdin:")]
	if strings.Contains(argv, "gv_admin_password") {
		t.Errorf("the password must reach grafana-cli through stdin, not argv:\n%s", argv)
	}
}
