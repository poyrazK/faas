package fcvm

// quarantineWakeRequest (ADR-732) clears every input that would give a
// production fork connectivity: the egress allowlist, the private network
// side-link and the static egress IP. The netns quarantine chains already
// drop all guest-originated traffic and every new inbound flow not from the
// platform veth; clearing the inputs as well means no route, SNAT
// registration or side-link exists for those chains to have to stop.
//
// The quarantine flag survives on Instance.Net, so later in-place updates
// for the same app (allowlist, ports, private network) re-render a fork with
// its quarantine chains intact.
func quarantineWakeRequest(req WakeRequest) WakeRequest {
	if !req.Quarantine {
		return req
	}
	req.EgressAllowlist = nil
	req.PrivateNetworkCIDRs = nil
	req.PrivateNetworkAllowedCIDRs = nil
	req.PrivateNetworkFirewallRules = nil
	req.PrivateNetworkID = ""
	req.PrivateNetworkAddress = ""
	req.StaticEgressIP = ""
	return req
}
