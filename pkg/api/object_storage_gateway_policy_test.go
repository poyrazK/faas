package api

import (
	"testing"
	"time"
)

func TestGatewaySafetyPolicyRequiresExplicitOptIn(t *testing.T) {
	since := time.Now().UTC().Add(-time.Minute)
	p := ObjectStoragePolicy{AccountingMode: ObjectStorageGatewaySafetyV1, GatewayMeteringSince: &since, MaxAccountBytes: 100, MaxBucketBytes: 100, MaxAccountKeys: 10, MaxMonthlyRequests: 100, MaxMonthlyEgressBytes: 100, MaxMonthlyAuthorizations: 100, MaxReportAgeSeconds: 60}
	if !p.Valid() {
		t.Fatal("explicit gateway safety policy rejected")
	}
	for _, tc := range []struct {
		name   string
		mutate func(*ObjectStoragePolicy)
	}{
		{"default still needs provider cost", func(p *ObjectStoragePolicy) { p.AccountingMode = ""; p.GatewayMeteringSince = nil }},
		{"unknown mode", func(p *ObjectStoragePolicy) { p.AccountingMode = "anything" }},
		{"missing coverage start", func(p *ObjectStoragePolicy) { p.GatewayMeteringSince = nil }},
		{"zero coverage start", func(p *ObjectStoragePolicy) { p.GatewayMeteringSince = new(time.Time) }},
		{"cost ceiling cannot be ignored", func(p *ObjectStoragePolicy) { p.MaxMonthlyCostMillicents = 1 }},
		{"requests still finite", func(p *ObjectStoragePolicy) { p.MaxMonthlyRequests = 0 }},
		{"egress still finite", func(p *ObjectStoragePolicy) { p.MaxMonthlyEgressBytes = 0 }},
		{"freshness still required", func(p *ObjectStoragePolicy) { p.MaxReportAgeSeconds = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := p
			tc.mutate(&candidate)
			if candidate.Valid() {
				t.Fatal("unsafe policy accepted")
			}
		})
	}
}
