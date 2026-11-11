package mail

import (
	"fmt"
	"time"
)

// Organization security notices (ADR 20261111130000). Changes to who
// controls an organization, and where it is billed, were recorded only in
// the in-app activity feed; these mails tell the owners and the member a
// change concerns.

// MaskEmail is maskEmail for callers composing a change description.
func MaskEmail(email string) string { return maskEmail(email) }

// OrgSecurityEventBody tells email that change happened in orgName.
func OrgSecurityEventBody(email, orgName, change string, at time.Time) (subject, body string) {
	org := oneLine(orgName)
	subject = fmt.Sprintf("Access to the Gregale organization %s changed", org)
	body = fmt.Sprintf(`Hi,

At %s, in the Gregale organization %s: %s.

You are receiving this as an owner of the organization or as the member it
concerns. If you did not expect it, sign in and review Organization →
Members and Activity, and contact support@gregale.dev.

— Gregale
`, mailTime(at), org, oneLine(change))
	return subject, body
}

// OrgBillingEmailChangedBody tells the previous billing address and the
// owners that invoices and billing sign-in links now go to newEmail.
func OrgBillingEmailChangedBody(email, orgName, newEmail string, at time.Time) (subject, body string) {
	org := oneLine(orgName)
	subject = fmt.Sprintf("Billing email changed for the Gregale organization %s", org)
	body = fmt.Sprintf(`Hi,

At %s the billing email of the Gregale organization %s was changed to %s.
Invoices, receipts and billing portal sign-in links now go there.

If you did not expect this, sign in, check Organization → Billing, and
contact support@gregale.dev.

— Gregale
`, mailTime(at), org, maskEmail(newEmail))
	return subject, body
}
