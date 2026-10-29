package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSealDomainContractRole pins each domain's contract step (ADR
// 20261022111812, step 4) in the seal_domains role: the read-only check runs
// before the reader's drop-in changes, the contracted drop-in resets the
// credentials before it loads the domain identity and unsets the legacy
// path, and the proof looks for the credential names the base units give
// the legacy identities.
func TestSealDomainContractRole(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..")
	role := filepath.Join(root, "deploy", "ansible", "roles", "seal_domains")
	read := func(t *testing.T, path string) string {
		t.Helper()
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	for _, tc := range []struct {
		domain string
		unit   string
		// legacyPath is the variable the contracted drop-in unsets.
		legacyPath string
		// legacy are the base units' legacy credentials the proof checks
		// are gone.
		legacy []string
		units  []string
	}{
		{
			domain:     "outbound",
			unit:       "faas-outboundd",
			legacyPath: "FAAS_FLEET_AGE_IDENTITY_PATH",
			legacy:     []string{"LoadCredential=faas_fleet_age_identity:/etc/faas/secrets/fleet.age"},
			units: []string{
				filepath.Join(root, "deploy", "systemd", "faas-outboundd.service"),
				filepath.Join(root, "deploy", "ansible", "roles", "control_plane_service", "files", "faas-outboundd.service"),
			},
		},
		{
			domain:     "storage",
			unit:       "faas-s3-gatewayd",
			legacyPath: "FAAS_HOST_AGE_IDENTITY_PATH",
			legacy: []string{
				"LoadCredential=faas_fleet_age_identity:/etc/faas/secrets/fleet.age",
				"LoadCredential=faas_host_age_identity:/etc/faas/secrets/host.age",
			},
			units: []string{filepath.Join(root, "deploy", "ansible", "roles", "s3_gateway_service", "files", "faas-s3-gatewayd.service")},
		},
	} {
		t.Run(tc.domain, func(t *testing.T) {
			t.Parallel()
			tasks := read(t, filepath.Join(role, "tasks", tc.domain+".yml"))
			guard := "not (faas_seal_domain_" + tc.domain + "_contracted | bool) or faas_seal_domain_" + tc.domain + "_writes == 'domain'"
			if !strings.Contains(tasks, guard) {
				t.Errorf("tasks/%s.yml is missing %q", tc.domain, guard)
			}
			check := strings.Index(tasks, "      - "+tc.domain+"\n      - --check\n")
			dropIn := strings.Index(tasks, "src: 60-faas-seal-domain-"+tc.domain+"-reader.conf.j2")
			restart := strings.Index(tasks, "ansible.builtin.meta: flush_handlers")
			proof := strings.Index(tasks, "paths: /run/credentials/"+tc.unit+".service")
			if check < 0 || dropIn < 0 || restart < 0 || proof < 0 || check >= dropIn || dropIn >= restart || restart >= proof {
				t.Fatalf("tasks/%s.yml order: check=%d drop-in=%d restart=%d proof=%d; want the check before the drop-in and the proof after the restart", tc.domain, check, dropIn, restart, proof)
			}

			template := read(t, filepath.Join(role, "templates", "60-faas-seal-domain-"+tc.domain+"-reader.conf.j2"))
			reset := strings.Index(template, "\nLoadCredential=\n")
			domain := strings.Index(template, "\nLoadCredential=faas_seal_domain_"+tc.domain+":")
			if reset < 0 || domain < 0 || reset >= domain {
				t.Fatalf("reader drop-in: reset=%d domain credential=%d; want the empty LoadCredential= before the domain identity", reset, domain)
			}
			if !strings.Contains(template, "UnsetEnvironment="+tc.legacyPath) {
				t.Errorf("the contracted reader drop-in does not unset %s", tc.legacyPath)
			}

			for _, line := range tc.legacy {
				name := strings.TrimPrefix(line[:strings.Index(line, ":")], "LoadCredential=")
				if !strings.Contains(tasks, "'"+name+"' in ("+"faas_seal_domain_"+tc.domain+"_credentials.files") {
					t.Errorf("the proof does not check %s is gone", name)
				}
				for _, unit := range tc.units {
					if !strings.Contains(read(t, unit), line) {
						t.Errorf("%s no longer has %q, which the contract proof expects", unit, line)
					}
				}
			}
		})
	}
}
