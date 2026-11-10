package mail

import (
	"fmt"
	"time"
)

// PasswordResetBodyV1 is frozen for durable handoff. The same link always has
// its original 15-minute validity; retrying delivery does not renew it.
func PasswordResetBodyV1(link string) (subject, body string) {
	return "Reset your Gregale password", "Hi,\n\nReset your Gregale password by clicking the link below (valid for 15 minutes):\n\n  " + link + "\n\nIf you did not request this, you can ignore this email.\n"
}

// MagicLinkBodyV1 preserves the existing signup/sign-in message and original
// validity window for every retry of a durable intent.
func MagicLinkBodyV1(link string) (subject, body string) {
	return "Confirm your Gregale account", "Hi,\n\nWelcome to faas. Confirm your email by clicking the link below (valid for 15 minutes):\n\n  " + link + "\n\nIf you did not request this, you can ignore this email.\n"
}

// EmailVerificationBody renders the one-shot address-verification email sent
// after password signup. The deadline is explicit so an old message is not
// mistaken for a live credential.
func EmailVerificationBody(email, link string, expiresAt time.Time) (subject, body string) {
	return EmailVerificationBodyV1(email, link, expiresAt)
}

// EmailVerificationBodyV1 is frozen for durable handoff retries.
func EmailVerificationBodyV1(email, link string, expiresAt time.Time) (subject, body string) {
	email = safeRecipient(email)
	subject = "Verify your Gregale email"
	body = fmt.Sprintf(`Hi,

Verify %s for your Gregale account by opening this link before %s:

  %s

Open it in a browser signed in to this Gregale account; you will be asked to
sign in otherwise. The link can be used once.

If you did not create this account, someone else signed up with your address.
Do not sign in with a password you were not given: request a sign-in link at
the Gregale sign-in page instead, which removes their password and keys.

— Gregale
`, email, expiresAt.UTC().Format("2006-01-02 15:04 UTC"), link)
	return subject, body
}
