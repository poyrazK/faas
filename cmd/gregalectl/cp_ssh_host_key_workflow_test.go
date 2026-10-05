package main

import (
	"regexp"
	"strings"
	"testing"
)

// TestControlPlaneSSHTrustsOnlyVerifiedHostKeys: every workflow that SSHes
// to the control plane as root must accept only a host key the operator
// verified in COMPUTE_KNOWN_HOSTS. They used to append whatever
// `ssh-keyscan` returned to known_hosts on every run, so a spoofed host
// would have received production secrets.
func TestControlPlaneSSHTrustsOnlyVerifiedHostKeys(t *testing.T) {
	unverifiedScan := regexp.MustCompile(`ssh-keyscan[^\n]*>>`)
	for _, name := range []string{"cd-controlplane.yml", "cd-platform.yml", "pki-renew.yml", "synthetic-canary.yml"} {
		t.Run(name, func(t *testing.T) {
			workflow := readWorkflow(t, name)
			if loc := unverifiedScan.FindStringIndex(workflow); loc != nil {
				t.Errorf("appends unverified ssh-keyscan output to known_hosts: %q", workflow[loc[0]:loc[1]])
			}
			for _, required := range []string{
				"COMPUTE_KNOWN_HOSTS: ${{ secrets.COMPUTE_KNOWN_HOSTS }}",
				`scanned="$(ssh-keyscan -T 10 -t ed25519,ecdsa,rsa "$CP_HOST" 2>/dev/null || true)"`,
				`<<<"$COMPUTE_KNOWN_HOSTS"`,
				"presented no host key listed in COMPUTE_KNOWN_HOSTS",
			} {
				if !strings.Contains(workflow, required) {
					t.Errorf("control-plane host key pinning is missing %q", required)
				}
			}
		})
	}
}
