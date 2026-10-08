// adr: 732
package fcvm

import (
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func connectedWakeRequest(quarantine bool) WakeRequest {
	return WakeRequest{
		Instance:                    "inst-1",
		Quarantine:                  quarantine,
		EgressAllowlist:             []string{"203.0.113.0/24"},
		EgressPorts:                 []uint16{5432},
		PrivateNetworkCIDRs:         []string{"10.30.0.0/16"},
		PrivateNetworkAllowedCIDRs:  []string{"10.30.1.0/24"},
		PrivateNetworkFirewallRules: []api.PrivateNetworkFirewallRule{{}},
		PrivateNetworkID:            "net-1",
		PrivateNetworkAddress:       "10.30.0.9",
		StaticEgressIP:              "198.51.100.7",
	}
}

func TestQuarantineWakeRequestClearsConnectivityInputs(t *testing.T) {
	got := quarantineWakeRequest(connectedWakeRequest(true))
	if got.EgressAllowlist != nil || got.PrivateNetworkCIDRs != nil || got.PrivateNetworkAllowedCIDRs != nil ||
		got.PrivateNetworkFirewallRules != nil || got.PrivateNetworkID != "" || got.PrivateNetworkAddress != "" ||
		got.StaticEgressIP != "" {
		t.Fatalf("quarantined request kept connectivity inputs: %+v", got)
	}
	if !got.Quarantine || got.Instance != "inst-1" {
		t.Fatalf("quarantined request lost identity: %+v", got)
	}
}

func TestQuarantineWakeRequestLeavesNormalWakesUntouched(t *testing.T) {
	in := connectedWakeRequest(false)
	if got := quarantineWakeRequest(in); !reflect.DeepEqual(got, in) {
		t.Fatalf("normal wake changed:\n got %+v\nwant %+v", got, in)
	}
}

func TestColdBootCarriesQuarantine(t *testing.T) {
	// ColdBoot builds its WakeRequest field by field; a fork that falls back
	// to cold boot (ADR-005) must not silently lose its quarantine.
	field, ok := reflect.TypeOf(ColdBootRequest{}).FieldByName("Quarantine")
	if !ok || field.Type.Kind() != reflect.Bool {
		t.Fatal("ColdBootRequest has no Quarantine bool")
	}
}
