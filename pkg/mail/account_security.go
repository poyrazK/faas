package mail

import (
	"fmt"
	"strings"
	"time"
)

// Account security notices (ADR 20261107103000). Plain text, no tracking.

func mailTime(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") }

// oneLine strips CR/LF so request-derived text cannot add mail lines.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.ReplaceAll(s, "\n", " ")
}

// EmailChangeConfirmBody asks the new address to confirm an account email change.
func EmailChangeConfirmBody(newEmail, link string, expiresAt time.Time) (subject, body string) {
	subject = "Confirm your new Gregale sign-in email"
	body = fmt.Sprintf(`Hi,

Someone signed in to a Gregale account asked to use %s as its sign-in email.
To confirm, open this link before %s:

  %s

The link can be used once. If you did not ask for this, ignore this email and
nothing changes.

— Gregale
`, safeRecipient(newEmail), mailTime(expiresAt), link)
	return subject, body
}

// EmailChangedNoticeBody tells the previous address that the account moved.
func EmailChangedNoticeBody(oldEmail, newEmail string, at time.Time) (subject, body string) {
	subject = "Your Gregale sign-in email was changed"
	body = fmt.Sprintf(`Hi,

At %s the Gregale account that used %s changed its sign-in email to
%s. Every signed-in session was signed out.

If you did not make this change, contact support@gregale.dev right away.

— Gregale
`, mailTime(at), safeRecipient(oldEmail), maskEmail(newEmail))
	return subject, body
}

// RecoveryCodesRegeneratedBody notifies that every old recovery code stopped working.
func RecoveryCodesRegeneratedBody(email string, at time.Time) (subject, body string) {
	subject = "New recovery codes for your Gregale account"
	body = fmt.Sprintf(`Hi,

At %s new two-factor recovery codes were created for %s. The previous codes no
longer work.

If you did not do this, sign in, review your active sessions and contact
support@gregale.dev.

— Gregale
`, mailTime(at), safeRecipient(email))
	return subject, body
}

// SignInMethodLinkedBody notifies that a provider can now sign in to the account.
func SignInMethodLinkedBody(email, provider string, at time.Time) (subject, body string) {
	name := providerName(provider)
	subject = fmt.Sprintf("%s sign-in added to your Gregale account", name)
	body = fmt.Sprintf(`Hi,

At %s "Continue with %s" was linked to the Gregale account %s. It can
now be used to sign in.

If you did not do this, remove it under Account → Sign-in methods and contact
support@gregale.dev.

— Gregale
`, mailTime(at), name, safeRecipient(email))
	return subject, body
}

// NewSignInBody notifies about a sign-in from a device or network not seen recently.
func NewSignInBody(email, ip, userAgent string, at time.Time) (subject, body string) {
	if len(userAgent) > 200 {
		userAgent = userAgent[:200]
	}
	subject = "New sign-in to your Gregale account"
	body = fmt.Sprintf(`Hi,

%s was signed in from a device or network we have not seen recently.

  Time:    %s
  Address: %s
  Browser: %s

If this was you, there is nothing to do. If not, change your password, revoke
the session under Account → Sessions and contact support@gregale.dev.

— Gregale
`, safeRecipient(email), mailTime(at), oneLine(ip), oneLine(userAgent))
	return subject, body
}

// KeysAfterResetBody lists the API keys a password reset left working: keys
// the owner created by hand (CI, deploys) are not revoked by a reset, so a
// key someone else created while they had access is the owner's to find.
// keys are "label (created YYYY-MM-DD)" lines.
func KeysAfterResetBody(email string, keys []string, accountURL string) (subject, body string) {
	subject = "Review the API keys on your Gregale account"
	var b strings.Builder
	fmt.Fprintf(&b, "Hi,\n\nThe password of %s was reset and every browser was signed out. These API keys\nstill work, because a reset does not revoke keys you created for CI and deploys:\n\n", email)
	for _, k := range keys {
		fmt.Fprintf(&b, "  - %s\n", k)
	}
	fmt.Fprintf(&b, "\nIf you do not recognise one, revoke it now at %s (or run\n`gregale key` and `gregale key delete ID`).\n\n— Gregale\n", accountURL)
	return subject, b.String()
}

// OperatorMFAResetBody tells the account that Gregale support turned off
// its two-factor authentication after an identity check.
func OperatorMFAResetBody(email string, at time.Time) (subject, body string) {
	subject = "Gregale support turned off two-factor authentication on your account"
	body = fmt.Sprintf(`Hi,

At %s Gregale support turned off two-factor authentication for your
Gregale account (%s), after checking your identity for an account recovery
request. The authenticator and its recovery codes were removed, every
browser was signed out and remembered browsers were forgotten. API keys
keep working.

If you asked for this, sign in and set up two-factor authentication again.

If you did not, contact security@gregale.dev at once: someone may be
trying to take over your account.

— Gregale
`, at.UTC().Format("2006-01-02 15:04 UTC"), email)
	return subject, body
}

// BillingEmailSyncFailedBody tells the new sign-in address which billing
// emails could not follow an email change. failed names each subject, such
// as `organization "Acme" (acme)`.
func BillingEmailSyncFailedBody(email string, failed []string, docsURL string) (subject, body string) {
	subject = "Update your Gregale billing email"
	var b strings.Builder
	fmt.Fprintf(&b, "Your Gregale sign-in email changed to %s, but the billing email of\n", email)
	for _, f := range failed {
		fmt.Fprintf(&b, "  - %s\n", f)
	}
	b.WriteString("could not be moved with it, so receipts and payment notices still go to the previous address.\n\n")
	fmt.Fprintf(&b, "An organization owner can set the billing email with PUT /v1/orgs/{slug}/billing/email (%s).\n", docsURL)
	b.WriteString("For your personal account, contact support.\n")
	return subject, b.String()
}

// CredentialCreatedBody notifies that a new API key or CLI/agent sign-in
// credential was created on the account. what is a short human phrase such
// as "An API key" or "A CLI sign-in".
func CredentialCreatedBody(email, what, label, ip, userAgent string, at time.Time) (subject, body string) {
	if len(userAgent) > 200 {
		userAgent = userAgent[:200]
	}
	if label == "" {
		label = "(no label)"
	}
	subject = "New API credential on your Gregale account"
	body = fmt.Sprintf(`Hi,

%s was created for %s.

  Time:    %s
  Label:   %s
  Address: %s
  Client:  %s

If this was you, there is nothing to do. If not, revoke it under
Account → API keys (or gregale key delete ID), change your password, and
contact support@gregale.dev.

— Gregale
`, oneLine(what), safeRecipient(email), mailTime(at), oneLine(label), oneLine(ip), oneLine(userAgent))
	return subject, body
}

func providerName(provider string) string {
	switch provider {
	case "google":
		return "Google"
	case "github":
		return "GitHub"
	}
	return oneLine(provider)
}

// maskEmail keeps the first character of the local part and the domain.
func maskEmail(email string) string {
	email = safeRecipient(email)
	at := strings.LastIndexByte(email, '@')
	if at <= 0 {
		return "***"
	}
	return email[:1] + "***" + email[at:]
}
