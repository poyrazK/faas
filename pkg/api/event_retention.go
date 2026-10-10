package api

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type EventRetentionQuery struct {
	Source string
	App    string
	Window time.Duration
	Limit  int
}

type EventRetentionSample struct {
	EventSource   string    `json:"event_source"`
	EventID       string    `json:"event_id"`
	AcceptedAt    time.Time `json:"accepted_at"`
	RetainUntil   time.Time `json:"retain_until"`
	RetainedBytes int64     `json:"retained_bytes"`
	Status        string    `json:"status"`
	HoldReason    string    `json:"hold_reason"`
}

type EventRetentionHealth struct {
	UnknownDeadlineReceipts    int64                     `json:"unknown_deadline_receipts"`
	ObservedAt                 time.Time                 `json:"observed_at"`
	WindowSeconds              int64                     `json:"window_seconds"`
	EventSource                string                    `json:"event_source,omitempty"`
	AppID                      string                    `json:"app_id,omitempty"`
	RetainedReceipts           int64                     `json:"retained_receipts"`
	RetainedBytes              int64                     `json:"retained_bytes"`
	UnsettledReceipts          int64                     `json:"unsettled_receipts"`
	HeldReceipts               int64                     `json:"held_receipts"`
	RecoveryHolds              int64                     `json:"recovery_holds"`
	RunningBackfillHolds       int64                     `json:"running_backfill_holds"`
	RetryableBackfillHolds     int64                     `json:"retryable_backfill_holds"`
	HeldDueReceipts            int64                     `json:"held_due_receipts"`
	EligibleForPruning         int64                     `json:"eligible_for_pruning"`
	ExpiringReceipts           int64                     `json:"expiring_receipts"`
	Storage                    EventStorageUsageResponse `json:"storage"`
	StorageCountUtilizationPct float64                   `json:"storage_count_utilization_pct"`
	StorageBytesUtilizationPct float64                   `json:"storage_bytes_utilization_pct"`
	StorageUtilizationPct      float64                   `json:"storage_utilization_pct"`
	Sample                     []EventRetentionSample    `json:"sample"`
	SampleTruncated            bool                      `json:"sample_truncated"`
}

func (q *EventRetentionQuery) Validate() error {
	if q.Window == 0 {
		q.Window = EventRetentionDefaultWindow
	}
	if q.Limit == 0 {
		q.Limit = EventRetentionSampleMax
	}
	if q.Window < time.Second || q.Window > EventRetentionMaxWindow || q.Window%time.Second != 0 || q.Limit < 1 || q.Limit > EventRetentionSampleMax || len(q.Source) > EventRetentionSourceMaxBytes || !utf8.ValidString(q.Source) || strings.ContainsRune(q.Source, 0) {
		return fmt.Errorf("window must be whole seconds from 1s to 720h, limit 1..100, source at most 256 bytes")
	}
	return nil
}

func (c *Client) GetEventRetentionHealth(ctx context.Context, q EventRetentionQuery) (EventRetentionHealth, error) {
	var out EventRetentionHealth
	if err := q.Validate(); err != nil {
		return out, err
	}
	values := url.Values{"window": {q.Window.String()}, "limit": {strconv.Itoa(q.Limit)}}
	if q.Source != "" {
		values.Set("source", q.Source)
	}
	if q.App != "" {
		values.Set("app", q.App)
	}
	err := c.do(ctx, "GET", "/v1/events/retention?"+values.Encode(), nil, &out)
	return out, err
}

func IsEventRetentionAlertMetric(metric string) bool {
	return metric == "event_retention_expiring_receipts" || metric == "event_storage_utilization_pct"
}
