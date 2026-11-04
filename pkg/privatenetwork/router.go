package privatenetwork

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

// RouterNamespace is one isolated routing and conntrack domain per account
// and region. Different accounts may reuse the same private address space.
func RouterNamespace(accountID, region string) string { return routerName("gpr-", accountID, region) }
func RouterHostLink(accountID, networkID string) string {
	return routerName("grh-", accountID, networkID)
}
func RouterPeerLink(accountID, networkID string) string {
	return routerName("gri-", accountID, networkID)
}

func routerName(prefix, accountID, identity string) string {
	sum := sha256.Sum256([]byte(prefix + accountID + "\x00" + identity))
	return prefix + hex.EncodeToString(sum[:])[:10]
}

// RouterPolicyCommands replaces the complete account/region policy in one
// nft transaction. There is no general established-flow exemption: removing
// a peering also revokes its existing flows. Unpeered networks cannot transit
// through a third network, and all source/destination addresses are checked.
func RouterPolicyCommands(accountID, region string, networks []FabricSpec, routes []PeeringRoute) ([][]string, error) {
	if accountID == "" || apiIdentifier(region) != nil {
		return nil, fmt.Errorf("privatenetwork: router account and region are required")
	}
	byID := make(map[string]FabricSpec, len(networks))
	for _, spec := range networks {
		validated, err := NewFabricSpecFromValues(spec.AccountID, spec.NetworkID, spec.Region, spec.CIDR.String())
		if err != nil {
			return nil, err
		}
		if validated.AccountID != accountID || validated.Region != region {
			return nil, fmt.Errorf("privatenetwork: router network scope mismatch")
		}
		if _, exists := byID[validated.NetworkID]; exists {
			return nil, fmt.Errorf("privatenetwork: duplicate router network %s", validated.NetworkID)
		}
		for _, prior := range byID {
			if validated.CIDR.Overlaps(prior.CIDR) {
				return nil, fmt.Errorf("privatenetwork: overlapping router networks")
			}
		}
		byID[validated.NetworkID] = validated
	}
	namespace := RouterNamespace(accountID, region)
	nft := func(args ...string) []string {
		return append([]string{"ip", "netns", "exec", namespace, "nft"}, args...)
	}
	commands := [][]string{
		nft("add", "table", "inet", "faas_peering"),
		nft("flush", "table", "inet", "faas_peering"),
		nft("add", "chain", "inet", "faas_peering", "input", "{ type filter hook input priority 0; policy drop; }"),
		nft("add", "chain", "inet", "faas_peering", "forward", "{ type filter hook forward priority 0; policy drop; }"),
	}
	canonical := append([]PeeringRoute(nil), routes...)
	sort.Slice(canonical, func(i, j int) bool {
		if canonical[i].FromNetworkID != canonical[j].FromNetworkID {
			return canonical[i].FromNetworkID < canonical[j].FromNetworkID
		}
		return canonical[i].DestinationNetwork < canonical[j].DestinationNetwork
	})
	seen := make(map[string]bool, len(routes))
	for _, route := range canonical {
		from, fromOK := byID[route.FromNetworkID]
		to, toOK := byID[route.DestinationNetwork]
		if !fromOK || !toOK || from.NetworkID == to.NetworkID || route.DestinationCIDR != to.CIDR {
			return nil, fmt.Errorf("privatenetwork: invalid router route %s -> %s", route.FromNetworkID, route.DestinationNetwork)
		}
		key := from.NetworkID + "\x00" + to.NetworkID
		if seen[key] {
			return nil, fmt.Errorf("privatenetwork: duplicate router route")
		}
		seen[key] = true
		commands = append(commands, nft("add", "rule", "inet", "faas_peering", "forward",
			"iifname", RouterPeerLink(accountID, from.NetworkID), "oifname", RouterPeerLink(accountID, to.NetworkID),
			"ip", "saddr", from.CIDR.String(), "ip", "daddr", to.CIDR.String(), "accept"))
	}
	for _, route := range canonical {
		if !seen[route.DestinationNetwork+"\x00"+route.FromNetworkID] {
			return nil, fmt.Errorf("privatenetwork: router routes must be bidirectional")
		}
	}
	return commands, nil
}
