package mail

import (
	"fmt"
	"time"
)

// AccountRestoredBodyV1 is frozen for retries of a committed billing recovery.
func AccountRestoredBodyV1(email string, restoredAt time.Time) (subject, body string) {
	subject = "Your Gregale account is back in good standing"
	body = fmt.Sprintf(`Hi,

Your billing provider confirmed a successful payment for your Gregale account
(%s), and its billing status was restored at %s.
Your account is no longer overdue.

You can review the payment and your billing status in the dashboard.
If you need help, contact support@gregale.dev.

— Gregale
`, safeRecipient(email), restoredAt.UTC().Format("2006-01-02 15:04:05 UTC"))
	return
}
