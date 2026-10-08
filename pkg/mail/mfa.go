// Package mail — MFA lifecycle bodies (issues #329 + #328,
// IAM-2 follow-up).
//
// Two templates land in this file as part of the 5-PR
// pre-launch security/MFA sprint. A third is forecast but
// staged for PR 2:
//
//   - RecoveryCodeBurnedBody — sent when /v1/account/mfa/recover
//     succeeds (one of the customer's recovery codes was used).
//     This file's only PRODUCTION-WIRED body in the PR #329 diff.
//   - MFADisableEmailRequestedBody — sent when
//     /v1/account/mfa/disable-email is invoked; the body
//     contains the confirm link with the 24h cool-down backstop
//     (issue #328). Lands as a STAGED template in PR #329 (PR 1
//     of the sprint) so the package stays tidy; wired into the
//     HTTP path in PR 2 (issue #328).
//   - MFADisableEmailConfirmedBody — sent after the 24h
//     cool-down succeeds and MFA is cleared. Lands alongside
//     the disable-email HTTP wiring in PR 2; does NOT live in
//     this file yet (adding now would orphan a template the
//     sprint hasn't reached).
//
// All three follow the spec's "plaintext only, no HTML alt"
// convention (see pkg/mail/account.go file header for the
// rationale). Subject lines are short enough to render fully
// in every mail client's summary view.
//
// CR/LF sanitisation (CWE-117): every customer-controlled email
// that could land in an outbound body goes through safeRecipient.
// The dashboard renders these on the customer-facing side, so a
// hostile email change must not be able to inject CR/LF into
// the SMTP body. slog's own sanitiser
// (pkg/logsanitize.Field on msg.To) keeps the log line
// independently safe — same dual-defence pattern as the
// account.go templates.

package mail

import (
	"fmt"
	"time"
)

// RecoveryCodeBurnedBody renders the "you used one of your MFA
// recovery codes" email. Sent from /v1/account/mfa/recover after
// ConsumeRecoveryCode succeeds.
//
// `remaining` is the number of recovery codes left after the burn.
// Three branches:
//
//	remaining > 2   — informational tone; one of many
//	remaining == 2  — warning tone; "use /disable if you also lose
//	                  your device, you're one code away from being
//	                  locked out"
//	remaining <= 1  — critical tone; "the next lost code locks you
//	                  out — /disable + /enroll is the recovery path"
//	                  (This state is reachable in two ways:
//	                  (a) the customer burns 9 of 10 codes
//	                       successfully across separate /recover calls
//	                       (MatchRecoveryCode refuses the last burn
//	                       on the /recover path, so this only lands
//	                       via /disable's recovery_code branch); or
//	                  (b) post-PR #328 ships, this branch is the
//	                       trigger for the 24h cool-down email.)
//
// `burnedAt` is the moment the consume committed; rendered as UTC
// so the customer doesn't have to do timezone arithmetic.
func RecoveryCodeBurnedBody(email string, remaining int, burnedAt time.Time) (subject, body string) {
	email = safeRecipient(email)
	atStr := burnedAt.UTC().Format("2006-01-02 15:04 UTC")
	subject = "Recovery code used on your Gregale account"

	switch {
	case remaining <= 1:
		body = fmt.Sprintf(`Hi,

A recovery code for your Gregale account (%s) was used at %s.

This was your second-to-last code. The next time a recovery code is
used, your account will have NO codes left — you would then need to
re-enroll MFA from scratch to regain access.

To re-enroll now and generate a fresh set of 10 codes, sign in and
visit the MFA page in the dashboard. You can also disable MFA
temporarily via /v1/account/mfa/disable (with your password) and
re-enroll fresh.

If this was not you, change your password immediately and contact
support@gregale.dev.

— Gregale
`, email, atStr)
	case remaining == 2:
		body = fmt.Sprintf(`Hi,

A recovery code for your Gregale account (%s) was used at %s.

You have 2 recovery codes left. If you also lose access to your
authenticator device, the next recovery-code burn will be the last
one — disable MFA from your dashboard now (Settings → Security →
MFA → Disable) and re-enroll to get a fresh set of 10 codes.

If this was not you, change your password immediately and contact
support@gregale.dev.

— Gregale
`, email, atStr)
	default:
		body = fmt.Sprintf(`Hi,

A recovery code for your Gregale account (%s) was used at %s. You have
%d recovery codes remaining.

If this was not you, change your password immediately and contact
support@gregale.dev.

— Gregale
`, email, atStr, remaining)
	}
	return
}

// MFADisableEmailRequestedBody renders the "click here to disable MFA
// in 24 hours" email (issue #328). Lands in PR 2 of the sprint plan.
//
// `confirmURL` is the absolute URL the customer clicks; it embeds a
// 32-byte random token as a query parameter. The server-side check at
// /v1/account/mfa/disable-email/confirm uses the same token hash
// against the row's mfa_disable_email_token_hash and computes the 24h
// delta against mfa_disable_email_requested_at (NOT the email client's
// clock — the row's server-stamped timestamp is the source of truth).
//
// `requestedAt` is the server-side stamp; we render both the UTC date
// and the local "you can confirm after <UTC datetime>" line so the
// customer can plan the confirmation for a time they'll be at a
// trusted device.
func MFADisableEmailRequestedBody(email, confirmURL string, requestedAt time.Time) (subject, body string) {
	email = safeRecipient(email)
	atStr := requestedAt.UTC().Format("2006-01-02 15:04 UTC")
	confirmAfter := requestedAt.UTC().Add(24 * time.Hour).Format("2006-01-02 15:04 UTC")
	subject = "Confirm MFA disable on your Gregale account (24h wait)"
	body = fmt.Sprintf(`Hi,

Someone — hopefully you — requested to disable MFA on your Gregale
account (%s) at %s.

This is the only path that works when you've lost BOTH your
authenticator device AND all your recovery codes. The 24-hour wait is
deliberate friction: an attacker who controls your account still has
to hold it for a day before MFA turns off.

You can confirm the disable after %s by opening this link from a
trusted device:

    %s

If you did not request this, do nothing — the request expires after
24 hours and no action is taken. You should also change your
password and contact support@gregale.dev so we can investigate.

— Gregale
`, email, atStr, confirmAfter, confirmURL)
	return
}
