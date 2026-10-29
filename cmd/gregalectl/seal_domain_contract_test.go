package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSealDomainOutboundContractRole pins the outbound contract step (ADR
// 20261022111812, step 4) in the seal_domains role: the read-only check runs
// before outboundd's drop-in changes, the contracted drop-in resets the
// credentials before it loads the domain identity, and the proof looks for
// the credential name the base unit gives the fleet identity.
func TestSealDomainOutboundContractRole(t *testing.T) {
	t.Parallel()
	role := filepath.Join("..", "..", "deploy", "ansible", "roles", "seal_domains")
	read := func(path string) string {
		t.Helper()
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}

	tasks := read(filepath.Join(role, "tasks", "outbound.yml"))
	for _, token := range []string{
		"not (faas_seal_domain_outbound_contracted | bool) or faas_seal_domain_outbound_writes == 'domain'",
		"      - --check\n",
	} {
		if !strings.Contains(tasks, token) {
			t.Errorf("tasks/outbound.yml is missing %q", token)
		}
	}
	check := strings.Index(tasks, "      - --check\n")
	dropIn := strings.Index(tasks, "src: 60-faas-seal-domain-outbound-reader.conf.j2")
	restart := strings.Index(tasks, "ansible.builtin.meta: flush_handlers")
	proof := strings.Index(tasks, "paths: /run/credentials/faas-outboundd.service")
	if check < 0 || dropIn < 0 || restart < 0 || proof < 0 || check >= dropIn || dropIn >= restart || restart >= proof {
		t.Fatalf("tasks/outbound.yml order: check=%d drop-in=%d restart=%d proof=%d; want the check before the drop-in and the proof after the restart", check, dropIn, restart, proof)
	}

	template := read(filepath.Join(role, "templates", "60-faas-seal-domain-outbound-reader.conf.j2"))
	reset := strings.Index(template, "\nLoadCredential=\n")
	domain := strings.Index(template, "\nLoadCredential=faas_seal_domain_outbound:")
	if reset < 0 || domain < 0 || reset >= domain {
		t.Fatalf("reader drop-in: reset=%d domain credential=%d; want the empty LoadCredential= before the domain identity", reset, domain)
	}
	if !strings.Contains(template, "UnsetEnvironment=FAAS_FLEET_AGE_IDENTITY_PATH") {
		t.Error("the contracted reader drop-in does not unset FAAS_FLEET_AGE_IDENTITY_PATH")
	}

	for _, unit := range []string{
		filepath.Join("..", "..", "deploy", "systemd", "faas-outboundd.service"),
		filepath.Join("..", "..", "deploy", "ansible", "roles", "control_plane_service", "files", "faas-outboundd.service"),
	} {
		if !strings.Contains(read(unit), "LoadCredential=faas_fleet_age_identity:/etc/faas/secrets/fleet.age") {
			t.Errorf("%s no longer loads the fleet identity as faas_fleet_age_identity, the name the contract proof looks for", unit)
		}
	}
}
