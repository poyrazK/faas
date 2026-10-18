package api

import (
	"context"
	"encoding/base64"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type MCPReadinessIncidentEvent struct {
	DestinationDeliveries []MCPReadinessDestinationDelivery `json:"destination_deliveries,omitempty"`
	DeliveryRouting       *MCPReadinessDeliveryRouting      `json:"delivery_routing,omitempty"`
	ReminderNumber        int                               `json:"reminder_number,omitempty"`
	DeliveryVersion       int64                             `json:"delivery_version"`
	LastOperatorAction    *MCPReadinessNotificationReceipt  `json:"last_operator_action,omitempty"`
	ID                    string                            `json:"id"`
	IncidentID            string                            `json:"incident_id"`
	EventID               string                            `json:"event_id"`
	Event                 string                            `json:"event"`
	RuleID                string                            `json:"rule_id"`
	ReadinessScope        string                            `json:"mcp_readiness_scope,omitempty"`
	Scopes                []string                          `json:"scopes"`
	OccurredAt            time.Time                         `json:"occurred_at"`
	AttemptedAt           *time.Time                        `json:"attempted_at,omitempty"`
	IncidentStatus        string                            `json:"incident_status"`
	AttemptCount          int                               `json:"attempt_count"`
	NextAttemptAt         *time.Time                        `json:"next_attempt_at,omitempty"`
	LastDeliveryID        string                            `json:"last_delivery_id,omitempty"`
	LastStatusCode        int                               `json:"last_status_code,omitempty"`
	DeliveryStatus        string                            `json:"delivery_status"`
	MaintenanceWindowIDs  []string                          `json:"maintenance_window_ids,omitempty"`
	SuppressionReason     string                            `json:"suppression_reason,omitempty"`
	NotificationReason    string                            `json:"notification_reason,omitempty"`
	Subjects              []MCPReadinessMonitorStatus       `json:"subjects"`
}
type MCPReadinessIncidentTimeline struct {
	Events     []MCPReadinessIncidentEvent `json:"events"`
	NextBefore string                      `json:"next_before,omitempty"`
}

func MCPReadinessIncidentCursor(at time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "/" + id))
}
func ParseMCPReadinessIncidentCursor(cursor string) (time.Time, string, error) {
	if cursor == "" {
		return time.Time{}, "", nil
	}
	if len(cursor) > 160 {
		return time.Time{}, "", errors.New("invalid incident cursor")
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(raw) > 100 {
		return time.Time{}, "", errors.New("invalid incident cursor")
	}
	at, id, ok := strings.Cut(string(raw), "/")
	t, err := time.Parse(time.RFC3339Nano, at)
	if !ok || err != nil || t.IsZero() || uuid.Validate(id) != nil {
		return time.Time{}, "", errors.New("invalid incident cursor")
	}
	return t, id, nil
}
func (c *Client) ListMCPReadinessIncidents(ctx context.Context, app, scope, before string, limit int) (MCPReadinessIncidentTimeline, error) {
	q := url.Values{}
	if scope != "" {
		q.Set("scope", scope)
	}
	if before != "" {
		q.Set("before", before)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out MCPReadinessIncidentTimeline
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(app)+"/mcp-readiness-incidents?"+q.Encode(), nil, &out)
	return out, err
}
