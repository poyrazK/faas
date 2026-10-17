package mail

import (
	"fmt"
	"time"
)

// PaymentFailedBodyV1 is frozen for retries. Both deadlines refer to the
// original overdue period, never the day an email attempt happens to run.
func PaymentFailedBodyV1(email string, pastDueAt time.Time) (subject, body string) {
	subject = "Your Gregale payment failed — update your payment method"
	at := pastDueAt.UTC()
	body = fmt.Sprintf(`Hi,

Your Gregale account (%s) became overdue at %s after a payment failed.
Existing apps continue serving, but new deploys are blocked while payment
is overdue.

Update your payment method in the dashboard billing portal, or run
gregale billing portal, then retry the payment.

If payment remains unresolved, apps may be suspended from %s and
account deletion may be scheduled from %s.
These deadlines are measured from the original overdue time above.

If you have already paid, check the billing portal for confirmation.
For help, contact support@gregale.dev.

— Gregale
`, safeRecipient(email), at.Format("2006-01-02 15:04:05 UTC"),
		at.Add(7*24*time.Hour).Format("2006-01-02 15:04:05 UTC"),
		at.Add(21*24*time.Hour).Format("2006-01-02 15:04:05 UTC"))
	return
}
