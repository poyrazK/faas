package api_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestDomainReadiness(t *testing.T) {
	now := time.Date(2026, 11, 6, 20, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, routing, cert, status string
		verified, expired           bool
		extra                       api.DomainDoctorCheck
	}{
		{"ownership pending", "ok", "pending", "awaiting_ownership", false, false, api.DomainDoctorCheck{}},
		{"expired claim", "ok", "pending", "expired", false, true, api.DomainDoctorCheck{}},
		{"routing unknown", "pending", "issued", "checking_dns", true, false, api.DomainDoctorCheck{}},
		{"routing wrong despite cert", "fail", "issued", "fix_routing", true, false, api.DomainDoctorCheck{}},
		{"certificate pending", "ok", "pending", "preparing_https", true, false, api.DomainDoctorCheck{}},
		{"ready", "ok", "issued", "ready", true, false, api.DomainDoctorCheck{}},
		{"renewing", "ok", "renewing", "ready", true, false, api.DomainDoctorCheck{}},
		{"CAA blocked", "ok", "issued", "needs_attention", true, false, api.DomainDoctorCheck{Name: "caa_permits", Status: "fail"}},
		{"IPv6 conflict", "ok", "issued", "needs_attention", true, false, api.DomainDoctorCheck{Name: "ipv6_conflict", Status: "fail"}},
		{"drifted", "fail", "dns_drifted", "needs_attention", false, false, api.DomainDoctorCheck{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := api.CustomDomainResponse{Verified: tc.verified, VerificationExpired: tc.expired, CertStatus: tc.cert, CertExpiresAt: now.Add(time.Hour).Format(time.RFC3339), Checks: []api.DomainDoctorCheck{{Name: "points_to_gregale", Status: tc.routing}, tc.extra}}
			r := d.AssessDomainReadiness(now)
			if r.Status != tc.status {
				t.Fatalf("got %+v, want %s", r, tc.status)
			}
			d.Readiness = &r
			if d.HTTPSReadyAt(now) != (tc.status == "ready") {
				t.Fatalf("inconsistent readiness: %+v", d)
			}
			if d.HTTPSReadyAt(now.Add(2 * time.Hour)) {
				t.Fatal("cached domain stayed ready after certificate expiry")
			}
		})
	}
}

func TestDomainReadyLegacyResponseRequiresValidCertificate(t *testing.T) {
	now := time.Now()
	d := api.CustomDomainResponse{Verified: true, CertStatus: "issued", CertNotAfter: now.Add(time.Hour).Format(time.RFC3339)}
	if !d.HTTPSReadyAt(now) {
		t.Fatal("older ready server lost compatibility")
	}
	d.CertNotAfter = "invalid"
	if d.HTTPSReadyAt(now) {
		t.Fatal("invalid expiry established readiness")
	}
}
