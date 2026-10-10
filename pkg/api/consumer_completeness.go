package api

import (
	"context"
	"net/url"
	"time"
)

// APIConsumerUsageCompletenessResponse compares a consumer's billing ledger
// with request telemetry over successful requests (ADR-955). Status is
// verified, partial, gaps_detected, or unverifiable. missing_requests is a
// lower bound of successful requests telemetry saw that the ledger never
// billed; checked_from/checked_until are the settled, retained hours checked.
type APIConsumerUsageCompletenessResponse struct {
	ConsumerID            string    `json:"consumer_id"`
	Status                string    `json:"status"`
	CheckedFrom           time.Time `json:"checked_from"`
	CheckedUntil          time.Time `json:"checked_until"`
	LedgerRequests        int64     `json:"ledger_requests"`
	TelemetryRequests     int64     `json:"telemetry_requests"`
	ConfirmedRequests     int64     `json:"confirmed_requests"`
	MissingRequests       int64     `json:"missing_requests"`
	HoursChecked          int       `json:"hours_checked"`
	HoursWithoutTelemetry int       `json:"hours_without_telemetry"`
}

// GetAPIConsumerUsageCompleteness checks a consumer's billed usage against
// request telemetry for [since, until).
func (c *Client) GetAPIConsumerUsageCompleteness(ctx context.Context, slug, consumerID string, since, until time.Time) (APIConsumerUsageCompletenessResponse, error) {
	q := url.Values{}
	q.Set("since", since.UTC().Format(time.RFC3339))
	q.Set("until", until.UTC().Format(time.RFC3339))
	var out APIConsumerUsageCompletenessResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/consumers/"+consumerID+"/usage-completeness?"+q.Encode(), nil, &out)
}
