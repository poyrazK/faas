package issueslack

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

var botTokenPattern = regexp.MustCompile(`^xoxb-[A-Za-z0-9-]+$`)
var safeReasonPattern = regexp.MustCompile(`^[a-z0-9_]{0,128}$`)

func ValidBotToken(s string) bool {
	return len(s) <= api.IssueSlackTokenMaxBytes && botTokenPattern.MatchString(s)
}

type Client struct{ http *http.Client }

func NewClient(c *http.Client) *Client {
	if c == nil {
		c = &http.Client{Timeout: api.IssueSlackAttemptTimeout}
	}
	copyClient := *c
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{http: &copyClient}
}
func (c *Client) Send(ctx context.Context, claim state.IssueSlackClaim, token, base string) state.IssueSlackResult {
	if !ValidBotToken(token) {
		return state.IssueSlackResult{Status: "dead", Error: "token_unavailable"}
	}
	body, err := message(claim, base)
	if err != nil {
		return state.IssueSlackResult{Status: "dead", Error: "invalid_response"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://slack.com/api/chat.postMessage", bytes.NewReader(body))
	if err != nil {
		return state.IssueSlackResult{Status: "dead", Error: "invalid_response"}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := c.http.Do(req)
	if err != nil {
		return retry(claim, "transport_error", 0, "")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusTooManyRequests {
		return retry(claim, "rate_limited", resp.StatusCode, resp.Header.Get("Retry-After"))
	}
	if resp.StatusCode >= 500 {
		return retry(claim, "slack_unavailable", resp.StatusCode, resp.Header.Get("Retry-After"))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return state.IssueSlackResult{Status: "dead", Error: "slack_rejected", ResponseCode: resp.StatusCode}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, api.IssueSlackResponseMaxBytes+1))
	if err != nil || len(raw) > api.IssueSlackResponseMaxBytes {
		return retry(claim, "invalid_response", resp.StatusCode, "")
	}
	var data struct {
		OK      bool   `json:"ok"`
		Error   string `json:"error"`
		TS      string `json:"ts"`
		Channel string `json:"channel"`
	}
	if json.Unmarshal(raw, &data) != nil {
		return retry(claim, "invalid_response", resp.StatusCode, "")
	}
	if data.OK {
		if !state.ValidIssueSlackTimestamp(data.TS) || data.Channel != claim.ChannelID {
			return retry(claim, "invalid_response", resp.StatusCode, "")
		}
		return state.IssueSlackResult{Status: "succeeded", MessageTS: data.TS, ResponseCode: resp.StatusCode}
	}
	switch data.Error {
	case "ratelimited":
		return retry(claim, "rate_limited", resp.StatusCode, resp.Header.Get("Retry-After"))
	case "internal_error", "fatal_error", "service_unavailable", "request_timeout":
		return retry(claim, "slack_unavailable", resp.StatusCode, "")
	case "thread_not_found", "invalid_thread_ts":
		r := retry(claim, "thread_unavailable", resp.StatusCode, "")
		r.ResetThread = r.Status == "failed"
		return r
	case "invalid_auth", "token_revoked", "account_inactive", "not_authed":
		return state.IssueSlackResult{Status: "dead", Error: "invalid_auth", ResponseCode: resp.StatusCode}
	case "channel_not_found", "is_archived":
		return state.IssueSlackResult{Status: "dead", Error: "channel_not_found", ResponseCode: resp.StatusCode}
	case "not_in_channel":
		return state.IssueSlackResult{Status: "dead", Error: "not_in_channel", ResponseCode: resp.StatusCode}
	case "missing_scope":
		return state.IssueSlackResult{Status: "dead", Error: "missing_scope", ResponseCode: resp.StatusCode}
	default:
		return state.IssueSlackResult{Status: "dead", Error: "slack_rejected", ResponseCode: resp.StatusCode}
	}
}
func message(c state.IssueSlackClaim, base string) ([]byte, error) {
	// Never include issue titles, stack frames, arbitrary URLs or provider response bodies.
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, state.ErrInvalidArgument
	}
	if c.Application {
		return applicationMessage(c, u)
	}
	switch c.Kind {
	case "paused", "closure", "failure", "reminder", "escalation", "recovered":
	default:
		return nil, state.ErrInvalidArgument
	}
	heading := "Gregale tracker sync · " + c.Kind
	detail := "App: " + c.AppSlug + "\nIncident: " + c.IncidentID
	if safeReasonPattern.MatchString(c.Reason) && c.Reason != "" {
		detail += "\nLast reported reason: " + c.Reason
	}
	if c.Kind != "recovered" {
		detail += "\nThis notice records the last reported problem; it is not a new provider check."
	}
	root := strings.TrimRight(u.String(), "/") + "/dashboard/apps/" + url.PathEscape(c.AppSlug)
	link := func(text, path string) map[string]any {
		return map[string]any{"type": "link", "text": text, "url": root + path}
	}
	body := map[string]any{"channel": c.ChannelID, "client_msg_id": c.ID, "text": heading, "parse": "none", "link_names": false, "unfurl_links": false, "unfurl_media": false, "blocks": []any{
		map[string]any{"type": "header", "text": map[string]any{"type": "plain_text", "text": heading}},
		map[string]any{"type": "section", "text": map[string]any{"type": "plain_text", "text": detail}},
		map[string]any{"type": "rich_text", "elements": []any{map[string]any{"type": "rich_text_section", "elements": []any{link("Open triage", "/issues/tracker-notifications/triage/"+url.PathEscape(c.NotificationID)), map[string]any{"type": "text", "text": " · "}, link("Sync health", "/issues/"+url.PathEscape(c.IssueID)+"/tracker-sync/"+url.PathEscape(c.LinkID))}}}},
	}}
	if c.ThreadTS != "" {
		if !state.ValidIssueSlackTimestamp(c.ThreadTS) {
			return nil, state.ErrInvalidArgument
		}
		body["thread_ts"] = c.ThreadTS
	}
	return json.Marshal(body)
}
func slackRetryAfter(s string, now time.Time) time.Time {
	// Slack uses seconds; also accept the standard HTTP-date used by webhook receivers.
	if n, err := strconv.ParseUint(s, 10, 32); err == nil {
		d := time.Duration(n) * time.Second
		if d > api.IssueSlackDeliveryTTL {
			d = api.IssueSlackDeliveryTTL
		}
		return now.Add(d)
	}
	if t, err := http.ParseTime(s); err == nil && t.After(now) {
		if t.After(now.Add(api.IssueSlackDeliveryTTL)) {
			t = now.Add(api.IssueSlackDeliveryTTL)
		}
		return t
	}
	return time.Time{}
}

var safeApplicationValue = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func applicationMessage(c state.IssueSlackClaim, base *url.URL) ([]byte, error) {
	labels := map[string]string{"application_escalation": "Unacknowledged issue escalated to backup team", "application_reminder": "Unacknowledged issue reminder", "created": "New issue", "regressed": "Issue regressed", "impact_threshold_reached": "Customer-impact threshold reached", "frequency_threshold_reached": "Event-frequency threshold reached", "compound_threshold_reached": "Compound alert thresholds reached"}
	label, ok := labels[c.Kind]
	if !ok {
		return nil, state.ErrInvalidArgument
	}
	heading := "Gregale Issues · " + label
	detail := "App: " + c.AppSlug + "\nIssue: " + c.IssueID
	if value := c.Details["environment"]; safeApplicationValue.MatchString(value) {
		detail += "\nEnvironment: " + value
	}
	if value := c.Details["rule_key"]; safeApplicationValue.MatchString(value) {
		detail += "\nRule: " + value
	}
	if state.ValidSlackRoutingKey(c.RoutingRuleKey) {
		detail += "\nChannel routing rule: " + c.RoutingRuleKey
	}
	for _, field := range []struct{ key, label string }{
		{"reminder_sequence", "Reminder"}, {"observed_events", "Events in window"}, {"minimum_events", "Event threshold"}, {"identified_customers", "Verified customers"}, {"minimum_customers", "Customer threshold"}, {"window_seconds", "Window (seconds)"},
	} {
		value := c.Details[field.key]
		if _, err := strconv.ParseUint(value, 10, 32); err == nil && len(value) <= 10 {
			detail += "\n" + field.label + ": " + value
		}
	}
	root := strings.TrimRight(base.String(), "/") + "/dashboard/apps/" + url.PathEscape(c.AppSlug)
	link := root + "/issues?issue=" + url.QueryEscape(c.IssueID)
	body := map[string]any{"channel": c.ChannelID, "client_msg_id": c.ID, "text": heading, "parse": "none", "link_names": false, "unfurl_links": false, "unfurl_media": false, "blocks": []any{
		map[string]any{"type": "header", "text": map[string]any{"type": "plain_text", "text": heading}},
		map[string]any{"type": "section", "text": map[string]any{"type": "plain_text", "text": detail}},
		map[string]any{"type": "rich_text", "elements": []any{map[string]any{"type": "rich_text_section", "elements": []any{map[string]any{"type": "link", "text": "Open issue", "url": link}, map[string]any{"type": "link", "text": "Acknowledge or snooze", "url": root + "/issues/" + url.PathEscape(c.IssueID) + "/triage"}}}}},
	}}
	if c.Interactive {
		buttons := []any{}
		for _, a := range []struct{ id, label string }{{"gregale_acknowledge", "Acknowledge"}, {"gregale_snooze_1h", "Snooze 1 hour"}, {"gregale_snooze_24h", "Snooze 24 hours"}} {
			buttons = append(buttons, map[string]any{"type": "button", "action_id": a.id, "value": c.ID, "text": map[string]any{"type": "plain_text", "text": a.label}})
		}
		body["blocks"] = append(body["blocks"].([]any), map[string]any{"type": "actions", "elements": buttons})
	}

	if c.ThreadTS != "" {
		if !state.ValidIssueSlackTimestamp(c.ThreadTS) {
			return nil, state.ErrInvalidArgument
		}
		body["thread_ts"] = c.ThreadTS
	}
	return json.Marshal(body)
}
