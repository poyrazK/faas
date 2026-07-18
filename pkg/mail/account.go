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
	"time"
)

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