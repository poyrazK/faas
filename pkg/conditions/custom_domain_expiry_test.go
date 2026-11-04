package conditions_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/conditions"
)

func TestDomainCertificateExpiryUsesEvaluationTime(t *testing.T) {
	at := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	for _, status := range []string{"issued", "renewing", "cdn"} {
		for _, offset := range []time.Duration{-time.Second, 0, time.Second} {
			d := api.CustomDomainResponse{Verified: true, CertStatus: status, CertExpiresAt: at.Add(offset).Format(time.RFC3339Nano)}
			wantStatus := api.ConditionFalse
			if offset > 0 {
				wantStatus = api.ConditionTrue
			}
			assertConditions(t, conditions.Domain(d, at), api.ResourceKindDomain, map[string]want{"CertificateReady": {wantStatus, map[bool]string{true: status, false: "certificate_expired"}[offset > 0], false}})
		}
	}
	legacy := api.CustomDomainResponse{Verified: true, CertStatus: "issued", CertNotAfter: at.Add(-time.Second).Format(time.RFC3339)}
	assertConditions(t, conditions.Domain(legacy, at), api.ResourceKindDomain, map[string]want{"Ready": {"False", "certificate_expired", false}})
	legacy.CertNotAfter = "invalid"
	assertConditions(t, conditions.Domain(legacy, at), api.ResourceKindDomain, map[string]want{"CertificateReady": {"Unknown", "not_assessed", false}})
}

func TestDomainVerificationDeadlineAgesCachedResponse(t *testing.T) {
	at := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	d := api.CustomDomainResponse{VerificationExpiresAt: at.Format(time.RFC3339)}
	assertConditions(t, conditions.Domain(d, at.Add(-time.Second)), api.ResourceKindDomain, map[string]want{"Verified": {"False", "dns_pending", false}})
	assertConditions(t, conditions.Domain(d, at), api.ResourceKindDomain, map[string]want{"Ready": {"False", "verification_expired", true}, "Verified": {"False", "verification_expired", true}})
	d.Verified = true
	d.CertStatus = "issued"
	assertConditions(t, conditions.Domain(d, at.Add(time.Second)), api.ResourceKindDomain, map[string]want{"Ready": {"True", "serving", false}})
}
