package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// DataAPIUsageCounts are Data API usage counts (ADR-20261019102108): admitted
// requests by access kind, gateway rejections (4xx), errors (5xx), requests
// refused by owner caps, rows the engine reported and response bytes. They
// are owner telemetry from the app's runtime, never billing records.
type DataAPIUsageCounts struct {
	UserRequests      int64 `json:"user_requests"`
	AnonymousRequests int64 `json:"anonymous_requests"`
	ServerRequests    int64 `json:"server_requests"`
	RejectedRequests  int64 `json:"rejected_requests"`
	ErrorRequests     int64 `json:"error_requests"`
	CappedRequests    int64 `json:"capped_requests"`
	RowsReturned      int64 `json:"rows_returned"`
	ResponseBytes     int64 `json:"response_bytes"`
}

// DataAPIUsageHour is one UTC hour's delta in a runtime report.
type DataAPIUsageHour struct {
	Hour time.Time `json:"hour"`
	DataAPIUsageCounts
}

// DataAPIUsageReport is a runtime's private report of hourly deltas. A report
// id is applied once, so a retried or snapshot-duplicated report adds nothing.
type DataAPIUsageReport struct {
	Version  int                `json:"version"`
	ReportID string             `json:"report_id"`
	Hours    []DataAPIUsageHour `json:"hours"`
	// Access, when present, replaces the app's stored access settings for
	// live reads (ADR-20261016151947). Runtimes send it after starting and hourly.
	Access *DataAPIAccessConfig `json:"access,omitempty"`
}

// DataAPIUsageMonthToDate is what owner caps compare against: admitted
// requests in the current UTC calendar month (YYYY-MM).
type DataAPIUsageMonthToDate struct {
	Month             string `json:"month"`
	Requests          int64  `json:"requests"`
	AnonymousRequests int64  `json:"anonymous_requests"`
}

// DataAPIUsageReportResponse answers a runtime report.
type DataAPIUsageReportResponse struct {
	MonthToDate DataAPIUsageMonthToDate `json:"month_to_date"`
}

// DataAPIUsageBucket is one hour or UTC day of an owner usage view.
type DataAPIUsageBucket struct {
	Start time.Time `json:"start"`
	DataAPIUsageCounts
}

// DataAPIUsage is an app's Data API usage over a window, zero-filled per bucket.
type DataAPIUsage struct {
	App         string                  `json:"app"`
	From        time.Time               `json:"from"`
	To          time.Time               `json:"to"`
	Granularity string                  `json:"granularity"`
	Totals      DataAPIUsageCounts      `json:"totals"`
	MonthToDate DataAPIUsageMonthToDate `json:"month_to_date"`
	Buckets     []DataAPIUsageBucket    `json:"buckets"`
}

// GetAppDataAPIUsage reads an app's Data API usage. since is a lookback such
// as 24h, 7d or 30d (default 7d); granularity is hour, day or empty for the
// server's choice.
func (c *Client) GetAppDataAPIUsage(ctx context.Context, slug, since, granularity string) (DataAPIUsage, error) {
	var out DataAPIUsage
	query := url.Values{}
	if since != "" {
		query.Set("since", since)
	}
	if granularity != "" {
		query.Set("granularity", granularity)
	}
	path := "/v1/apps/" + url.PathEscape(slug) + "/data-api/usage"
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}
