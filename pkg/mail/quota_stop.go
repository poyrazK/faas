package mail

import (
	"fmt"
	"html"
	"time"
)

// QuotaKind names the monthly allowance a quota mail is about.
type QuotaKind string

const (
	// QuotaKindCompute is the Free plan's included GB-RAM-hours.
	QuotaKindCompute QuotaKind = "compute"
	// QuotaKindEgress is the plan's monthly app egress, which stops apps
	// unless egress overage is on.
	QuotaKindEgress QuotaKind = "egress"
)

func quotaKindNoun(kind QuotaKind) string {
	if kind == QuotaKindEgress {
		return "app egress"
	}
	return "included compute (GB-RAM-hours)"
}

func quotaKindFix(kind QuotaKind) string {
	if kind == QuotaKindEgress {
		return "Upgrade your plan, or on a paid plan turn on egress overage\n(gregale billing egress-overage on) to pay for egress past the allowance."
	}
	return "Upgrade to a paid plan to keep your apps running past the allowance."
}

// QuotaApproachingBody warns that the month's usage reached 80% of a
// monthly allowance that stops the account's apps when it runs out: the Free
// compute allowance, or the app egress allowance of a plan without egress
// overage (ADR-20261103131742).
func QuotaApproachingBody(email, plan string, kind QuotaKind, percent int, day time.Time) (subject, text, htmlBody string) {
	email = safeRecipient(email)
	noun := quotaKindNoun(kind)
	date := day.UTC().Format("2006-01-02")
	subject = fmt.Sprintf("Your Gregale account used %d%% of its monthly %s", percent, noun)
	text = fmt.Sprintf(`Hi,

Your Gregale account (%s) on the %s plan used %d%% of this month's %s
on %s.

When it reaches 100%%, your apps are parked until the month ends. Your
data, databases and buckets are kept.

%s

— Gregale
`, email, plan, percent, noun, date, quotaKindFix(kind))
	htmlBody = fmt.Sprintf(`<!doctype html><html><body>
<p>Hi,</p><p>Your Gregale account (<code>%s</code>) on the %s plan used %d%% of this month's %s on %s.</p>
<p>When it reaches 100%%, your apps are parked until the month ends. Your data, databases and buckets are kept.</p>
<p>%s</p>
<p>&mdash; Gregale</p></body></html>
`, html.EscapeString(email), html.EscapeString(plan), percent, html.EscapeString(noun), date, html.EscapeString(quotaKindFix(kind)))
	return
}

// QuotaStoppedBody tells the account its apps were parked because a monthly
// allowance ran out, and how to bring them back.
func QuotaStoppedBody(email, plan string, kind QuotaKind, at time.Time) (subject, text, htmlBody string) {
	email = safeRecipient(email)
	noun := quotaKindNoun(kind)
	date := at.UTC().Format("2006-01-02 15:04 UTC")
	subject = fmt.Sprintf("Your Gregale apps are parked: monthly %s used up", noun)
	text = fmt.Sprintf(`Hi,

Your Gregale account (%s) on the %s plan used all of this month's %s
at %s, so its apps are parked and new requests are refused until the
month ends.

Your data, databases and buckets are kept.

%s

— Gregale
`, email, plan, noun, date, quotaKindFix(kind))
	htmlBody = fmt.Sprintf(`<!doctype html><html><body>
<p>Hi,</p><p>Your Gregale account (<code>%s</code>) on the %s plan used all of this month's %s at %s, so its apps are parked and new requests are refused until the month ends.</p>
<p>Your data, databases and buckets are kept.</p>
<p>%s</p>
<p>&mdash; Gregale</p></body></html>
`, html.EscapeString(email), html.EscapeString(plan), html.EscapeString(noun), date, html.EscapeString(quotaKindFix(kind)))
	return
}
