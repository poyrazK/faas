// Focused factory tests for ADR-115 (G4-closure fail-closed contract).
// transports_test.go covers the per-transport wire-shape; this file
// pins the SenderFromEnv selector behaviour as a single table so the
// G4 closure is auditable end-to-end.
//
// Run with `go test ./pkg/mail/...`.
package mail_test

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/onebox-faas/faas/pkg/mail"
)

// TestSenderFromEnv_FactoryContract is the single table-driven source
// of truth on how SenderFromEnv resolves FAAS_MAIL_TRANSPORT. Every
// operator-visible branch lives here: explicit log/noop, live
// resend / postmark with full creds, the four credential-missing
// fail-closed branches from ADR-115 §D5, and the two transport-
// selection fail-closed branches added by issue #246 (unset on a
// production box, unknown transport on a production box). Dev-box
// escapes (FAAS_DEV=1) collapse to LogSender and are pinned here too.
func TestSenderFromEnv_FactoryContract(t *testing.T) {
	quietLog := slog.New(slog.NewTextHandler(io.Discard, nil))
	cases := []struct {
		name        string
		env         map[string]string
		wantType    string // empty = expect nil sender
		wantErrIs   error  // expected sentinel via errors.Is; nil = no error
		wantErrWrap error  // expected wrapped sentinel via errors.Is; nil = any
	}{
		{
			// Issue #246: an unset FAAS_MAIL_TRANSPORT on a
			// production box now refuses to boot — a production
			// box that journals the dunning ladder looks healthy
			// while the customer receives nothing.
			name:      "unset-on-prod-fails-closed",
			env:       map[string]string{},
			wantErrIs: mail.ErrMailUnsetInProd,
		},
		{
			// The dev-box escape hatch: FAAS_DEV=1 collapses an
			// unset transport to LogSender so a developer's local
			// stack still boots without ceremony.
			name: "unset-on-dev-resolves-to-log",
			env: map[string]string{
				"FAAS_DEV": "1",
			},
			wantType: "*mail.LogSender",
		},
		{
			name:     "explicit-log",
			env:      map[string]string{"FAAS_MAIL_TRANSPORT": "log"},
			wantType: "*mail.LogSender",
		},
		{
			// Explicit log is honoured on prod too — it is the
			// operator-visible escape hatch when an operator
			// really does want mail in the journal.
			name: "explicit-log-on-prod-is-allowed",
			env: map[string]string{
				"FAAS_MAIL_TRANSPORT": "log",
			},
			wantType: "*mail.LogSender",
		},
		{
			// A FAAS_DEV box keeps the silent sender tests rely on.
			name:     "explicit-noop-on-dev",
			env:      map[string]string{"FAAS_MAIL_TRANSPORT": "noop", "FAAS_DEV": "1"},
			wantType: "mail.NoopSender",
		},
		{
			// Production ran noop from bootstrap and every verification
			// email vanished without a trace. On a non-dev box noop still
			// boots, but resolves to the sender that logs and counts drops.
			name:     "explicit-noop-on-prod-is-loud",
			env:      map[string]string{"FAAS_MAIL_TRANSPORT": "noop"},
			wantType: "*mail.DroppingSender",
		},
		{
			name: "resend-with-key",
			env: map[string]string{
				"FAAS_MAIL_TRANSPORT":      "resend",
				"FAAS_MAIL_RESEND_API_KEY": "re_test_key",
				"FAAS_MAIL_FROM":           "noreply@example.test",
			},
			wantType: "*mail.ResendSender",
		},
		{
			name: "postmark-with-token",
			env: map[string]string{
				"FAAS_MAIL_TRANSPORT":      "postmark",
				"FAAS_MAIL_POSTMARK_TOKEN": "pm_test_token",
				"FAAS_MAIL_FROM":           "noreply@example.test",
			},
			wantType: "*mail.PostmarkSender",
		},
		{
			// ADR-115 §D5: operator-selected resend with no
			// FAAS_MAIL_RESEND_API_KEY fails closed (nil sender +
			// ErrMailerMisconfigured + wrapped ErrResendMissingAPIKey)
			// so apid/meterd refuse to boot.
			name: "resend-without-key-fails-closed",
			env: map[string]string{
				"FAAS_MAIL_TRANSPORT": "resend",
				"FAAS_MAIL_FROM":      "noreply@example.test",
			},
			wantErrIs:   mail.ErrMailerMisconfigured,
			wantErrWrap: mail.ErrResendMissingAPIKey,
		},
		{
			// ADR-115 §D5: Postmark mirror of the resend fail-closed.
			name: "postmark-without-token-fails-closed",
			env: map[string]string{
				"FAAS_MAIL_TRANSPORT": "postmark",
				"FAAS_MAIL_FROM":      "noreply@example.test",
			},
			wantErrIs:   mail.ErrMailerMisconfigured,
			wantErrWrap: mail.ErrPostmarkMissingToken,
		},
		{
			// ADR-115 §D5: Resend selected with valid APIKey but
			// empty From also fails closed; the wrapped sentinel
			// is ErrResendMissingFrom (code-review finding — the
			// pre-#115 code returned a literal error string that
			// broke the errors.Is chain).
			name: "resend-without-from-fails-closed",
			env: map[string]string{
				"FAAS_MAIL_TRANSPORT":      "resend",
				"FAAS_MAIL_RESEND_API_KEY": "re_test_key",
			},
			wantErrIs:   mail.ErrMailerMisconfigured,
			wantErrWrap: mail.ErrResendMissingFrom,
		},
		{
			// ADR-115 §D5: Postmark mirror of the From-branch.
			name: "postmark-without-from-fails-closed",
			env: map[string]string{
				"FAAS_MAIL_TRANSPORT":      "postmark",
				"FAAS_MAIL_POSTMARK_TOKEN": "pm_test_token",
			},
			wantErrIs:   mail.ErrMailerMisconfigured,
			wantErrWrap: mail.ErrPostmarkMissingFrom,
		},
		{
			// Resend selected, both key + from missing: the key
			// check fires first (NewResendSender validates APIKey
			// before From).
			name: "resend-without-key-or-from-fails-closed",
			env: map[string]string{
				"FAAS_MAIL_TRANSPORT": "resend",
			},
			wantErrIs: mail.ErrMailerMisconfigured,
		},
		{
			// Issue #246: a typo (e.g. "resned") on a production
			// box used to fall back to LogSender — the same
			// silent drop as the unset case with none of the
			// visibility. It now fails closed.
			name:      "bogus-transport-on-prod-fails-closed",
			env:       map[string]string{"FAAS_MAIL_TRANSPORT": "carrier-pigeon"},
			wantErrIs: mail.ErrMailUnknownTransport,
		},
		{
			// Dev-box escape: FAAS_DEV=1 collapses an unknown
			// transport name to LogSender with a WARN so a
			// developer iterating on a brand-new transport name
			// still boots. The on-prod fail-closed row above
			// pins the strict contract.
			name: "bogus-transport-on-dev-warns-and-falls-back",
			env: map[string]string{
				"FAAS_MAIL_TRANSPORT": "carrier-pigeon",
				"FAAS_DEV":            "1",
			},
			wantType: "*mail.LogSender",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(k string) string { return tc.env[k] }
			s, err := mail.SenderFromEnv(getenv, quietLog)
			if tc.wantErrIs != nil {
				if !errors.Is(err, tc.wantErrIs) {
					t.Fatalf("err = %v, want errors.Is(err, %v)", err, tc.wantErrIs)
				}
				if tc.wantErrWrap != nil && !errors.Is(err, tc.wantErrWrap) {
					t.Errorf("err = %v, want wrapped %v", err, tc.wantErrWrap)
				}
				if s != nil {
					t.Errorf("sender = %T, want nil on fail-closed", s)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if s == nil {
				t.Fatal("sender = nil on happy path")
			}
			if got := fmt.Sprintf("%T", s); got != tc.wantType {
				t.Errorf("transport = %s, want %s", got, tc.wantType)
			}
		})
	}
}
