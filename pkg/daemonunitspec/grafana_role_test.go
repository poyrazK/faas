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
