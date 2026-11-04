package privatenetwork

import (
	"net/netip"
	"strings"
	"testing"
)

func routerFixture() ([]FabricSpec, []PeeringRoute) {
	networks := []FabricSpec{
		{AccountID: "acct-router", NetworkID: "left", Region: "fra1", CIDR: netip.MustParsePrefix("10.42.0.0/24")},
		{AccountID: "acct-router", NetworkID: "right", Region: "fra1", CIDR: netip.MustParsePrefix("10.43.0.0/24")},
		{AccountID: "acct-router", NetworkID: "unpeered", Region: "fra1", CIDR: netip.MustParsePrefix("10.44.0.0/24")},
	}
	routes := []PeeringRoute{
		{FromNetworkID: "left", DestinationNetwork: "right", DestinationCIDR: networks[1].CIDR},
		{FromNetworkID: "right", DestinationNetwork: "left", DestinationCIDR: networks[0].CIDR},
	}
	return networks, routes
}

func TestRouterPolicyIsolationAndRevocation(t *testing.T) {
	networks, routes := routerFixture()
	commands, err := RouterPolicyCommands("acct-router", "fra1", networks, routes)
	if err != nil {
		t.Fatal(err)
	}
	var script strings.Builder
	for _, command := range commands {
		script.WriteString(strings.Join(command, " ") + "\n")
	}
	if strings.Count(script.String(), " accept\n") != 2 || !strings.Contains(script.String(), "policy drop") {
		t.Fatalf("router must admit only two authorized directions: %s", script.String())
	}
	for _, forbidden := range []string{"established", "10.44.0.0/24", "masquerade"} {
		if strings.Contains(script.String(), forbidden) {
			t.Fatalf("router unexpectedly admits %s: %s", forbidden, script.String())
		}
	}
	if RouterNamespace("acct-router", "fra1") == RouterNamespace("other-account", "fra1") || RouterNamespace("acct-router", "fra1") == RouterNamespace("acct-router", "hel1") {
		t.Fatal("router namespace identity crosses scope")
	}
	commands, err = RouterPolicyCommands("acct-router", "fra1", networks, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range commands {
		if strings.Contains(strings.Join(command, " "), "accept") {
			t.Fatal("revocation retained a forward allow")
		}
	}
}

func TestRouterPolicyRejectsInvalidRoutesBeforeMutation(t *testing.T) {
	for _, scenario := range []string{"one-way", "wrong-account", "wrong-region", "overlap", "wrong-destination", "unknown-network", "duplicate"} {
		t.Run(scenario, func(t *testing.T) {
			networks, routes := routerFixture()
			switch scenario {
			case "one-way":
				routes = routes[:1]
			case "wrong-account":
				networks[1].AccountID = "other"
			case "wrong-region":
				networks[1].Region = "hel1"
			case "overlap":
				networks[1].CIDR = networks[0].CIDR
			case "wrong-destination":
				routes[0].DestinationCIDR = networks[2].CIDR
			case "unknown-network":
				routes[0].FromNetworkID = "missing"
			case "duplicate":
				routes = append(routes, routes[0])
			}
			if _, err := RouterPolicyCommands("acct-router", "fra1", networks, routes); err == nil {
				t.Fatal("invalid router policy accepted")
			}
		})
	}
}
