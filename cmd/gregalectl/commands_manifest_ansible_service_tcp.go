// adr: 576
package main

import (
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/manifest"
)

// renderServiceTCPVars is a compute-only host's private TCP service
// addressing (ADR-576), stage by stage from the manifest's service_tcp
// block. Other roles run no tenant guests and get nothing.
func renderServiceTCPVars(s *manifest.ServiceTCP, host manifest.Host) string {
	if s == nil || !s.Node || host.Role != roleComputeOnly {
		return ""
	}
	var b strings.Builder
	b.WriteString("# ADR-576: private TCP service addressing, from the manifest.\n")
	// vmmd (its routing drop-in) and the boot-time nftables ruleset.
	b.WriteString("faas_service_tcp_enabled: true\n")
	// Port 443 is DNATed only while the private service HTTPS listener is
	// staged; the nftables template reads this through | bool.
	b.WriteString("faas_service_tcp_https_enabled: \"{{ gatewayd_service_proxy_https_listen | default('') | length > 0 }}\"\n")
	if s.Listener {
		// The bridge address of the service proxy, on the service TCP port.
		fmt.Fprintf(&b, "gatewayd_service_tcp_listen: \"{{ gatewayd_service_proxy_listen | regex_replace(':[0-9]+$', ':%d') }}\"\n", api.ServiceTCPProxyPort)
	}
	if s.DNS {
		b.WriteString("gatewayd_service_tcp_dns: true\n")
	}
	return b.String()
}
