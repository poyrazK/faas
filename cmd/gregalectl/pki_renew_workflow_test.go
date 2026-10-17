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
		// The control-plane key must match COMPUTE_KNOWN_HOSTS; see
		// TestControlPlaneSSHTrustsOnlyVerifiedHostKeys.
		`scanned="$(ssh-keyscan -T 10 -t ed25519,ecdsa,rsa "$CP_HOST" 2>/dev/null || true)"`,
		`"root@${CP_HOST}"`,
		"test -r /etc/faas/tls/ca/ca.key",
		"rendered inventory contains an unsafe archive path",
		"control_vars=",
		"ansible_user: __PKI_COMPUTE_USER__",
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

// Renewal ran only against the production environment on the faas-fleet
// runner, so production-us compute leaves were never renewed and
// FaasInternalMTLSRenewalNeverCompleted paged on both nodes. The schedule
// renews every fleet on that fleet's runner with that fleet's secrets, and a
// fleet whose runner is offline cannot hold another fleet's renewal behind a
// shared concurrency group. The schedule lists live fleets only: the retired
// EU `production` fleet stays a manual dispatch option, because its scheduled
// leg queued on offline runners and was cancelled every day, turning the
// daily renewal red.
func TestPKIRenewTargetsEveryFleet(t *testing.T) {
	workflow := readWorkflow(t, "pki-renew.yml")
	for _, want := range []string{
		"      deploy_environment:\n",
		"          - production\n          - production-us\n",
		`deploy_environment: ${{ fromJSON(github.event_name == 'workflow_dispatch' && format('["{0}"]', inputs.deploy_environment) || '["production-us"]') }}`,
		"fail-fast: false",
		"group: internal-pki-renewal-${{ matrix.deploy_environment }}",
		"environment: ${{ matrix.deploy_environment }}",
		`runs-on: ${{ fromJSON(matrix.deploy_environment == 'production-us' && '["self-hosted","linux","faas-fleet-us"]' || '["self-hosted","linux","faas-fleet"]') }}`,
		"COMPUTE_SSH_USER: ${{ matrix.deploy_environment == 'production-us' && 'root' || 'faas-runner' }}",
		`-e "s|__PKI_COMPUTE_USER__|$COMPUTE_SSH_USER|"`,
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("pki-renew.yml missing %q", want)
		}
	}
	for _, line := range strings.Split(workflow, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "environment: production" || trimmed == "group: internal-pki-renewal" ||
			trimmed == "runs-on: [self-hosted, linux, faas-fleet]" {
			t.Errorf("pki-renew.yml pins one fleet: %s", trimmed)
		}
	}
}
