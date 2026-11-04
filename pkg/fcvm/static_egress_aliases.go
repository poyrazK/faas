package fcvm

import (
	"net/netip"
	"strings"
)

// The label is kernel-persisted ownership, allowing failed removals to be
// rediscovered after vmmd restarts without touching operator-owned addresses.
const staticEgressAliasLabel = "br-tenants:se"

func managedStaticEgressAliases(output []byte) map[netip.Addr]bool {
	aliases := make(map[netip.Addr]bool)
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		managed := false
		for _, field := range fields {
			if field == staticEgressAliasLabel {
				managed = true
			}
		}
		if !managed {
			continue
		}
		for i, field := range fields {
			if field == "inet" && i+1 < len(fields) {
				prefix, err := netip.ParsePrefix(fields[i+1])
				if err == nil && prefix.Addr().Is4() && prefix.Bits() == 32 {
					aliases[prefix.Addr()] = true
				}
			}
		}
	}
	return aliases
}
