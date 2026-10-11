package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type AppEventPublishStatusQuery struct {
	ExpectedAcceptedAt *time.Time
	Key                string
	After              string
	Limit              int
}

func (q *AppEventPublishStatusQuery) Normalize() error {
	if err := (AppEventAcceptanceGuard{ExpectedAcceptedAt: q.ExpectedAcceptedAt}).Validate(); err != nil {
		return err
	}
	if err := ValidateAppEventProducerKey(q.Key); err != nil {
		return err
	}
	if q.Limit == 0 {
		q.Limit = AppEventPublishStatusRecipientsDefault
	}
	if q.Limit < 1 || q.Limit > AppEventPublishStatusRecipientsMax || len(q.After) > AppEventPublishStatusCursorMaxBytes {
		return fmt.Errorf("invalid receipt page limit or cursor")
	}
	return nil
}

// Consumer rows remain lossless JSON because the standalone SDK does not yet
// mirror the complete existing event receipt execution/recovery DTO family.
type AppEventPublishStatusEvidence struct {
	EventID                string            `json:"event_id"`
	ClientEventID          string            `json:"client_event_id,omitempty"`
	EventSource            string            `json:"event_source"`
	EventType              string            `json:"event_type"`
	SchemaVersion          string            `json:"schema_version,omitempty"`
	AcceptedAt             time.Time         `json:"accepted_at"`
	RoutingSettledAt       *time.Time        `json:"routing_settled_at,omitempty"`
	RetainUntil            *time.Time        `json:"retain_until,omitempty"`
	SnapshotCaptured       bool              `json:"snapshot_captured"`
	RoutingMode            string            `json:"routing_mode"`
	RecipientCount         int               `json:"recipient_count"`
	RoutingSummary         map[string]int    `json:"routing_summary"`
	BackfillRecipientCount int               `json:"backfill_recipient_count,omitempty"`
	BackfillRoutingSummary map[string]int    `json:"backfill_routing_summary,omitempty"`
	Recipients             []json.RawMessage `json:"recipients"`
	NextAfter              string            `json:"next_after,omitempty"`
}
type AppEventPublishStatusResponse struct {
	Acceptance         string                         `json:"acceptance,omitempty"`
	ExpectedAcceptedAt *time.Time                     `json:"expected_accepted_at,omitempty"`
	AppID              string                         `json:"app_id"`
	Source             string                         `json:"source"`
	EventID            string                         `json:"event_id"`
	ObservedAt         time.Time                      `json:"observed_at"`
	Status             string                         `json:"status"`
	Reason             string                         `json:"reason,omitempty"`
	ReceiptURL         string                         `json:"receipt_url"`
	Receipt            *AppPublishedEventReceipt      `json:"receipt,omitempty"`
	Evidence           *AppEventPublishStatusEvidence `json:"evidence,omitempty"`
}

func (c *Client) GetAppEventPublishStatus(ctx context.Context, slug string, query AppEventPublishStatusQuery) (AppEventPublishStatusResponse, error) {
	var out AppEventPublishStatusResponse
	if err := query.Normalize(); err != nil {
		return out, err
	}
	values := url.Values{"key": {query.Key}, "limit": {strconv.Itoa(query.Limit)}}
	if query.ExpectedAcceptedAt != nil {
		values.Set("expected_accepted_at", query.ExpectedAcceptedAt.UTC().Format(time.RFC3339Nano))
	}
	if query.After != "" {
		values.Set("after", query.After)
	}
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/events/publish-status?"+values.Encode(), nil, &out)
	return out, err
}
