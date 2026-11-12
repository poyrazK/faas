package renderer

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/manifest"
)

// The control plane's public_edge role refuses to deploy a site that admits
// Cloudflare unless the manifest says how to treat its connections
// (ADR-20261103172551), so an unset dns.origin_pull must fail here, not in
// the next control-plane deploy.
func TestGCPProductionManifestPicksOriginPull(t *testing.T) {
	m, err := manifest.Load(filepath.Join("..", "..", "deploy", "manifest", "production", "gcp-live.template.yaml"))
	if err != nil {
		t.Fatalf("load production manifest: %v", err)
	}
	if m.DNS.Mode == "cloudflare" && m.DNS.OriginPull == "" {
		t.Fatal("production manifest uses dns.mode cloudflare without dns.origin_pull (required or any_zone); the control-plane deploy would fail")
	}
}

func TestGCPProductionManifestRendersPrivateComputeReadiness(t *testing.T) {
	manifestPath := filepath.Join("..", "..", "deploy", "manifest", "production", "gcp-live.template.yaml")
	m, err := manifest.Load(manifestPath)
	if err != nil {
		t.Fatalf("load production manifest: %v", err)
	}

	for daemon, want := range map[string]string{
		"vmmd":     `metrics_addr = "fsn-2.gregale.dev:9104"`,
		"imaged":   `metrics_addr = "fsn-2.gregale.dev:9102"`,
		"builderd": `metrics_addr = "fsn-2.gregale.dev:9105"`,
		"schedd":   `metrics_addr = "fsn-2.gregale.dev:9103"`,
	} {
		t.Run(daemon, func(t *testing.T) {
			dc := daemonConfigFor(m, daemon)
			if dc == nil || dc.Bind == "" {
				t.Fatalf("production manifest does not declare %s with a non-empty bind", daemon)
			}
			body, _, err := renderTOML(tomlRenderCtx{
				Daemon:      daemon,
				DC:          dc,
				HostName:    "fsn-2",
				HostAddress: "fsn-2.gregale.dev:50051",
				HostRole:    "compute-only",
			})
			if err != nil {
				t.Fatalf("render %s: %v", daemon, err)
			}
			if !strings.Contains(string(body), want) {
				t.Fatalf("rendered %s config missing %q:\n%s", daemon, want, body)
			}
		})
	}
}
