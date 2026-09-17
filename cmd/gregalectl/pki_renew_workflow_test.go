package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPKIRenewWorkflowPreservesManifestSSHUsers(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "pki-renew.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(body)
	if strings.Contains(workflow, "ansible_user: root") {
		t.Fatal("PKI renewal globally overrides the per-host SSH user with root")
	}
	for _, want := range []string{
		"gregalectl manifest ansible",
		"sudo -n test -r /etc/faas/tls/ca/ca.key",
		"group_vars/control_plane.yml",
		"ansible_connection: local",
		"ansible_ssh_private_key_file:",
		"StrictHostKeyChecking=yes",
		"UserKnownHostsFile=",
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("PKI workflow missing %q", want)
		}
	}
}
