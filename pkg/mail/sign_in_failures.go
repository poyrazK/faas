package mail

import (
	"fmt"
	"time"
)

// SignInFailuresBodyV1 tells an account owner that password sign-in for
// their address was paused after repeated failures.
func SignInFailuresBodyV1(email string, failures int, window time.Duration, at time.Time) (subject, body string) {
	subject = "Repeated failed sign-ins on your Gregale account"
	body = fmt.Sprintf(`Hi,

Gregale refused %d password sign-ins for your account (%s) within %s,
the last at %s. Password sign-in for this address is paused until the
failures age out.

If this was you, wait a while or reset your password; a successful reset
lifts the pause.

If this was not you, someone may be guessing your password. Use a long,
unique password, turn on MFA and contact support@gregale.dev if it keeps
happening.

— Gregale
`, failures, safeRecipient(email), window, at.UTC().Format("2006-01-02 15:04:05 UTC"))
	return
}
