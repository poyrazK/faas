package mail

import (
	"fmt"
	"time"
)

// AccountDeletionCancelledBody tells the owner that a scheduled deletion of
// their account was cancelled and the account works again
// (ADR-20261111150000). Restoring re-enables every key and app the deletion
// stopped, so a restore the owner did not make must be noticed.
func AccountDeletionCancelledBody(email string, at time.Time) (subject, body string) {
	email = safeRecipient(email)
	subject = "Your Gregale account deletion was cancelled"
	body = fmt.Sprintf(`Hi,

The scheduled deletion of your Gregale account (%s) was cancelled on
%s UTC. The account is active again: its API keys, apps and
sign-in work as before.

If you did not cancel it, sign in, revoke API keys you do not recognise
(gregale key list), change your password, and request the deletion
again. Contact support@gregale.dev if you cannot sign in.

— Gregale
`, email, at.UTC().Format("2006-01-02 15:04"))
	return
}
