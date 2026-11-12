package daemonunitspec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCustomDomainTLSModeMatchesInternalAndPublicGateway(t *testing.T) {
	root := repoRoot(t)
	for _, enabled := range []bool{false, true} {
		for _, role := range []string{"gatewayd_internal_service", "gatewayd_public_service"} {
			body, err := os.ReadFile(filepath.Join(root, "deploy", "ansible", "roles", role, "templates", "99-faas-custom-domain-tls.conf.j2"))
			if err != nil {
				t.Fatal(err)
			}
			out := renderPublicEdgeBlock(t, string(body), map[string]any{"faas_custom_domain_tls": enabled})
			want := "Environment=FAAS_CUSTOM_DOMAIN_TLS="
			if enabled {
				want += "on_demand"
			}
			if !strings.Contains(out, want+"\n") {
				t.Fatalf("%s mode %v: %s", role, enabled, out)
			}
		}
	}
}
