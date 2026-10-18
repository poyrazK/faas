package mail

import (
	"fmt"
	"html"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// QuotaWarningBodyV1 freezes the usage snapshot in both alternatives. It does
// not compute charges or name a particular billing provider.
func QuotaWarningBodyV1(email, plan string, mbSeconds int64, quotaGB int, day time.Time) (subject, text, htmlBody string) {
	email = safeRecipient(email)
	used := api.InternalMailQuotaWarningUsageGBHours(mbSeconds)
	date := day.UTC().Format("2006-01-02")
	subject = fmt.Sprintf("Your Gregale account is over its %s plan quota", plan)
	text = fmt.Sprintf(`Hi,

Your Gregale account (%s) reached its %s plan quota on %s.

  Used this month: %.2f GB-h
  Included quota:  %d GB-h

Further usage may accrue overage at the rates listed in the billing dashboard.
To reduce usage, stop running instances or upgrade your plan.

This is your quota warning for today. You may receive another tomorrow if
usage is still over the quota.

— Gregale
`, email, plan, date, used, quotaGB)
	htmlBody = fmt.Sprintf(`<!doctype html><html><body>
<p>Hi,</p><p>Your Gregale account (<code>%s</code>) reached its %s plan quota on %s.</p>
<table><tr><th>Used this month</th><td>%.2f GB-h</td></tr><tr><th>Included quota</th><td>%d GB-h</td></tr></table>
<p>Further usage may accrue overage at the rates listed in the billing dashboard.
To reduce usage, stop running instances or upgrade your plan.</p>
<p>This is your quota warning for today. You may receive another tomorrow if usage is still over the quota.</p>
<p>&mdash; Gregale</p></body></html>
`, html.EscapeString(email), html.EscapeString(plan), date, used, quotaGB)
	return
}
