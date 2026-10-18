package mail

import (
	"fmt"
	"time"
)

// PasswordChangedBodyV1 is frozen for retries of committed password changes.
func PasswordChangedBodyV1(email, method string, changedAt time.Time) (subject, body string) {
	source := "from your account settings"
	if method == "password_reset" {
		source = "using a password reset link"
	}
	subject = "Password updated on your Gregale account"
	body = fmt.Sprintf(`Hi,

The password for your Gregale account (%s) was updated at %s %s.

If this was you, no further action is needed.

If this was not you, reset your password immediately, review your
MFA settings, revoke other sessions and contact support@gregale.dev.

— Gregale
`, safeRecipient(email), changedAt.UTC().Format("2006-01-02 15:04:05 UTC"), source)
	return
}
