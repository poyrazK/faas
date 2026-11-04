package orgbilling

import (
	"net/mail"
	"strings"
)

// maxBillingEmailLen is the longest address SMTP carries (RFC 5321 §4.5.3.1).
const maxBillingEmailLen = 254

// NormalizeBillingEmail returns raw as a bare, lower-case address, or false
// when raw is not one address. A display name or a list is refused: the
// billing provider keys its customer, and the portal sign-in, on the address
// (ADR-20261103131742).
func NormalizeBillingEmail(raw string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if email == "" || len(email) > maxBillingEmailLen {
		return "", false
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Name != "" || addr.Address != email || !strings.Contains(email[strings.LastIndex(email, "@")+1:], ".") {
		return "", false
	}
	return email, true
}

// IsBillingContact reports whether memberEmail is the organization's billing
// email. The billing provider sends that address the organization's invoices
// and signs it in to the organization's portal, so the member keeps billing
// control until the organization's billing email moves.
func IsBillingContact(billingEmail, memberEmail string) bool {
	billingEmail = strings.TrimSpace(billingEmail)
	return billingEmail != "" && strings.EqualFold(billingEmail, strings.TrimSpace(memberEmail))
}
