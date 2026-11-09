// Package agentapprovalnotify emails the people who may decide an agent's
// approval request (ADR-20261107140000 §2, §5). meterd runs it; apid writes
// the notification rows it drains.
package agentapprovalnotify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Store is what the notifier reads and writes.
type Store interface {
	state.AgentApprovalNotificationStore
	AccountByID(ctx context.Context, id string) (state.Account, error)
	OrgMemberByAccount(ctx context.Context, orgID, accountID string) (state.OrgMembership, error)
	OrgByID(ctx context.Context, id string) (state.Org, error)
	ListOrgMembers(ctx context.Context, orgID string) ([]state.OrgMembership, error)
}

// Mailer sends one plain-text email; idempotencyKey lets the provider drop a
// retried duplicate. alertchannels.Mailer has the same shape.
type Mailer interface {
	SendEmail(ctx context.Context, to, subject, body, idempotencyKey string) error
}

// Outcomes reported to Outcome.
const (
	OutcomeSent     = "sent"
	OutcomeDeferred = "deferred"
	OutcomeSkipped  = "skipped"
	OutcomeFailed   = "failed"
)

const (
	batchSize   = 50
	lease       = 2 * time.Minute
	retryDelay  = time.Minute
	maxAttempts = 5
	// Interval is how often Run looks for due notifications.
	Interval = 15 * time.Second
)

// Notifier delivers agent approval notifications.
type Notifier struct {
	Store Store
	Mail  Mailer
	Log   *slog.Logger
	Now   func() time.Time
	// Channels, when set, sends new requests to their bound notification
	// channels (§3).
	Channels ChannelSender
	// Outcome, when set, counts each notification's outcome.
	Outcome func(outcome string)
}

