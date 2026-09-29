package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// peerMappedUsers returns the OS users the postgres role maps to the faas
// database role through faas_map.
func peerMappedUsers(t *testing.T) map[string]bool {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "roles", "postgres", "tasks", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var tasks []map[string]any
	if err := yaml.Unmarshal(body, &tasks); err != nil {
		t.Fatal(err)
	}
	line := regexp.MustCompile(`(?m)^faas_map\s+(\S+)\s+faas\s*$`)
	users := map[string]bool{}
	for _, task := range tasks {
		args, _ := task["ansible.builtin.blockinfile"].(map[string]any)
		if args == nil || !strings.Contains(toYAMLString(args["path"]), "pg_ident.conf") {
			continue
		}
		for _, m := range line.FindAllStringSubmatch(toYAMLString(args["block"]), -1) {
			users[m[1]] = true
		}
	}
	if len(users) == 0 {
		t.Fatal("postgres role has no faas_map ident block")
	}
	return users
}

func toYAMLString(v any) string {
	s, _ := v.(string)
	return s
}

// The control plane's DSN is a peer-authenticated Unix socket
// (postgres:///faas?host=/run/postgresql&user=faas). Every OS user that
// connects with it must be in faas_map: the control-plane daemons, and root,
// because cd-controlplane runs fleet-seal migrate, deployctl and migrate as
// root with --db-env /etc/faas/sealed.env. Root was missing, so the first
// from-scratch rollout (production-us) failed with "Peer authentication
// failed for user faas".
func TestControlPlaneDatabaseUsersArePeerMapped(t *testing.T) {
	mapped := peerMappedUsers(t)
	workflow := readWorkflow(t, "cd-controlplane.yml")
	if strings.Contains(workflow, "--db-env /etc/faas/sealed.env") && strings.Contains(workflow, "root@${{ env.CP_HOST }}") && !mapped["root"] {
		t.Error("cd-controlplane runs sealed.env database commands as root, but faas_map does not map root")
	}
	units, err := filepath.Glob(filepath.Join("..", "..", "deploy", "ansible", "roles", "*", "files", "faas-*.service"))
	if err != nil {
		t.Fatal(err)
	}
	userLine := regexp.MustCompile(`(?m)^User=(\S+)$`)
	checked := 0
	for _, unit := range units {
		body, err := os.ReadFile(unit)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "EnvironmentFile=/etc/faas/sealed.env") && !strings.Contains(string(body), "EnvironmentFile=-/etc/faas/sealed.env") {
			continue
		}
		m := userLine.FindStringSubmatch(string(body))
		if m == nil || m[1] == "root" {
			continue
		}
		checked++
		if !mapped[m[1]] {
			t.Errorf("%s runs as %s with sealed.env, but faas_map does not map it", filepath.Base(unit), m[1])
		}
	}
	if checked == 0 {
		t.Fatal("found no control-plane units that load sealed.env; the scan is broken")
	}
}
