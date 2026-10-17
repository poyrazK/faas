package mail

import (
	"fmt"
	"time"
)

// MFAEnabledBodyV1 is frozen for retries of committed enrollment events.
func MFAEnabledBodyV1(email string, enabledAt time.Time) (subject, body string) {
	subject = "MFA enabled on your Gregale account"
	body = fmt.Sprintf(`Hi,

MFA was enabled for your Gregale account (%s) at %s.
You will need your authenticator or a recovery code when you sign in.

If this was you, keep your recovery codes in a safe place.

If this was not you, reset your password immediately, review your
MFA settings, revoke other sessions and contact support@gregale.dev.

— Gregale
`, safeRecipient(email), enabledAt.UTC().Format("2006-01-02 15:04:05 UTC"))
	return
}
