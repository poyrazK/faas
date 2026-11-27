package manifest

// OutboundRelayPort is outboundd's ADR-989 app relay port on the control
// plane's private address. The nftables role opens it to compute nodes, and
// compute vmmd dials it as outboundd.faas (the relay server leaf's SAN).
const OutboundRelayPort = 8096

// OutboundRelay turns on the ADR-989 app outbound relay: apps reach managed
// outbound integrations and the AI gateway at GREGALE_OUTBOUND_URL inside
// their VM, and vmmd splices those connections to outboundd on the control
// plane. Absent keeps the relay closed: vmmd logs "outbound relay disabled"
// and GREGALE_OUTBOUND_URL answers nothing.
//
// The relay authenticates each app with a workload-identity assertion, so
// the cluster workload-identity key must be installed first (workload_identity
// or docs/runbooks/FaasWorkloadIdentityNotConfigured.md). The control-plane
// play refuses to open the relay without the JWKS.
type OutboundRelay struct {
	// Enabled starts outboundd's relay listener on the control plane and
	// issues every compute node's vmmd its relay client leaf.
	Enabled bool `yaml:"enabled,omitempty"`
}
