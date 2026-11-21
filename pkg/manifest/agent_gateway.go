package manifest

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// AgentEgressGateway describes the agent egress gateway
// (ADR-20261117185346): a WireGuard listener with its own tunnel and public
// addresses, apart from the tenant gateway's, that carries every agent VM's
// egress. It may share the tenant gateway's host.
type AgentEgressGateway struct {
	// Endpoint is the gateway's agent WireGuard listener, host:port. On a
	// host shared with the tenant gateway it is a different port.
	Endpoint string `yaml:"endpoint"`
	// PublicKey is the gateway's agent WireGuard public key (base64, 32
	// bytes), its own, never the tenant tunnel's.
	PublicKey string `yaml:"public_key"`
	// TunnelCIDR is the private IPv4 agent tunnel network. It is cut into
	// node slices of NodePrefix: slice 0 is the gateway's, and a compute
	// node named <prefix>-<N> takes slice N (AgentNodeSlice).
	TunnelCIDR string `yaml:"tunnel_cidr"`
	// NodePrefix is the length of each node's slice. A node holds the
	// slice's first host address and gives each agent VM another one, so
	// a /22 serves 1,021 agent VMs at once. Zero is AgentNodePrefixDefault.
	NodePrefix int `yaml:"node_prefix,omitempty"`
	// Addresses are the gateway's public IPv4 addresses agent traffic
	// leaves from. They carry only agent traffic, never the tenant
	// gateway's; the operator assigns them to the gateway host.
	Addresses []string `yaml:"addresses"`
	// SSHHost is the address Ansible uses to converge the gateway. Empty
	// means the endpoint host.
	SSHHost string `yaml:"ssh_host,omitempty"`
	// Proxy puts agentproxyd, the politeness proxy, in the path of agent
	// TCP 80 and 443 (ADR-20261117185346 §4), and has every node's vmmd
	// register its agent VMs with it. Off, the gateway forwards agent
	// traffic directly. The gateway needs a release with agentproxyd
	// installed before this is turned on.
	Proxy bool `yaml:"proxy,omitempty"`
}

// AgentProxyRegistrationPort is agentproxyd's registration listener on the
// gateway's tunnel address, which nodes' vmmd calls over wg-agent.
const AgentProxyRegistrationPort = 15081

// AgentProxyURL is the registration API's base URL for the nodes' vmmd.
func AgentProxyURL(tunnelCIDR string) (string, error) {
	gw, err := AgentGatewayAddress(tunnelCIDR)
	if err != nil {
		return "", err
	}
	return "http://" + netip.AddrPortFrom(gw.Addr(), AgentProxyRegistrationPort).String(), nil
}

// AgentNodePrefixDefault is a node slice's length when the manifest names
// none.
const AgentNodePrefixDefault = 22

// agentGatewayAddressesMax bounds the agent public addresses.
const agentGatewayAddressesMax = 32

// AnsibleHost is the inventory address of the gateway.
func (g *AgentEgressGateway) AnsibleHost() string {
	if g.SSHHost != "" {
		return g.SSHHost
	}
	host, _, _ := net.SplitHostPort(g.Endpoint)
	return host
}

// ListenPort is the gateway's agent WireGuard UDP port from Endpoint.
func (g *AgentEgressGateway) ListenPort() int {
	_, port, _ := net.SplitHostPort(g.Endpoint)
	n, _ := strconv.Atoi(port)
	return n
}

// Prefix is the node slice length in effect.
func (g *AgentEgressGateway) Prefix() int {
	if g.NodePrefix == 0 {
		return AgentNodePrefixDefault
	}
	return g.NodePrefix
}

// AgentGatewayAddress is the gateway's agent tunnel address: the first host
// address of the tunnel, inside slice 0.
func AgentGatewayAddress(tunnelCIDR string) (netip.Prefix, error) {
	tunnel, err := netip.ParsePrefix(tunnelCIDR)
	if err != nil || !tunnel.Addr().Is4() {
		return netip.Prefix{}, fmt.Errorf("agent tunnel CIDR %q is not IPv4", tunnelCIDR)
	}
	return netip.PrefixFrom(tunnel.Masked().Addr().Next(), tunnel.Bits()), nil
}

