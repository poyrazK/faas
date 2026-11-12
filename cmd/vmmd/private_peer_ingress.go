package main

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/onebox-faas/faas/pkg/netns"
)

// validatePrivatePeerIngress normalises the ADR-20261110120000 ingress
// lists: the control plane's exact sources need the control-plane ports, and
// peer sources and peer ports come together.
func validatePrivatePeerIngress(cn *ComputeNodeConfig) error {
	if len(cn.PrivateControlIngressCIDRs) > 0 && len(cn.PrivateIngressTCPPorts) == 0 {
		return fmt.Errorf("vmmd: private_control_ingress_cidrs requires private_ingress_tcp_ports")
	}
	if (len(cn.PrivatePeerIngressCIDRs) > 0) != (len(cn.PrivatePeerIngressTCPPorts) > 0) {
		return fmt.Errorf("vmmd: private_peer_ingress_cidrs and private_peer_ingress_tcp_ports must be set together")
	}
	for _, list := range []struct {
		name  string
		cidrs []string
	}{{"private_control_ingress_cidrs", cn.PrivateControlIngressCIDRs}, {"private_peer_ingress_cidrs", cn.PrivatePeerIngressCIDRs}} {
		for i, raw := range list.cidrs {
			prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
			if err != nil {
				return fmt.Errorf("vmmd: %s[%d] %q invalid: %w", list.name, i, raw, err)
			}
			list.cidrs[i] = prefix.Masked().String()
		}
	}
	for _, port := range cn.PrivatePeerIngressTCPPorts {
		if port < 1 || port > 65535 {
			return fmt.Errorf("vmmd: private_peer_ingress_tcp_ports contains invalid TCP port %d", port)
		}
	}
	return nil
}

// appendPrivateInputRules admits each source to ports. LoadConfig validated
// the sources; an unparsable one is skipped so the helper stays pure.
func appendPrivateInputRules(rules []netns.PrivateInputAllowRule, sources []string, ports []int) []netns.PrivateInputAllowRule {
	if len(ports) == 0 {
		return rules
	}
	for _, raw := range sources {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			continue
		}
		rules = append(rules, netns.PrivateInputAllowRule{Source: prefix.Masked(), Ports: append([]int(nil), ports...)})
	}
	return rules
}
