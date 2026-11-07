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
%s. Other signed-in sessions were signed out.

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
