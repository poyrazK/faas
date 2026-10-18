// Package alertchannels delivers alert notifications to Slack, PagerDuty,
// and email (ADR-749). Destinations are fixed hosts: the customer supplies
// a Slack webhook path or a PagerDuty routing key, never a host, so a
// channel cannot be pointed at an internal address.
package alertchannels

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Channel kinds; mirror notification_channels_kind_chk.
const (
	KindSlack     = "slack"
	KindPagerDuty = "pagerduty"
	KindEmail     = "email"
)

// PagerDuty service regions and their fixed Events API v2 endpoints.
const (
	RegionUS = "us"
	RegionEU = "eu"
)

var pagerDutyEndpoints = map[string]string{
	RegionUS: "https://events.pagerduty.com/v2/enqueue",
	RegionEU: "https://events.eu.pagerduty.com/v2/enqueue",
}

var (
	slackPathPattern  = regexp.MustCompile(`^/services/[A-Za-z0-9]+/[A-Za-z0-9]+/[A-Za-z0-9]+$`)
	routingKeyPattern = regexp.MustCompile(`^[A-Za-z0-9]{32}$`)
)

// Event kinds a channel can receive.
const (
	EventFire    = "fire"
	EventResolve = "resolve"
	EventTest    = "test"
)

// Message is one notification. DedupKey groups a fire with its resolve
// (the alert rule id); DashboardURL links back to the app.
type Message struct {
	Event        string
	RuleName     string
	AppSlug      string
	Metric       string
	Comparison   string
	Threshold    float64
	Observed     float64
	Window       string
	DashboardURL string
	OccurredAt   time.Time
	DedupKey     string
}

// Target is a channel's decrypted destination.
type Target struct {
	Kind       string
	SlackURL   string // kind slack
	RoutingKey string // kind pagerduty
	Region     string // kind pagerduty
	Email      string // kind email
}

// ValidateSlackURL accepts only Slack incoming-webhook URLs.
func ValidateSlackURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "hooks.slack.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !slackPathPattern.MatchString(u.Path) {
		return errors.New("slack_webhook_url must be a Slack incoming webhook (https://hooks.slack.com/services/…)")
	}
	return nil
}

// ValidatePagerDuty accepts a 32-character Events API v2 routing key and a
// known service region.
func ValidatePagerDuty(routingKey, region string) error {
	if !routingKeyPattern.MatchString(routingKey) {
		return errors.New("pagerduty_routing_key must be the 32-character Events API v2 integration key")
	}
	if _, ok := pagerDutyEndpoints[region]; !ok {
		return errors.New("pagerduty_region must be us or eu")
	}
	return nil
}

// Mailer sends one plain-text email. Each daemon adapts its own mail
// transport to it, so this package stays free of transport dependencies.
// idempotencyKey lets providers drop a retried duplicate.
type Mailer interface {
	SendEmail(ctx context.Context, to, subject, body, idempotencyKey string) error
}

// Sender delivers messages. HTTP is used for Slack and PagerDuty; Mail for
// email.
type Sender struct {
	HTTP     *http.Client
	Mail     Mailer
	Attempts int           // default 3
	Backoff  time.Duration // default 1s, doubled per retry
}

// NewHTTPClient returns a client that never follows redirects, so a
// destination cannot bounce a notification to another host.
func NewHTTPClient() *http.Client {
	return &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Send delivers msg to t.
func (s *Sender) Send(ctx context.Context, t Target, msg Message) error {
	switch t.Kind {
	case KindSlack:
		if err := ValidateSlackURL(t.SlackURL); err != nil {
			return err
		}
		return s.postJSON(ctx, t.SlackURL, SlackPayload(msg))
	case KindPagerDuty:
		if err := ValidatePagerDuty(t.RoutingKey, t.Region); err != nil {
			return err
		}
		return s.postJSON(ctx, pagerDutyEndpoints[t.Region], PagerDutyPayload(t.RoutingKey, msg))
	case KindEmail:
		if s.Mail == nil {
			return errors.New("alertchannels: email transport not configured")
		}
		subject, body := EmailContent(msg)
		return s.Mail.SendEmail(ctx, t.Email, subject, body, msg.DedupKey+":"+msg.Event+":"+msg.OccurredAt.UTC().Format(time.RFC3339))
	}
	return fmt.Errorf("alertchannels: unknown channel kind %q", t.Kind)
}

// postJSON posts body with bounded retries on transport errors, 429, and
// 5xx. A 4xx other than 429 is the customer's configuration and is final.
func (s *Sender) postJSON(ctx context.Context, endpoint string, body any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("alertchannels: encode: %w", err)
	}
	client := s.HTTP
	if client == nil {
		client = NewHTTPClient()
	}
	attempts, backoff := max(s.Attempts, 1), s.Backoff
	if s.Attempts == 0 {
		attempts = 3
	}
	if backoff == 0 {
		backoff = time.Second
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff << (i - 1)):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
		if err != nil {
			return fmt.Errorf("alertchannels: build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("alertchannels: deliver: %w", err)
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		_ = resp.Body.Close()
		switch {
		case resp.StatusCode < 300:
			return nil
		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
			lastErr = fmt.Errorf("alertchannels: destination returned %d", resp.StatusCode)
		default:
			return fmt.Errorf("alertchannels: destination rejected the notification (%d); check the channel configuration", resp.StatusCode)
		}
	}
	return lastErr
}

// Headline is the one-line summary shared by every channel.
func Headline(msg Message) string {
	switch msg.Event {
	case EventResolve:
		return fmt.Sprintf("Resolved: %s", msg.RuleName)
	case EventTest:
		return "Test notification from Gregale"
	}
	return fmt.Sprintf("Alert: %s", msg.RuleName)
}

// Detail describes the condition: "metric observed vs comparison threshold".
func Detail(msg Message) string {
	if msg.Event == EventTest {
		return "This channel is set up correctly. Real alerts will look like this, with the rule, the observed value and a dashboard link."
	}
	scope := ""
	if msg.AppSlug != "" {
		scope = " on " + msg.AppSlug
	}
	verb := "is"
	if msg.Event == EventResolve {
		verb = "is back within its threshold:"
	}
	return fmt.Sprintf("%s%s %s %s (threshold %s %s, window %s)", msg.Metric, scope, verb, fmtNum(msg.Observed), comparisonWord(msg.Comparison), fmtNum(msg.Threshold), msg.Window)
}

func comparisonWord(c string) string {
	return map[string]string{"gt": ">", "gte": "≥", "lt": "<", "lte": "≤"}[c]
}

func fmtNum(v float64) string {
	s := fmt.Sprintf("%.4f", v)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "" || s == "-" {
		return "0"
	}
	return s
}

// SealNamespace labels sealed channel destinations (Slack URL, PagerDuty
// routing key) so they can never be opened as another kind of secret.
const SealNamespace = "alert_channel_target"
