package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestSealDomainContractRoleComputeReaders pins the contract steps of the
// last compute readers of the fleet key (ADR 20261022111812, step 4):
// gatewayd-internal (edge) and schedd (control and delivery together).
// Their reset lives in a 30- drop-in, which sorts before the optional
// credentials (40-) and the domain identities (60-), so it drops only what
// the base unit loads; whatever a base unit loads besides the fleet and host
// identities is loaded again after the reset.
func TestSealDomainContractRoleComputeReaders(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..")
	role := filepath.Join(root, "deploy", "ansible", "roles", "seal_domains")
	read := func(path string) string {
		t.Helper()
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	credentialRe := regexp.MustCompile(`(?m)^LoadCredential=([^:\s]+):\S+$`)
	for _, tc := range []struct {
		name, contract, unit string
		units                []string
		// dropped are the base-unit credentials the contract drops.
		dropped []string
	}{
		{
			name: "edge", contract: "30-faas-seal-domain-edge-contract.conf.j2", unit: "faas-gatewayd-internal",
			units:   []string{filepath.Join(root, "deploy", "ansible", "roles", "gatewayd_internal_service", "files", "faas-gatewayd-internal.service")},
			dropped: []string{"fleet.age", "host.age"},
		},
		{
			name: "schedd", contract: "30-faas-seal-domain-schedd-contract.conf.j2", unit: "faas-schedd",
			units: []string{
				filepath.Join(root, "deploy", "ansible", "roles", "compute_only_service", "files", "faas-schedd.service"),
				filepath.Join(root, "deploy", "ansible", "roles", "control_plane_service", "files", "faas-schedd.service"),
			},
			dropped: []string{"fleet.age"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			template := read(filepath.Join(role, "templates", tc.contract))
			reset := strings.Index(template, "\nLoadCredential=\n")
			if reset < 0 {
				t.Fatalf("%s has no credential reset", tc.contract)
			}
			dropped := map[string]bool{}
			for _, name := range tc.dropped {
				dropped[name] = true
			}
			for _, unit := range tc.units {
				for _, m := range credentialRe.FindAllStringSubmatch(read(unit), -1) {
					readded := strings.Index(template, "\n"+m[0]+"\n") > reset
					if dropped[m[1]] && readded {
						t.Errorf("%s loads %s again; the contract must drop it", tc.contract, m[1])
					}
					if !dropped[m[1]] && !readded {
						t.Errorf("%s loads %s, which the reset drops; load it again after the reset", unit, m[1])
					}
				}
			}
		})
	}

	edge := read(filepath.Join(role, "tasks", "edge.yml"))
	if !strings.Contains(edge, "not (faas_seal_domain_edge_contracted | bool) or faas_seal_domain_edge_writes == 'domain'") {
		t.Error("edge.yml must refuse a contract without writes=domain")
	}
	if !strings.Contains(read(filepath.Join(role, "templates", "30-faas-seal-domain-edge-contract.conf.j2")), "UnsetEnvironment=FAAS_HOST_KEY_PATH") {
		t.Error("the edge contract must unset FAAS_HOST_KEY_PATH")
	}
	inOrder(t, "edge.yml", edge,
		"      - edge\n      - --check\n",
		"dest: /etc/systemd/system/faas-gatewayd-internal.service.d/30-faas-seal-domain-edge-contract.conf",
		"ansible.builtin.meta: flush_handlers",
		"paths: /run/credentials/faas-gatewayd-internal.service")

	// control.yml runs before delivery.yml, so it checks both of schedd's
	// domains before it writes the drop-in that removes the fleet key, and
	// delivery.yml proves the result once both drop-ins are in place.
	control := read(filepath.Join(role, "tasks", "control.yml"))
	if !strings.Contains(control, "(faas_seal_domain_control_contracted | bool) == (faas_seal_domain_delivery_contracted | bool)") {
		t.Error("control.yml must refuse one of schedd's domains contracted without the other")
	}
	inOrder(t, "control.yml", control,
		"loop: [control, delivery]",
		"dest: /etc/systemd/system/faas-schedd.service.d/30-faas-seal-domain-schedd-contract.conf")
	main := read(filepath.Join(role, "tasks", "main.yml"))
	inOrder(t, "main.yml", main, "include_tasks: control.yml", "include_tasks: delivery.yml")
	delivery := read(filepath.Join(role, "tasks", "delivery.yml"))
	inOrder(t, "delivery.yml", delivery,
		"dest: /etc/systemd/system/faas-schedd.service.d/60-faas-seal-domain-delivery.conf",
		"ansible.builtin.meta: flush_handlers",
		"paths: /run/credentials/faas-schedd.service")
}

func inOrder(t *testing.T, name, body string, parts ...string) {
	t.Helper()
	last := -1
	for _, p := range parts {
		i := strings.Index(body, p)
		if i < 0 || i < last {
			t.Errorf("%s: %q missing or out of order (want %q in order)", name, p, parts)
			return
		}
		last = i
	}
}