// Run delivers due notifications every Interval until ctx ends.
func (n *Notifier) Run(ctx context.Context) {
	ticker := time.NewTicker(Interval)
	defer ticker.Stop()
	for {
		if err := n.RunOnce(ctx); err != nil && ctx.Err() == nil {
			n.log().Warn("agent approval notifications: tick failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// RunOnce delivers the notifications due now.
func (n *Notifier) RunOnce(ctx context.Context) error {
	due, err := n.Store.ClaimAgentApprovalNotifications(ctx, batchSize, lease)
	if err != nil {
		return fmt.Errorf("claim agent approval notifications: %w", err)
	}
	for _, item := range due {
		n.count(n.deliver(ctx, item))
	}
	return nil
}

func (n *Notifier) deliver(ctx context.Context, item state.AgentApprovalNotification) string {
	now := n.now()
	status, expiresAt, err := n.Store.AgentApprovalRequestState(ctx, item.ApprovalID)
	if errors.Is(err, state.ErrNotFound) || err == nil && (status != api.AgentApprovalPending || !now.Before(expiresAt)) {
		// Decided, expired or gone: there is nothing left to ask.
		return n.finish(ctx, []string{item.ApprovalID}, state.AgentApprovalNotificationSkipped, "")
	}
	if err != nil {
		return n.retry(ctx, item, err)
	}
	n.notifyChannels(ctx, item, now)
	recipients, err := n.recipients(ctx, item)
	if err != nil {
		return n.retry(ctx, item, err)
	}
	if len(recipients) == 0 {
		return n.finish(ctx, []string{item.ApprovalID}, state.AgentApprovalNotificationSkipped, "no recipient with a verified email")
	}
	ready, until, err := n.readyRecipients(ctx, item.KeyID, recipients, now)
	if err != nil {
		return n.retry(ctx, item, err)
	}
	if len(ready) == 0 {
		// Everyone heard about this key recently; the next email lists it.
		if err := n.Store.DeferAgentApprovalNotification(ctx, item.ApprovalID, until); err != nil {
			n.log().Warn("agent approval notifications: defer", "approval_id", item.ApprovalID, "err", err)
		}
		return OutcomeDeferred
	}
	lines, err := n.Store.PendingAgentApprovalNotificationsForKey(ctx, item.KeyID)
	if err != nil {
		return n.retry(ctx, item, err)
	}
	if len(lines) == 0 {
		return n.finish(ctx, []string{item.ApprovalID}, state.AgentApprovalNotificationSkipped, "")
	}
	subject, body := message(item.AgentName, lines)
	for _, recipient := range ready {
		key := "agent-approval-" + item.ApprovalID + "-" + recipient.ID
		if err := n.Mail.SendEmail(ctx, recipient.Email, subject, body, key); err != nil {
			return n.retry(ctx, item, err)
		}
		if err := n.Store.RecordAgentApprovalEmailSent(ctx, recipient.ID, item.KeyID); err != nil {
			n.log().Warn("agent approval notifications: record email", "recipient", recipient.ID, "err", err)
		}
	}
	ids := make([]string, 0, len(lines))
	for _, line := range lines {
		ids = append(ids, line.ApprovalID)
	}
	if n.finish(ctx, ids, state.AgentApprovalNotificationSent, "") == OutcomeFailed {
		return OutcomeFailed
	}
	return OutcomeSent
}

// recipients are the member who connected the agent; when the key acts in
// an organization the member has left, its owners and admins (§1). Only
// accounts with a verified email are returned.
func (n *Notifier) recipients(ctx context.Context, item state.AgentApprovalNotification) ([]state.Account, error) {
	connectorIsMember, err := n.connectorIsMember(ctx, item)
	if err != nil {
		return nil, err
	}
	ids := []string{item.AccountID}
	if !connectorIsMember {
		members, err := n.Store.ListOrgMembers(ctx, item.OrgID)
		if err != nil {
			return nil, err
		}
		ids = ids[:0]
		for _, m := range members {
			if m.RemovedAt == nil && (m.Role == state.OrgRoleOwner || m.Role == state.OrgRoleAdmin) {
				ids = append(ids, m.AccountID)
			}
		}
	}
	out := make([]state.Account, 0, len(ids))
	for _, id := range ids {
		acct, err := n.Store.AccountByID(ctx, id)
		if errors.Is(err, state.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if acct.Email != "" && acct.EmailVerified() {
			out = append(out, acct)
		}
	}
	return out, nil
}

// connectorIsMember reports whether the member who connected the agent still
// belongs to the organization the key acts in. Every key acts in one; a
// personal organization's owner may have no membership row.
func (n *Notifier) connectorIsMember(ctx context.Context, item state.AgentApprovalNotification) (bool, error) {
	if item.OrgID == "" {
		return true, nil
	}
	member, err := n.Store.OrgMemberByAccount(ctx, item.OrgID, item.AccountID)
	if err == nil {
		return member.RemovedAt == nil, nil
	}
	if !errors.Is(err, state.ErrNotFound) {
		return false, err
	}
	org, err := n.Store.OrgByID(ctx, item.OrgID)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return org.PersonalOwnerAccountID != nil && *org.PersonalOwnerAccountID == item.AccountID, nil
}

// readyRecipients drops recipients emailed about the key within
// api.AgentApprovalEmailInterval and reports when the first of them is due.
func (n *Notifier) readyRecipients(ctx context.Context, keyID string, recipients []state.Account, now time.Time) ([]state.Account, time.Time, error) {
	var ready []state.Account
	var until time.Time
	for _, r := range recipients {
		last, ok, err := n.Store.AgentApprovalEmailLastSent(ctx, r.ID, keyID)
		if err != nil {
			return nil, time.Time{}, err
		}
		next := last.Add(api.AgentApprovalEmailInterval)
		if ok && now.Before(next) {
			if until.IsZero() || next.Before(until) {
				until = next
			}
			continue
		}
		ready = append(ready, r)
	}
	return ready, until, nil
}

var categoryLabels = map[string]string{
	api.AgentApprovalDelete:      "Delete",
	api.AgentApprovalProduction:  "Production change",
	api.AgentApprovalCredentials: "Credentials",
	api.AgentApprovalBilling:     "Billing",
}

// message is the email: the agent's waiting requests, each with its review
// link. It never includes a request body.
func message(agent string, lines []state.PendingAgentApprovalLine) (string, string) {
	subject := agent + " is waiting for your approval"
	if len(lines) > 1 {
		subject = fmt.Sprintf("%s has %d requests waiting for your approval", agent, len(lines))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s, an agent connected to your Gregale account, asked to do something that needs a person's approval.\n\n", agent)
	for _, line := range lines {
		label := categoryLabels[line.Category]
		if label == "" {
			label = line.Category
		}
		fmt.Fprintf(&b, "%s: %s %s\nExpires %s\nReview: %s\n\n", label, line.Method, line.Path,
			line.ExpiresAt.UTC().Format("2006-01-02 15:04 MST"), line.ReviewURL)
	}
	b.WriteString("Sign in to approve or deny. If you deny, you can tell the agent why. ")
	b.WriteString("A request you do not decide expires; the agent is told and stops asking.\n")
	return subject, b.String()
}

func (n *Notifier) finish(ctx context.Context, ids []string, outcome, reason string) string {
	if err := n.Store.FinishAgentApprovalNotifications(ctx, ids, outcome, reason); err != nil {
		n.log().Warn("agent approval notifications: finish", "approval_ids", ids, "err", err)
		return OutcomeFailed
	}
	if outcome == state.AgentApprovalNotificationSent {
		return OutcomeSent
	}
	return OutcomeSkipped
}

func (n *Notifier) retry(ctx context.Context, item state.AgentApprovalNotification, cause error) string {
	n.log().Warn("agent approval notifications: delivery failed", "approval_id", item.ApprovalID, "attempt", item.Attempts+1, "err", cause)
	if err := n.Store.RetryAgentApprovalNotification(ctx, item.ApprovalID, cause.Error(), retryDelay, maxAttempts); err != nil {
		n.log().Warn("agent approval notifications: record retry", "approval_id", item.ApprovalID, "err", err)
	}
	return OutcomeFailed
}

func (n *Notifier) count(outcome string) {
	if n.Outcome != nil {
		n.Outcome(outcome)
	}
}

func (n *Notifier) now() time.Time {
	if n.Now != nil {
		return n.Now()
	}
	return time.Now()
}

func (n *Notifier) log() *slog.Logger {
	if n.Log != nil {
		return n.Log
	}
	return slog.Default()
}
