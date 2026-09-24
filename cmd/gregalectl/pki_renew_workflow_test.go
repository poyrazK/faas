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
	// The deployment runner is a dedicated host, not the issuer: renewal must
	// not assume the CA, manifest or gregalectl are on the runner's machine.
	if strings.Contains(workflow, "127.0.0.1") {
		t.Fatal("PKI renewal assumes the runner is the control-plane host")
	}
	for _, want := range []string{
		"gregalectl manifest ansible",
		"CP_HOST: ${{ secrets.CP_HOST",
		"CONTROL_PLANE_SSH_KEY:",
		"pki-control-key",
		`ssh-keyscan -H "$CP_HOST"`,
		`"root@${CP_HOST}"`,
		"test -r /etc/faas/tls/ca/ca.key",
		"rendered inventory contains an unsafe archive path",
		"control_vars=",
		"ansible_user: faas-runner",
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
