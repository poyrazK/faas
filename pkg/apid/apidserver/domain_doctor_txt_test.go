package apidserver_test

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// prod hunt 2026-11-21: `domain doctor` on an unverified domain checked only
// the CNAME and promised a certificate "once the domain resolves". No
// certificate is issued until the _faas-verify TXT record is seen, so the
// TLS check names that record while the domain is unverified.
func TestDomainDoctorNamesTheMissingVerificationTXT(t *testing.T) {
	obs := state.DomainDoctorObservation{
		Domain: "api.customer.test", ObservedAt: time.Now(), DNSCheckedAt: time.Now(),
		DNSRecordFound: true, PointsToGregale: true, CertState: "none",
	}
	tlsCheck := func(d state.CustomDomain) (status, detail, remediation string) {
		for _, c := range apidserver.DoctorReportFromObs(d, obs, false).Checks {
			if c.Name == "tls_certificate" {
				return c.Status, c.Detail, c.Remediation
			}
		}
		t.Fatal("missing tls_certificate check")
		return "", "", ""
	}

	status, detail, rem := tlsCheck(state.CustomDomain{Domain: "api.customer.test", ChallengeToken: "tok123"})
	if status != "pending" || !strings.Contains(detail, "TXT") || !strings.Contains(rem, "_faas-verify.api.customer.test") || !strings.Contains(rem, "tok123") {
		t.Fatalf("unverified domain TLS check = %q %q %q, want the TXT record named", status, detail, rem)
	}
	_, detail, rem = tlsCheck(state.CustomDomain{Domain: "api.customer.test", ChallengeToken: "tok123", VerifiedAt: time.Now()})
	if strings.Contains(detail+rem, "_faas-verify") {
		t.Fatalf("verified domain still asks for the TXT record: %q %q", detail, rem)
	}
}
