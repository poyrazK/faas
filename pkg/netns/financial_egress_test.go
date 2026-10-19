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
	for _, want := range []string{"hook egress device " + c.VethPeer, "policy drop", "quota 4194304 bytes", "meta length < 20 drop", "ip,ip6,arp", "meta time >= 1793491200 drop", "jump lifetime"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %s", want, text)
		}
	}
	if strings.Contains(text, "ct state") {
		t.Fatal("established traffic bypasses monetary quota")
	}
}
func TestFinancialEgressTimeRenewalCannotResetByteQuota(t *testing.T) {
	c := testConfig()
	c.EgressBudgetBytes = api.FinancialEgressCreditBytes
	c.EgressBudgetUntil = time.Now().Add(time.Minute)
	deadline := flatten(c.EgressBudgetDeadlineCommands())
	if strings.Contains(deadline, "quota") || strings.Contains(deadline, "flush chain netdev faas_budget egress") {
		t.Fatal("time renewal reissued bytes")
	}
	refill := flatten(c.EgressBudgetRefillCommands(api.FinancialEgressMinimumCreditBytes))
	if !strings.Contains(refill, "quota 2048 bytes") || !strings.Contains(refill, "jump lifetime") {
		t.Fatal("refill lost paid bound or kernel expiry")
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
