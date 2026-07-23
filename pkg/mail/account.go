// Package mail — account lifecycle bodies (spec §17 G6, ADR-021).
//
// The bodies here are intentionally plaintext (no HTML alt) because
// the receiving audience is the customer, not a marketing subscriber,
// and a readable plain-text message is the most honest medium for a
// "your data will be deleted in 30 days" notice. Subject lines are
// short enough to render fully in every mail client's summary view.

package mail

import (
	"fmt"
	"strings"
	"time"
)

// safeRecipient strips CR/LF from a customer-controlled email address
// before it is interpolated into a mail body. CR/LF in an SMTP body
// (or anywhere downstream of the producer's output) is the canonical
// go/log-injection seam (CWE-117) — CodeQL flags
// strings.ReplaceAll("\r","")+strings.ReplaceAll("\n","") as a
// recognised sanitiser and we follow the same shape that PR #119 /
// pkg/gateway/forwardproxy.go::safeLogField uses. Email addresses
// that legitimately contain CR/LF don't exist on the public internet,
// so strip-not-replace is correct here.
//
// This guards the outbound SMTP body. The slog transport has its own
// sanitiser for the log line (pkg/logsanitize.Field on msg.To), so
// the two surfaces stay defended independently — the same email
// value can land safely in both.
func safeRecipient(email string) string {
	email = strings.ReplaceAll(email, "\r", "")
	email = strings.ReplaceAll(email, "\n", "")
	return email
}

// AccountDeletionPendingBody renders the "your account will be deleted
// in 30 days" email. ScheduledAt is the moment the customer hit
// DELETE (deletion_requested_at); restoreUntil is the same moment + 30
// days, the deadline after which pkg/grace performs the hard delete.
func AccountDeletionPendingBody(email string, scheduledAt, restoreUntil time.Time) (subject, body string) {
	scheduled := scheduledAt.UTC().Format("2006-01-02")
	deadline := restoreUntil.UTC().Format("2006-01-02")
	subject = fmt.Sprintf("Your faas account will be deleted on %s", deadline)
	body = fmt.Sprintf(`Hi,

You scheduled your faas account (%s) for deletion on %s.

If you change your mind, you can cancel the deletion any time before %s by running:

    faas account restore

After %s every row tied to your account — apps, deployments, builds,
secrets, API keys, domains, crons, and usage history — will be
permanently deleted from our database. There is no recovery option
after that point.

If this request was not made by you, change your password immediately
and contact support@DOMAIN.

— onebox faas
`, email, scheduled, deadline, deadline)
	return
}

// AccountDeletionCompleteBody is sent by pkg/grace after the hard
// delete runs. Kept separate from AccountDeletionPendingBody so the
// two never share a single mutable template (they have different
// tone and a different set of next-action links).
//
// accountEmail is the email address that was on the deleted row; we
// keep it on the message so a forward of this email to support@DOMAIN
// lets us identify the account without a separate lookup.
func AccountDeletionCompleteBody(accountEmail string) (subject, body string) {
	subject = "Your faas account has been deleted"
	body = fmt.Sprintf(`Hi,

The 30-day grace period for your faas account (%s) has ended and
every row tied to it — apps, deployments, builds, secrets, API keys,
domains, crons, and usage history — has been permanently deleted from
our database.

If this was not your intent, contact support@DOMAIN within 24 hours
and we'll work with you to recover what we can from our backups.

— onebox faas
`, accountEmail)
	return
}

// AccountSuspendedBody is the dunning-driven "your apps are now
// suspended" email (spec §4.7, §17 dunning state machine). Sent when
// pkg/meter.Dunning transitions an account from past_due to suspended
// after 7 days without payment. Distinct subject from the deletion
// emails so the customer can tell what happened at a glance.
func AccountSuspendedBody(email string, at time.Time) (subject, body string) {
	atStr := at.UTC().Format("2006-01-02 15:04 UTC")
	subject = "Your faas apps have been suspended"
	body = fmt.Sprintf(`Hi,

Your faas account (%s) has not received a successful payment for 7
days. As of %s, every running instance tied to your account has been
parked and new deploys are blocked.

To restore service, update your payment method and run:

    faas billing retry

Once Stripe confirms the payment, meterd will resume your apps on the
next quota tick (within 60 s). If the payment does not arrive within
21 days of the original failure (i.e. %s — 14 days from now), your
account will be scheduled for permanent deletion.

If this charge is unexpected, contact support@DOMAIN.

— onebox faas
`, email, atStr, atStr)
	return
}

