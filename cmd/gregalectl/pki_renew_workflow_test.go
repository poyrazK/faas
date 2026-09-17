package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPKIRenewWorkflowUsesRoleScopedSSHIdentities(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "pki-renew.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(body)
	if strings.Contains(workflow, "ansible_connection: local") {
		t.Fatal("PKI renewal uses the no-new-privileges runner process as the issuer")
	}
	if strings.Contains(workflow, "sudo -n test -r /etc/faas/tls/ca/ca.key") {
		t.Fatal("PKI renewal tries to sudo inside the hardened runner")
	}
	for _, want := range []string{
		"gregalectl manifest ansible",
		"CONTROL_PLANE_SSH_KEY:",
		"pki-control-key",
		"ssh-keyscan -H 127.0.0.1",
		"root@127.0.0.1 test -r /etc/faas/tls/ca/ca.key",
		"control_vars=",
		"ansible_user: root",
		"ansible_ssh_private_key_file: __PKI_CONTROL_KEY__",
		"ansible_ssh_private_key_file:",
		"StrictHostKeyChecking=yes",
		"UserKnownHostsFile=",
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("PKI workflow missing %q", want)
		}
	}
}
