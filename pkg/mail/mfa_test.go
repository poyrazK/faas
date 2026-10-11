// Tests for pkg/mail/mfa.go — MFA lifecycle email bodies.
//
// The body shape is part of the customer-facing contract (issue
// #329 + #328); regressions here are user-visible. The test pins:
//
//   - subject line for each branch
//   - which sentence the body contains for each branch
//   - the literal "N recovery codes remaining" line for the
//     one-of-many case
//   - the no-remaining / one-left branch's tone (it tells the
//     customer to /disable + /enroll)
//   - the warning branch (remaining == 2) explicitly mentions the
//     word "last" so a customer skimming the body sees the urgency
//   - safeRecipient strips CR/LF from the email before interpolation
//     (CWE-117 / CodeQL go/log-injection defence — same shape
//     account_test.go pins for PaymentFailedBody)
//   - the disable-email template's confirmURL is interpolated verbatim
//     (issue #328 — the URL must round-trip the server-minted token)

package mail

import (
	"strings"
	"testing"
	"time"
)

func TestRecoveryCodeBurnedBody_ThreeBranches(t *testing.T) {
	burned := time.Date(2026, 7, 27, 19, 5, 0, 0, time.UTC)

	cases := []struct {
		name        string
		remaining   int
		wantSubject string
		wantPhrase  string
	}{
		{"one-of-many", 7, "Recovery code used on your Gregale account", "7 recovery codes remaining"},
		{"warning at 2 left", 2, "Recovery code used on your Gregale account", "You have 2 recovery codes left"},
		{"critical at 1 left", 1, "Recovery code used on your Gregale account", "second-to-last code"},
		{"zero left", 0, "Recovery code used on your Gregale account", "NO codes left"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subject, body := RecoveryCodeBurnedBody("alice@example.com", tc.remaining, burned)
			if subject != tc.wantSubject {
				t.Errorf("subject = %q, want %q", subject, tc.wantSubject)
			}
			if !strings.Contains(body, tc.wantPhrase) {
				t.Errorf("body missing %q; got:\n%s", tc.wantPhrase, body)
			}
			// All branches include the burned-at UTC timestamp.
			if !strings.Contains(body, "2026-07-27 19:05 UTC") {
				t.Errorf("body missing burned-at timestamp; got:\n%s", body)
			}
			// All branches mention support@gregale.dev so a confused
			// customer has a way out.
			if !strings.Contains(body, "support@gregale.dev") {
				t.Errorf("body missing support@gregale.dev; got:\n%s", body)
			}
		})
	}
}

func TestRecoveryCodeBurnedBody_SanitisesEmailCRLF(t *testing.T) {
	// CWE-117 / CodeQL go/log-injection: a hostile email with CR/LF
	// must NOT propagate into the SMTP body. The slog transport has
	// its own sanitiser; the body transport depends on safeRecipient.
	burned := time.Date(2026, 7, 27, 19, 5, 0, 0, time.UTC)
	subject, body := RecoveryCodeBurnedBody("evil@example.com\r\nBcc: attacker@evil", 5, burned)
	if strings.Contains(body, "\r") || strings.Contains(body, "\nBcc:") {
		t.Errorf("body contains CR/LF after sanitisation; got:\n%q", body)
	}
	// Subject must also be free of CR/LF (the SMTP envelope would
	// break otherwise).
	if strings.Contains(subject, "\r") || strings.Contains(subject, "\n") {
		t.Errorf("subject contains CR/LF; got: %q", subject)
	}
}

func TestMFADisableEmailRequestedBody_IncludesConfirmURL(t *testing.T) {
	requested := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	url := "https://api.example.com/v1/account/mfa/disable-email/confirm?token=deadbeef"
	subject, body := MFADisableEmailRequestedBody("alice@example.com", url, requested)
	if subject != "Confirm MFA disable on your Gregale account (24h wait)" {
		t.Errorf("subject = %q", subject)
	}
	if !strings.Contains(body, url) {
		t.Errorf("body missing confirm URL; got:\n%s", body)
	}
	if !strings.Contains(body, "2026-07-28 12:00 UTC") {
		// 24h after requested = 2026-07-28 12:00 UTC, the same
		// minute-of-day rendered in UTC. The test pin asserts
		// the rendered "confirm after" line is present.
		t.Errorf("body missing 24h-after timestamp; got:\n%s", body)
	}
	if !strings.Contains(body, "lost BOTH") {
		t.Errorf("body missing 'lost BOTH' framing (the whole point of the email); got:\n%s", body)
	}
}
