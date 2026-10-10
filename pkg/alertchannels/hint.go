package alertchannels

import "strings"

// Hint is a short, non-secret label for a channel's destination: the Slack
// workspace and hook ids (the secret is the last path segment), the routing
// key's last four characters, or the email address.
func Hint(t Target) string {
	switch t.Kind {
	case KindSlack:
		parts := strings.Split(strings.TrimPrefix(t.SlackURL, "https://hooks.slack.com/services/"), "/")
		if len(parts) == 3 {
			return "hooks.slack.com/" + parts[0] + "/" + parts[1] + "/…"
		}
	case KindPagerDuty:
		if len(t.RoutingKey) >= 4 {
			return "…" + t.RoutingKey[len(t.RoutingKey)-4:]
		}
	case KindEmail:
		return t.Email
	}
	return ""
}
