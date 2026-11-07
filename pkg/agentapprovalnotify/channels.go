package agentapprovalnotify

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/alertchannels"
	"github.com/onebox-faas/faas/pkg/state"
)

// ChannelSender delivers a message to notification channels; the alert
// evaluator implements it with the alerting seal domain's identities.
type ChannelSender interface {
	SendToChannels(ctx context.Context, chans []state.NotificationChannel, msg alertchannels.Message, now time.Time) (attempted, delivered int)
}

// notifyChannels sends a new request to its bound channels once
// (ADR-20261107140000 §3); they are not subject to the email interval.
func (n *Notifier) notifyChannels(ctx context.Context, item state.AgentApprovalNotification, now time.Time) {
	if n.Channels == nil || item.ChannelsNotified {
		return
	}
	chans, err := n.Store.NotificationChannelsForAgentApproval(ctx, item.AccountID, item.OrgID)
	if err != nil {
		n.log().Warn("agent approval notifications: list channels", "approval_id", item.ApprovalID, "err", err)
		return
	}
	if len(chans) > 0 {
		line, ok := n.line(ctx, item)
		if !ok {
			return
		}
		label := categoryLabels[line.Category]
		if label == "" {
			label = line.Category
		}
		msg := alertchannels.Message{
			Event: alertchannels.EventFire, RuleName: item.AgentName + " needs approval",
			Summary: fmt.Sprintf("%s: %s %s, expires %s. Open the link to approve or deny.", label, line.Method, line.Path,
				line.ExpiresAt.UTC().Format("2006-01-02 15:04 MST")),
			DashboardURL: item.ReviewURL, OccurredAt: now, DedupKey: "agent-approval-" + item.ApprovalID,
			IdempotencyKey: "agent-approval-channel-" + item.ApprovalID, Severity: "warning",
			Footer: "You receive this because this channel gets approval requests from agents connected to a Gregale account. Change it with `gregale mcp approval-channels`.",
		}
		n.Channels.SendToChannels(ctx, chans, msg, now)
	}
	if err := n.Store.MarkAgentApprovalChannelsNotified(ctx, item.ApprovalID); err != nil {
		n.log().Warn("agent approval notifications: mark channels", "approval_id", item.ApprovalID, "err", err)
	}
}

// line is the request's entry among its key's pending notifications.
func (n *Notifier) line(ctx context.Context, item state.AgentApprovalNotification) (state.PendingAgentApprovalLine, bool) {
	lines, err := n.Store.PendingAgentApprovalNotificationsForKey(ctx, item.KeyID)
	if err != nil {
		n.log().Warn("agent approval notifications: load request", "approval_id", item.ApprovalID, "err", err)
		return state.PendingAgentApprovalLine{}, false
	}
	for _, line := range lines {
		if line.ApprovalID == item.ApprovalID {
			return line, true
		}
	}
	return state.PendingAgentApprovalLine{}, false
}
