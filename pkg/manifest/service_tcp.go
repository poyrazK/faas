// adr: 576
package manifest

// ServiceTCP stages private TCP service addressing (ADR-576) on the
// fleet's compute-only hosts. The stages turn on in ADR-576's rollout
// order and turn off in reverse, so each needs the one before it:
//
//   - Node: vmmd admits service addresses and stamps its readiness, and
//     the boot-time nftables ruleset carries the service-address DNAT.
//   - Listener: gatewayd-internal accepts service TCP on the bridge
//     (api.ServiceTCPProxyPort).
//   - DNS: gatewayd-internal answers guest lookups for service names.
//
// Omitted, every stage stays off. Hand-set daemon values are not kept
// across a rollout, so this block is how the feature stays on.
type ServiceTCP struct {
	Node     bool `yaml:"node,omitempty"`
	Listener bool `yaml:"listener,omitempty"`
	DNS      bool `yaml:"dns,omitempty"`
}

func (m *Manifest) validateServiceTCP() []Error {
	s := m.ServiceTCP
	if s == nil {
		return nil
	}
	var errs []Error
	if s.Listener && !s.Node {
		errs = append(errs, Error{"service_tcp.listener", "requires service_tcp.node: vmmd and nftables turn on before the listener (ADR-576)"})
	}
	if s.DNS && !s.Listener {
		errs = append(errs, Error{"service_tcp.dns", "requires service_tcp.listener: gatewayd-internal refuses service_tcp_dns without its listener"})
	}
	return errs
}
