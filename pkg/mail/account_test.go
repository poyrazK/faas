package mail_test

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/mail"
)

// TestPaymentFailedBody renders the entry-point email and pins the
// pieces the customer relies on: subject is short + tells them they
// must act, body includes the email, the failure timestamp, the
// 7-day deadline as a UTC date, and the recovery command. The 21-day
// deletion line is also pinned because it's the second deadline they
// need to know about.
func TestPaymentFailedBody(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 7, 23, 14, 30, 0, 0, time.UTC)
	subject, body := mail.PaymentFailedBody("alice@example.com", at)

	if !strings.Contains(subject, "payment failed") {
		t.Errorf("subject = %q, want it to mention payment failed", subject)
	}
	if !strings.Contains(subject, "7 days") {
		t.Errorf("subject = %q, want it to mention the 7-day window", subject)
	}
	wantDeadline := at.UTC().Add(7 * 24 * time.Hour).Format("2006-01-02")
	if !strings.Contains(body, wantDeadline) {
		t.Errorf("body missing 7-day deadline %s:\n%s", wantDeadline, body)
	}
	if !strings.Contains(body, "alice@example.com") {
		t.Errorf("body missing recipient email:\n%s", body)
	}
	if !strings.Contains(body, "2026-07-23 14:30 UTC") {
		t.Errorf("body missing failure timestamp:\n%s", body)
	}
	if !strings.Contains(body, "faas billing retry") {
		t.Errorf("body missing recovery command:\n%s", body)
	}
	if !strings.Contains(body, "21 days") {
		t.Errorf("body missing 21-day deletion deadline:\n%s", body)
	}
	if strings.Contains(body, "<") {
		t.Errorf("body contains HTML; must be plaintext:\n%s", body)
	}
}

// TestAccountRestoredBody pins the recovery email: subject tells the
// customer they're back, body acknowledges the Stripe confirmation
// timestamp and the 60-second resume window. The "if apps were parked"
// qualifier matters — most customers won't have hit the 7-day mark.
func TestAccountRestoredBody(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 7, 23, 15, 0, 0, 0, time.UTC)
	subject, body := mail.AccountRestoredBody("alice@example.com", at)

	if !strings.Contains(subject, "good standing") {
		t.Errorf("subject = %q, want it to mention good standing", subject)
	}
	if !strings.Contains(body, "alice@example.com") {
		t.Errorf("body missing recipient email:\n%s", body)
	}
	if !strings.Contains(body, "2026-07-23 15:00 UTC") {
		t.Errorf("body missing restored-at timestamp:\n%s", body)
	}
	if !strings.Contains(body, "60 seconds") {
		t.Errorf("body missing 60-second resume window:\n%s", body)
	}
	if !strings.Contains(body, "faas status") {
		t.Errorf("body missing status command:\n%s", body)
	}
	if strings.Contains(body, "<") {
		t.Errorf("body contains HTML; must be plaintext:\n%s", body)
	}
}

// TestQuotaWarningBody pins the paid-tier overage email. Plan name
// lands in subject + body so a customer receiving the email for a
// Pro account sees "Pro" not "plan". Used/quota render with 2 dp so
// the customer sees the same shape as the dashboard.
func TestQuotaWarningBody(t *testing.T) {
	t.Parallel()
	day := time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC)
	subject, body := mail.QuotaWarningBody("alice@example.com", "pro", 250.5, 250, day)

	if !strings.Contains(subject, "pro") {
		t.Errorf("subject = %q, want it to mention the plan", subject)
	}
	if !strings.Contains(subject, "quota") {
		t.Errorf("subject = %q, want it to mention quota", subject)
	}
	if !strings.Contains(body, "alice@example.com") {
		t.Errorf("body missing recipient email:\n%s", body)
	}
	if !strings.Contains(body, "pro") {
		t.Errorf("body missing plan name:\n%s", body)
	}
	if !strings.Contains(body, "2026-07-23") {
		t.Errorf("body missing day stamp:\n%s", body)
	}
	if !strings.Contains(body, "250.50 GB-h") {
		t.Errorf("body missing formatted used figure (250.50 GB-h):\n%s", body)
	}
	if !strings.Contains(body, "250 GB-h") {
		t.Errorf("body missing quota figure (250 GB-h):\n%s", body)
	}
	if !strings.Contains(body, "only quota warning you'll get today") {
		t.Errorf("body missing dedupe-language line:\n%s", body)
	}
	if strings.Contains(body, "<") {
		t.Errorf("body contains HTML; must be plaintext:\n%s", body)
	}
}

// TestAccountBodies_StripCRLFFromEmailRegression pins the CWE-117
// sanitiser on every body helper. The pattern matches the CodeQL-
// accepted shape (strings.ReplaceAll "\r" then "\n"); a future
// relax of the supplier-side format check at pkg/api.CreateAccount
// cannot smuggle header-injection bytes into the SMTP body without
// this test failing.
func TestAccountBodies_StripCRLFFromEmailRegression(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 7, 23, 14, 30, 0, 0, time.UTC)
	day := time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC)
	hostile := "alice\r\nBcc: attacker@example.com@example.com"
	for _, tc := range []struct {
		name string
		body string
	}{
		{"PaymentFailedBody", mustBody(mail.PaymentFailedBody(hostile, at))},
		{"AccountRestoredBody", mustBody(mail.AccountRestoredBody(hostile, at))},
		{"QuotaWarningBody", mustBody(mail.QuotaWarningBody(hostile, "pro", 250.5, 250, day))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if strings.Contains(tc.body, "\r") {
				t.Errorf("%s body still contains \\r: %q", tc.name, tc.body)
			}
			if strings.Contains(tc.body, "\nBcc:") || strings.Contains(tc.body, "\nbcc:") {
				t.Errorf("%s body contains smuggled Bcc: header: %q", tc.name, tc.body)
			}
		})
	}
}

func mustBody(_, body string) string { return body }
