package mail

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// RenderTemplate is a single sample for the dry-run output. The CLI
// (cmd/gregale mail dry-run) writes one JSON object per template so
// an operator can eyeball the wire payload before flipping the box
// to FAAS_MAIL_TRANSPORT=resend in production.
type RenderTemplate struct {
	// Name is the template id (e.g. "quota_warning").
	Name string `json:"name"`
	// Subject is the rendered subject line.
	Subject string `json:"subject"`
	// TextBody is the rendered plain-text body.
	TextBody string `json:"text_body"`
	// HTMLBody is the rendered HTML alt (empty for templates
	// that have no HTML variant).
	HTMLBody string `json:"html_body,omitempty"`
	// Headers is the message.Headers map after ApplyMarketingHeaders
	// runs against the dry-run unsubscribe URL. Empty when the
	// template does not receive bulk-sender headers.
	Headers map[string]string `json:"headers,omitempty"`
	// MessageID is the stable idempotency key derived from
	// (account_id, template, day) so the dry-run output matches
	// what the production sender will write.
	MessageID string `json:"message_id"`
}

// RenderAllTemplates renders every production template against a
// fixture account + day, applies marketing headers when an
// unsubscribe URL is provided, and returns the wire payloads.
//
// The fixture is intentionally synthetic — the dry-run is for
// operators validating content, not for replaying real customer
// data. Pins:
//   - email: alice@example.test
//   - account_id: acc_dryrun_xxx (cosmetic — the MessageID is
//     what Resend / Postmark use for idempotency, not the literal
//     account id)
//   - day: today UTC midnight (a dry-run today matches what
//     meterd will send today)
//   - used_gb: 80% of the plan's quota
//
// The unsubscribe URL, when non-empty, is run through
// ValidateUnsubscribeURL — an operator who passes a malformed URL
// gets a loud error rather than a silently dropped header.
func RenderAllTemplates(unsubscribeURL string, now time.Time) ([]RenderTemplate, error) {
	if unsubscribeURL != "" {
		if err := ValidateUnsubscribeURL(unsubscribeURL); err != nil {
			return nil, fmt.Errorf("mail: dry-run: invalid unsubscribe url: %w", err)
		}
	}
	today := now.UTC().Truncate(24 * time.Hour)
	pastDueAt := today.Add(-24 * time.Hour)
	deletionAt := today.Add(30 * 24 * time.Hour)

	const (
		accountID = "acc_dryrun_xxx"
		email     = "alice@example.test"
	)

	// Quota warning — paid plan, 80% used.
	quotaSubject, quotaBody := QuotaWarningBody(email, "pro", 200, 250, today)
	quotaHTML := QuotaWarningHTMLBody(email, "pro", 200, 250, today.Format("2006-01-02"))
	quotaMsg := Message{
		To:       []string{email},
		Subject:  quotaSubject,
		TextBody: quotaBody,
		HTMLBody: quotaHTML,
	}
	if unsubscribeURL != "" {
		quotaMsg = ApplyMarketingHeaders(quotaMsg, unsubscribeURL)
	}
	quotaMsg.MessageID = fmt.Sprintf("%s:quota_warning:%s", accountID, today.Format("2006-01-02"))

	// Account suspended.
	suspSubject, suspBody := AccountSuspendedBody(email, today, pastDueAt.Add(21*24*time.Hour))
	suspMsg := Message{To: []string{email}, Subject: suspSubject, TextBody: suspBody}
	suspMsg.MessageID = fmt.Sprintf("%s:account_suspended:%s", accountID, today.Format("2006-01-02"))

	// Deletion pending — dunning's 30-day non-payment notice.
	delSubject, delBody := AccountDeletionForNonPaymentBody(email, pastDueAt, deletionAt)
	delMsg := Message{To: []string{email}, Subject: delSubject, TextBody: delBody}
	delMsg.MessageID = fmt.Sprintf("%s:account_deletion_pending:%s", accountID, today.Format("2006-01-02"))

	// Payment failed.
	pfSubject, pfBody := PaymentFailedBody(email, pastDueAt)
	pfMsg := Message{To: []string{email}, Subject: pfSubject, TextBody: pfBody}
	pfMsg.MessageID = fmt.Sprintf("%s:payment_failed:%s", accountID, today.Format("2006-01-02"))

	renders := []RenderTemplate{
		toRender("quota_warning", quotaMsg),
		toRender("account_suspended", suspMsg),
		toRender("account_deletion_pending", delMsg),
		toRender("payment_failed", pfMsg),
	}
	return renders, nil
}

// toRender flattens a Message into the dry-run wire shape.
func toRender(name string, msg Message) RenderTemplate {
	return RenderTemplate{
		Name:      name,
		Subject:   msg.Subject,
		TextBody:  msg.TextBody,
		HTMLBody:  msg.HTMLBody,
		Headers:   msg.Headers,
		MessageID: msg.MessageID,
	}
}

// WriteDryRunJSON serializes renders to w as a JSON array (one
// element per template). Pretty-printed so an operator scanning
// the terminal can read each subject without expanding a JSON
// viewer. The shape is intentionally stable so an operator can
// pipe the output through `jq '.[] | select(.name=="…")'`.
func WriteDryRunJSON(w io.Writer, renders []RenderTemplate) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(renders)
}
