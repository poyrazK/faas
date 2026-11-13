package mail

import (
	"fmt"
	"time"
)

// MovedToFreeForNonPaymentBody tells an account that its payment stayed
// overdue for seven days, so it now runs on the Free plan (Terms §3, billing
// review 2026-11-13 R9-1). Nothing is deleted for non-payment: apps and data
// past the Free plan follow the plan's own limits, and paying the balance or
// subscribing again restores the paid plan.
func MovedToFreeForNonPaymentBody(email, plan string, pastDueAt, movedAt time.Time) (subject, body string) {
	email = safeRecipient(email)
	since := pastDueAt.UTC().Format("2006-01-02")
	at := movedAt.UTC().Format("2006-01-02 15:04 UTC")
	subject = "Your Gregale account moved to the Free plan for an overdue payment"
	body = fmt.Sprintf(`Hi,

Your Gregale account (%s) has had an overdue payment since %s, so at %s
it moved from the %s plan to the Free plan.

Your account, apps and data are kept; nothing is deleted because of the
overdue payment. Apps, databases and storage beyond the Free plan's limits
stop until they fit it or you upgrade, and you can export your data at any
time from the dashboard or the CLI.

To return to your plan, pay the outstanding balance or subscribe again:

    gregale billing portal

The balance stays on your account until it is paid.

If you believe this is a mistake, contact support@gregale.dev.

— Gregale
`, email, since, at, plan)
	return subject, body
}
