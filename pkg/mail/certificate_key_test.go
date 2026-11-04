package mail_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/mail"
)

func TestCertificateFailureKeyBindsClaimAndDelivery(t *testing.T) {
	key := mail.CertificateIssuanceFailedKey("app", "private-proof", []string{"owner@example.com"}, "subject", "body")
	if len(key) > 128 || strings.Contains(key, "private-proof") {
		t.Fatalf("unbounded or exposed claim key: %q", key)
	}
	if retry := mail.CertificateIssuanceFailedKey("app", "private-proof", []string{"owner@example.com"}, "subject", "body"); retry != key {
		t.Fatal("retry changed idempotency key")
	}
	for _, changed := range []string{
		mail.CertificateIssuanceFailedKey("another-app", "private-proof", []string{"owner@example.com"}, "subject", "body"),
		mail.CertificateIssuanceFailedKey("app", "new-proof", []string{"owner@example.com"}, "subject", "body"),
		mail.CertificateIssuanceFailedKey("app", "private-proof", []string{"new-owner@example.com"}, "subject", "body"),
		mail.CertificateIssuanceFailedKey("app", "private-proof", []string{"owner@example.com"}, "subject", "new failure"),
	} {
		if changed == key {
			t.Fatal("different claim or payload reused an idempotency key")
		}
	}
}
