package api

import (
	"math"
	"strings"
	"testing"
)

func TestMCPReadinessPagerDutyAcknowledgementValidation(t *testing.T) {
	zero, one, max := int64(0), int64(1), int64(math.MaxInt64)
	on, off := true, false
	valid := SetMCPReadinessPagerDutyAcknowledgementsRequest{ExpectedVersion: &zero, Enabled: &on, ExpectedPagerDutyVersion: &one, ServiceID: "PSVC001", SigningSecret: strings.Repeat("s", 32)}
	release := valid
	release.SyncUnacknowledgements = true
	for _, r := range []SetMCPReadinessPagerDutyAcknowledgementsRequest{valid, release, {ExpectedVersion: &zero, Enabled: &off}, {ExpectedVersion: &one, Enabled: &off, Destination: "primary"}, {ExpectedVersion: &one, Enabled: &off, Destination: "fallback"}} {
		if err := ValidateMCPReadinessPagerDutyAcknowledgementsRequest(r); err != nil {
			t.Fatal(err)
		}
	}
	cases := []SetMCPReadinessPagerDutyAcknowledgementsRequest{{ExpectedVersion: &zero, Enabled: &off, SyncUnacknowledgements: true}, {ExpectedVersion: &one, Enabled: &off, Destination: "other"}, {}, {ExpectedVersion: &zero}, {ExpectedVersion: &max, Enabled: &off}, {ExpectedVersion: &zero, Enabled: &off, SigningSecret: "should-not-accept"}}
	for _, mutate := range []func(*SetMCPReadinessPagerDutyAcknowledgementsRequest){func(r *SetMCPReadinessPagerDutyAcknowledgementsRequest) { r.ExpectedPagerDutyVersion = nil }, func(r *SetMCPReadinessPagerDutyAcknowledgementsRequest) { r.ExpectedPagerDutyVersion = &zero }, func(r *SetMCPReadinessPagerDutyAcknowledgementsRequest) { r.ServiceID = "https://evil.example" }, func(r *SetMCPReadinessPagerDutyAcknowledgementsRequest) { r.SigningSecret = "short" }, func(r *SetMCPReadinessPagerDutyAcknowledgementsRequest) { r.SigningSecret = "includes space in secret" }, func(r *SetMCPReadinessPagerDutyAcknowledgementsRequest) { r.Enabled = &off }} {
		r := valid
		mutate(&r)
		cases = append(cases, r)
	}
	for n, r := range cases {
		if ValidateMCPReadinessPagerDutyAcknowledgementsRequest(r) == nil {
			t.Fatal("invalid request accepted", n)
		}
	}
}
