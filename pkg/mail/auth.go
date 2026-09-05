package mail

import (
	"fmt"
	"time"
)

// EmailVerificationBody renders the one-shot address-verification email sent
// after password signup. The deadline is explicit so an old message is not
// mistaken for a live credential.
func EmailVerificationBody(email, link string, expiresAt time.Time) (subject, body string) {
	email = safeRecipient(email)
	subject = "Verify your Gregale email"
	body = fmt.Sprintf(`Hi,

Verify %s for your Gregale account by opening this link before %s:

  %s

The link can be used once. If you did not create this account, you can ignore
this email.

— Gregale
`, email, expiresAt.UTC().Format("2006-01-02 15:04 UTC"), link)
	return subject, body
}
