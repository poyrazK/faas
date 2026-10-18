package alertchannels

import (
	"fmt"
	"strings"
	"time"
)

// SlackPayload renders a message for a Slack incoming webhook: a fallback
// text plus a small block layout.
func SlackPayload(msg Message) map[string]any {
	icon := map[string]string{EventFire: ":rotating_light:", EventResolve: ":white_check_mark:", EventTest: ":wave:"}[msg.Event]
	text := fmt.Sprintf("%s *%s*\n%s", icon, Headline(msg), Detail(msg))
	blocks := []map[string]any{{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": text}}}
	if msg.DashboardURL != "" {
		blocks = append(blocks, map[string]any{"type": "context", "elements": []map[string]any{{"type": "mrkdwn", "text": fmt.Sprintf("<%s|Open in Gregale>", msg.DashboardURL)}}})
	}
	return map[string]any{"text": Headline(msg) + " — " + Detail(msg), "blocks": blocks}
}

// PagerDutyPayload renders an Events API v2 event. A fire triggers, a
// resolve resolves the incident with the same dedup key, and a test
// triggers a low-severity event the customer can resolve by hand.
func PagerDutyPayload(routingKey string, msg Message) map[string]any {
	action := "trigger"
	if msg.Event == EventResolve {
		action = "resolve"
	}
	out := map[string]any{"routing_key": routingKey, "event_action": action, "dedup_key": "gregale-" + msg.DedupKey}
	if action == "resolve" {
		return out
	}
	severity := "error"
	if msg.Event == EventTest {
		severity = "info"
	}
	source := msg.AppSlug
	if source == "" {
		source = "gregale"
	}
	out["payload"] = map[string]any{
		"summary":   truncate(Headline(msg)+" — "+Detail(msg), 1024),
		"source":    source,
		"severity":  severity,
		"timestamp": msg.OccurredAt.UTC().Format(time.RFC3339),
		"component": msg.Metric,
		"custom_details": map[string]any{
			"rule": msg.RuleName, "metric": msg.Metric, "observed": msg.Observed,
			"comparison": msg.Comparison, "threshold": msg.Threshold, "window": msg.Window,
		},
	}
	if msg.DashboardURL != "" {
		out["links"] = []map[string]any{{"href": msg.DashboardURL, "text": "Open in Gregale"}}
	}
	return out
}

// EmailContent renders a plain-text email.
func EmailContent(msg Message) (subject, body string) {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n%s\n", Headline(msg), Detail(msg))
	if !msg.OccurredAt.IsZero() {
		fmt.Fprintf(&b, "\nAt: %s\n", msg.OccurredAt.UTC().Format(time.RFC1123))
	}
	if msg.DashboardURL != "" {
		fmt.Fprintf(&b, "Dashboard: %s\n", msg.DashboardURL)
	}
	b.WriteString("\nYou receive this because a Gregale alert rule sends to this address. Manage channels with `gregale channels list`.\n")
	return "[Gregale] " + Headline(msg), b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
