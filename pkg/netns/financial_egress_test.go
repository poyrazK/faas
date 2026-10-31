package netns

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestFinancialEgressGatePrecedesPeerActivation(t *testing.T) {
	c := testConfig()
	c.EgressBudgetBytes = api.FinancialEgressCreditBytes
	c.EgressBudgetUntil = time.Unix(1793491200, 0)
	cmds := c.SetupCommands()
	gate, up := -1, -1
	for i, cmd := range cmds {
		line := strings.Join(cmd, " ")
		if strings.Contains(line, "add table netdev faas_budget") {
			gate = i
		}
		if strings.Contains(line, "link set "+c.VethPeer+" up") {
			up = i
		}
	}
	if gate < 0 || up <= gate {
		t.Fatalf("unfunded link activation: %v", cmds)
	}
	text := flatten(c.EgressBudgetSetupCommands())
	for _, want := range []string{"hook egress device " + c.VethPeer, "policy drop", "netdev faas_budget egress quota 2097152 bytes", "meta length < 20 drop", "ip,ip6,arp", "meta time >= 1793491200 drop", "jump lifetime",
		"inet faas_budget postrouting { type filter hook postrouting", "postrouting oifname " + c.VethPeer + " jump gate", "inet faas_budget gate quota 3145728 bytes", "inet faas_budget gate drop", "inet faas_budget lifetime meta time >= 1793491200 drop"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %s", want, text)
		}
	}
	if strings.Contains(text, "ct state") {
		t.Fatal("established traffic bypasses monetary quota")
	}
}

// adr: 1748 amendment 1 — guest IP skips the netdev egress hook after the
// segmentation IFB, so it is counted at inet postrouting in network bytes.
func TestFinancialEgressGuestIPGateFollowsSegmentation(t *testing.T) {
	c := testConfig()
	c.EgressBudgetBytes = api.FinancialEgressCreditBytes
	cmds := c.EgressBudgetSetupCommands()
	jump, gate := -1, -1
	for i, cmd := range cmds {
		line := strings.Join(cmd, " ")
		if strings.Contains(line, "add chain inet faas_budget gate") {
			gate = i
		}
		if strings.HasSuffix(line, "postrouting oifname "+c.VethPeer+" jump gate") {
			if jump >= 0 {
				t.Fatal("guest IP is counted twice")
			}
			jump = i
		}
	}
	if gate < 0 || jump <= gate {
		t.Fatalf("guest IP reaches the peer before its gate: %v", cmds)
	}
	if text := flatten(c.EgressBudgetRefillCommands(api.FinancialEgressCreditBytes)); strings.Contains(text, "postrouting") {
		t.Fatal("refill duplicated the guest IP hook")
	}
}

// Root veth RX stays within the financed bytes for every credit size: the
// netdev share counts whole frames, the inet share network bytes that framing
// at most doubles. The netdev share is the refill low-water mark.
func TestFinancialEgressQuotasStayWithinFinancedBytes(t *testing.T) {
	for _, bytes := range []int64{api.FinancialEgressMinimumCreditBytes, api.FinancialEgressMinimumCreditBytes + 1, 1 << 20, api.FinancialEgressCreditBytes - 1, api.FinancialEgressCreditBytes} {
		local, ip := EgressBudgetQuotas(bytes)
		if local <= 0 || ip <= 0 || local+api.FinancialEgressFramingFactor*ip > bytes {
			t.Errorf("credit %d: local=%d ip=%d exceeds financed bytes", bytes, local, ip)
		}
		if local != bytes/(2*api.FinancialEgressFramingFactor) {
			t.Errorf("credit %d: netdev share %d is not the refill mark", bytes, local)
		}
	}
}
func TestFinancialEgressTimeRenewalCannotResetByteQuota(t *testing.T) {
	c := testConfig()
	c.EgressBudgetBytes = api.FinancialEgressCreditBytes
	c.EgressBudgetUntil = time.Now().Add(time.Minute)
	deadline := flatten(c.EgressBudgetDeadlineCommands())
	if strings.Contains(deadline, "quota") || strings.Contains(deadline, "flush chain netdev faas_budget egress") || strings.Contains(deadline, "flush chain inet faas_budget gate") {
		t.Fatal("time renewal reissued bytes")
	}
	if !strings.Contains(deadline, "flush chain inet faas_budget lifetime") {
		t.Fatal("time renewal left the guest IP gate on the old expiry")
	}
	refill := flatten(c.EgressBudgetRefillCommands(api.FinancialEgressMinimumCreditBytes))
	for _, want := range []string{"flush chain netdev faas_budget egress", "flush chain inet faas_budget gate", "egress quota 1024 bytes", "gate quota 1536 bytes", "egress jump lifetime", "gate jump lifetime"} {
		if !strings.Contains(refill, want) {
			t.Fatalf("refill lost paid bound or kernel expiry: missing %q", want)
		}
	}
}

func TestFinancialEgressSegmentsBeforeRaisingGuestTAP(t *testing.T) {
	c := testConfig()
	c.EgressBudgetBytes = api.FinancialEgressCreditBytes
	commands := c.SetupCommands()
	redirect, tapUp := -1, -1
	for i, argv := range commands {
		line := strings.Join(argv, " ")
		if strings.Contains(line, "redirect dev fb-egress") {
			redirect = i
		}
		if strings.Contains(line, "link set "+c.Tap+" up") {
			tapUp = i
		}
	}
	if redirect < 0 || tapUp <= redirect {
		t.Fatalf("guest can emit GSO before segmentation: %v", commands)
	}
	text := flatten(c.EgressBudgetSegmentationCommands())
	for _, want := range []string{"type ifb", "sg off tso off gso off gro off", "tx-udp-segmentation off", "tx-gso-list off", "protocol all matchall", "redirect dev fb-egress"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing offload boundary %q", want)
		}
	}
}