// PaymentFailedBody is the entry-point email sent the moment a Stripe
// `invoice.payment_failed` event flips an account from active to
// past_due (spec §4.7, §17 dunning state machine, §171 "All transitions
// emailed"). The 7-day grace clock starts at pastDueAt — telling the
// customer the deadline here is the load-bearing piece; without this
// email a customer first hears about the failure 7 days later when
// their apps are already parked.
//
// The 7-day deadline is rendered as a UTC date so the customer doesn't
// have to do timezone arithmetic against the timestamp we stamped.
func PaymentFailedBody(email string, pastDueAt time.Time) (subject, body string) {
	email = safeRecipient(email)
	atStr := pastDueAt.UTC().Format("2006-01-02 15:04 UTC")
	deadline := pastDueAt.UTC().Add(7 * 24 * time.Hour).Format("2006-01-02")
	subject = "Your faas payment failed — action needed within 7 days"
	body = fmt.Sprintf(`Hi,

The most recent charge to your faas account (%s) failed at %s.

Your apps are still serving and you can still query usage, but new
deploys are blocked while the charge is unpaid. If we don't see a
successful payment by %s (7 days from now), every running instance
will be parked. If the payment still hasn't arrived 21 days from the
original failure, your account will be scheduled for permanent
deletion.

To fix this:

  1. Update your payment method in the dashboard, or
  2. Run:    faas billing retry

Once Stripe confirms the payment, meterd will resume your apps on the
next quota tick (within 60 s) and send you a confirmation email.

If this charge is unexpected, contact support@DOMAIN.

— onebox faas
`, email, atStr, deadline)
	return
}

// AccountRestoredBody is the recovery email sent on Stripe
// `invoice.payment_succeeded` after a past_due → active flip. Light
// tone: it acknowledges the customer fixed the problem, tells them what
// happened while they were gone (apps were parked at the 7-day mark),
// and notes that the next quota tick resumes them. Distinct subject
// from the suspended / deletion emails so the customer can tell what
// happened at a glance.
func AccountRestoredBody(email string, restoredAt time.Time) (subject, body string) {
	email = safeRecipient(email)
	atStr := restoredAt.UTC().Format("2006-01-02 15:04 UTC")
	subject = "Your faas account is back in good standing"
	body = fmt.Sprintf(`Hi,

Stripe confirmed a successful payment for your faas account (%s) at %s.
Your account is now active again.

If your apps had been parked during the grace period (after 7 days of
non-payment), meterd will resume them on the next quota tick — within
60 seconds. New deploys are unblocked.

If you don't see your apps come back within a minute, run:

    faas status

If that doesn't show them resuming, contact support@DOMAIN and we'll
sort it out.

— onebox faas
`, email, atStr)
	return
}

// QuotaWarningBody is the paid-tier overage notice (spec §4.7). Sent
// at most once per UTC day via the LoadAndStampLastQuotaWarning dedupe
// gate (migration 00013). Distinct subject from the dunning emails so
// a customer over their quota but paying normally doesn't confuse the
// two states.
//
// The used/quota figures are formatted to 2 dp — billing math is in
// float only at this presentation seam (the wire-quantity path is
// integer-arithmetic — pkg/meter/quota.go calls pkg/stripex which uses
// pure int64). The email is the one place customers see the numbers,
// and a one-decimal display is what customers expect.
func QuotaWarningBody(email string, plan string, usedGB float64, quotaGB int, day time.Time) (subject, body string) {
	email = safeRecipient(email)
	dayStr := day.UTC().Format("2006-01-02")
	subject = fmt.Sprintf("Your faas account is over its %s plan quota", plan)
	body = fmt.Sprintf(`Hi,

Your faas account (%s) crossed 100 %% of its %s plan quota on %s.
You're now accruing overage at the rates listed in the dashboard.

  Used:   %.2f GB-h
  Quota:  %d GB-h

Overage is billed on the next invoice via Stripe's metered subscription
item. To stop the overage, either upgrade your plan or reduce the
running instances on your account.

This is the only quota warning you'll get today; the next one arrives
tomorrow if usage is still over the quota.

— onebox faas
`, email, plan, dayStr, usedGB, quotaGB)
	return
}
