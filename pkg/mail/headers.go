package mail

import (
	"fmt"
	"net/url"
	"strings"
)

// MarketingHeaders returns the List-Unsubscribe + List-Unsubscribe-Post
// pair required by RFC 8058 (one-click unsubscribe). Gmail and
// Yahoo's bulk-sender rules (Feb 2024) require both headers on any
// message sent to more than one recipient at a time; the platform
// only sends single-recipient mail today, but adding the headers now
// means a future marketing / newsletter flow inherits the
// compliance surface for free.
//
// Per-template allow-list policy (NOT all outbound mail carries
// these headers — see ApplyMarketingHeaders below):
//
//   - Quota warning:        YES — operational notice the customer
//     can opt out of; the warning is
//     informational, not a security or
//     billing event.
//   - Account suspended:    NO — billing/security; a customer
//     who unsubscribes from "your apps are
//     suspended" stops receiving the
//     deletion warning and gets deleted
//     silently, which is exactly what
//     #246 exists to prevent.
//   - Deletion pending:     NO — same reasoning as suspended;
//     a one-click unsubscribe here is a
//     self-deletion footgun.
//   - Payment failed:       NO — security/billing.
//   - Account restored:     NO — billing follow-up.
//   - Magic link / MFA /    NO — security; an attacker who
//     password reset:           convinces the customer to click
//     unsubscribe has just blocked
//     future security notifications.
//
// The header infrastructure is generic so a future template
// category (e.g. newsletter) can plug in by extending the
// allow-list without touching this file. Until then, only the
// quota-warning path calls ApplyMarketingHeaders.
func MarketingHeaders(unsubscribeURL string) string {
	if unsubscribeURL == "" {
		// Empty URL is a programming error rather than a runtime
		// condition: an empty unsubscribe URL defeats the RFC 8058
		// contract and the bulk-sender compliance we depend on.
		// Fail loud so a misconfigured wiring is visible at boot,
		// not via a 0.05 % Gmail delivery rate six weeks later.
		panic("mail: MarketingHeaders called with empty unsubscribeURL — wire ApplyMarketingHeaders(URL) at the quota-warning send site")
	}
	return unsubscribeURL
}

// MarketingHeadersMap is the keyed-map form that drops straight
// into Message.Headers. Keeping the string form (MarketingHeaders)
// as the primary return lets tests assert on the exact RFC 8058
// byte sequence; the map form is the wire shape.
func MarketingHeadersMap(unsubscribeURL string) map[string]string {
	if unsubscribeURL == "" {
		panic("mail: MarketingHeadersMap called with empty unsubscribeURL")
	}
	return map[string]string{
		"List-Unsubscribe":      "<" + unsubscribeURL + ">",
		"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
	}
}

// ApplyMarketingHeaders is the one-call seam the quota-warning
// send site uses. It copies the RFC 8058 pair into msg.Headers
// without disturbing any header the caller already set — the
// override is intentional because a misconfigured call site
// cannot accidentally drop the unsubscribe by passing a partial
// map.
//
// Returns the same Message for fluent use at the call site:
//
//	msg := mail.ApplyMarketingHeaders(mail.Message{...}, unsubURL)
func ApplyMarketingHeaders(msg Message, unsubscribeURL string) Message {
	hdrs := msg.Headers
	if hdrs == nil {
		hdrs = map[string]string{}
	}
	for k, v := range MarketingHeadersMap(unsubscribeURL) {
		hdrs[k] = v
	}
	msg.Headers = hdrs
	return msg
}

// ValidateUnsubscribeURL is the boot-time guard: a malformed URL
// would let the List-Unsubscribe header ship as a string that
// receivers cannot parse, which is the bulk-sender rejection path
// Gmail/Yahoo document. We accept any absolute http/https URL —
// mailto: is not RFC 8058 compliant for one-click and we want
// the receiver to follow an HTTP link anyway.
//
// Returns nil on success; the caller should fail-closed at boot
// if ValidateUnsubscribeURL returns an error so the message never
// goes out without a valid header.
func ValidateUnsubscribeURL(unsubscribeURL string) error {
	if strings.TrimSpace(unsubscribeURL) == "" {
		return fmt.Errorf("mail: unsubscribe URL is empty")
	}
	u, err := url.Parse(unsubscribeURL)
	if err != nil {
		return fmt.Errorf("mail: parse unsubscribe URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("mail: unsubscribe URL must be http or https (got %q)", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("mail: unsubscribe URL has empty host")
	}
	return nil
}

// QuotaWarningHTMLBody is the HTML alt of QuotaWarningBody. Plain
// text remains the canonical form (RFC 8058 + the existing
// template); the HTML alt is what Gmail/Yahoo use to render the
// "View as HTML" link in their summary view. Keeping the HTML
// side deliberately simple — no CSS, no images, no tracking
// pixels — matches the "honest medium for a paid-tier notice"
// rationale in account.go and avoids the image-tracking consent
// surface that bulk-sender rules scrutinise.
//
// usedGB is rendered to 2 dp to match the plain-text body.
func QuotaWarningHTMLBody(email, plan string, usedGB float64, quotaGB int, day string) string {
	email = safeRecipient(email)
	return fmt.Sprintf(`<!doctype html>
<html><body>
<p>Hi,</p>
<p>Your Gregale account (<code>%s</code>) crossed 100%% of its %s plan quota on %s.
You're now accruing overage at the rates listed in the dashboard.</p>
<table>
  <tr><th>Used</th><td>%.2f GB-h</td></tr>
  <tr><th>Quota</th><td>%d GB-h</td></tr>
</table>
<p>Overage is billed on the next invoice via Stripe's metered
subscription item. To stop the overage, either upgrade your plan or
reduce the running instances on your account.</p>
<p>This is the only quota warning you'll get today; the next one
arrives tomorrow if usage is still over the quota.</p>
<p>&mdash; Gregale</p>
</body></html>
`, email, plan, day, usedGB, quotaGB)
}
