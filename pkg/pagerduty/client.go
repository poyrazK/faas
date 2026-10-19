// Package pagerduty sends MCP readiness events to fixed US/EU Events API v2 endpoints.
package pagerduty

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
)

type Result struct {
	Accepted   bool
	StatusCode int
	Failure    string
	RetryAfter *time.Time
}
type Client struct{ http *http.Client }
type Event struct {
	IncidentID         string
	EventID            string
	Kind               string
	RuleName           string
	AppID              string
	RuleID             string
	OccurredAt         time.Time
	NotificationReason string
	ReminderNumber     int
}
type envelope struct {
	RoutingKey  string   `json:"routing_key"`
	EventAction string   `json:"event_action"`
	DedupKey    string   `json:"dedup_key"`
	Payload     *payload `json:"payload,omitempty"`
}
type payload struct {
	Summary       string         `json:"summary"`
	Source        string         `json:"source"`
	Severity      string         `json:"severity"`
	Timestamp     string         `json:"timestamp"`
	Component     string         `json:"component"`
	CustomDetails map[string]any `json:"custom_details"`
}

func New(client *http.Client) *Client {
	if client == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		transport.DialContext = oci.EgressDialContext(nil)
		client = &http.Client{Transport: transport}
	}
	copy := *client
	timeout := time.Duration(api.MCPReadinessPagerDutyTimeoutSeconds) * time.Second
	if copy.Timeout <= 0 || copy.Timeout > timeout {
		copy.Timeout = timeout
	}
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{http: &copy}
}
func requestEnvelope(key string, e Event) (envelope, bool) {
	if !api.ValidMCPReadinessPagerDutyKey(key) || uuid.Validate(e.IncidentID) != nil || uuid.Validate(e.EventID) != nil || e.OccurredAt.IsZero() {
		return envelope{}, false
	}
	out := envelope{RoutingKey: key, DedupKey: "gregale:mcp:" + uuid.MustParse(e.IncidentID).String(), EventAction: "trigger"}
	if e.Kind == "alert.resolved" {
		out.EventAction = "resolve"
		return out, true
	}
	if e.Kind != "alert.fired" {
		return envelope{}, false
	}
	summary := "MCP readiness failed: " + e.RuleName
	for len(summary) > api.MCPReadinessPagerDutySummaryMaxBytes {
		_, n := utf8.DecodeLastRuneInString(summary)
		summary = summary[:len(summary)-n]
	}
	out.Payload = &payload{Summary: summary, Source: "gregale:" + e.AppID, Severity: "error", Timestamp: e.OccurredAt.UTC().Format(time.RFC3339Nano), Component: "mcp-readiness", CustomDetails: map[string]any{"app_id": e.AppID, "rule_id": e.RuleID, "incident_id": e.IncidentID, "event_id": e.EventID, "notification_reason": e.NotificationReason, "reminder_number": e.ReminderNumber}}
	return out, true
}
func (c *Client) Send(ctx context.Context, key, endpoint string, event Event, now func() time.Time) Result {
	if endpoint != api.MCPReadinessPagerDutyEndpoint && endpoint != api.MCPReadinessPagerDutyEUEndpoint {
		return Result{Failure: "invalid_event"}
	}
	body, ok := requestEnvelope(key, event)
	if !ok {
		return Result{Failure: "invalid_event"}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return Result{Failure: "invalid_event"}
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return Result{Failure: "invalid_event"}
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json")
	response, err := c.http.Do(r)
	if err != nil {
		return Result{Failure: "transport_error"}
	}
	defer func() { _ = response.Body.Close() }()
	out := Result{StatusCode: response.StatusCode, Failure: "provider_rejected"}
	if response.StatusCode != http.StatusAccepted {
		if response.StatusCode == 429 || response.StatusCode == 503 {
			out.RetryAfter = retryAfter(response.Header.Get("Retry-After"), now())
		}
		return out
	}
	var ack struct {
		Status   string `json:"status"`
		DedupKey string `json:"dedup_key"`
	}
	raw, err = io.ReadAll(io.LimitReader(response.Body, int64(api.MCPReadinessPagerDutyResponseMaxBytes)+1))
	if err != nil || len(raw) > api.MCPReadinessPagerDutyResponseMaxBytes || json.Unmarshal(raw, &ack) != nil || ack.Status != "success" || ack.DedupKey != body.DedupKey {
		out.Failure = "invalid_acknowledgement"
		return out
	}
	out.Accepted = true
	out.Failure = ""
	return out
}
func retryAfter(value string, now time.Time) *time.Time {
	max := time.Duration(api.MCPReadinessPagerDutyMaxRetryAfterSeconds) * time.Second
	var duration time.Duration
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 {
			return nil
		}
		if seconds > int64(api.MCPReadinessPagerDutyMaxRetryAfterSeconds) {
			duration = max
		} else {
			duration = time.Duration(seconds) * time.Second
		}
	} else if at, err := http.ParseTime(value); err == nil {
		duration = at.Sub(now)
		if duration <= 0 {
			return nil
		}
		if duration > max {
			duration = max
		}
	} else {
		return nil
	}
	at := now.Add(duration)
	return &at
}
