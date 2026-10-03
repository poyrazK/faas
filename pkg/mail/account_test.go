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
	if !strings.Contains(body, "gregale billing portal") {
		t.Errorf("body missing provider-neutral recovery command:\n%s", body)
	}
	if strings.Contains(body, "billing retry") {
		t.Errorf("body promises unsupported direct retry command:\n%s", body)
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
	if !strings.Contains(body, "gregale status") {
		t.Errorf("body missing status command:\n%s", body)
	}
	if strings.Contains(body, "Stripe") {
		t.Errorf("body names a provider-specific recovery path:\n%s", body)
	}
	if strings.Contains(body, "<") {
		t.Errorf("body contains HTML; must be plaintext:\n%s", body)
	}
}

// TestSubscriptionEndedBody pins the voluntary-cancellation email
// (spec §4.7 "downgrade at period end"): names the old plan, the
// timestamp, the Free limits the customer is now under, and the CLI
// path back to a paid plan — without naming a provider.
func TestSubscriptionEndedBody(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	subject, body := mail.SubscriptionEndedBody("alice@example.com", "pro", at)

	if !strings.Contains(subject, "Free plan") {
		t.Errorf("subject = %q, want it to mention the Free plan", subject)
	}
	for _, want := range []string{"alice@example.com", "2026-09-05 10:00 UTC", "pro subscription", "5 GB-hours", "gregale plan"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
	for _, forbidden := range []string{"Polar", "Paddle", "Stripe", "<"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("body contains %q:\n%s", forbidden, body)
		}
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

// TestAccountSuspendedBody_NamesTheDeletionDeadline — the suspension email
// rendered the suspension time as the deletion deadline ("i.e. <today> —
// 14 days from now"), so the customer was told their account was due for
// deletion on the day it was suspended.
func TestAccountSuspendedBody_NamesTheDeletionDeadline(t *testing.T) {
	t.Parallel()
	pastDueAt := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	suspendedAt := pastDueAt.Add(7 * 24 * time.Hour)
	_, body := mail.AccountSuspendedBody("alice@example.com", suspendedAt, pastDueAt.Add(21*24*time.Hour))

	if !strings.Contains(body, "2026-09-22") {
		t.Errorf("body missing the deletion deadline 2026-09-22:\n%s", body)
	}
	if strings.Count(body, "2026-09-08") != 1 {
		t.Errorf("suspension date must appear once, as the suspension time, not as the deadline:\n%s", body)
	}
	if !strings.Contains(body, "gregale billing portal") {
		t.Errorf("body missing the recovery command:\n%s", body)
	}
}

// TestAccountDeletionForNonPaymentBody — dunning reused the customer-initiated
// deletion email, which told the customer they had scheduled the deletion,
// to cancel it with `account restore` (the API refuses that for a dunning
// deletion), and to change their password. Paying is the only way back.
func TestAccountDeletionForNonPaymentBody(t *testing.T) {
	t.Parallel()
	pastDueAt := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	deleteOn := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	subject, body := mail.AccountDeletionForNonPaymentBody("alice@example.com", pastDueAt, deleteOn)

	if !strings.Contains(subject, "2026-10-22") || !strings.Contains(subject, "non-payment") {
		t.Errorf("subject = %q, want the deadline and the reason", subject)
	}
	for _, want := range []string{"alice@example.com", "2026-09-01", "2026-10-22", "gregale billing portal"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
	for _, forbidden := range []string{"You scheduled", "account restore", "change your password", "Polar", "Paddle", "Stripe", "<"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("body contains %q:\n%s", forbidden, body)
		}
	}
}

// TestAccountBodies_NameTheShippedCLI — every account email told customers
// to run `faas …` commands; the CLI binary is `gregale`, so each recovery
// step the email offered failed with "command not found".
func TestAccountBodies_NameTheShippedCLI(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 7, 23, 14, 30, 0, 0, time.UTC)
	for name, body := range map[string]string{
		"PaymentFailedBody":                mustBody(mail.PaymentFailedBody("a@example.com", at)),
		"AccountSuspendedBody":             mustBody(mail.AccountSuspendedBody("a@example.com", at, at)),
		"AccountRestoredBody":              mustBody(mail.AccountRestoredBody("a@example.com", at)),
		"AccountDeletionPendingBody":       mustBody(mail.AccountDeletionPendingBody("a@example.com", at, at)),
		"AccountDeletionForNonPaymentBody": mustBody(mail.AccountDeletionForNonPaymentBody("a@example.com", at, at)),
		"SubscriptionEndedBody":            mustBody(mail.SubscriptionEndedBody("a@example.com", "pro", at)),
	} {
		for _, line := range strings.Split(body, "\n") {
			if !strings.HasPrefix(line, "  ") {
				continue // prose, not a command line
			}
			trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "2. Run:"))
			if strings.HasPrefix(trimmed, "faas ") {
				t.Errorf("%s tells the customer to run %q; the CLI is gregale", name, trimmed)
			}
		}
	}
}

func mustBody(_, body string) string { return body }
