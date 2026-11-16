package mail_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/mail"
)

func TestSupportUpdateEmailsContainOnlyMetadataAndAuthenticatedLinks(t *testing.T) {
	for _, kind := range []string{"staff_reply", "customer_reply", "ticket_created", "assigned"} {
		subject, body := mail.SupportUpdateBody(kind, "case-123", "https://api.gregale.dev/dashboard/support/case-123")
		if !strings.Contains(subject, "Gregale support") || !strings.Contains(body, "Ticket: case-123") || !strings.Contains(body, "https://api.gregale.dev/dashboard/support/case-123") || !strings.Contains(body, "Sign in") {
			t.Fatalf("incomplete %s notification: %q %q", kind, subject, body)
		}
	}
}
