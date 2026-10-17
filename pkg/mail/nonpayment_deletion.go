package mail

import (
	"fmt"
	"time"
)

// NonpaymentDeletionBodyV1 freezes the original overdue date and saved hard
// deletion deadline for durable retries. Payment recovery cancels the deletion.
func NonpaymentDeletionBodyV1(email string, pastDueAt, deleteOn time.Time) (subject, body string) {
	deadline := deleteOn.UTC().Format("2006-01-02 15:04:05 UTC")
	subject = "Your Gregale account is scheduled for deletion for non-payment"
	body = fmt.Sprintf(`Hi,

Your Gregale account (%s) has an overdue payment dating from %s.
It is now scheduled for permanent deletion at %s.

To keep your account, pay the outstanding balance before that deadline:

    gregale billing portal

Once your billing provider confirms payment, the deletion is cancelled and
your account becomes active again. After permanent deletion, your account
and its data cannot be recovered.

If you believe this is a mistake, contact support@gregale.dev.

— Gregale
`, safeRecipient(email), pastDueAt.UTC().Format("2006-01-02 15:04:05 UTC"), deadline)
	return
}
