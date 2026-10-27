package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSealDomainCDContract pins how CD hands the seal-domain keys (ADR
// 20261022111812) to the seal_domains role: the control-plane converges and
// the compute join both stage them from SEAL_DOMAIN_* secrets.
func TestSealDomainCDContract(t *testing.T) {
	t.Parallel()
	repoRoot := filepath.Join("..", "..")
	secrets := func(domains ...string) []string {
		var out []string
		for _, d := range domains {
			for _, kind := range []string{"AGE_KEY", "AGE_RECIPIENT"} {
				name := "SEAL_DOMAIN_" + d + "_" + kind
				out = append(out, name+": ${{ secrets."+name+" }}")
			}
		}
		return out
	}
	tests := []struct {
		workflow string
		tokens   []string
		// count is how many times the staging call must appear.
		call  string
		count int
	}{
		{
			workflow: ".github/workflows/cd-controlplane.yml",
			tokens:   secrets("OUTBOUND", "SOURCE", "ALERTING", "STORAGE", "CONTROL", "DELIVERY", "ACCOUNT", "RUNTIME", "BUILD", "EDGE"),
			// The full converge and the host-configuration lane.
			call:  `scripts/ci/seal_domain_vars.sh control-plane "$work/seal-domains" "$work/vars.yml"`,
			count: 2,
		},
		{
			workflow: ".github/workflows/cd-compute.yml",
			tokens:   secrets("RUNTIME", "BUILD", "EDGE", "CONTROL", "DELIVERY"),
			call:     `scripts/ci/seal_domain_vars.sh compute "$ARTIFACT_DIR/seal-domains" "$ARTIFACT_DIR/ansible-vars.yml"`,
			count:    1,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(filepath.Base(test.workflow), func(t *testing.T) {
			t.Parallel()
			body, err := os.ReadFile(filepath.Join(repoRoot, test.workflow))
			if err != nil {
				t.Fatal(err)
			}
			for _, token := range test.tokens {
				if !strings.Contains(string(body), token) {
					t.Errorf("%s is missing seal-domain CD contract %q", test.workflow, token)
				}
			}
			if got := strings.Count(string(body), test.call); got != test.count {
				t.Errorf("%s stages seal-domain keys %d times, want %d: %s", test.workflow, got, test.count, test.call)
			}
		})
	}
}
