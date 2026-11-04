package netns

import "strconv"

// PrivateNetworkMTUCommands repairs both ends of an already existing link.
// The peer's MTU also gives the guest a real routed PMTU boundary for UDP and
// ICMP, rather than a silent drop later at a smaller L2 tunnel port.
func (c Config) PrivateNetworkMTUCommands() [][]string {
	if !c.privateNetworkEnabled() || c.PrivateNetworkMTU == 0 {
		return nil
	}
	mtu := strconv.Itoa(c.PrivateNetworkMTU)
	return [][]string{
		{"ip", "link", "set", "dev", c.PrivateVethHost, "mtu", mtu},
		{"ip", "netns", "exec", c.Netns, "ip", "link", "set", "dev", c.PrivateVethPeer, "mtu", mtu},
	}
}

func (c Config) privateNetworkMSSCommands(nft func(...string) []string) [][]string {
	if !c.privateNetworkEnabled() || c.PrivateNetworkMTU == 0 || c.PrivateNetworkMTU >= 1500 {
		return nil
	}
	mss := strconv.Itoa(c.PrivateNetworkMTU - 40)
	var commands [][]string
	for _, direction := range []string{"iifname", "oifname"} {
		commands = append(commands, nft("add", "rule", "ip", "faas", "forward", direction, c.PrivateVethPeer,
			"tcp", "flags", "&", "(", "syn", "|", "rst", ")", "==", "syn", "tcp", "option", "maxseg", "size", ">", mss,
			"tcp", "option", "maxseg", "size", "set", mss))
	}
	return commands
}
