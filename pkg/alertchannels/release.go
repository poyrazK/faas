package alertchannels

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Release approval events a channel can receive (ADR-20261020085706).
const (
	EventApprovalRequested = "approval_requested"
	EventApprovalApproved  = "approval_approved"
	EventApprovalRejected  = "approval_rejected"
	EventApprovalExpired   = "approval_expired"
)

// ReleaseNotice is the snapshot apid records for one approval event; meterd
// renders it. ReviewURL opens the approval page, which acts only for a
// signed-in person with access to the app.
type ReleaseNotice struct {
	Event          string `json:"event"`
	App            string `json:"app"`
	DeploymentID   string `json:"deployment_id"`
	Revision       int    `json:"revision,omitempty"`
	DeployedBy     string `json:"deployed_by,omitempty"`
	Commit         string `json:"commit,omitempty"`
	Reason         string `json:"reason,omitempty"`
	Stage          int    `json:"stage,omitempty"`
	TotalStages    int    `json:"total_stages,omitempty"`
	TrafficPercent int    `json:"traffic_percent,omitempty"`
	Approvals      int    `json:"approvals"`
	Required       int    `json:"required"`
	ExpiresAt      string `json:"expires_at,omitempty"`
	Actor          string `json:"actor,omitempty"`
	Comment        string `json:"comment,omitempty"`
	ReviewURL      string `json:"review_url,omitempty"`
	// Changes summarizes the release diff (ADR-20261021101117), such as
	// "new code, 2 config keys".
	Changes string `json:"changes,omitempty"`
}

// ErrReleaseNoticeKind refuses a channel kind that does not receive release
// approvals: a waiting release is not an incident to page about.
var ErrReleaseNoticeKind = errors.New("alertchannels: PagerDuty channels do not receive release approvals")

// ReleaseMessage wraps a notice for Send.
func ReleaseMessage(n ReleaseNotice, at time.Time) Message {
	return Message{Event: n.Event, AppSlug: n.App, OccurredAt: at, DedupKey: n.DeploymentID, Release: &n}
}

func releaseHeadline(n ReleaseNotice) string {
	release := releaseName(n)
	switch n.Event {
	case EventApprovalApproved:
		return fmt.Sprintf("Approved: %s of %s", release, n.App)
	case EventApprovalRejected:
		return fmt.Sprintf("Rejected: %s of %s", release, n.App)
	case EventApprovalExpired:
		return fmt.Sprintf("Approval timed out: %s of %s", release, n.App)
	}
	return fmt.Sprintf("Approval needed: %s of %s", release, n.App)
}

func releaseName(n ReleaseNotice) string {
	if n.Revision > 0 {
		return fmt.Sprintf("release v%d", n.Revision)
	}
	return "release " + shortID(n.DeploymentID)
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// releaseLines describes the release, one fact per line, in plain text.
func releaseLines(n ReleaseNotice) []string {
	var lines []string
	switch n.Event {
	case EventApprovalApproved:
		lines = append(lines, fmt.Sprintf("Approved by %s (%d of %d); the rollout continues.", n.Actor, n.Approvals, n.Required))
	case EventApprovalRejected:
		lines = append(lines, fmt.Sprintf("Rejected by %s; the rollout was aborted and the previous release serves again.", n.Actor))
	case EventApprovalExpired:
		lines = append(lines, "Nobody approved it in time; the rollout was aborted and the previous release serves again.")
	default:
		lines = append(lines, fmt.Sprintf("Waiting for %d more approval(s) (%d of %d) at %d%% traffic, canary stage %d of %d.",
			max(n.Required-n.Approvals, 0), n.Approvals, n.Required, n.TrafficPercent, n.Stage, n.TotalStages))
		if n.ExpiresAt != "" {
			lines = append(lines, "It is aborted if nobody approves by "+n.ExpiresAt+".")
		}
		if n.Changes != "" {
			lines = append(lines, "What changed: "+n.Changes)
		}
	}
	if n.Comment != "" {
		lines = append(lines, "Comment: "+n.Comment)
	}
	if n.DeployedBy != "" {
		lines = append(lines, "Deployed by "+n.DeployedBy)
	}
	if n.Commit != "" {
		lines = append(lines, "Commit "+n.Commit)
	}
	if n.Reason != "" {
		lines = append(lines, "Reason: "+n.Reason)
	}
	return lines
}

// slackEscape keeps customer text from forming Slack links or mentions.
func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// reviewLink adds the intended decision to the approval page's URL; a URL
// that is not https is dropped rather than linked.
func reviewLink(review, intent string) string {
	u, err := url.Parse(review)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return ""
	}
	if intent != "" {
		q := u.Query()
		q.Set("intent", intent)
		u.RawQuery = q.Encode()
	}
	return u.String()
}

func releaseSlackPayload(n ReleaseNotice) map[string]any {
	icon := map[string]string{EventApprovalRequested: ":hourglass_flowing_sand:", EventApprovalApproved: ":white_check_mark:",
		EventApprovalRejected: ":no_entry:", EventApprovalExpired: ":alarm_clock:"}[n.Event]
	lines := releaseLines(n)
	escaped := make([]string, 0, len(lines))
	for _, line := range lines {
		escaped = append(escaped, slackEscape(line))
	}
	headline := releaseHeadline(n)
	text := fmt.Sprintf("%s *%s*\n%s", icon, slackEscape(headline), strings.Join(escaped, "\n"))
	blocks := []map[string]any{{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": text}}}
	if review := reviewLink(n.ReviewURL, ""); review != "" {
		var buttons []map[string]any
		if n.Event == EventApprovalRequested {
			buttons = append(buttons,
				slackButton("Approve", reviewLink(n.ReviewURL, "approve"), "release_approve", "primary"),
				slackButton("Reject", reviewLink(n.ReviewURL, "reject"), "release_reject", "danger"))
		}
		buttons = append(buttons, slackButton("Review in Gregale", review, "release_review", ""))
		blocks = append(blocks, map[string]any{"type": "actions", "elements": buttons})
	}
	return map[string]any{"text": headline, "blocks": blocks}
}

func slackButton(label, link, actionID, style string) map[string]any {
	b := map[string]any{"type": "button", "text": map[string]any{"type": "plain_text", "text": label}, "url": link, "action_id": actionID}
	if style != "" {
		b["style"] = style
	}
	return b
}

func releaseEmailContent(n ReleaseNotice) (subject, body string) {
	var b strings.Builder
	headline := releaseHeadline(n)
	fmt.Fprintf(&b, "%s\n\n%s\n", headline, strings.Join(releaseLines(n), "\n"))
	if n.Event == EventApprovalRequested {
		if link := reviewLink(n.ReviewURL, "approve"); link != "" {
			fmt.Fprintf(&b, "\nApprove: %s\nReject: %s\n", link, reviewLink(n.ReviewURL, "reject"))
		}
		fmt.Fprintf(&b, "Or from a terminal: gregale approvals approve %s\n", n.DeploymentID)
	} else if link := reviewLink(n.ReviewURL, ""); link != "" {
		fmt.Fprintf(&b, "\nReview: %s\n", link)
	}
	b.WriteString("\nYou receive this because this app's release approval policy notifies this address. Change it with `gregale approvals require --notify`.\n")
	return "[Gregale] " + headline, b.String()
}