// AgentNodeSlice is the compute node nodeName's slice of the agent tunnel
// and its own address in it, the slice's first host address
// (ADR-20261117185346 §2): slice N for a name ending in -N.
func AgentNodeSlice(tunnelCIDR string, nodePrefix int, nodeName string) (slice netip.Prefix, node netip.Addr, err error) {
	tunnel, err := netip.ParsePrefix(tunnelCIDR)
	if err != nil || !tunnel.Addr().Is4() {
		return netip.Prefix{}, netip.Addr{}, fmt.Errorf("agent tunnel CIDR %q is not IPv4", tunnelCIDR)
	}
	if nodePrefix <= tunnel.Bits() || nodePrefix > 28 {
		return netip.Prefix{}, netip.Addr{}, fmt.Errorf("agent node prefix /%d does not fit in tunnel %s", nodePrefix, tunnelCIDR)
	}
	label, _, _ := strings.Cut(nodeName, ".")
	m := nodeNameIndex.FindStringSubmatch(label)
	if m == nil {
		return netip.Prefix{}, netip.Addr{}, fmt.Errorf("node name %q does not end in -<number>", nodeName)
	}
	slices := uint64(1) << (nodePrefix - tunnel.Bits())
	n, err := strconv.ParseUint(m[1], 10, 32)
	if err != nil || n < 1 || n >= slices {
		return netip.Prefix{}, netip.Addr{}, fmt.Errorf("node %q does not fit in agent tunnel %s at /%d", nodeName, tunnelCIDR, nodePrefix)
	}
	base := tunnel.Masked().Addr().As4()
	v := uint32(base[0])<<24 | uint32(base[1])<<16 | uint32(base[2])<<8 | uint32(base[3])
	v += uint32(n) << (32 - nodePrefix) // n < slices, checked above
	start := netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
	slice = netip.PrefixFrom(start, nodePrefix)
	return slice, start.Next(), nil
}

func (g *AgentEgressGateway) validate(overlayCIDR string, tenant *TenantEgressGateway) Errors {
	if g == nil {
		return nil
	}
	var errs Errors
	add := func(field, msg string) { errs = append(errs, Error{"egress.agent_gateway." + field, msg}) }
	host, port, err := net.SplitHostPort(g.Endpoint)
	if n, perr := strconv.Atoi(port); err != nil || host == "" || perr != nil || n < 1 || n > 65535 {
		add("endpoint", fmt.Sprintf("%q must be host:port", g.Endpoint))
	} else if tenant != nil && g.Endpoint == tenant.Endpoint {
		add("endpoint", "must not be the tenant gateway's listener; give the agent tunnel its own port")
	}
	if g.SSHHost != "" && strings.ContainsAny(g.SSHHost, " \t\n/") {
		add("ssh_host", fmt.Sprintf("%q must be a host name or address", g.SSHHost))
	}
	if key, err := base64.StdEncoding.DecodeString(g.PublicKey); err != nil || len(key) != 32 {
		add("public_key", "must be a base64 WireGuard public key (32 bytes)")
	} else if tenant != nil && g.PublicKey == tenant.PublicKey {
		add("public_key", "must be the agent tunnel's own key, not the tenant gateway's")
	}
	errs = append(errs, g.validateTunnel(overlayCIDR, tenant)...)
	if len(g.Addresses) == 0 || len(g.Addresses) > agentGatewayAddressesMax {
		add("addresses", fmt.Sprintf("must list 1-%d public IPv4 addresses", agentGatewayAddressesMax))
	}
	seen := map[netip.Addr]bool{}
	for i, raw := range g.Addresses {
		a, err := netip.ParseAddr(raw)
		switch {
		case err != nil || !a.Is4():
			add(fmt.Sprintf("addresses[%d]", i), fmt.Sprintf("%q must be an IPv4 address", raw))
		case !a.IsGlobalUnicast() || a.IsPrivate():
			add(fmt.Sprintf("addresses[%d]", i), fmt.Sprintf("%q must be a public address", raw))
		case seen[a]:
			add(fmt.Sprintf("addresses[%d]", i), fmt.Sprintf("%q is listed twice", raw))
		}
		seen[a] = true
	}
	return errs
}

func (g *AgentEgressGateway) validateTunnel(overlayCIDR string, tenant *TenantEgressGateway) Errors {
	var errs Errors
	add := func(field, msg string) { errs = append(errs, Error{"egress.agent_gateway." + field, msg}) }
	tunnel, err := netip.ParsePrefix(g.TunnelCIDR)
	switch {
	case err != nil || !tunnel.Addr().Is4():
		add("tunnel_cidr", fmt.Sprintf("%q must be an IPv4 CIDR", g.TunnelCIDR))
		return errs
	case !tunnel.Addr().IsPrivate() || tunnel.Bits() < 12 || tunnel.Bits() > 24:
		add("tunnel_cidr", fmt.Sprintf("%q must be a private IPv4 network between /12 and /24", g.TunnelCIDR))
		return errs
	case tunnel.Overlaps(tenantBridgeCIDR):
		add("tunnel_cidr", fmt.Sprintf("%q overlaps the tenant bridge %s", g.TunnelCIDR, tenantBridgeCIDR))
	}
	if overlay, oerr := netip.ParsePrefix(overlayCIDR); oerr == nil && tunnel.Overlaps(overlay) {
		add("tunnel_cidr", fmt.Sprintf("%q overlaps the overlay %s", g.TunnelCIDR, overlay))
	}
	if tenant != nil {
		if t, terr := netip.ParsePrefix(tenant.TunnelCIDR); terr == nil && tunnel.Overlaps(t) {
			add("tunnel_cidr", fmt.Sprintf("%q overlaps the tenant tunnel %s", g.TunnelCIDR, t))
		}
	}
	if p := g.Prefix(); p <= tunnel.Bits() || p > 28 {
		add("node_prefix", fmt.Sprintf("/%d must leave at least two slices in %s and be at most /28", p, g.TunnelCIDR))
	}
	return errs
}
