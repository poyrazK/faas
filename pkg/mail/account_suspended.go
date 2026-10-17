package mail

import (
	"fmt"
	"time"
)

// AccountSuspendedBodyV1 freezes the original suspension and deletion deadline.
func AccountSuspendedBodyV1(email string, suspendedAt, deletionAt time.Time) (subject, body string) {
	subject = "Your Gregale account is suspended for an overdue payment"
	body = fmt.Sprintf(`Hi,

Your Gregale account (%s) was suspended at %s because a payment is overdue.

Please review and pay the outstanding invoice in the billing dashboard.
Unless payment is resolved, your account is scheduled for deletion at %s.

If you need help, contact support@gregale.dev.

— Gregale
`, safeRecipient(email), suspendedAt.UTC().Format("2006-01-02 15:04:05 UTC"), deletionAt.UTC().Format("2006-01-02 15:04:05 UTC"))
	return
}
