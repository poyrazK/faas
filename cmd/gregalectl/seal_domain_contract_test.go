package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestSealDomainContractRole pins each domain's contract step (ADR
// 20261022111812, step 4) in the seal_domains role: the read-only check runs
// before the reader's drop-in changes, the contracted drop-in resets the
// credentials before it loads the domain identity and unsets the legacy
// path, and the proof checks every legacy identity the reset drops is gone.
// The reset drops what the base unit and the optional_credentials role
// (40-*, before the 60- drop-in) load, so each of those must be a legacy
// identity or be loaded again by the contracted drop-in.
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
	var optional struct {
		Credentials []struct {
			Service string `yaml:"service"`
			Name    string `yaml:"name"`
		} `yaml:"faas_optional_credentials"`
	}
	if err := yaml.Unmarshal([]byte(read(t, filepath.Join(root, "deploy", "ansible", "roles", "optional_credentials", "defaults", "main.yml"))), &optional); err != nil {
		t.Fatal(err)
	}
	credentialRe := regexp.MustCompile(`(?m)^LoadCredential=([^:\s]+):\S+$`)
	for _, tc := range []struct {
		domain string
		unit   string
		// legacyPath is the variable the contracted drop-in unsets.
		legacyPath string
		// legacy are the legacy identity credentials the reset drops and
		// the proof checks are gone.
		legacy []string
		units  []string
	}{
		{
			domain:     "outbound",
			unit:       "faas-outboundd",
			legacyPath: "FAAS_FLEET_AGE_IDENTITY_PATH",
			legacy:     []string{"faas_fleet_age_identity"},
			units: []string{
				filepath.Join(root, "deploy", "systemd", "faas-outboundd.service"),
				filepath.Join(root, "deploy", "ansible", "roles", "control_plane_service", "files", "faas-outboundd.service"),
			},
		},
		{
			domain:     "storage",
			unit:       "faas-s3-gatewayd",
			legacyPath: "FAAS_HOST_AGE_IDENTITY_PATH",
			legacy:     []string{"faas_fleet_age_identity", "faas_host_age_identity", "faas_host_age_identity_previous"},
			units:      []string{filepath.Join(root, "deploy", "ansible", "roles", "s3_gateway_service", "files", "faas-s3-gatewayd.service")},
		},
		{
			domain:     "alerting",
			unit:       "faas-meterd",
			legacyPath: "FAAS_HOST_AGE_IDENTITY_PATH",
			legacy:     []string{"faas_fleet_age_identity", "faas_host_age_identity", "faas_host_age_identity_previous"},
			units:      []string{filepath.Join(root, "deploy", "ansible", "roles", "control_plane_service", "files", "faas-meterd.service")},
		},
		{
			domain:     "build",
			unit:       "faas-imaged",
			legacyPath: "FAAS_HOST_AGE_IDENTITY_PATH",
			legacy:     []string{"faas_fleet_age_identity", "faas_host_age_identity"},
			units: []string{
				filepath.Join(root, "deploy", "systemd", "faas-imaged.service"),
				filepath.Join(root, "deploy", "ansible", "roles", "compute_only_service", "files", "faas-imaged.service"),
			},
		},
		{
			domain:     "source",
			unit:       "faas-githubd",
			legacyPath: "FAAS_HOST_AGE_IDENTITY_PATH",
			legacy:     []string{"faas_fleet_age_identity", "faas_host_age_identity"},
			units:      []string{filepath.Join(root, "deploy", "ansible", "roles", "githubd_service", "files", "faas-githubd.service")},
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

			legacy := map[string]bool{}
			for _, name := range tc.legacy {
				legacy[name] = true
				if !strings.Contains(tasks, "'"+name+"' in (faas_seal_domain_"+tc.domain+"_credentials.files") {
					t.Errorf("the proof does not check %s is gone", name)
				}
			}
			for _, unit := range tc.units {
				for _, m := range credentialRe.FindAllStringSubmatch(read(t, unit), -1) {
					if !legacy[m[1]] && strings.Index(template, "\n"+m[0]+"\n") < reset {
						t.Errorf("%s loads %s, which the contracted drop-in's reset would drop; load it again after the reset or list it as legacy", unit, m[1])
					}
				}
			}
			for _, c := range optional.Credentials {
				if c.Service == tc.unit && !legacy[c.Name] {
					t.Errorf("optional_credentials projects %s into %s before the 60- drop-in, so the reset drops it; re-add it there or list it as legacy", c.Name, tc.unit)
				}
			}
		})
	}
}
