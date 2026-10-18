package api

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestMCPReadinessPagerDutyValidation(t *testing.T) {
	zero := int64(0)
	on, off := true, false
	max := int64(math.MaxInt64)
	for _, region := range []string{"", "us", "eu"} {
		if err := ValidateMCPReadinessPagerDutyRequest(SetMCPReadinessPagerDutyRequest{ExpectedVersion: &zero, Enabled: &on, IntegrationKey: strings.Repeat("a", 32), Region: region}); err != nil {
			t.Fatal(region, err)
		}
	}
	for _, region := range []string{"EU", " us", "ap", "https://events.eu.pagerduty.com/v2/enqueue"} {
		if ValidateMCPReadinessPagerDutyRequest(SetMCPReadinessPagerDutyRequest{ExpectedVersion: &zero, Enabled: &on, IntegrationKey: strings.Repeat("a", 32), Region: region}) == nil {
			t.Fatal("invalid region accepted", region)
		}
	}
	if ValidateMCPReadinessPagerDutyRequest(SetMCPReadinessPagerDutyRequest{ExpectedVersion: &zero, Enabled: &off, Region: "eu"}) == nil {
		t.Fatal("disabled region accepted")
	}
	for _, r := range []SetMCPReadinessPagerDutyRequest{{ExpectedVersion: &zero, Enabled: &on, IntegrationKey: strings.Repeat("a", 32)}, {ExpectedVersion: &zero, Enabled: &off}} {
		if e := ValidateMCPReadinessPagerDutyRequest(r); e != nil {
			t.Fatal(e)
		}
	}
	for _, r := range []SetMCPReadinessPagerDutyRequest{{}, {ExpectedVersion: &zero}, {ExpectedVersion: &zero, Enabled: &on}, {ExpectedVersion: &zero, Enabled: &on, IntegrationKey: strings.Repeat("a", 31) + " "}, {ExpectedVersion: &zero, Enabled: &off, IntegrationKey: strings.Repeat("a", 32)}, {ExpectedVersion: &max, Enabled: &off}} {
		if ValidateMCPReadinessPagerDutyRequest(r) == nil {
			t.Fatal("invalid accepted", r.Enabled)
		}
	}
	b, e := json.Marshal(MCPReadinessPagerDutyPolicy{Version: 1, Enabled: true, IntegrationKeyMasked: "***"})
	if e != nil || strings.Contains(string(b), "sealed") || strings.Contains(string(b), "fingerprint") {
		t.Fatal(string(b), e)
	}
}
